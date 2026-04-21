package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"
)

func TestWebFetchToolMeta(t *testing.T) {
	tool := NewWebFetchTool()

	if got := tool.Name(); got != "web_fetch" {
		t.Errorf("Name() = %q, want %q", got, "web_fetch")
	}
	if got := tool.Description(); got == "" {
		t.Error("Description() returned empty string")
	}
	if schema := tool.Schema(); len(schema) == 0 {
		t.Error("Schema() returned empty JSON")
	}
}

func TestWebFetchToolExecute(t *testing.T) {
	ts200 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello from test server"))
	}))
	defer ts200.Close()

	ts500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer ts500.Close()

	tests := []struct {
		name       string
		params     any
		rawParams  []byte
		wantError  bool
		wantOutput string
	}{
		{
			name:       "successful 200 fetch",
			params:     map[string]any{"url": ts200.URL},
			wantError:  false,
			wantOutput: "hello from test server",
		},
		{
			name:      "HTTP 500 returns error result",
			params:    map[string]any{"url": ts500.URL},
			wantError: true,
		},
		{
			name:      "empty URL returns error result",
			params:    map[string]any{"url": ""},
			wantError: true,
		},
		{
			name:      "invalid JSON params returns error result",
			rawParams: []byte("not json{{{"),
			wantError: true,
		},
		{
			name:      "invalid URL returns error result",
			params:    map[string]any{"url": "://bad-url"},
			wantError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tool := NewWebFetchTool()

			var params []byte
			if tc.rawParams != nil {
				params = tc.rawParams
			} else {
				params = mustMarshal(t, tc.params)
			}

			res, err := tool.Execute(context.Background(), params)
			if err != nil {
				t.Fatalf("Execute returned unexpected Go error: %v", err)
			}
			if tc.wantError && !res.IsError {
				t.Errorf("expected error result, got output: %s", res.Output)
			}
			if !tc.wantError && res.IsError {
				t.Errorf("unexpected error result: %s", res.Output)
			}
			if tc.wantOutput != "" && !strings.Contains(res.Output, tc.wantOutput) {
				t.Errorf("output does not contain %q:\n%s", tc.wantOutput, res.Output)
			}
		})
	}
}

// TestWebFetchToolSchemaAcceptsPrompt pins the Claude Code parity schema
// for WebFetchTool: the tool accepts both a url (required) and a prompt
// (optional today; Phase A.2+ uses it to drive a secondary-model extract
// over the fetched markdown). Before Phase A the schema carried only
// {url}; this test guards against regressing that.
//
// Reference: /Users/stello/claude-code-src/src/tools/WebFetchTool/
// WebFetchTool.ts:24-29 (inputSchema with url + prompt).
func TestWebFetchToolSchemaAcceptsPrompt(t *testing.T) {
	tool := NewWebFetchTool()
	schema := tool.Schema()

	var decoded map[string]any
	if err := json.Unmarshal(schema, &decoded); err != nil {
		t.Fatalf("Schema() JSON invalid: %v", err)
	}
	props, ok := decoded["properties"].(map[string]any)
	if !ok {
		t.Fatalf(`schema missing "properties": %v`, decoded)
	}
	if _, hasURL := props["url"]; !hasURL {
		t.Error(`schema properties missing "url" field`)
	}
	if _, hasPrompt := props["prompt"]; !hasPrompt {
		t.Error(`schema properties missing "prompt" field (required for Phase A Claude Code parity)`)
	}
}

// TestWebFetchToolExecuteAcceptsPrompt confirms the tool tolerates a
// {url, prompt} call without Phase A.2+ markdown extraction wired —
// keeps Phase A.1 backwards-compatible with raw-body callers while
// unlocking the later-phase plumbing.
func TestWebFetchToolExecuteAcceptsPrompt(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("page body"))
	}))
	defer ts.Close()

	tool := NewWebFetchTool()
	params := mustMarshal(t, map[string]any{
		"url":    ts.URL,
		"prompt": "Summarize the main content.",
	})
	res, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("Execute returned unexpected Go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Output)
	}
	if !strings.Contains(res.Output, "page body") {
		t.Errorf("output missing page body:\n%s", res.Output)
	}
}

