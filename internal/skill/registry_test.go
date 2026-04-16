package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"testing"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/tools"
	"github.com/stello/elnath/internal/wiki"
)

type mockTool struct {
	name         string
	executeCount *int
}

func (m *mockTool) Name() string                           { return m.name }
func (m *mockTool) Description() string                    { return m.name }
func (m *mockTool) Schema() json.RawMessage                { return json.RawMessage(`{"type":"object"}`) }
func (m *mockTool) IsConcurrencySafe(json.RawMessage) bool { return false }
func (m *mockTool) Reversible() bool                       { return false }
func (m *mockTool) Scope(json.RawMessage) tools.ToolScope  { return tools.ConservativeScope() }
func (m *mockTool) ShouldCancelSiblingsOnError() bool      { return false }
func (m *mockTool) Execute(context.Context, json.RawMessage) (*tools.Result, error) {
	if m.executeCount != nil {
		*m.executeCount++
	}
	return tools.SuccessResult("ok"), nil
}

type mockProvider struct {
	streamFn func(context.Context, llm.ChatRequest, func(llm.StreamEvent)) error
}

func (m *mockProvider) Chat(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}

func (m *mockProvider) Stream(ctx context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
	if m.streamFn != nil {
		return m.streamFn(ctx, req, cb)
	}
	return nil
}

func (m *mockProvider) Name() string            { return "mock" }
func (m *mockProvider) Models() []llm.ModelInfo { return nil }

type denyHook struct{ toolName string }

func (h *denyHook) PreToolUse(_ context.Context, toolName string, _ json.RawMessage) (agent.HookResult, error) {
	if toolName == h.toolName {
		return agent.HookResult{Action: agent.HookDeny, Message: "blocked"}, nil
	}
	return agent.HookResult{Action: agent.HookAllow}, nil
}

func (h *denyHook) PostToolUse(context.Context, string, json.RawMessage, *tools.Result) error {
	return nil
}

func TestRegistryLoad(t *testing.T) {
	t.Parallel()

	store, err := wiki.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	pages := []*wiki.Page{
		{
			Path:    "skills/pr-review.md",
			Title:   "PR Review",
			Type:    wiki.PageTypeAnalysis,
			Tags:    []string{"skill"},
			Content: "Review PR {pr_number}",
			Extra: map[string]any{
				"name":           "pr-review",
				"required_tools": []string{"bash", "read_file"},
			},
		},
		{
			Path:    "skills/audit-security.md",
			Title:   "Security Audit",
			Type:    wiki.PageTypeAnalysis,
			Tags:    []string{"skill"},
			Content: "Audit current repo",
			Extra: map[string]any{
				"name": "audit-security",
			},
		},
		{
			Path:    "analysis/notes.md",
			Title:   "Notes",
			Type:    wiki.PageTypeAnalysis,
			Tags:    []string{"analysis"},
			Content: "Not a skill",
		},
	}

	for _, page := range pages {
		if err := store.Create(page); err != nil {
			t.Fatalf("Create(%q) error = %v", page.Path, err)
		}
	}

	reg := NewRegistry()
	if err := reg.Load(store); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := len(reg.List()); got != 2 {
		t.Fatalf("len(List()) = %d, want 2", got)
	}
}

func TestRegistryLoadSkipsDraft(t *testing.T) {
	t.Parallel()

	store, err := wiki.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	pages := []*wiki.Page{
		{
			Path:    "skills/active-skill.md",
			Title:   "Active Skill",
			Type:    wiki.PageTypeAnalysis,
			Tags:    []string{"skill"},
			Content: "Do active things",
			Extra: map[string]any{
				"name":   "active-skill",
				"status": "active",
			},
		},
		{
			Path:    "skills/draft-skill.md",
			Title:   "Draft Skill",
			Type:    wiki.PageTypeAnalysis,
			Tags:    []string{"skill"},
			Content: "Do draft things",
			Extra: map[string]any{
				"name":   "draft-skill",
				"status": "draft",
			},
		},
	}
	for _, page := range pages {
		if err := store.Create(page); err != nil {
			t.Fatalf("Create(%q) error = %v", page.Path, err)
		}
	}

	reg := NewRegistry()
	if err := reg.Load(store); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if _, ok := reg.Get("active-skill"); !ok {
		t.Fatal("active skill missing after Load()")
	}
	if _, ok := reg.Get("draft-skill"); ok {
		t.Fatal("draft skill loaded by Load()")
	}
	if got := len(reg.List()); got != 1 {
		t.Fatalf("len(List()) = %d, want 1", got)
	}
}

