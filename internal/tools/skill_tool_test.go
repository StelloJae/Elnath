package tools_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/skill"
	"github.com/stello/elnath/internal/tools"
	"github.com/stello/elnath/internal/wiki"
)

func TestSkillToolCreate(t *testing.T) {
	t.Parallel()

	tool, store := newTestSkillTool(t)
	params := marshalSkillToolParams(t, map[string]any{
		"action":         "create",
		"name":           "test-skill",
		"description":    "A test skill",
		"trigger":        "/test-skill <arg>",
		"required_tools": []string{"bash"},
		"prompt":         "Do test with {arg}.",
	})

	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute() returned error result: %s", result.Output)
	}

	page, err := store.Read("skills/test-skill.md")
	if err != nil {
		t.Fatalf("Read(created skill) error = %v", err)
	}
	if got := page.Extra["source"]; got != "hint" {
		t.Fatalf("created source = %v, want hint", got)
	}
	if got := page.Extra["status"]; got != "active" {
		t.Fatalf("created status = %v, want active", got)
	}
}

func TestSkillToolList(t *testing.T) {
	t.Parallel()

	tool, _ := newTestSkillTool(t)
	params := marshalSkillToolParams(t, map[string]any{"action": "create", "name": "existing", "description": "Existing skill", "prompt": "test"})
	if result, err := tool.Execute(context.Background(), params); err != nil {
		t.Fatalf("Execute(create) error = %v", err)
	} else if result.IsError {
		t.Fatalf("Execute(create) returned error result: %s", result.Output)
	}

	result, err := tool.Execute(context.Background(), marshalSkillToolParams(t, map[string]any{"action": "list"}))
	if err != nil {
		t.Fatalf("Execute(list) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute(list) returned error result: %s", result.Output)
	}
	if !strings.Contains(result.Output, "/existing") {
		t.Fatalf("Execute(list) output = %q, want skill entry", result.Output)
	}
}

func TestSkillToolDelete(t *testing.T) {
	t.Parallel()

	tool, store := newTestSkillTool(t)
	if result, err := tool.Execute(context.Background(), marshalSkillToolParams(t, map[string]any{"action": "create", "name": "to-delete", "prompt": "test"})); err != nil {
		t.Fatalf("Execute(create) error = %v", err)
	} else if result.IsError {
		t.Fatalf("Execute(create) returned error result: %s", result.Output)
	}

	result, err := tool.Execute(context.Background(), marshalSkillToolParams(t, map[string]any{"action": "delete", "name": "to-delete"}))
	if err != nil {
		t.Fatalf("Execute(delete) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute(delete) returned error result: %s", result.Output)
	}
	if _, err := store.Read("skills/to-delete.md"); err == nil {
		t.Fatal("Read(deleted skill) error = nil, want not found")
	}
}

func TestSkillToolDeleteHidesDeletedSkillFromList(t *testing.T) {
	t.Parallel()

	tool, _ := newTestSkillTool(t)
	if result, err := tool.Execute(context.Background(), marshalSkillToolParams(t, map[string]any{"action": "create", "name": "hidden", "description": "Hidden skill", "prompt": "test"})); err != nil {
		t.Fatalf("Execute(create) error = %v", err)
	} else if result.IsError {
		t.Fatalf("Execute(create) returned error result: %s", result.Output)
	}
	if result, err := tool.Execute(context.Background(), marshalSkillToolParams(t, map[string]any{"action": "delete", "name": "hidden"})); err != nil {
		t.Fatalf("Execute(delete) error = %v", err)
	} else if result.IsError {
		t.Fatalf("Execute(delete) returned error result: %s", result.Output)
	}

	result, err := tool.Execute(context.Background(), marshalSkillToolParams(t, map[string]any{"action": "list"}))
	if err != nil {
		t.Fatalf("Execute(list) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute(list) returned error result: %s", result.Output)
	}
	if strings.Contains(result.Output, "/hidden") {
		t.Fatalf("Execute(list) output = %q, deleted skill should be hidden", result.Output)
	}
}