// TestWebFetchTool_ConvertsHTMLToMarkdown pins Phase A.2: when the
// server reports text/html, Execute converts the HTML body to markdown
// before returning. Mirrors Claude Code's Turndown stage
// (/Users/stello/claude-code-src/src/tools/WebFetchTool/utils.ts:456-458)
// so the LLM consumes markdown, not raw tags.
func TestWebFetchTool_ConvertsHTMLToMarkdown(t *testing.T) {
	const htmlBody = `<html><head><title>Ignored</title></head><body>` +
		`<h1>Hello</h1>` +
		`<p>World</p>` +
		`<a href="https://example.com">link</a>` +
		`</body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(htmlBody))
	}))
	defer ts.Close()

	tool := NewWebFetchTool()
	res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{"url": ts.URL}))
	if err != nil {
		t.Fatalf("Execute returned unexpected Go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Output)
	}
	if strings.Contains(res.Output, "<h1>") || strings.Contains(res.Output, "<p>") {
		t.Errorf("output still contains raw HTML tags:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "# Hello") {
		t.Errorf("output missing markdown H1 (# Hello):\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "World") {
		t.Errorf("output missing paragraph text:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "[link](https://example.com)") {
		t.Errorf("output missing markdown link form:\n%s", res.Output)
	}
}

// TestWebFetchTool_PreservesNonHTMLContent pins Phase A.2 scope fence:
// non-HTML content types (text/plain, JSON, etc.) pass through as-is.
// Matches Claude Code's else-branch in utils.ts:459-466.
func TestWebFetchTool_PreservesNonHTMLContent(t *testing.T) {
	const rawBody = "plain text content, line 1\nline 2"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(rawBody))
	}))
	defer ts.Close()

	tool := NewWebFetchTool()
	res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{"url": ts.URL}))
	if err != nil {
		t.Fatalf("Execute returned unexpected Go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Output)
	}
	if res.Output != rawBody {
		t.Errorf("non-HTML content should be preserved raw:\ngot:  %q\nwant: %q", res.Output, rawBody)
	}
}

// TestWebFetchTool_TruncatesLargeMarkdown pins Phase A.2's 100K-char cap
// (Claude Code MAX_MARKDOWN_LENGTH, utils.ts:128) with the truncation
// marker appended. Prevents downstream model from blowing its context.
func TestWebFetchTool_TruncatesLargeMarkdown(t *testing.T) {
	// Generate HTML whose markdown conversion well exceeds 100 000 bytes.
	// Each <p> block yields roughly 205 bytes post-conversion
	// (200 chars + "\n\n"); 700 blocks → ~143 500 bytes of markdown,
	// comfortably above the cap and under the 1 MiB body limit.
	var sb strings.Builder
	sb.WriteString("<html><body>")
	for i := 0; i < 700; i++ {
		sb.WriteString("<p>")
		sb.WriteString(strings.Repeat("abcd ", 40)) // 200 chars
		sb.WriteString("</p>")
	}
	sb.WriteString("</body></html>")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(sb.String()))
	}))
	defer ts.Close()

	tool := NewWebFetchTool()
	res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{"url": ts.URL}))
	if err != nil {
		t.Fatalf("Execute returned unexpected Go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Output)
	}
	const marker = "[Content truncated due to length...]"
	if !strings.Contains(res.Output, marker) {
		t.Errorf("large output missing truncation marker %q, len=%d", marker, len(res.Output))
	}
	// 100 000 byte cap + leading "\n\n" + marker text. Allow a small slack
	// for rune-boundary cuts (at most 3 UTF-8 continuation bytes).
	maxLen := 100_000 + len("\n\n"+marker) + 4
	if len(res.Output) > maxLen {
		t.Errorf("output length %d exceeds expected cap %d", len(res.Output), maxLen)
	}
}

// TestWebFetchTool_CachesSuccessfulFetch pins Phase A.3: a successful
// fetch lands in the LRU cache so a repeated identical URL skips the
// network round-trip. Mirrors Claude Code's URL_CACHE Get/Set pattern
// in /Users/stello/claude-code-src/src/tools/WebFetchTool/utils.ts:356-481.
func TestWebFetchTool_CachesSuccessfulFetch(t *testing.T) {
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("cacheable body"))
	}))
	defer ts.Close()

	// Isolated cache so the test never leaks into or out of the
	// process-wide singleton returned by getSharedWebFetchCache.
	cache := expirable.NewLRU[string, webFetchCacheEntry](10, nil, 15*time.Minute)
	tool := NewWebFetchTool(withWebFetchCache(cache))
	params := mustMarshal(t, map[string]any{"url": ts.URL})

	res1, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("first Execute error: %v", err)
	}
	if res1.IsError {
		t.Fatalf("first Execute returned error result: %s", res1.Output)
	}
	res2, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("second Execute error: %v", err)
	}
	if res2.IsError {
		t.Fatalf("second Execute returned error result: %s", res2.Output)
	}

	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("expected 1 upstream hit with cache warm, got %d", got)
	}
	if res1.Output != res2.Output {
		t.Errorf("cached output differs:\n  first:  %q\n  second: %q", res1.Output, res2.Output)
	}
}

// TestWebFetchTool_EvictsExpiredEntries guards the TTL behaviour of the
// cache: once an entry has aged past its TTL, the next call must fall
// through to the network. Uses a 50 ms TTL so the test runs in well
// under a second without flakiness.
func TestWebFetchTool_EvictsExpiredEntries(t *testing.T) {
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("body"))
	}))
	defer ts.Close()

	cache := expirable.NewLRU[string, webFetchCacheEntry](10, nil, 50*time.Millisecond)
	tool := NewWebFetchTool(withWebFetchCache(cache))
	params := mustMarshal(t, map[string]any{"url": ts.URL})

	if _, err := tool.Execute(context.Background(), params); err != nil {
		t.Fatalf("warm Execute error: %v", err)
	}
	time.Sleep(120 * time.Millisecond) // comfortably past TTL
	if _, err := tool.Execute(context.Background(), params); err != nil {
		t.Fatalf("post-expiry Execute error: %v", err)
	}

	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("expected 2 upstream hits after TTL expiry, got %d", got)
	}
}

// TestWebFetchTool_FollowsSameHostRedirect is a regression guard for Phase A.4:
// even after CheckRedirect adds a same-host policy, a 302 between two paths
// on the same origin must still be followed transparently so the caller sees
// the final body. Mirrors the happy path of Claude Code's isPermittedRedirect
// (/Users/stello/claude-code-src/src/tools/WebFetchTool/utils.ts:212-243).
//
// Note: before the CheckRedirect hook lands, Go's default http.Client already
// follows same-host redirects — so on RED this passes by accident. It is kept
// so the new hook cannot regress by over-rejecting legitimate same-host hops.
func TestWebFetchTool_FollowsSameHostRedirect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/final", http.StatusFound)
	})
	mux.HandleFunc("/final", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("final content"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	cache := expirable.NewLRU[string, webFetchCacheEntry](10, nil, 15*time.Minute)
	tool := NewWebFetchTool(withWebFetchCache(cache))
	res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{"url": ts.URL + "/start"}))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Output)
	}
	if !strings.Contains(res.Output, "final content") {
		t.Errorf("same-host redirect should surface final body:\n%s", res.Output)
	}
}

// TestWebFetchTool_BlocksCrossHostRedirect is the true RED for Phase A.4:
// without a CheckRedirect hook, Go's default http.Client transparently
// follows a cross-host 302 and returns serverB's body. The expected Phase
// A.4 behaviour is to abort at the 302, never touch serverB, and surface a
// structured "cross-host redirect blocked" message so the caller can decide
// whether to fetch the new host explicitly. The blocked result must also
// stay out of the LRU: caching a non-terminal response would freeze the
// LLM into refusing every retry for 15 minutes.
// Reference: /Users/stello/claude-code-src/src/tools/WebFetchTool/utils.ts:262-329
// (getWithPermittedRedirects returns a RedirectInfo on cross-host hops).
func TestWebFetchTool_BlocksCrossHostRedirect(t *testing.T) {
	var serverBHits int32
	serverB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&serverBHits, 1)
		w.Write([]byte("should not be reached"))
	}))
	defer serverB.Close()

	serverA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, serverB.URL+"/target", http.StatusFound)
	}))
	defer serverA.Close()

	cache := expirable.NewLRU[string, webFetchCacheEntry](10, nil, 15*time.Minute)
	tool := NewWebFetchTool(withWebFetchCache(cache))
	res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{"url": serverA.URL}))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.IsError {
		t.Fatalf("cross-host redirect should surface as non-error result: %s", res.Output)
	}
	if strings.Contains(res.Output, "should not be reached") {
		t.Errorf("cross-host redirect leaked serverB body into output:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "cross-host redirect blocked") {
		t.Errorf("output missing redirect-blocked marker:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, serverA.URL) {
		t.Errorf("output missing original URL %q:\n%s", serverA.URL, res.Output)
	}
	if !strings.Contains(res.Output, serverB.URL) {
		t.Errorf("output missing target URL %q:\n%s", serverB.URL, res.Output)
	}
	if got := atomic.LoadInt32(&serverBHits); got != 0 {
		t.Errorf("serverB was hit %d times — cross-host redirect was followed", got)
	}
	if _, ok := cache.Get(serverA.URL); ok {
		t.Error("cross-host redirect outcome must not be cached (non-terminal result)")
	}
}

// TestIsSameHostRedirect_AllowsWwwCanonicalization pins the stripWww clause
// of the same-host check: example.com ↔ www.example.com are treated as the
// same origin, while scheme, port, credentials, and truly different hosts
// remain rejections. Mirror of
// /Users/stello/claude-code-src/src/tools/WebFetchTool/utils.ts:220-239.
func TestIsSameHostRedirect_AllowsWwwCanonicalization(t *testing.T) {
	cases := []struct {
		name   string
		orig   string
		target string
		allow  bool
	}{
		{"strip www to bare", "https://www.example.com/a", "https://example.com/b", true},
		{"add www to bare", "https://example.com/a", "https://www.example.com/b", true},
		{"same host path change", "https://example.com/a", "https://example.com/b", true},
		{"cross host", "https://example.com/a", "https://evil.com/b", false},
		{"protocol downgrade", "https://example.com/a", "http://example.com/b", false},
		{"port change", "https://example.com/a", "https://example.com:8443/b", false},
		{"credentials injected", "https://example.com/a", "https://user:pass@example.com/b", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSameHostRedirect(tc.orig, tc.target); got != tc.allow {
				t.Errorf("isSameHostRedirect(%q, %q) = %v, want %v", tc.orig, tc.target, got, tc.allow)
			}
		})
	}
}

func TestWebSearchToolMeta(t *testing.T) {
	tool := NewWebSearchTool()

	if got := tool.Name(); got != "web_search" {
		t.Errorf("Name() = %q, want %q", got, "web_search")
	}
	if got := tool.Description(); got == "" {
		t.Error("Description() returned empty string")
	}
	if schema := tool.Schema(); len(schema) == 0 {
		t.Error("Schema() returned empty JSON")
	}
}

func TestWebSearchToolExecute(t *testing.T) {
	// Fake DuckDuckGo HTML response with result links.
	fakeHTML := `<html><body>
		<a class="result__a" href="https://example.com/1">First Result</a>
		<a class="result__a" href="https://example.com/2">Second Result</a>
	</body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(fakeHTML))
	}))
	defer ts.Close()

	tool := &WebSearchTool{client: ts.Client()}
	// Override the search URL by using a query that the tool will encode.
	// We need to point the tool at our test server instead of DuckDuckGo,
	// so we test the parsing logic with a direct fetch to the test server.
	fetchTool := &WebSearchTool{client: &http.Client{Transport: &rewriteTransport{ts.URL}}}

	t.Run("successful search", func(t *testing.T) {
		res, err := fetchTool.Execute(context.Background(), mustMarshal(t, map[string]any{"query": "test"}))
		if err != nil {
			t.Fatalf("Execute returned unexpected Go error: %v", err)
		}
		if res.IsError {
			t.Fatalf("unexpected error result: %s", res.Output)
		}
		if !strings.Contains(res.Output, "First Result") {
			t.Errorf("output missing 'First Result': %s", res.Output)
		}
		if !strings.Contains(res.Output, "https://example.com/1") {
			t.Errorf("output missing URL: %s", res.Output)
		}
	})

	t.Run("empty query", func(t *testing.T) {
		res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{"query": ""}))
		if err != nil {
			t.Fatalf("Execute returned unexpected Go error: %v", err)
		}
		if !res.IsError {
			t.Errorf("expected error result for empty query, got: %s", res.Output)
		}
	})

	t.Run("invalid params", func(t *testing.T) {
		res, err := tool.Execute(context.Background(), []byte("not json{{{"))
		if err != nil {
			t.Fatalf("Execute returned unexpected Go error: %v", err)
		}
		if !res.IsError {
			t.Errorf("expected error result for bad JSON, got: %s", res.Output)
		}
	})
}

