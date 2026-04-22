package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/stello/elnath/internal/llm"
)

const webFetchTimeout = 30 * time.Second

// Phase A.4 redirect control. Mirrors Claude Code's MAX_REDIRECTS
// (/Users/stello/claude-code-src/src/tools/WebFetchTool/utils.ts:268) and the
// "Do not automatically follow cross-host redirects" guidance (utils.ts:250-254).
// The hop cap kills malicious 302 loops; the same-host check in
// isSameHostRedirect enforces the origin boundary on each hop.
const webFetchMaxRedirectHops = 10

// Phase A.2 parity with Claude Code's MAX_MARKDOWN_LENGTH
// (/Users/stello/claude-code-src/src/tools/WebFetchTool/utils.ts:128).
// Keep the cap byte-based for Go string semantics; rune-boundary safe cut
// is handled in truncateMarkdown so a cut mid-codepoint never corrupts
// the Korean/emoji content common in partner dogfood.
const (
	webFetchMaxMarkdownLen   = 100_000
	webFetchTruncationMarker = "\n\n[Content truncated due to length...]"
)

// Phase A.3 LRU cache. golang-lru/v2 caps on entry count, not bytes —
// Claude Code's 50 MiB byte-cap (utils.ts:64-69) maps here to an entry
// cap sized for typical partner dogfood (Yahoo-class pages at ~40-80 KiB
// of markdown → ~100 entries lands comfortably under ~8 MiB). TTL is
// kept at 15 min so repeated reads inside a single chat sprint are free
// but stale content always ages out before the next conversation.
const (
	webFetchCacheSize = 100
	webFetchCacheTTL  = 15 * time.Minute
)

type webFetchCacheEntry struct {
	output string
}

var (
	sharedWebFetchCache     *expirable.LRU[string, webFetchCacheEntry]
	sharedWebFetchCacheOnce sync.Once
)

func getSharedWebFetchCache() *expirable.LRU[string, webFetchCacheEntry] {
	sharedWebFetchCacheOnce.Do(func() {
		sharedWebFetchCache = expirable.NewLRU[string, webFetchCacheEntry](webFetchCacheSize, nil, webFetchCacheTTL)
	})
	return sharedWebFetchCache
}

// webFetchOption customizes a WebFetchTool at construction. Kept package-private
// — tests inject a short-TTL or isolated cache so suite runs don't share state
// with the process-wide singleton returned by getSharedWebFetchCache.
type webFetchOption func(*WebFetchTool)

func withWebFetchCache(cache *expirable.LRU[string, webFetchCacheEntry]) webFetchOption {
	return func(t *WebFetchTool) { t.cache = cache }
}

// withSecondary injects the SecondaryModelCaller used in Phase A.5's
// prompt-driven extract. Tests pass a scripted caller to pin the "prompt
// present → secondary consulted" contract; production wires the real
// per-provider caller at daemon/chat setup.
func withSecondary(caller llm.SecondaryModelCaller) webFetchOption {
	return func(t *WebFetchTool) { t.secondary = caller }
}

// WithSecondaryCaller is the exported wrapper around the package-private
// withSecondary option. The wire-site in cmd/elnath calls this at daemon
// setup to inject the provider-aware secondary dispatcher built by
// llm.NewSecondaryModelCaller.
func WithSecondaryCaller(caller llm.SecondaryModelCaller) func(*WebFetchTool) {
	return func(t *WebFetchTool) { t.secondary = caller }
}

// Lazy singleton: the html-to-markdown Converter builds a rule table on
// construction; reusing one instance across Execute calls keeps the hot
// path allocation-free. Mirrors the Turndown lazy-init in
// /Users/stello/claude-code-src/src/tools/WebFetchTool/utils.ts:91-97.
var (
	htmlConverterOnce sync.Once
	htmlConverter     *md.Converter
)

func getHTMLConverter() *md.Converter {
	htmlConverterOnce.Do(func() {
		htmlConverter = md.NewConverter("", true, nil)
	})
	return htmlConverter
}

func isHTMLContentType(contentType string) bool {
	ct := strings.ToLower(contentType)
	return strings.Contains(ct, "text/html") || strings.Contains(ct, "application/xhtml+xml")
}

// isMarkdownContentType gates the Phase A.5 fast-path: when a preapproved
// host serves text/markdown directly and the body is under the 100K cap,
// the raw markdown is the best answer a documentation query can receive,
// so the secondary-model extract is skipped. Reference:
// /Users/stello/claude-code-src/src/tools/WebFetchTool/WebFetchTool.ts:264.
func isMarkdownContentType(contentType string) bool {
	ct := strings.ToLower(contentType)
	return strings.Contains(ct, "text/markdown")
}