func TestSkillToolDeleteRemovesSkillFromRegistry(t *testing.T) {
	t.Parallel()

	store, err := wiki.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	registry := skill.NewRegistry()
	creator := skill.NewCreator(store, skill.NewTracker(t.TempDir()), nil)
	tool := tools.NewSkillTool(creator, registry)

	if result, err := tool.Execute(context.Background(), marshalSkillToolParams(t, map[string]any{"action": "create", "name": "live", "prompt": "test"})); err != nil {
		t.Fatalf("Execute(create) error = %v", err)
	} else if result.IsError {
		t.Fatalf("Execute(create) returned error result: %s", result.Output)
	}
	if _, ok := registry.Get("live"); !ok {
		t.Fatal("registry missing created skill before delete")
	}

	if result, err := tool.Execute(context.Background(), marshalSkillToolParams(t, map[string]any{"action": "delete", "name": "live"})); err != nil {
		t.Fatalf("Execute(delete) error = %v", err)
	} else if result.IsError {
		t.Fatalf("Execute(delete) returned error result: %s", result.Output)
	}
	if _, ok := registry.Get("live"); ok {
		t.Fatal("registry still contains deleted skill")
	}
}

func TestSkillToolScope(t *testing.T) {
	t.Parallel()

	tool, store := newTestSkillTool(t)
	wikiDir := filepath.Clean(store.WikiDir())

	listScope := tool.Scope(marshalSkillToolParams(t, map[string]any{"action": "list"}))
	if want := (tools.ToolScope{ReadPaths: []string{wikiDir}}); !reflect.DeepEqual(listScope, want) {
		t.Fatalf("Scope(list) = %+v, want %+v", listScope, want)
	}

	createScope := tool.Scope(marshalSkillToolParams(t, map[string]any{"action": "create", "name": "scoped", "prompt": "test"}))
	if want := (tools.ToolScope{ReadPaths: []string{wikiDir}, WritePaths: []string{wikiDir}, Persistent: true}); !reflect.DeepEqual(createScope, want) {
		t.Fatalf("Scope(create) = %+v, want %+v", createScope, want)
	}

	deleteScope := tool.Scope(marshalSkillToolParams(t, map[string]any{"action": "delete", "name": "scoped"}))
	if want := (tools.ToolScope{ReadPaths: []string{wikiDir}, WritePaths: []string{wikiDir}, Persistent: true}); !reflect.DeepEqual(deleteScope, want) {
		t.Fatalf("Scope(delete) = %+v, want %+v", deleteScope, want)
	}
}

func TestSkillToolCreateRejectsEmptyName(t *testing.T) {
	t.Parallel()

	tool, _ := newTestSkillTool(t)
	result, err := tool.Execute(context.Background(), marshalSkillToolParams(t, map[string]any{"action": "create", "name": "   ", "prompt": "test"}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.IsError {
		t.Fatalf("Execute() IsError = false, want true with output %q", result.Output)
	}
	if !strings.Contains(result.Output, "name") {
		t.Fatalf("Execute() output = %q, want name error", result.Output)
	}
}

func TestSkillToolCreateRejectsEmptyPrompt(t *testing.T) {
	t.Parallel()

	tool, _ := newTestSkillTool(t)
	result, err := tool.Execute(context.Background(), marshalSkillToolParams(t, map[string]any{"action": "create", "name": "blank-prompt", "prompt": "   "}))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.IsError {
		t.Fatalf("Execute() IsError = false, want true with output %q", result.Output)
	}
	if !strings.Contains(result.Output, "prompt") {
		t.Fatalf("Execute() output = %q, want prompt error", result.Output)
	}
}

func newTestSkillTool(t *testing.T) (*tools.SkillTool, *wiki.Store) {
	t.Helper()

	store, err := wiki.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	registry := skill.NewRegistry()
	creator := skill.NewCreator(store, skill.NewTracker(t.TempDir()), nil)
	return tools.NewSkillTool(creator, registry), store
}

func marshalSkillToolParams(t *testing.T, input map[string]any) json.RawMessage {
	t.Helper()

	params, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return params
}
