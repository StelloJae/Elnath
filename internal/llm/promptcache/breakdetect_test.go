package promptcache

import (
	"encoding/json"
	"testing"
	"time"
)

// fixtureAsResponse lifts a fixture into the post-call Response shape.
func fixtureAsResponse(f APIResponseFixture) *Response {
	return RecordResponse(Usage{
		InputTokens:              f.Usage.InputTokens,
		OutputTokens:             f.Usage.OutputTokens,
		CacheReadInputTokens:     f.Usage.CacheReadInputTokens,
		CacheCreationInputTokens: f.Usage.CacheCreationInputTokens,
	}, f.ReceivedAt)
}

// baselineState returns a matching pair of pre/post states with no diffs;
// callers mutate only the axis they are testing so attributions stay
// isolated.
func baselineState(t time.Time) *PromptState {
	return RecordPromptState(Input{
		Model:      "claude-opus-4-7[1m]",
		APIModel:   "claude-opus-4-7",
		System:     "You are Elnath.",
		Tools:      []Tool{{Name: "bash", Schema: json.RawMessage(`{"type":"object","properties":{"cmd":{"type":"string"}}}`)}},
		Betas:      []string{"context-1m-2025-08-07"},
		Effort:     "standard",
		CacheScope: "ephemeral",
		CacheTTL:   5 * time.Minute,
		Now:        t,
	})
}

// TestCheckForCacheBreak_CleanHitNoAttribution confirms the detector stays
// silent when there's no creation activity (a pure hit).
func TestCheckForCacheBreak_CleanHitNoAttribution(t *testing.T) {
	f := loadFixture(t, "cache_hit_warm")
	resp := fixtureAsResponse(f)
	pre := baselineState(time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC))
	post := baselineState(f.ReceivedAt)

	report := CheckForCacheBreak(pre, post, resp)
	if report.Happened {
		t.Errorf("Happened = true, want false (pure hit)")
	}
	if report.BelowThreshold {
		t.Errorf("BelowThreshold = true, want false for a clean hit")
	}
	if report.ReadTokens == 0 {
		t.Errorf("ReadTokens = 0, want non-zero for a hit")
	}
	if len(report.Reasons) != 0 {
		t.Errorf("Reasons = %v, want none", report.Reasons)
	}
}

// TestCheckForCacheBreak_BelowThresholdNoiseSuppressed confirms small
// creation counts (partner content edit) do not fire attribution.
func TestCheckForCacheBreak_BelowThresholdNoiseSuppressed(t *testing.T) {
	f := loadFixture(t, "cache_miss_small")
	resp := fixtureAsResponse(f)
	pre := baselineState(time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC))
	post := baselineState(f.ReceivedAt)

	report := CheckForCacheBreak(pre, post, resp)
	if report.Happened {
		t.Errorf("Happened = true, want false (below threshold)")
	}
	if !report.BelowThreshold {
		t.Errorf("BelowThreshold = false, want true for %d creation tokens", report.CreationTokens)
	}
	if len(report.Reasons) != 0 {
		t.Errorf("Reasons populated despite below-threshold suppression: %v", report.Reasons)
	}
}

// TestCheckForCacheBreak_SystemPromptAttribution exercises the
// system-prompt vector with the cache_miss_system_edit fixture.
func TestCheckForCacheBreak_SystemPromptAttribution(t *testing.T) {
	f := loadFixture(t, "cache_miss_system_edit")
	resp := fixtureAsResponse(f)
	pre := baselineState(time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC))
	post := RecordPromptState(Input{
		Model:      pre.Model,
		APIModel:   pre.APIModel,
		System:     "You are Elnath. New bullet: cite references.",
		Tools:      []Tool{{Name: "bash", Schema: json.RawMessage(`{"type":"object","properties":{"cmd":{"type":"string"}}}`)}},
		Betas:      pre.Betas,
		Effort:     pre.Effort,
		CacheScope: pre.CacheScope,
		CacheTTL:   pre.CacheTTL,
		Now:        f.ReceivedAt,
	})

	report := CheckForCacheBreak(pre, post, resp)
	if !report.Happened {
		t.Fatalf("Happened = false, want true (creation %d > threshold)", report.CreationTokens)
	}
	if !hasReason(report.Reasons, ReasonSystemPrompt) {
		t.Errorf("Reasons = %v, want system_prompt", report.Reasons)
	}
}

