package promptcache

import (
	"fmt"
	"sort"
	"time"
)

// MinCacheMissTokens mirrors Claude Code's MIN_CACHE_MISS_TOKENS (audit.txt
// §03). Creation-token counts below this threshold are treated as noise
// from content edits rather than a real cache break, to avoid attribution
// whiplash on every user keystroke.
const MinCacheMissTokens = 2000

// TTLExpiryWindow is the gap after which an otherwise-unexplained break
// is attributed to TTL expiry rather than a server-side cache flip.
// audit.txt §03: ">5min gap suggests TTL expiry; <5min suggests server issue".
const TTLExpiryWindow = 5 * time.Minute

// Usage is the cache-relevant subset of an Anthropic Messages API response.
// Projected from any provider SDK so promptcache stays dependency-free.
type Usage struct {
	InputTokens              int
	OutputTokens             int
	CacheReadInputTokens     int
	CacheCreationInputTokens int
}

// Response is the post-call snapshot. ReceivedAt drives the TTL-gap
// heuristic; Usage drives the threshold gate and attribution direction.
type Response struct {
	Usage      Usage
	ReceivedAt time.Time
}

// RecordResponse is the dual of RecordPromptState. Captures usage at the
// exact wall-clock instant needed by the TTL-gap heuristic.
func RecordResponse(usage Usage, receivedAt time.Time) *Response {
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	}
	return &Response{Usage: usage, ReceivedAt: receivedAt}
}

// BreakReason enumerates the closed set of attribution reasons. A closed
// enum is deliberate (CLAUDE.md: "structured over free-text hint"):
// downstream consumers (reflection hooks, CLI surface in Commit 6) match
// on exact values rather than parsing prose.
type BreakReason string

const (
	ReasonSystemPrompt      BreakReason = "system_prompt"
	ReasonToolSchemaAdded   BreakReason = "tool_schema_added"
	ReasonToolSchemaRemoved BreakReason = "tool_schema_removed"
	ReasonToolSchemaEdit    BreakReason = "tool_schema_edit"
	ReasonToolOrder         BreakReason = "tool_order"
	ReasonBetaAdded         BreakReason = "beta_added"
	ReasonBetaRemoved       BreakReason = "beta_removed"
	ReasonScopeFlip         BreakReason = "scope_flip"
	ReasonTTLFlip           BreakReason = "ttl_flip"
	ReasonModelSwap         BreakReason = "model_swap"
	ReasonAPIModelSwap      BreakReason = "api_model_swap"
	ReasonEffortChange      BreakReason = "effort_change"
	ReasonTTLExpiry         BreakReason = "ttl_expiry"
	ReasonServerSide        BreakReason = "server_side"
)

// BreakDetail pairs a closed-enum reason with a narrow string identifier
// for the specific source (e.g. tool name, beta flag, field pair).
type BreakDetail struct {
	Reason BreakReason
	Detail string
}

// BreakReport is the detector's complete verdict for one call.
type BreakReport struct {
	// Happened is true when the response shows a real cache miss worth
	// surfacing (above MinCacheMissTokens).
	Happened bool

	// BelowThreshold reports that creation tokens were non-zero but
	// below MinCacheMissTokens. Treated as non-actionable noise.
	BelowThreshold bool

	// CreationTokens / ReadTokens forward the usage fields so callers
	// can present a quick summary without re-reading Response.
	CreationTokens int
	ReadTokens     int

	// GapSince is post.CapturedAt - pre.CapturedAt, used by the TTL
	// heuristic. Zero if either side is missing a timestamp.
	GapSince time.Duration

	// Reasons is the ordered list of structural diffs the attributor
	// found. An empty slice with Happened=true means no structural diff
	// explains the miss — see ReasonTTLExpiry or ReasonServerSide for
	// classification.
	Reasons []BreakDetail
}

// CheckForCacheBreak is the two-phase detector from audit.txt §03.
//
// Phase 1 (gate): inspect resp.Usage against MinCacheMissTokens. Large
// cache_read dominates; small creation is noise; small creation with no
// read is a below-threshold signal we annotate but do not surface as a
// break.
//
// Phase 2 (attribute): diff pre vs post PromptState across every trigger
// vector CC tracks (system prompt hash, per-tool schema hash + order,
// beta flags, scope/TTL, model IDs, effort). If no structural vector
// explains a real miss, classify by TTL gap; otherwise server_side.
//
// Argument semantics — callers MUST preserve:
//   - `pre` is the PROMPT STATE CAPTURED BEFORE THE **PRIOR TURN's** CALL.
//     Not the prior response snapshot, not the current call's pre-state.
//     `GapSince` is computed as `post.CapturedAt - pre.CapturedAt`, so
//     feeding the current turn as both arguments yields GapSince=0 and
//     ttl_expiry can never fire. Writers integrating this detector must
//     cache the prior turn's PromptState per session.
//   - `post` is the pre-call snapshot for the CURRENT turn.
//   - `resp` is the response from the current turn.
//
// Nil pre/post/resp are tolerated and yield an empty (non-happened) report.
func CheckForCacheBreak(pre, post *PromptState, resp *Response) *BreakReport {
	report := &BreakReport{}
	if resp == nil {
		return report
	}
	report.CreationTokens = resp.Usage.CacheCreationInputTokens
	report.ReadTokens = resp.Usage.CacheReadInputTokens

	// Phase 1: threshold gate.
	if report.CreationTokens == 0 {
		return report // clean cache hit, nothing to attribute
	}
	if report.CreationTokens < MinCacheMissTokens {
		report.BelowThreshold = true
		return report
	}
	report.Happened = true

	if pre != nil && post != nil && !pre.CapturedAt.IsZero() && !post.CapturedAt.IsZero() {
		report.GapSince = post.CapturedAt.Sub(pre.CapturedAt)
	}

	// Phase 2: attribution.
	report.Reasons = attributeBreak(pre, post)
	if len(report.Reasons) == 0 {
		if report.GapSince >= TTLExpiryWindow {
			report.Reasons = []BreakDetail{{Reason: ReasonTTLExpiry, Detail: report.GapSince.Round(time.Second).String()}}
		} else {
			report.Reasons = []BreakDetail{{Reason: ReasonServerSide}}
		}
	}
	return report
}