// rewriteTransport redirects all requests to the test server.
type rewriteTransport struct {
	baseURL string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	parsed, _ := http.NewRequest(req.Method, t.baseURL+req.URL.Path+"?"+req.URL.RawQuery, req.Body)
	parsed.Header = req.Header
	return http.DefaultTransport.RoundTrip(parsed)
}

// mockSecondaryCaller is the scripted llm.SecondaryModelCaller used in the
// Phase A.5 R2-R6 suite. It records the argument triple (markdown, prompt,
// isPreapproved) on every call so tests can pin that the WebFetch Execute
// path forwards the correct values, and lets each test dial in a canned
// return value (or error) without standing up a real LLM client.
type mockSecondaryCaller struct {
	mu           sync.Mutex
	calls        int
	lastMarkdown string
	lastPrompt   string
	lastPreapp   bool
	returnVal    string
	returnErr    error
}

func (m *mockSecondaryCaller) Extract(_ context.Context, markdown, prompt string, isPreapproved bool) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.lastMarkdown = markdown
	m.lastPrompt = prompt
	m.lastPreapp = isPreapproved
	if m.returnErr != nil {
		return "", m.returnErr
	}
	return m.returnVal, nil
}

func (m *mockSecondaryCaller) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// TestWebFetchTool_SkipsSecondaryWhenPromptEmpty (R2) pins the "no prompt
// → no secondary round-trip" invariant. Callers that want raw markdown
// should not pay an LLM hop just because a SecondaryModelCaller is wired.
func TestWebFetchTool_SkipsSecondaryWhenPromptEmpty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("body"))
	}))
	defer ts.Close()

	mock := &mockSecondaryCaller{returnVal: "must-not-appear"}
	cache := expirable.NewLRU[string, webFetchCacheEntry](10, nil, 15*time.Minute)
	tool := NewWebFetchTool(withWebFetchCache(cache), withSecondary(mock))

	res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{"url": ts.URL}))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Output)
	}
	if got := mock.CallCount(); got != 0 {
		t.Errorf("secondary must not be consulted when prompt is empty; calls=%d", got)
	}
	if res.Output != "body" {
		t.Errorf("output should equal raw body; got %q", res.Output)
	}
}

