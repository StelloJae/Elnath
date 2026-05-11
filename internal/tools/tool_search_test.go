package tools

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type testToolSearchOutput struct {
	Query      string `json:"query"`
	TotalTools int    `json:"total_tools"`
	Matches    []struct {
		Name          string `json:"name"`
		Description   string `json:"description"`
		SchemaPreview string `json:"schema_preview"`
	} `json:"matches"`
}

func executeToolSearch(t *testing.T, tool *ToolSearchTool, input string) testToolSearchOutput {
	t.Helper()
	res, err := tool.Execute(context.Background(), json.RawMessage(input))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}
	var out testToolSearchOutput
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	return out
}

func TestToolSearchFindsToolsByNameAndDescription(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockTool{name: "grep", result: SuccessResult("")})
	reg.Register(&mockTool{name: "web_fetch", result: SuccessResult("")})
	search := NewToolSearchTool(reg)
	reg.Register(search)

	out := executeToolSearch(t, search, `{"query":"grep","max_results":5}`)

	if out.Query != "grep" {
		t.Fatalf("Query = %q, want grep", out.Query)
	}
	if out.TotalTools != 2 {
		t.Fatalf("TotalTools = %d, want 2", out.TotalTools)
	}
	if len(out.Matches) != 1 {
		t.Fatalf("matches len = %d, want 1: %+v", len(out.Matches), out.Matches)
	}
	if out.Matches[0].Name != "grep" {
		t.Fatalf("first match = %q, want grep", out.Matches[0].Name)
	}
	if out.Matches[0].Description == "" {
		t.Fatal("description is empty")
	}
	if !strings.Contains(out.Matches[0].SchemaPreview, `"type":"object"`) {
		t.Fatalf("schema preview = %q, want object schema", out.Matches[0].SchemaPreview)
	}
}

func TestToolSearchSelectsExplicitTools(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockTool{name: "grep", result: SuccessResult("")})
	reg.Register(&mockTool{name: "web_fetch", result: SuccessResult("")})
	search := NewToolSearchTool(reg)
	reg.Register(search)

	out := executeToolSearch(t, search, `{"query":"select:web_fetch,grep,missing","max_results":5}`)

	got := []string{}
	for _, match := range out.Matches {
		got = append(got, match.Name)
	}
	want := []string{"web_fetch", "grep"}
	if len(got) != len(want) {
		t.Fatalf("matches = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("matches = %v, want %v", got, want)
		}
	}
}

func TestToolSearchEmptyQueryListsCompactCatalog(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockTool{name: "beta", result: SuccessResult("")})
	reg.Register(&mockTool{name: "alpha", result: SuccessResult("")})
	search := NewToolSearchTool(reg)
	reg.Register(search)

	out := executeToolSearch(t, search, `{"query":"","max_results":1}`)

	if out.TotalTools != 2 {
		t.Fatalf("TotalTools = %d, want 2", out.TotalTools)
	}
	if len(out.Matches) != 1 {
		t.Fatalf("matches len = %d, want 1", len(out.Matches))
	}
	if out.Matches[0].Name != "alpha" {
		t.Fatalf("first match = %q, want alpha", out.Matches[0].Name)
	}
}

func TestToolSearchMetadataIsReadOnly(t *testing.T) {
	tool := NewToolSearchTool(NewRegistry())

	if tool.Name() != "tool_search" {
		t.Fatalf("Name() = %q, want tool_search", tool.Name())
	}
	if !tool.IsConcurrencySafe(nil) {
		t.Fatal("tool_search should be concurrency-safe")
	}
	if !tool.Reversible() {
		t.Fatal("tool_search should be reversible")
	}
	if got := tool.Scope(nil); !reflect.DeepEqual(got, ToolScope{}) {
		t.Fatalf("Scope(nil) = %+v, want empty read-only scope", got)
	}
	if tool.ShouldCancelSiblingsOnError() {
		t.Fatal("tool_search should not cancel siblings")
	}
}
