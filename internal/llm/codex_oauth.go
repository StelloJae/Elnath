package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/stello/elnath/internal/userfacingerr"
)

const (
	codexOAuthBaseURL    = "https://chatgpt.com/backend-api/codex/responses"
	codexOAuthRefreshURL = "https://auth.openai.com/oauth/token"
	codexOAuthClientID   = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexOAuthTimeout    = 120 * time.Second
)

// codexOAuthAuthFile mirrors ~/.codex/auth.json written by the Codex CLI.
type codexOAuthAuthFile struct {
	AuthMode string `json:"auth_mode"`
	Tokens   struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		AccountID    string `json:"account_id"`
	} `json:"tokens"`
}

type codexOAuthStatusError struct {
	code int
	msg  string
}

func (e codexOAuthStatusError) Error() string { return e.msg }

// CodexOAuthProvider implements Provider using ChatGPT Backend OAuth tokens.
type CodexOAuthProvider struct {
	authPath string
	model    string
	client   *http.Client
}

// CodexOAuthOption configures a CodexOAuthProvider.
type CodexOAuthOption func(*CodexOAuthProvider)

// WithCodexOAuthTimeout sets the HTTP client timeout.
func WithCodexOAuthTimeout(d time.Duration) CodexOAuthOption {
	return func(p *CodexOAuthProvider) { p.client = &http.Client{Timeout: d} }
}

// DefaultCodexAuthPath returns the default path to the Codex CLI auth file.
func DefaultCodexAuthPath() string {
	home, _ := os.UserHomeDir()
	return home + "/.codex/auth.json"
}

