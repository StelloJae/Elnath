package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