// TestWebFetchTool_SkipsSecondaryForNoopCaller (R3) pins the default
// construction contract: a tool built without an explicit SecondaryModelCaller
// must behave identically to Phase A.4 even when the caller supplies a
// prompt. Guards against accidentally wiring a silent LLM call into every
// web_fetch invocation.
func TestWebFetchTool_SkipsSecondaryForNoopCaller(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("raw body"))
	}))
	defer ts.Close()

	cache := expirable.NewLRU[string, webFetchCacheEntry](10, nil, 15*time.Minute)
	tool := NewWebFetchTool(withWebFetchCache(cache))

	res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"url":    ts.URL,
		"prompt": "extract price",
	}))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Output)
	}
	if res.Output != "raw body" {
		t.Errorf("default noop caller should return body verbatim; got %q", res.Output)
	}
}

// TestWebFetchTool_AppliesPromptViaSecondary (R4) is the core Phase A.5
// contract: when a prompt is present and the caller wires a real secondary,
// Execute must hand off post-HTML-convert markdown + the exact prompt +
// the correctly computed isPreapproved flag, and the caller's return value
// must replace the raw markdown in res.Output.
func TestWebFetchTool_AppliesPromptViaSecondary(t *testing.T) {
	const html = `<html><body><h1>Title</h1><p>AAPL $180</p></body></html>`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(html))
	}))
	defer ts.Close()

	mock := &mockSecondaryCaller{returnVal: "AAPL: $180"}
	cache := expirable.NewLRU[string, webFetchCacheEntry](10, nil, 15*time.Minute)
	tool := NewWebFetchTool(withWebFetchCache(cache), withSecondary(mock))

	res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"url":    ts.URL,
		"prompt": "extract stock prices",
	}))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Output)
	}
	if got := mock.CallCount(); got != 1 {
		t.Errorf("secondary should be consulted exactly once; calls=%d", got)
	}
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.lastPrompt != "extract stock prices" {
		t.Errorf("secondary received wrong prompt: %q", mock.lastPrompt)
	}
	if !strings.Contains(mock.lastMarkdown, "AAPL $180") {
		t.Errorf("secondary received wrong markdown (missing body text): %q", mock.lastMarkdown)
	}
	if strings.Contains(mock.lastMarkdown, "<h1>") {
		t.Errorf("secondary should receive post-convert markdown, not raw HTML: %q", mock.lastMarkdown)
	}
	if mock.lastPreapp {
		t.Errorf("httptest host is not preapproved; isPreapproved should be false")
	}
	if res.Output != "AAPL: $180" {
		t.Errorf("output should equal secondary return value; got %q", res.Output)
	}
}