// NewCodexOAuthProvider constructs a Codex OAuth provider.
func NewCodexOAuthProvider(model string, opts ...CodexOAuthOption) *CodexOAuthProvider {
	p := &CodexOAuthProvider{
		authPath: DefaultCodexAuthPath(),
		model:    model,
		client:   &http.Client{Timeout: codexOAuthTimeout},
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

func (p *CodexOAuthProvider) Name() string { return "codex" }

func (p *CodexOAuthProvider) Models() []ModelInfo {
	return []ModelInfo{
		{ID: "o3", Name: "o3", MaxTokens: 100000, ContextWindow: 200_000},
		{ID: "o4-mini", Name: "o4-mini", MaxTokens: 100000, ContextWindow: 200_000},
		{ID: "gpt-4.1", Name: "GPT-4.1", MaxTokens: 32768, ContextWindow: 1_047_576},
		{ID: "codex-mini-latest", Name: "Codex Mini", MaxTokens: 100000, ContextWindow: 200_000},
	}
}

// Stream sends a streaming request via the Codex OAuth endpoint.
func (p *CodexOAuthProvider) Stream(ctx context.Context, req ChatRequest, cb func(StreamEvent)) error {
	auth, err := p.loadAuth()
	if err != nil {
		return fmt.Errorf("codex: load auth: %w", err)
	}

	return p.streamWithRefresh(ctx, auth, req, cb)
}

// Chat sends a non-streaming request by accumulating the stream.
func (p *CodexOAuthProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	var textParts []string
	var toolCalls []ToolCall
	var usage UsageStats
	pending := map[string]*ToolCall{}

	err := p.Stream(ctx, req, func(ev StreamEvent) {
		switch ev.Type {
		case EventTextDelta:
			if ev.Content != "" {
				textParts = append(textParts, ev.Content)
			}
		case EventToolUseStart:
			if ev.ToolCall != nil {
				pending[ev.ToolCall.ID] = &ToolCall{ID: ev.ToolCall.ID, Name: ev.ToolCall.Name}
			}
		case EventToolUseDelta:
			if ev.ToolCall != nil {
				if tc, ok := pending[ev.ToolCall.ID]; ok {
					tc.Input += ev.ToolCall.Input
				}
			}
		case EventToolUseDone:
			if ev.ToolCall != nil {
				if tc, ok := pending[ev.ToolCall.ID]; ok {
					toolCalls = append(toolCalls, *tc)
					delete(pending, ev.ToolCall.ID)
				}
			}
		case EventDone:
			if ev.Usage != nil {
				usage = *ev.Usage
			}
		}
	})
	if err != nil {
		return nil, err
	}

	return &ChatResponse{
		Content:   strings.Join(textParts, ""),
		ToolCalls: toolCalls,
		Usage:     Usage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens},
	}, nil
}

// streamWithRefresh tries the request; on 401, refreshes the token and retries.
func (p *CodexOAuthProvider) streamWithRefresh(ctx context.Context, auth codexOAuthAuthFile, req ChatRequest, cb func(StreamEvent)) error {
	err := p.streamOnce(ctx, auth, req, cb)
	if err == nil {
		return nil
	}

	var statusErr codexOAuthStatusError
	if errors.As(err, &statusErr) && statusErr.code == http.StatusUnauthorized {
		refreshed, refreshErr := p.refreshAuth(ctx, auth)
		if refreshErr != nil {
			inner := fmt.Errorf("codex: refresh failed (re-run `codex auth` to re-authenticate): %w", refreshErr)
			return userfacingerr.Wrap(userfacingerr.ELN002, inner, "codex refresh")
		}
		return p.streamOnce(ctx, refreshed, req, cb)
	}
	return err
}

// streamOnce makes a single SSE-streaming POST to the Codex responses endpoint.
func (p *CodexOAuthProvider) streamOnce(ctx context.Context, auth codexOAuthAuthFile, req ChatRequest, cb func(StreamEvent)) error {
	body, err := buildCodexRequest(req, p.model)
	if err != nil {
		return fmt.Errorf("codex: build request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, codexOAuthBaseURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("codex: new request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+auth.Tokens.AccessToken)
	httpReq.Header.Set("chatgpt-account-id", auth.Tokens.AccountID)
	httpReq.Header.Set("OpenAI-Beta", "responses=experimental")
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("codex: http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return codexOAuthStatusError{
			code: resp.StatusCode,
			msg:  fmt.Sprintf("codex: status %d: %s", resp.StatusCode, errBody),
		}
	}

	return parseCodexSSE(resp.Body, cb)
}

// buildCodexRequest constructs the Codex Responses API payload.
func buildCodexRequest(req ChatRequest, defaultModel string) ([]byte, error) {
	model := req.Model
	if model == "" {
		model = defaultModel
	}

	payload := map[string]any{
		"model":  model,
		"stream": true,
		"store":  false,
	}

	instructions := req.System
	if instructions == "" {
		instructions = "You are a helpful assistant."
	}
	payload["instructions"] = instructions

	// Convert messages to Codex input format.
	var input []any
	for _, msg := range req.Messages {
		var textParts []map[string]any
		for _, block := range msg.Content {
			switch b := block.(type) {
			case TextBlock:
				textParts = append(textParts, map[string]any{
					"type": "input_text",
					"text": b.Text,
				})
			case ToolResultBlock:
				if len(textParts) > 0 {
					input = append(input, buildRoleMessage(msg.Role, textParts))
					textParts = nil
				}
				input = append(input, map[string]any{
					"type":    "function_call_output",
					"call_id": b.ToolUseID,
					"output":  b.Content,
				})
			case ToolUseBlock:
				if len(textParts) > 0 {
					input = append(input, buildRoleMessage(msg.Role, textParts))
					textParts = nil
				}
				input = append(input, map[string]any{
					"type":      "function_call",
					"call_id":   b.ID,
					"name":      b.Name,
					"arguments": string(b.Input),
				})
			}
		}
		if len(textParts) > 0 {
			input = append(input, buildRoleMessage(msg.Role, textParts))
		}
	}
	payload["input"] = input

	// Convert tools to Responses API format.
	if len(req.Tools) > 0 {
		var tools []map[string]any
		for _, t := range req.Tools {
			tool := map[string]any{
				"type": "function",
				"name": t.Name,
			}
			if t.Description != "" {
				tool["description"] = t.Description
			}
			if len(t.InputSchema) > 0 {
				var schema any
				if err := json.Unmarshal(t.InputSchema, &schema); err == nil {
					tool["parameters"] = schema
				}
			}
			tools = append(tools, tool)
		}
		payload["tools"] = tools
	}

	return json.Marshal(payload)
}

// parseCodexSSE reads the Codex Responses API SSE stream.
func parseCodexSSE(r io.Reader, cb func(StreamEvent)) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	dumpPath := os.Getenv("ELNATH_CODEX_SSE_DUMP")
	var dumpFile *os.File
	if dumpPath != "" {
		if f, err := os.OpenFile(dumpPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			dumpFile = f
			defer dumpFile.Close()
		}
	}

	type toolState struct {
		id    string
		name  string
		input strings.Builder
	}
	pendingTools := map[string]*toolState{}
	gotTextDelta := false
	var usage UsageStats

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		if data == "" || data == "[DONE]" {
			continue
		}
		if dumpFile != nil {
			_, _ = dumpFile.WriteString(data + "\n")
		}

		var event struct {
			Type     string `json:"type"`
			Delta    string `json:"delta"`
			Text     string `json:"text"`
			ItemID   string `json:"item_id"`
			CallID   string `json:"call_id"`
			Name     string `json:"name"`
			Response struct {
				Usage struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
				Output []struct {
					Type    string `json:"type"`
					ID      string `json:"id"`
					Name    string `json:"name"`
					CallID  string `json:"call_id"`
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
					Arguments string `json:"arguments"`
				} `json:"output"`
			} `json:"response"`
			Item *struct {
				Type      string `json:"type"`
				ID        string `json:"id"`
				Name      string `json:"name"`
				CallID    string `json:"call_id"`
				Arguments string `json:"arguments"`
			} `json:"item,omitempty"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		switch event.Type {
		case "response.output_text.delta":
			if event.Delta != "" {
				gotTextDelta = true
				cb(StreamEvent{Type: EventTextDelta, Content: event.Delta})
			}

		case "response.output_text.done":
			// Fallback: emit full text only when no prior deltas were received.
			if event.Text != "" && !gotTextDelta {
				cb(StreamEvent{Type: EventTextDelta, Content: event.Text})
			}

		case "response.function_call_arguments.delta":
			id := event.ItemID
			if id == "" {
				id = event.CallID
			}
			if ts, ok := pendingTools[id]; ok {
				ts.input.WriteString(event.Delta)
				cb(StreamEvent{
					Type: EventToolUseDelta,
					ToolCall: &ToolUseEvent{
						ID:    ts.id,
						Name:  ts.name,
						Input: event.Delta,
					},
				})
			}

		case "response.output_item.added":
			// A new output item (could be function_call). Codex may send it either
			// in event.item or embedded under response.output.
			if event.Item != nil && event.Item.Type == "function_call" {
				ts := &toolState{id: event.Item.CallID, name: event.Item.Name}
				pendingTools[event.Item.ID] = ts
				cb(StreamEvent{
					Type: EventToolUseStart,
					ToolCall: &ToolUseEvent{
						ID:   event.Item.CallID,
						Name: event.Item.Name,
					},
				})
				continue
			}
			for _, item := range event.Response.Output {
				if item.Type == "function_call" {
					ts := &toolState{id: item.CallID, name: item.Name}
					pendingTools[item.ID] = ts
					cb(StreamEvent{
						Type: EventToolUseStart,
						ToolCall: &ToolUseEvent{
							ID:   item.CallID,
							Name: item.Name,
						},
					})
				}
			}

		case "response.function_call_arguments.done":
			id := event.ItemID
			if ts, ok := pendingTools[id]; ok {
				cb(StreamEvent{
					Type: EventToolUseDone,
					ToolCall: &ToolUseEvent{
						ID:    ts.id,
						Name:  ts.name,
						Input: ts.input.String(),
					},
				})
				delete(pendingTools, id)
			}

		case "response.completed":
			if event.Response.Usage.InputTokens > 0 {
				usage.InputTokens = event.Response.Usage.InputTokens
				usage.OutputTokens = event.Response.Usage.OutputTokens
			}
			// Extract any remaining function calls from completed response.
			for _, item := range event.Response.Output {
				if item.Type == "function_call" {
					if _, done := pendingTools[item.ID]; !done {
						cb(StreamEvent{
							Type: EventToolUseStart,
							ToolCall: &ToolUseEvent{
								ID:   item.CallID,
								Name: item.Name,
							},
						})
						cb(StreamEvent{
							Type: EventToolUseDone,
							ToolCall: &ToolUseEvent{
								ID:    item.CallID,
								Name:  item.Name,
								Input: item.Arguments,
							},
						})
					}
				}
			}
			cb(StreamEvent{Type: EventDone, Usage: &usage})
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("codex: scan: %w", err)
	}
	return nil
}

// Refresh implements RefreshableProvider.
func (p *CodexOAuthProvider) Refresh(ctx context.Context) error {
	auth, err := p.loadAuth()
	if err != nil {
		return err
	}
	_, err = p.refreshAuth(ctx, auth)
	return err
}

// refreshAuth exchanges the refresh token for a new access token.
// Uses a 10-second timeout to avoid blocking the daemon when the auth server is unresponsive.
func (p *CodexOAuthProvider) refreshAuth(ctx context.Context, auth codexOAuthAuthFile) (codexOAuthAuthFile, error) {
	refreshCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	payload, _ := json.Marshal(map[string]any{
		"grant_type":    "refresh_token",
		"client_id":     codexOAuthClientID,
		"refresh_token": auth.Tokens.RefreshToken,
	})

	req, err := http.NewRequestWithContext(refreshCtx, http.MethodPost, codexOAuthRefreshURL, bytes.NewReader(payload))
	if err != nil {
		return codexOAuthAuthFile{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := p.client.Do(req)
	if err != nil {
		return codexOAuthAuthFile{}, fmt.Errorf("codex: refresh http: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		return codexOAuthAuthFile{}, fmt.Errorf("codex: refresh failed: %s", res.Status)
	}

	var parsed struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return codexOAuthAuthFile{}, err
	}
	if parsed.AccessToken == "" {
		return codexOAuthAuthFile{}, errors.New("codex: refresh returned empty access token")
	}

	auth.Tokens.AccessToken = parsed.AccessToken
	if parsed.RefreshToken != "" {
		auth.Tokens.RefreshToken = parsed.RefreshToken
	}

	raw, err := json.MarshalIndent(auth, "", "  ")
	if err == nil {
		_ = os.WriteFile(p.authPath, raw, 0o600)
	}
	return auth, nil
}

// loadAuth reads the auth file.
func (p *CodexOAuthProvider) loadAuth() (codexOAuthAuthFile, error) {
	raw, err := os.ReadFile(p.authPath)
	if err != nil {
		return codexOAuthAuthFile{}, fmt.Errorf("codex: read auth: %w", err)
	}
	var auth codexOAuthAuthFile
	if err := json.Unmarshal(raw, &auth); err != nil {
		return codexOAuthAuthFile{}, fmt.Errorf("codex: parse auth: %w", err)
	}
	if auth.Tokens.AccessToken == "" {
		return codexOAuthAuthFile{}, errors.New("codex: no access token in auth file")
	}
	return auth, nil
}

// CodexOAuthAvailable checks if Codex OAuth credentials exist.
func CodexOAuthAvailable() bool {
	_, err := os.Stat(DefaultCodexAuthPath())
	return err == nil
}
