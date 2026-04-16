package skill

import (
	"testing"

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