// TestCheckForCacheBreak_ToolSchemaEditAttribution flips a tool's schema
// between pre and post. Per-tool hash should carry the signal.
func TestCheckForCacheBreak_ToolSchemaEditAttribution(t *testing.T) {
	f := loadFixture(t, "cache_miss_tool_edit")
	resp := fixtureAsResponse(f)
	pre := baselineState(time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC))
	post := RecordPromptState(Input{
		Model:      pre.Model,
		APIModel:   pre.APIModel,
		System:     pre.System,
		Tools:      []Tool{{Name: "bash", Schema: json.RawMessage(`{"type":"object","properties":{"cmd":{"type":"string"},"cwd":{"type":"string"}}}`)}},
		Betas:      pre.Betas,
		Effort:     pre.Effort,
		CacheScope: pre.CacheScope,
		CacheTTL:   pre.CacheTTL,
		Now:        f.ReceivedAt,
	})

	report := CheckForCacheBreak(pre, post, resp)
	if !report.Happened {
		t.Fatalf("Happened = false, want true")
	}
	if !hasReasonWithDetail(report.Reasons, ReasonToolSchemaEdit, "bash") {
		t.Errorf("Reasons = %v, want tool_schema_edit:bash", report.Reasons)
	}
}

func TestCheckForCacheBreak_ToolSchemaAddedAttribution(t *testing.T) {
	f := loadFixture(t, "cache_miss_tool_edit") // reuse tokens; scenario is add
	resp := fixtureAsResponse(f)
	pre := baselineState(time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC))
	post := RecordPromptState(Input{
		Model:    pre.Model,
		APIModel: pre.APIModel,
		System:   pre.System,
		Tools: []Tool{
			{Name: "bash", Schema: json.RawMessage(`{"type":"object","properties":{"cmd":{"type":"string"}}}`)},
			{Name: "grep", Schema: json.RawMessage(`{"type":"object"}`)},
		},
		Betas:      pre.Betas,
		Effort:     pre.Effort,
		CacheScope: pre.CacheScope,
		CacheTTL:   pre.CacheTTL,
		Now:        f.ReceivedAt,
	})

	report := CheckForCacheBreak(pre, post, resp)
	if !hasReasonWithDetail(report.Reasons, ReasonToolSchemaAdded, "grep") {
		t.Errorf("Reasons = %v, want tool_schema_added:grep", report.Reasons)
	}
	if hasReason(report.Reasons, ReasonToolOrder) {
		t.Errorf("unexpected tool_order reason when a tool was added: %v", report.Reasons)
	}
}

func TestCheckForCacheBreak_BetaDeltaAttribution(t *testing.T) {
	f := loadFixture(t, "cache_miss_beta_delta")
	resp := fixtureAsResponse(f)
	pre := baselineState(time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC))
	post := RecordPromptState(Input{
		Model:      pre.Model,
		APIModel:   pre.APIModel,
		System:     pre.System,
		Tools:      []Tool{{Name: "bash", Schema: json.RawMessage(`{"type":"object","properties":{"cmd":{"type":"string"}}}`)}},
		Betas:      []string{"context-1m-2025-08-07", "fast-mode-2026-02-01"},
		Effort:     pre.Effort,
		CacheScope: pre.CacheScope,
		CacheTTL:   pre.CacheTTL,
		Now:        f.ReceivedAt,
	})

	report := CheckForCacheBreak(pre, post, resp)
	if !hasReasonWithDetail(report.Reasons, ReasonBetaAdded, "fast-mode-2026-02-01") {
		t.Errorf("Reasons = %v, want beta_added:fast-mode-2026-02-01", report.Reasons)
	}
}

// TestCheckForCacheBreak_TTLExpiryFallback exercises the >5min gap path
// where no structural vector explains the break.
func TestCheckForCacheBreak_TTLExpiryFallback(t *testing.T) {
	f := loadFixture(t, "cache_miss_ttl_gap")
	resp := fixtureAsResponse(f)
	pre := baselineState(time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC))
	post := baselineState(f.ReceivedAt) // no structural change
	report := CheckForCacheBreak(pre, post, resp)
	if !report.Happened {
		t.Fatalf("Happened = false, want true")
	}
	if len(report.Reasons) != 1 || report.Reasons[0].Reason != ReasonTTLExpiry {
		t.Errorf("Reasons = %v, want single ttl_expiry attribution", report.Reasons)
	}
	if report.GapSince < 5*time.Minute {
		t.Errorf("GapSince = %v, want >=5m", report.GapSince)
	}
}