func attributeBreak(pre, post *PromptState) []BreakDetail {
	if pre == nil || post == nil {
		return nil
	}
	var out []BreakDetail

	if pre.Model != post.Model {
		out = append(out, BreakDetail{
			Reason: ReasonModelSwap,
			Detail: fmt.Sprintf("%s→%s", pre.Model, post.Model),
		})
	}
	if pre.APIModel != post.APIModel {
		out = append(out, BreakDetail{
			Reason: ReasonAPIModelSwap,
			Detail: fmt.Sprintf("%s→%s", pre.APIModel, post.APIModel),
		})
	}

	if pre.SystemHash != post.SystemHash {
		out = append(out, BreakDetail{
			Reason: ReasonSystemPrompt,
			Detail: fmt.Sprintf("len %d→%d", pre.SystemLen, post.SystemLen),
		})
	}

	added, removed, edited := diffToolHashes(pre.ToolHashes, post.ToolHashes)
	for _, n := range added {
		out = append(out, BreakDetail{Reason: ReasonToolSchemaAdded, Detail: n})
	}
	for _, n := range removed {
		out = append(out, BreakDetail{Reason: ReasonToolSchemaRemoved, Detail: n})
	}
	for _, n := range edited {
		out = append(out, BreakDetail{Reason: ReasonToolSchemaEdit, Detail: n})
	}
	// Order diff only matters when the tool set is otherwise identical;
	// added/removed already dominate attribution when the sets differ.
	if len(added) == 0 && len(removed) == 0 && !sliceEqual(pre.ToolOrder, post.ToolOrder) {
		out = append(out, BreakDetail{Reason: ReasonToolOrder})
	}

	betaAdded, betaRemoved := diffStringSets(pre.Betas, post.Betas)
	for _, b := range betaAdded {
		out = append(out, BreakDetail{Reason: ReasonBetaAdded, Detail: b})
	}
	for _, b := range betaRemoved {
		out = append(out, BreakDetail{Reason: ReasonBetaRemoved, Detail: b})
	}

	if pre.CacheScope != post.CacheScope {
		out = append(out, BreakDetail{
			Reason: ReasonScopeFlip,
			Detail: fmt.Sprintf("%q→%q", pre.CacheScope, post.CacheScope),
		})
	}
	if pre.CacheTTL != post.CacheTTL {
		out = append(out, BreakDetail{
			Reason: ReasonTTLFlip,
			Detail: fmt.Sprintf("%v→%v", pre.CacheTTL, post.CacheTTL),
		})
	}

	if pre.Effort != post.Effort {
		out = append(out, BreakDetail{
			Reason: ReasonEffortChange,
			Detail: fmt.Sprintf("%q→%q", pre.Effort, post.Effort),
		})
	}

	return out
}

// diffToolHashes partitions names into added/removed/edited relative to pre.
func diffToolHashes(pre, post map[string]string) (added, removed, edited []string) {
	for name, postHash := range post {
		preHash, ok := pre[name]
		if !ok {
			added = append(added, name)
			continue
		}
		if preHash != postHash {
			edited = append(edited, name)
		}
	}
	for name := range pre {
		if _, ok := post[name]; !ok {
			removed = append(removed, name)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(edited)
	return
}

// diffStringSets returns (added, removed). Inputs are expected to be sorted
// and deduplicated (invariant established by sortedUnique in promptcache.go).
func diffStringSets(pre, post []string) (added, removed []string) {
	preSet := make(map[string]struct{}, len(pre))
	for _, s := range pre {
		preSet[s] = struct{}{}
	}
	postSet := make(map[string]struct{}, len(post))
	for _, s := range post {
		postSet[s] = struct{}{}
	}
	for _, s := range post {
		if _, ok := preSet[s]; !ok {
			added = append(added, s)
		}
	}
	for _, s := range pre {
		if _, ok := postSet[s]; !ok {
			removed = append(removed, s)
		}
	}
	return
}

func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