func TestRegistryLoadReturnsStoreError(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := wiki.NewStore(root)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatalf("RemoveAll() error = %v", err)
	}

	reg := NewRegistry()
	if err := reg.Load(store); err == nil {
		t.Fatal("Load() error = nil, want non-nil")
	}
}

func TestRegistryLoadOverwritesDuplicateSkillNames(t *testing.T) {
	t.Parallel()

	store, err := wiki.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	pages := []*wiki.Page{
		{
			Path:    "skills/01-first.md",
			Title:   "First",
			Type:    wiki.PageTypeAnalysis,
			Tags:    []string{"skill"},
			Content: "first prompt",
			Extra: map[string]any{
				"name": "duplicate-skill",
			},
		},
		{
			Path:    "skills/02-second.md",
			Title:   "Second",
			Type:    wiki.PageTypeAnalysis,
			Tags:    []string{"skill"},
			Content: "second prompt",
			Extra: map[string]any{
				"name": "duplicate-skill",
			},
		},
	}

	for _, page := range pages {
		if err := store.Create(page); err != nil {
			t.Fatalf("Create(%q) error = %v", page.Path, err)
		}
	}

	reg := NewRegistry()
	if err := reg.Load(store); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	skill, ok := reg.Get("duplicate-skill")
	if !ok {
		t.Fatal("Get(duplicate-skill) found = false, want true")
	}
	if skill.Prompt != "second prompt\n" {
		t.Fatalf("duplicate skill prompt = %q, want %q", skill.Prompt, "second prompt\n")
	}
}

func TestRegistryGet(t *testing.T) {
	t.Parallel()

	reg := &Registry{skills: map[string]*Skill{
		"pr-review": {Name: "pr-review"},
	}}

	if _, ok := reg.Get("pr-review"); !ok {
		t.Fatal("Get(pr-review) found = false, want true")
	}
	if _, ok := reg.Get("missing"); ok {
		t.Fatal("Get(missing) found = true, want false")
	}
}

func TestRegistryList(t *testing.T) {
	t.Parallel()

	reg := &Registry{skills: map[string]*Skill{
		"zeta":  {Name: "zeta"},
		"alpha": {Name: "alpha"},
		"beta":  {Name: "beta"},
	}}

	list := reg.List()
	got := make([]string, 0, len(list))
	for _, skill := range list {
		got = append(got, skill.Name)
	}

	want := []string{"alpha", "beta", "zeta"}
	if !equalStrings(got, want) {
		t.Fatalf("List() names = %v, want %v", got, want)
	}
}

