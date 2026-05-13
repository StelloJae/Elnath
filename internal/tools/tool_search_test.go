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
		Name                  string `json:"name"`
		Description           string `json:"description"`
		SchemaPreview         string `json:"schema_preview"`
		Deferred              bool   `json:"deferred"`
		DeferReason           string `json:"defer_reason,omitempty"`
		ConcurrencySafe       bool   `json:"concurrency_safe"`
		Reversible            bool   `json:"reversible"`
		CancelSiblingsOnError bool   `json:"cancel_siblings_on_error"`
	} `json:"matches"`
	Receipt struct {
		Tool               string `json:"tool"`
		Action             string `json:"action"`
		ReadOnly           bool   `json:"read_only"`
		RegistryAvailable  bool   `json:"registry_available"`
		ExecutionAvailable bool   `json:"execution_available"`
		ExecutionPolicy    string `json:"execution_policy"`
		TotalTools         int    `json:"total_tools"`
		ReturnedMatches    int    `json:"returned_matches"`
		DeferredMatches    int    `json:"deferred_matches"`
		MaxResults         int    `json:"max_results"`
		AllowNamesCount    int    `json:"allow_names_count"`
		Query              string `json:"query"`
	} `json:"receipt"`
}

type toolSearchMetadataTool struct {
	name        string
	description string
	safe        bool
	reversible  bool
	cancel      bool
	deferSchema bool
}

func (t *toolSearchMetadataTool) Name() string        { return t.name }
func (t *toolSearchMetadataTool) Description() string { return t.description }
func (t *toolSearchMetadataTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (t *toolSearchMetadataTool) IsConcurrencySafe(json.RawMessage) bool { return t.safe }
func (t *toolSearchMetadataTool) Reversible() bool                       { return t.reversible }
func (t *toolSearchMetadataTool) Scope(json.RawMessage) ToolScope        { return ConservativeScope() }
func (t *toolSearchMetadataTool) ShouldCancelSiblingsOnError() bool      { return t.cancel }
func (t *toolSearchMetadataTool) DeferInitialToolSchema() bool           { return t.deferSchema }
func (t *toolSearchMetadataTool) Execute(context.Context, json.RawMessage) (*Result, error) {
	return SuccessResult("ok"), nil
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

func TestToolSearchReportsStableMetadata(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&toolSearchMetadataTool{
		name:        "mcp_github_issue",
		description: "Create GitHub issues",
		safe:        true,
		reversible:  true,
		cancel:      true,
	})
	search := NewToolSearchTool(reg)
	reg.Register(search)

	out := executeToolSearch(t, search, `{"query":"github","max_results":5}`)

	if len(out.Matches) != 1 {
		t.Fatalf("matches len = %d, want 1", len(out.Matches))
	}
	match := out.Matches[0]
	if !match.Deferred {
		t.Fatalf("Deferred = false, want true for mcp_* tool")
	}
	if match.DeferReason != "mcp_prefix" {
		t.Fatalf("DeferReason = %q, want mcp_prefix", match.DeferReason)
	}
	if !match.ConcurrencySafe || !match.Reversible || !match.CancelSiblingsOnError {
		t.Fatalf("metadata = %+v, want safe/reversible/cancel true", match)
	}
}

func TestToolSearchReportsDeclaredDeferReason(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&toolSearchMetadataTool{
		name:        "task_create",
		description: "Create daemon task",
		deferSchema: true,
	})
	search := NewToolSearchTool(reg)
	reg.Register(search)

	out := executeToolSearch(t, search, `{"query":"task","max_results":5}`)

	if len(out.Matches) != 1 {
		t.Fatalf("matches len = %d, want 1", len(out.Matches))
	}
	match := out.Matches[0]
	if !match.Deferred {
		t.Fatalf("Deferred = false, want true for declared deferred tool")
	}
	if match.DeferReason != "tool_declared_deferred" {
		t.Fatalf("DeferReason = %q, want tool_declared_deferred", match.DeferReason)
	}
}

func TestToolSearchFindsCodeSymbolsAsDeferredCodeIntelligence(t *testing.T) {
	reg := NewRegistry()
	reg.Register(NewCodeSymbolsTool(NewPathGuard(t.TempDir(), nil)))
	search := NewToolSearchTool(reg)
	reg.Register(search)

	out := executeToolSearch(t, search, `{"query":"symbols outline code intelligence","max_results":5}`)

	if len(out.Matches) != 1 {
		t.Fatalf("matches len = %d, want 1: %+v", len(out.Matches), out.Matches)
	}
	match := out.Matches[0]
	if match.Name != CodeSymbolsToolName {
		t.Fatalf("match = %q, want %s", match.Name, CodeSymbolsToolName)
	}
	if !match.Deferred || match.DeferReason != "tool_declared_deferred" {
		t.Fatalf("defer metadata = deferred:%t reason:%q", match.Deferred, match.DeferReason)
	}
	if !match.ConcurrencySafe || !match.Reversible {
		t.Fatalf("metadata = %+v, want read-only", match)
	}
}

func TestToolSearchAllowNamesRestrictsCandidates(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockTool{name: "grep", result: SuccessResult("")})
	reg.Register(&mockTool{name: "web_fetch", result: SuccessResult("")})
	search := NewToolSearchTool(reg)
	reg.Register(search)

	out := executeToolSearch(t, search, `{"query":"","allow_names":["web_fetch"],"max_results":5}`)

	if out.TotalTools != 1 {
		t.Fatalf("TotalTools = %d, want allowlisted candidate count 1", out.TotalTools)
	}
	if len(out.Matches) != 1 || out.Matches[0].Name != "web_fetch" {
		t.Fatalf("matches = %+v, want only web_fetch", out.Matches)
	}
}

func TestToolSearchIncludesDiscoveryReceipt(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockTool{name: "grep", result: SuccessResult("")})
	reg.Register(&toolSearchMetadataTool{
		name:        "task_create",
		description: "Create daemon task",
		deferSchema: true,
	})
	search := NewToolSearchTool(reg)
	reg.Register(search)

	out := executeToolSearch(t, search, `{"query":"task","allow_names":["task_create","grep"],"max_results":3}`)

	if out.Receipt.Tool != ToolSearchName || out.Receipt.Action != "search" {
		t.Fatalf("receipt identity = %+v", out.Receipt)
	}
	if !out.Receipt.ReadOnly || !out.Receipt.RegistryAvailable {
		t.Fatalf("receipt read-only/registry = %+v", out.Receipt)
	}
	if out.Receipt.ExecutionAvailable || out.Receipt.ExecutionPolicy != "metadata_only" {
		t.Fatalf("receipt execution boundary = %+v", out.Receipt)
	}
	if out.Receipt.TotalTools != 2 || out.Receipt.ReturnedMatches != len(out.Matches) || out.Receipt.DeferredMatches != 1 {
		t.Fatalf("receipt counts = %+v matches=%d", out.Receipt, len(out.Matches))
	}
	if out.Receipt.MaxResults != 3 || out.Receipt.AllowNamesCount != 2 || out.Receipt.Query != "task" {
		t.Fatalf("receipt request bounds = %+v", out.Receipt)
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