// TestWebFetchTool_SecondaryCallerErrorSurfaces (R5) guarantees that a
// failing secondary bubbles up as an IsError=true result and keeps the
// entry out of the LRU. Caching a failed extract would freeze every retry
// for 15 minutes into the same error — worse UX than a fresh re-fetch.
func TestWebFetchTool_SecondaryCallerErrorSurfaces(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("body"))
	}))
	defer ts.Close()

	mock := &mockSecondaryCaller{returnErr: errors.New("budget exceeded")}
	cache := expirable.NewLRU[string, webFetchCacheEntry](10, nil, 15*time.Minute)
	tool := NewWebFetchTool(withWebFetchCache(cache), withSecondary(mock))

	res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"url":    ts.URL,
		"prompt": "summarize",
	}))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("secondary error must surface as IsError=true; output=%q", res.Output)
	}
	if !strings.Contains(res.Output, "budget exceeded") {
		t.Errorf("error result should carry underlying message: %q", res.Output)
	}
	if _, ok := cache.Get(ts.URL); ok {
		t.Error("failed secondary extraction must not leave an entry in the LRU")
	}
}

// TestWebFetchTool_PreapprovedMarkdownSkipsSecondary (R6) pins the Claude
// Code fast path (WebFetchTool.ts:264-269): when a preapproved host
// serves text/markdown directly under the 100K cap, the secondary is
// skipped and the raw markdown reaches the caller verbatim — the most
// faithful answer for a documentation query. The test uses
// rewriteTransport so the caller-visible hostname stays "go.dev"
// (preapproved) while the actual bytes come from the local server.
func TestWebFetchTool_PreapprovedMarkdownSkipsSecondary(t *testing.T) {
	const markdownBody = "# Hello\n\nPreapproved doc content."
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Write([]byte(markdownBody))
	}))
	defer ts.Close()

	mock := &mockSecondaryCaller{returnVal: "must-not-be-returned"}
	cache := expirable.NewLRU[string, webFetchCacheEntry](10, nil, 15*time.Minute)
	tool := NewWebFetchTool(withWebFetchCache(cache), withSecondary(mock))
	tool.client = &http.Client{
		Timeout:   webFetchTimeout,
		Transport: &rewriteTransport{baseURL: ts.URL},
	}

	res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"url":    "https://go.dev/doc",
		"prompt": "what does it say?",
	}))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Output)
	}
	if got := mock.CallCount(); got != 0 {
		t.Errorf("fast path should skip secondary; calls=%d", got)
	}
	if !strings.Contains(res.Output, "Preapproved doc content") {
		t.Errorf("fast path should return markdown verbatim; got %q", res.Output)
	}
	if strings.Contains(res.Output, "must-not-be-returned") {
		t.Errorf("mock return value leaked into output despite fast path: %q", res.Output)
	}
}
