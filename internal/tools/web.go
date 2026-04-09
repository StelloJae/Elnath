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
	"time"
)

const webFetchTimeout = 30 * time.Second

// WebFetchTool fetches the body of a URL via HTTP GET.
type WebFetchTool struct {
	client *http.Client
}

func NewWebFetchTool() *WebFetchTool {
	return &WebFetchTool{
		client: &http.Client{Timeout: webFetchTimeout},
	}
}

func (t *WebFetchTool) Name() string        { return "web_fetch" }
func (t *WebFetchTool) Description() string { return "Fetch the content of a URL via HTTP GET." }

func (t *WebFetchTool) Schema() json.RawMessage {
	return Object(map[string]Property{
		"url": String("The URL to fetch."),
	}, []string{"url"})
}

type webFetchParams struct {
	URL string `json:"url"`
}

func (t *WebFetchTool) Execute(ctx context.Context, params json.RawMessage) (*Result, error) {
	var p webFetchParams
	if err := json.Unmarshal(params, &p); err != nil {
		return ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
	}
	if p.URL == "" {
		return ErrorResult("url must not be empty"), nil
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

	if resp.StatusCode >= 400 {
		return ErrorResult(fmt.Sprintf("web_fetch: HTTP %d\n%s", resp.StatusCode, body)), nil
	}

	return &Result{Output: string(body)}, nil
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