// TestCheckForCacheBreak_ServerSideFallback covers the case where no
// structural vector explains a break and the gap is under the TTL
// window — attribution should be server_side.
func TestCheckForCacheBreak_ServerSideFallback(t *testing.T) {
	pre := baselineState(time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC))
	post := baselineState(time.Date(2026, 4, 22, 12, 1, 0, 0, time.UTC)) // 1min gap
	resp := RecordResponse(Usage{CacheCreationInputTokens: 3500}, time.Date(2026, 4, 22, 12, 1, 0, 0, time.UTC))

	report := CheckForCacheBreak(pre, post, resp)
	if !report.Happened {
		t.Fatalf("Happened = false, want true")
	}
	if len(report.Reasons) != 1 || report.Reasons[0].Reason != ReasonServerSide {
		t.Errorf("Reasons = %v, want single server_side attribution", report.Reasons)
	}
}

// TestCheckForCacheBreak_ModelSwapAttribution verifies model-ID diffs get
// both model_swap and api_model_swap reasons when both fields diverge.
func TestCheckForCacheBreak_ModelSwapAttribution(t *testing.T) {
	pre := baselineState(time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC))
	post := RecordPromptState(Input{
		Model:      "claude-opus-4-6",
		APIModel:   "claude-opus-4-6",
		System:     pre.System,
		Tools:      []Tool{{Name: "bash", Schema: json.RawMessage(`{"type":"object","properties":{"cmd":{"type":"string"}}}`)}},
		Betas:      pre.Betas,
		Effort:     pre.Effort,
		CacheScope: pre.CacheScope,
		CacheTTL:   pre.CacheTTL,
		Now:        time.Date(2026, 4, 22, 12, 1, 0, 0, time.UTC),
	})
	resp := RecordResponse(Usage{CacheCreationInputTokens: 4000}, post.CapturedAt)

	report := CheckForCacheBreak(pre, post, resp)
	if !hasReason(report.Reasons, ReasonModelSwap) {
		t.Errorf("Reasons = %v, want model_swap", report.Reasons)
	}
	if !hasReason(report.Reasons, ReasonAPIModelSwap) {
		t.Errorf("Reasons = %v, want api_model_swap", report.Reasons)
	}
}

// TestCheckForCacheBreak_ToolOrderOnly exercises order-only diff with
// identical tool content hashes.
func TestCheckForCacheBreak_ToolOrderOnly(t *testing.T) {
	pre := RecordPromptState(Input{
		System: "x",
		Tools: []Tool{
			{Name: "a", Schema: json.RawMessage(`{}`)},
			{Name: "b", Schema: json.RawMessage(`{}`)},
		},
		Now: time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC),
	})
	post := RecordPromptState(Input{
		System: "x",
		Tools: []Tool{
			{Name: "b", Schema: json.RawMessage(`{}`)},
			{Name: "a", Schema: json.RawMessage(`{}`)},
		},
		Now: time.Date(2026, 4, 22, 12, 1, 0, 0, time.UTC),
	})
	resp := RecordResponse(Usage{CacheCreationInputTokens: 3000}, post.CapturedAt)

	report := CheckForCacheBreak(pre, post, resp)
	if !hasReason(report.Reasons, ReasonToolOrder) {
		t.Errorf("Reasons = %v, want tool_order", report.Reasons)
	}
}

// TestRecordResponse_DefaultsToWallclock verifies zero timestamps get
// replaced with time.Now() so accidental missing ReceivedAt doesn't
// break TTL attribution.
func TestRecordResponse_DefaultsToWallclock(t *testing.T) {
	before := time.Now().UTC()
	r := RecordResponse(Usage{}, time.Time{})
	after := time.Now().UTC().Add(time.Second)
	if r.ReceivedAt.Before(before) || r.ReceivedAt.After(after) {
		t.Errorf("ReceivedAt = %v, want between %v and %v", r.ReceivedAt, before, after)
	}
}

// TestCheckForCacheBreak_NilResponseNoOp guards the nil path so the
// caller can call with partial state without panicking.
func TestCheckForCacheBreak_NilResponseNoOp(t *testing.T) {
	report := CheckForCacheBreak(nil, nil, nil)
	if report == nil {
		t.Fatal("report is nil")
	}
	if report.Happened || report.BelowThreshold || len(report.Reasons) != 0 {
		t.Errorf("expected empty report, got %+v", report)
	}
}

// --- helpers ---

func hasReason(reasons []BreakDetail, want BreakReason) bool {
	for _, r := range reasons {
		if r.Reason == want {
			return true
		}
	}
	return false
}

func hasReasonWithDetail(reasons []BreakDetail, want BreakReason, detail string) bool {
	for _, r := range reasons {
		if r.Reason == want && r.Detail == detail {
			return true
		}
	}
	return false
}
