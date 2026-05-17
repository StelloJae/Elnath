package skill

import (
	"strings"
	"testing"
	"time"

	"github.com/stello/elnath/internal/wiki"
)

func TestCreatorCreate(t *testing.T) {
	t.Parallel()

	store, err := wiki.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	creator := NewCreator(store, NewTracker(t.TempDir()), nil)

	sk, err := creator.Create(CreateParams{
		Name:          "deploy-check",
		Description:   "Check deployment status",
		Trigger:       "/deploy-check <env>",
		RequiredTools: []string{"bash"},
		Prompt:        "Check deployment for {env}.",
		Status:        "active",
		Source:        "user",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if sk.Name != "deploy-check" {
		t.Fatalf("Create().Name = %q, want %q", sk.Name, "deploy-check")
	}

	page, err := store.Read("skills/deploy-check.md")
	if err != nil {
		t.Fatalf("Read(created skill) error = %v", err)
	}
	if got := page.Extra["status"]; got != "active" {
		t.Fatalf("created status = %v, want active", got)
	}
	if got := page.Extra["source"]; got != "user" {
		t.Fatalf("created source = %v, want user", got)
	}
}

func TestCreatorCreateDuplicate(t *testing.T) {
	t.Parallel()

	store, err := wiki.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	creator := NewCreator(store, NewTracker(t.TempDir()), nil)
	params := CreateParams{Name: "dup", Prompt: "test", Status: "active"}

	if _, err := creator.Create(params); err != nil {
		t.Fatalf("first Create() error = %v", err)
	}
	if _, err := creator.Create(params); err == nil {
		t.Fatal("second Create() error = nil, want duplicate error")
	}
}

func TestCreatorRejectsInvalidName(t *testing.T) {
	t.Parallel()

	store, err := wiki.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	creator := NewCreator(store, NewTracker(t.TempDir()), nil)

	if _, err := creator.Create(CreateParams{Name: "../escape", Prompt: "test"}); err == nil {
		t.Fatal("Create() error = nil, want invalid name error")
	}
}

func TestCreatorRejectsEmptyPrompt(t *testing.T) {
	t.Parallel()

	store, err := wiki.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	creator := NewCreator(store, NewTracker(t.TempDir()), nil)

	if _, err := creator.Create(CreateParams{Name: "blank-prompt", Prompt: "   "}); err == nil {
		t.Fatal("Create() error = nil, want empty prompt error")
	}
}

func TestCreatorDelete(t *testing.T) {
	t.Parallel()

	store, err := wiki.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	creator := NewCreator(store, NewTracker(t.TempDir()), nil)

	if _, err := creator.Create(CreateParams{Name: "to-delete", Prompt: "test", Status: "active"}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := creator.Delete("to-delete"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := store.Read("skills/to-delete.md"); err == nil {
		t.Fatal("Read(deleted skill) error = nil, want not found")
	}
}

func TestCreatorPromoteWithHotReload(t *testing.T) {
	t.Parallel()

	store, err := wiki.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	reg := NewRegistry()
	creator := NewCreator(store, NewTracker(t.TempDir()), reg)

	if _, err := creator.Create(CreateParams{
		Name:   "promoted-skill",
		Prompt: "do things",
		Status: "draft",
		Source: "analyst",
	}); err != nil {
		t.Fatalf("Create(draft) error = %v", err)
	}
	if _, ok := reg.Get("promoted-skill"); ok {
		t.Fatal("registry contains draft skill before Promote()")
	}

	if err := creator.Promote("promoted-skill"); err != nil {
		t.Fatalf("Promote() error = %v", err)
	}

	page, err := store.Read("skills/promoted-skill.md")
	if err != nil {
		t.Fatalf("Read(promoted skill) error = %v", err)
	}
	if got := page.Extra["status"]; got != "active" {
		t.Fatalf("promoted status = %v, want active", got)
	}
	if got := page.Extra["source"]; got != "promoted" {
		t.Fatalf("promoted source = %v, want promoted", got)
	}
	if _, ok := reg.Get("promoted-skill"); !ok {
		t.Fatal("registry missing promoted skill after Promote()")
	}
}

func TestCreatorApplyImprovementProposal(t *testing.T) {
	t.Parallel()

	store, err := wiki.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	tracker := NewTracker(t.TempDir())
	reg := NewRegistry()
	creator := NewCreator(store, tracker, reg)
	if _, err := creator.Create(CreateParams{
		Name:   "review-pr",
		Prompt: "Review pull requests.",
		Status: "active",
		Source: "user",
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	proposalPath, err := tracker.WriteImprovementProposal(ImprovementProposal{
		SkillName:       "review-pr",
		SessionID:       "sess-1",
		Reason:          "User corrected ordering.",
		SuggestedChange: "Start with findings before summary.",
		CreatedAt:       time.Date(2026, 5, 17, 4, 5, 6, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("WriteImprovementProposal() error = %v", err)
	}

	sk, err := creator.ApplyImprovementProposal(proposalPath)
	if err != nil {
		t.Fatalf("ApplyImprovementProposal() error = %v", err)
	}
	if sk == nil || !strings.Contains(sk.Prompt, "Start with findings before summary.") {
		t.Fatalf("updated skill = %+v, want suggested change in prompt", sk)
	}
	page, err := store.Read("skills/review-pr.md")
	if err != nil {
		t.Fatalf("Read(updated skill) error = %v", err)
	}
	if !strings.Contains(page.Content, "Applied improvement proposal: 20260517T040506Z-review-pr.md") {
		t.Fatalf("page content = %q, want applied proposal marker", page.Content)
	}
	if page.Extra["last_improvement_proposal"] != "20260517T040506Z-review-pr.md" {
		t.Fatalf("last_improvement_proposal = %v", page.Extra["last_improvement_proposal"])
	}
	if _, ok := reg.Get("review-pr"); !ok {
		t.Fatal("registry missing updated skill after ApplyImprovementProposal")
	}
}
