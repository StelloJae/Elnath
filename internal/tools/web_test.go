package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

func TestWebSearchToolExecuteAlwaysErrors(t *testing.T) {
	tool := NewWebSearchTool()

	res, err := tool.Execute(context.Background(), mustMarshal(t, map[string]any{"query": "anything"}))
	if err != nil {
		t.Fatalf("Execute returned unexpected Go error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected error result from stub, got output: %s", res.Output)
	}
	if !strings.Contains(res.Output, "not implemented") {
		t.Errorf("error message does not mention 'not implemented': %s", res.Output)
	}
}
