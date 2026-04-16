package magicdocs

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/wiki"
)

func TestMagicDocs_Lifecycle(t *testing.T) {
	store, err := wiki.NewStore(filepath.Join(t.TempDir(), "wiki"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	provider := &mockProvider{
		response: `{"pages": [{"action": "create", "path": "analyses/lifecycle.md", "title": "Lifecycle Test", "type": "analysis", "content": "Works", "confidence": "medium", "tags": []}]}`,
	}

	md := New(Config{
		Enabled:   true,
		Store:     store,
		Provider:  provider,
		Model:     "test-model",
		Logger:    slog.Default(),
		SessionID: "test-session",
	})

	ctx := context.Background()
	md.Start(ctx)

	bus := event.NewBus()
	bus.Subscribe(md.Observer())

	base := event.NewBaseWith(time.Now(), "test-session")
	bus.Emit(event.ResearchProgressEvent{Base: base, Phase: "conclusion", Round: 3, Message: "found it"})
	bus.Emit(event.AgentFinishEvent{Base: base, FinishReason: "end_turn"})

	closeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := md.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	page, err := store.Read("analyses/lifecycle.md")
	if err != nil {
		t.Fatalf("wiki page not created: %v", err)
	}
	if page.Title != "Lifecycle Test" {
		t.Errorf("Title = %q, want %q", page.Title, "Lifecycle Test")
	}
}

func TestMagicDocs_Disabled(t *testing.T) {
	md := New(Config{Enabled: false})
	ctx := context.Background()
	md.Start(ctx)

	obs := md.Observer()
	base := event.NewBaseWith(time.Now(), "test")
	obs.OnEvent(event.AgentFinishEvent{Base: base, FinishReason: "end_turn", Usage: llm.UsageStats{}})

	if err := md.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestMagicDocs_GracefulShutdown_Timeout(t *testing.T) {
	provider := &mockProvider{response: `{"pages": []}`}
	store, _ := wiki.NewStore(filepath.Join(t.TempDir(), "wiki"))

	md := New(Config{
		Enabled:   true,
		Store:     store,
		Provider:  provider,
		Model:     "test",
		Logger:    slog.Default(),
		SessionID: "test",
	})

	ctx := context.Background()
	md.Start(ctx)

	closeCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_ = md.Close(closeCtx)
}

func TestIntegration_FullPipeline(t *testing.T) {
	store, err := wiki.NewStore(filepath.Join(t.TempDir(), "wiki"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	provider := &mockProvider{
		response: `{
			"pages": [
				{"action": "create", "path": "analyses/research-finding.md", "title": "Research Finding", "type": "analysis", "content": "## Go Error Patterns\n\nSentinel errors should be preferred.", "confidence": "high", "tags": ["go", "errors"]},
				{"action": "create", "path": "concepts/sentinel-errors.md", "title": "Sentinel Errors", "type": "concept", "content": "A pattern in Go.", "confidence": "medium", "tags": ["go"]}
			]
		}`,
	}

	md := New(Config{
		Enabled:   true,
		Store:     store,
		Provider:  provider,
		Model:     "test-model",
		Logger:    slog.Default(),
		SessionID: "integration-test",
	})

	ctx := context.Background()
	md.Start(ctx)

	bus := event.NewBus()
	bus.Subscribe(md.Observer())

	base := event.NewBaseWith(time.Now(), "integration-test")
	bus.Emit(event.WorkflowProgressEvent{Base: base, Intent: "research", Workflow: "deep_research"})
	bus.Emit(event.ToolUseDoneEvent{Base: base, ID: "1", Name: "read_file", Input: `{"path":"agent.go"}`})
	bus.Emit(event.ResearchProgressEvent{Base: base, Phase: "exploring", Round: 1, Message: "looking at errors"})
	bus.Emit(event.HypothesisEvent{Base: base, HypothesisID: "h1", Statement: "sentinels are better", Status: "validated"})
	bus.Emit(event.ResearchProgressEvent{Base: base, Phase: "conclusion", Round: 3, Message: "sentinel errors preferred"})
	bus.Emit(event.AgentFinishEvent{Base: base, FinishReason: "end_turn", Usage: llm.UsageStats{}})

	closeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := md.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	p1, err := store.Read("analyses/research-finding.md")
	if err != nil {
		t.Fatalf("analyses page not created: %v", err)
	}
	if p1.Type != wiki.PageTypeAnalysis {
		t.Errorf("p1.Type = %q, want %q", p1.Type, wiki.PageTypeAnalysis)
	}
	source, _ := p1.Extra["source"].(string)
	if source != "magic-docs" {
		t.Errorf("p1 source = %q, want magic-docs", source)
	}

	p2, err := store.Read("concepts/sentinel-errors.md")
	if err != nil {
		t.Fatalf("concepts page not created: %v", err)
	}
	if p2.Type != wiki.PageTypeConcept {
		t.Errorf("p2.Type = %q, want %q", p2.Type, wiki.PageTypeConcept)
	}
	sess, _ := p2.Extra["source_session"].(string)
	if sess != "integration-test" {
		t.Errorf("p2 source_session = %q, want integration-test", sess)
	}
}
