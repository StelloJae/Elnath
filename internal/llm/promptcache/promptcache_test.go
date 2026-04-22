package promptcache

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestRecordPromptState_FieldsPopulated(t *testing.T) {
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	state := RecordPromptState(Input{
		Model:      "claude-opus-4-7[1m]",
		APIModel:   "claude-opus-4-7",
		System:     "You are Elnath.",
		Tools:      []Tool{{Name: "bash", Schema: json.RawMessage(`{"type":"object"}`)}},
		Betas:      []string{"context-1m-2025-08-07"},
		Effort:     "standard",
		CacheScope: "ephemeral",
		CacheTTL:   5 * time.Minute,
		Now:        now,
	})

	if state.Model != "claude-opus-4-7[1m]" {
		t.Errorf("Model = %q, want %q", state.Model, "claude-opus-4-7[1m]")
	}
	if state.APIModel != "claude-opus-4-7" {
		t.Errorf("APIModel = %q, want %q", state.APIModel, "claude-opus-4-7")
	}
	if state.SystemLen != len("You are Elnath.") {
		t.Errorf("SystemLen = %d, want %d", state.SystemLen, len("You are Elnath."))
	}
	if state.SystemHash == "" {
		t.Error("SystemHash is empty")
	}
	if _, ok := state.ToolHashes["bash"]; !ok {
		t.Error("ToolHashes missing bash entry")
	}
	if !reflect.DeepEqual(state.ToolOrder, []string{"bash"}) {
		t.Errorf("ToolOrder = %v, want [bash]", state.ToolOrder)
	}
	if state.ToolsHash == "" {
		t.Error("ToolsHash is empty")
	}
	if !reflect.DeepEqual(state.Betas, []string{"context-1m-2025-08-07"}) {
		t.Errorf("Betas = %v, want [context-1m-2025-08-07]", state.Betas)
	}
	if state.Effort != "standard" {
		t.Errorf("Effort = %q, want standard", state.Effort)
	}
	if state.CacheScope != "ephemeral" {
		t.Errorf("CacheScope = %q, want ephemeral", state.CacheScope)
	}
	if state.CacheTTL != 5*time.Minute {
		t.Errorf("CacheTTL = %v, want 5m", state.CacheTTL)
	}
	if !state.CapturedAt.Equal(now) {
		t.Errorf("CapturedAt = %v, want %v", state.CapturedAt, now)
	}
}

func TestRecordPromptState_SystemHashDiffers(t *testing.T) {
	a := RecordPromptState(Input{System: "prompt one"})
	b := RecordPromptState(Input{System: "prompt two"})
	if a.SystemHash == b.SystemHash {
		t.Errorf("SystemHash collision: both = %q", a.SystemHash)
	}
}

func TestRecordPromptState_IdenticalSystemReusesHash(t *testing.T) {
	a := RecordPromptState(Input{System: "identical"})
	b := RecordPromptState(Input{System: "identical"})
	if a.SystemHash != b.SystemHash {
		t.Errorf("SystemHash stability broken: %q vs %q", a.SystemHash, b.SystemHash)
	}
}

func TestRecordPromptState_ToolsHashReflectsSchemaEdit(t *testing.T) {
	base := RecordPromptState(Input{Tools: []Tool{
		{Name: "bash", Schema: json.RawMessage(`{"type":"object","properties":{"cmd":{"type":"string"}}}`)},
	}})
	edited := RecordPromptState(Input{Tools: []Tool{
		{Name: "bash", Schema: json.RawMessage(`{"type":"object","properties":{"cmd":{"type":"string"},"cwd":{"type":"string"}}}`)},
	}})
	if base.ToolsHash == edited.ToolsHash {
		t.Error("ToolsHash did not change after schema edit")
	}
	if base.ToolHashes["bash"] == edited.ToolHashes["bash"] {
		t.Error("per-tool hash did not change after schema edit")
	}
}

func TestRecordPromptState_ToolsHashOrderSensitive(t *testing.T) {
	a := RecordPromptState(Input{Tools: []Tool{
		{Name: "bash", Schema: json.RawMessage(`{}`)},
		{Name: "grep", Schema: json.RawMessage(`{}`)},
	}})
	b := RecordPromptState(Input{Tools: []Tool{
		{Name: "grep", Schema: json.RawMessage(`{}`)},
		{Name: "bash", Schema: json.RawMessage(`{}`)},
	}})
	if a.ToolsHash == b.ToolsHash {
		t.Error("ToolsHash should be order-sensitive (cache key stability)")
	}
	if !reflect.DeepEqual(a.ToolOrder, []string{"bash", "grep"}) {
		t.Errorf("a.ToolOrder = %v, want [bash grep]", a.ToolOrder)
	}
	if !reflect.DeepEqual(b.ToolOrder, []string{"grep", "bash"}) {
		t.Errorf("b.ToolOrder = %v, want [grep bash]", b.ToolOrder)
	}
}

func TestRecordPromptState_BetasSortedAndDeduped(t *testing.T) {
	state := RecordPromptState(Input{
		Betas: []string{"z-flag", "a-flag", "z-flag", "", "m-flag"},
	})
	want := []string{"a-flag", "m-flag", "z-flag"}
	if !reflect.DeepEqual(state.Betas, want) {
		t.Errorf("Betas = %v, want %v", state.Betas, want)
	}
}

func TestRecordPromptState_EmptyBetasReturnsNil(t *testing.T) {
	state := RecordPromptState(Input{})
	if state.Betas != nil {
		t.Errorf("Betas = %v, want nil for empty input", state.Betas)
	}
}

func TestRecordPromptState_NowDefaultsToWallclockWhenZero(t *testing.T) {
	before := time.Now().UTC()
	state := RecordPromptState(Input{System: "x"})
	after := time.Now().UTC().Add(time.Second)
	if state.CapturedAt.Before(before) || state.CapturedAt.After(after) {
		t.Errorf("CapturedAt = %v, want between %v and %v", state.CapturedAt, before, after)
	}
}

func TestRecordPromptState_ToolOrderDeepCopied(t *testing.T) {
	tools := []Tool{{Name: "bash", Schema: json.RawMessage(`{}`)}}
	state := RecordPromptState(Input{Tools: tools})
	if len(state.ToolOrder) != 1 || state.ToolOrder[0] != "bash" {
		t.Fatalf("unexpected ToolOrder: %v", state.ToolOrder)
	}
	// Mutate caller-owned slice; the snapshot must not track the change.
	tools[0].Name = "mutated"
	if state.ToolOrder[0] != "bash" {
		t.Errorf("PromptState.ToolOrder mutated via caller's slice: %v", state.ToolOrder)
	}
}