// isSameHostRedirect mirrors Claude Code's isPermittedRedirect — a redirect
// is permitted only when it preserves the origin (scheme + port + hostname
// up to the optional www prefix) and carries no embedded credentials.
// Reference: /Users/stello/claude-code-src/src/tools/WebFetchTool/utils.ts:212-243.
func isSameHostRedirect(origURL, redirectURL string) bool {
	orig, err := url.Parse(origURL)
	if err != nil {
		return false
	}
	target, err := url.Parse(redirectURL)
	if err != nil {
		return false
	}
	if orig.Scheme != target.Scheme {
		return false
	}
	if orig.Port() != target.Port() {
		return false
	}
	if target.User != nil {
		return false
	}
	stripWww := func(h string) string { return strings.TrimPrefix(h, "www.") }
	return stripWww(orig.Hostname()) == stripWww(target.Hostname())
}

// sameHostRedirectChecker is the CheckRedirect hook installed on
// WebFetchTool's http.Client. It enforces the Phase A.4 same-host policy:
// each hop must pass isSameHostRedirect against its immediate predecessor;
// at most webFetchMaxRedirectHops hops are chained; cross-host transitions
// return http.ErrUseLastResponse so Execute can surface the block to the
// caller without ever issuing a request to the new host.
func sameHostRedirectChecker(req *http.Request, via []*http.Request) error {
	if len(via) >= webFetchMaxRedirectHops {
		return fmt.Errorf("too many redirects (exceeded %d)", webFetchMaxRedirectHops)
	}
	source := via[len(via)-1].URL.String()
	next := req.URL.String()
	if !isSameHostRedirect(source, next) {
		return http.ErrUseLastResponse
	}
	return nil
}

// truncateMarkdown enforces the A.2 byte cap but steps back to the nearest
// UTF-8 rune start before appending the marker. Mid-codepoint cuts would
// emit U+FFFD on JSON re-encode and break the downstream LLM's reading.
func truncateMarkdown(s string) string {
	if len(s) <= webFetchMaxMarkdownLen {
		return s
	}
	cut := webFetchMaxMarkdownLen
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + webFetchTruncationMarker
}

// WebFetchTool fetches the body of a URL via HTTP GET.
type WebFetchTool struct {
	client    *http.Client
	cache     *expirable.LRU[string, webFetchCacheEntry]
	secondary llm.SecondaryModelCaller
}

func NewWebFetchTool(opts ...webFetchOption) *WebFetchTool {
	t := &WebFetchTool{
		client: &http.Client{
			Timeout:       webFetchTimeout,
			CheckRedirect: sameHostRedirectChecker,
		},
		cache:     getSharedWebFetchCache(),
		secondary: llm.NewNoopSecondaryModelCaller(),
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *WebFetchTool) Name() string { return "web_fetch" }

// Description follows the Claude Code WebFetchTool upstream wording so
// Codex/Claude receive matching guidance on when to fire the tool and
// how the {url, prompt} pair is consumed. Today (Phase A.1) Execute
// still returns the raw body; Phase A.2+ wires HTML→markdown conversion
// and a secondary-model extract driven by `prompt`. The description is
// kept parity-accurate now so the model's tool-use decisions don't
// flip when the internals upgrade.
// Reference: /Users/stello/claude-code-src/src/tools/WebFetchTool/prompt.ts:3-21.
func (t *WebFetchTool) Description() string {
	return "Fetches content from a specified URL and processes it using an AI model. " +
		"Takes a URL and a prompt as input, fetches the URL content, converts HTML to " +
		"markdown, and processes the content with the prompt using a small, fast model. " +
		"Use this tool when you need to retrieve and analyze web content. HTTP URLs are " +
		"upgraded to HTTPS automatically."
}

func (t *WebFetchTool) Schema() json.RawMessage {
	return Object(map[string]Property{
		"url":    String("The URL to fetch."),
		"prompt": String("What information to extract from the page. Optional today; required for Phase A.2+ secondary-model extraction."),
	}, []string{"url"})
}

func (t *WebFetchTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *WebFetchTool) Reversible() bool { return true }

func (t *WebFetchTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *WebFetchTool) Scope(params json.RawMessage) ToolScope {
	var p webFetchParams
	if err := json.Unmarshal(params, &p); err != nil {
		return ConservativeScope()
	}
	return ToolScope{Network: true}
}

type webFetchParams struct {
	URL    string `json:"url"`
	Prompt string `json:"prompt,omitempty"`
}

func (t *WebFetchTool) Execute(ctx context.Context, params json.RawMessage) (*Result, error) {
	var p webFetchParams
	if err := json.Unmarshal(params, &p); err != nil {
		return ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
	}
	if p.URL == "" {
		return ErrorResult("url must not be empty"), nil
	}

	if entry, ok := t.cache.Get(p.URL); ok {
		return &Result{Output: entry.output}, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL, nil)
	if err != nil {
		return ErrorResult(fmt.Sprintf("web_fetch: bad URL: %v", err)), nil
	}
	req.Header.Set("User-Agent", "elnath/0.1")

	resp, err := t.client.Do(req)
	if err != nil {
		return ErrorResult(fmt.Sprintf("web_fetch: %v", err)), nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		return ErrorResult(fmt.Sprintf("web_fetch: read body: %v", err)), nil
	}

	// Phase A.4: a 3xx here means sameHostRedirectChecker returned
	// ErrUseLastResponse (cross-host hop). Surface the blocked redirect so
	// the caller can decide whether to re-fetch the new origin explicitly,
	// and keep this non-terminal result out of the LRU — a 15-min cache hit
	// on a "blocked" outcome would lock every retry into the same refusal.
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		loc := resp.Header.Get("Location")
		if loc != "" {
			target := loc
			if u, err := resp.Request.URL.Parse(loc); err == nil {
				target = u.String()
			}
			msg := fmt.Sprintf("[web_fetch: cross-host redirect blocked]\nfrom: %s\nto: %s\nstatus: %d",
				p.URL, target, resp.StatusCode)
			return &Result{Output: msg}, nil
		}
	}

	if resp.StatusCode >= 400 {
		return ErrorResult(fmt.Sprintf("web_fetch: HTTP %d\n%s", resp.StatusCode, body)), nil
	}

	contentType := resp.Header.Get("Content-Type")
	output := string(body)
	if isHTMLContentType(contentType) {
		markdown, err := getHTMLConverter().ConvertString(output)
		if err != nil {
			return ErrorResult(fmt.Sprintf("web_fetch: html→markdown: %v", err)), nil
		}
		output = markdown
	}
	output = truncateMarkdown(output)

	// Phase A.5: when the caller supplies a prompt, hand the markdown to a
	// secondary model for focused extraction — unless the Claude Code
	// fast path fires (preapproved host + server-provided text/markdown +
	// body under the 100K cap), in which case the raw markdown is the
	// best answer a documentation query can get. Reference:
	// /Users/stello/claude-code-src/src/tools/WebFetchTool/WebFetchTool.ts:261-278.
	// Secondary-model failure surfaces as an error result and keeps the
	// entry out of the cache so a retry isn't locked into a 15-min miss.
	if p.Prompt != "" {
		isPreapproved := isPreapprovedURL(p.URL)
		fastPath := isPreapproved && isMarkdownContentType(contentType) && len(output) < webFetchMaxMarkdownLen
		if !fastPath {
			extracted, err := t.secondary.Extract(ctx, output, p.Prompt, isPreapproved)
			if err != nil {
				return ErrorResult(fmt.Sprintf("web_fetch: secondary extract: %v", err)), nil
			}
			output = extracted
		}
	}

	t.cache.Add(p.URL, webFetchCacheEntry{output: output})

	return &Result{Output: output}, nil
}