func TestRegistryNames(t *testing.T) {
	t.Parallel()

	reg := &Registry{skills: map[string]*Skill{
		"zeta":  {Name: "zeta"},
		"alpha": {Name: "alpha"},
		"beta":  {Name: "beta"},
	}}

	want := []string{"alpha", "beta", "zeta"}
	if got := reg.Names(); !equalStrings(got, want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
}

func TestFilterRegistry(t *testing.T) {
	t.Parallel()

	full := tools.NewRegistry()
	full.Register(&mockTool{name: "bash"})
	full.Register(&mockTool{name: "read_file"})
	full.Register(&mockTool{name: "write_file"})

	t.Run("filters allow list", func(t *testing.T) {
		filtered := FilterRegistry(full, []string{"bash", "read_file"})
		want := []string{"bash", "read_file"}
		if got := filtered.Names(); !equalStrings(got, want) {
			t.Fatalf("FilterRegistry() names = %v, want %v", got, want)
		}
	})

	t.Run("empty allow list returns original", func(t *testing.T) {
		filtered := FilterRegistry(full, nil)
		if filtered != full {
			t.Fatal("FilterRegistry() should return original registry when allow list is empty")
		}
	})

	t.Run("unknown tool skipped", func(t *testing.T) {
		filtered := FilterRegistry(full, []string{"bash", "missing"})
		want := []string{"bash"}
		if got := filtered.Names(); !equalStrings(got, want) {
			t.Fatalf("FilterRegistry() names = %v, want %v", got, want)
		}
	})
}

func TestExecuteHonorsPermissionAndHooks(t *testing.T) {
	t.Parallel()

	newProvider := func(toolName string) llm.Provider {
		callCount := 0
		return &mockProvider{streamFn: func(_ context.Context, _ llm.ChatRequest, cb func(llm.StreamEvent)) error {
			callCount++
			if callCount == 1 {
				cb(llm.StreamEvent{Type: llm.EventToolUseStart, ToolCall: &llm.ToolUseEvent{ID: "tool-1", Name: toolName}})
				cb(llm.StreamEvent{Type: llm.EventToolUseDone, ToolCall: &llm.ToolUseEvent{ID: "tool-1", Name: toolName, Input: `{}`}})
				cb(llm.StreamEvent{Type: llm.EventDone})
				return nil
			}
			cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: fmt.Sprintf("%s finished", toolName)})
			cb(llm.StreamEvent{Type: llm.EventDone})
			return nil
		}}
	}

	t.Run("permission blocks tool execution", func(t *testing.T) {
		execCount := 0
		toolReg := tools.NewRegistry()
		toolReg.Register(&mockTool{name: "bash", executeCount: &execCount})

		reg := NewRegistry()
		reg.Add(&Skill{Name: "audit-security", RequiredTools: []string{"bash"}, Prompt: "Audit repo"})

		result, err := reg.Execute(context.Background(), ExecuteParams{
			SkillName:  "audit-security",
			Provider:   newProvider("bash"),
			ToolReg:    toolReg,
			Permission: agent.NewPermission(agent.WithMode(agent.ModePlan)),
		})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if execCount != 0 {
			t.Fatalf("tool execute count = %d, want 0 when permission denies", execCount)
		}
		if result.Output != "bash finished" {
			t.Fatalf("Output = %q, want %q", result.Output, "bash finished")
		}
	})

	t.Run("hooks block tool execution", func(t *testing.T) {
		execCount := 0
		toolReg := tools.NewRegistry()
		toolReg.Register(&mockTool{name: "read_file", executeCount: &execCount})

		hooks := agent.NewHookRegistry()
		hooks.Add(&denyHook{toolName: "read_file"})

		reg := NewRegistry()
		reg.Add(&Skill{Name: "read-skill", RequiredTools: []string{"read_file"}, Prompt: "Read file"})

		result, err := reg.Execute(context.Background(), ExecuteParams{
			SkillName:  "read-skill",
			Provider:   newProvider("read_file"),
			ToolReg:    toolReg,
			Permission: agent.NewPermission(agent.WithMode(agent.ModeBypass)),
			Hooks:      hooks,
		})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if execCount != 0 {
			t.Fatalf("tool execute count = %d, want 0 when hook denies", execCount)
		}
		if result.Output != "read_file finished" {
			t.Fatalf("Output = %q, want %q", result.Output, "read_file finished")
		}
	})
}

func TestExecuteAppendsLocaleDirective(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		locale     string
		wantSuffix string
	}{
		{name: "korean locale appends directive", locale: "ko", wantSuffix: "\n\nRespond in Korean."},
		{name: "japanese locale appends directive", locale: "ja", wantSuffix: "\n\nRespond in Japanese."},
		{name: "english locale leaves prompt unchanged", locale: "en", wantSuffix: ""},
		{name: "empty locale leaves prompt unchanged", locale: "", wantSuffix: ""},
		{name: "unknown locale leaves prompt unchanged", locale: "fr", wantSuffix: ""},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var capturedSystem string
			provider := &mockProvider{streamFn: func(_ context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
				capturedSystem = req.System
				cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: "ok"})
				cb(llm.StreamEvent{Type: llm.EventDone})
				return nil
			}}

			reg := NewRegistry()
			reg.Add(&Skill{Name: "locale-skill", Prompt: "Base prompt body."})

			_, err := reg.Execute(context.Background(), ExecuteParams{
				SkillName: "locale-skill",
				Provider:  provider,
				ToolReg:   tools.NewRegistry(),
				Locale:    tc.locale,
			})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			wantPrompt := "Base prompt body." + tc.wantSuffix
			if capturedSystem != wantPrompt {
				t.Fatalf("system prompt = %q, want %q", capturedSystem, wantPrompt)
			}
		})
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	gotCopy := append([]string(nil), got...)
	wantCopy := append([]string(nil), want...)
	sort.Strings(gotCopy)
	sort.Strings(wantCopy)
	for i := range gotCopy {
		if gotCopy[i] != wantCopy[i] {
			return false
		}
	}
	return true
}