// WebSearchTool searches the web via DuckDuckGo HTML.
type WebSearchTool struct {
	client *http.Client
}

func NewWebSearchTool() *WebSearchTool {
	return &WebSearchTool{
		client: &http.Client{Timeout: webFetchTimeout},
	}
}

func (t *WebSearchTool) Name() string        { return "web_search" }
func (t *WebSearchTool) Description() string { return "Search the web via DuckDuckGo." }

func (t *WebSearchTool) Schema() json.RawMessage {
	return Object(map[string]Property{
		"query": String("Search query."),
	}, []string{"query"})
}

func (t *WebSearchTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *WebSearchTool) Reversible() bool { return true }

func (t *WebSearchTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *WebSearchTool) Scope(params json.RawMessage) ToolScope {
	var p webSearchParams
	if err := json.Unmarshal(params, &p); err != nil {
		return ConservativeScope()
	}
	return ToolScope{Network: true}
}

type webSearchParams struct {
	Query string `json:"query"`
}

var resultLinkRe = regexp.MustCompile(`<a[^>]+class="result__a"[^>]*href="([^"]*)"[^>]*>([^<]*)`)

func (t *WebSearchTool) Execute(ctx context.Context, params json.RawMessage) (*Result, error) {
	var p webSearchParams
	if err := json.Unmarshal(params, &p); err != nil {
		return ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
	}
	if p.Query == "" {
		return ErrorResult("query must not be empty"), nil
	}

	searchURL := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(p.Query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return ErrorResult(fmt.Sprintf("web_search: bad request: %v", err)), nil
	}
	req.Header.Set("User-Agent", "elnath/0.1")

	resp, err := t.client.Do(req)
	if err != nil {
		return ErrorResult(fmt.Sprintf("web_search: %v", err)), nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ErrorResult(fmt.Sprintf("web_search: read body: %v", err)), nil
	}

	if resp.StatusCode >= 400 {
		return ErrorResult(fmt.Sprintf("web_search: HTTP %d", resp.StatusCode)), nil
	}

	matches := resultLinkRe.FindAllStringSubmatch(string(body), 5)
	if len(matches) == 0 {
		return &Result{Output: "No results found."}, nil
	}

	var sb strings.Builder
	for i, m := range matches {
		title := strings.TrimSpace(m[2])
		href := m[1]
		fmt.Fprintf(&sb, "%d. %s\n   %s\n", i+1, title, href)
	}
	return &Result{Output: sb.String()}, nil
}
