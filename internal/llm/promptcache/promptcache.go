// Package promptcache captures pre-call prompt state so a later step can
// attribute prompt-cache breaks to specific trigger vectors.
//
// The design mirrors Claude Code's promptCacheBreakDetection.ts semantics
// (audit.txt §03) adapted to Elnath's llm.Provider shape: a snapshot taken
// before the API call records the exact inputs that influence cache
// hashing — system prompt, per-tool schema hashes, model ID, beta headers,
// thinking-effort tier, and cache scope/TTL. Post-call break detection
// (RecordResponse / checkResponseForCacheBreak) lands in a follow-up change
// (Phase 8.1 Commit 4).
//
// This package intentionally does not import internal/llm to keep the
// dependency direction one-way (llm → promptcache) and avoid an import
// cycle. Callers project their Request into Input before recording.
package promptcache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"
)

// Input is the intake shape the caller populates from their request just
// before the HTTP call. It is decoupled from llm.ChatRequest so this
// package stays dependency-free w.r.t. llm.
type Input struct {
	// Model is the caller-visible model ID, including Elnath-internal
	// suffixes like "[1m]". Retained alongside APIModel because the
	// suffix itself is a cache-key input.
	Model string

	// APIModel is what actually went on the wire (suffix stripped).
	APIModel string

	// System is the full system prompt text sent to the provider.
	System string

	// Tools is the ordered tool catalog presented to the model. Order
	// matters for cache stability (Claude Code sorts by name + source
	// before presenting; we capture the caller's order and compute a
	// normalized hash below).
	Tools []Tool

	// Betas is the set of anthropic-beta flags attached to the request.
	// Sorted + deduplicated on capture so add/remove diffs are trivial
	// to compute later.
	Betas []string

	// Effort identifies the thinking-budget tier (e.g. "off", "low",
	// "standard", "ultra"). CC tracks this as an explicit break trigger
	// (audit.txt §03 "effort value" bullet).
	Effort string

	// CacheScope is the cache_control scope applied to system/tools
	// (e.g. "ephemeral" or "" when disabled).
	CacheScope string

	// CacheTTL is a caller-supplied TTL hint if the provider supports
	// it. Zero means "provider default".
	CacheTTL time.Duration

	// Now is an injectable clock for deterministic tests. Zero value
	// means use time.Now().
	Now time.Time
}

// Tool pairs a tool name with its JSON schema. Keeping this type in the
// promptcache package avoids importing internal/llm's ToolDef.
type Tool struct {
	Name   string
	Schema json.RawMessage
}

// PromptState is the immutable snapshot captured by RecordPromptState. It
// carries everything a later cache-break detector needs to attribute a
// miss to a specific vector (system prompt edit, tool schema change,
// model swap, beta flag delta, scope/TTL flip, effort change).
//
// Consumers must treat PromptState as read-only; mutating a captured
// state defeats the break-attribution guarantee.
type PromptState struct {
	Model        string
	APIModel     string
	System       string
	SystemHash   string
	SystemLen    int
	ToolHashes   map[string]string
	ToolOrder    []string
	ToolsHash    string
	Betas        []string
	Effort       string
	CacheScope   string
	CacheTTL     time.Duration
	CapturedAt   time.Time
}

// RecordPromptState captures the pre-call state. The returned PromptState
// is a deep copy of the caller's inputs; subsequent mutation of Input
// fields does not affect the snapshot.
func RecordPromptState(in Input) *PromptState {
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	toolHashes := make(map[string]string, len(in.Tools))
	toolOrder := make([]string, 0, len(in.Tools))
	for _, t := range in.Tools {
		toolHashes[t.Name] = hashBytes(t.Schema)
		toolOrder = append(toolOrder, t.Name)
	}
	return &PromptState{
		Model:      in.Model,
		APIModel:   in.APIModel,
		System:     in.System,
		SystemHash: hashString(in.System),
		SystemLen:  len(in.System),
		ToolHashes: toolHashes,
		ToolOrder:  append([]string(nil), toolOrder...),
		ToolsHash:  aggregateToolsHash(toolOrder, toolHashes),
		Betas:      sortedUnique(in.Betas),
		Effort:     in.Effort,
		CacheScope: in.CacheScope,
		CacheTTL:   in.CacheTTL,
		CapturedAt: now,
	}
}

// aggregateToolsHash produces a stable hash across the full tool set so a
// single comparison can detect "any tool changed" before callers drill
// into per-tool hashes for attribution. Computed over the caller's
// presentation order to mirror cache-key semantics on the API side.
func aggregateToolsHash(order []string, hashes map[string]string) string {
	h := sha256.New()
	for _, name := range order {
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write([]byte(hashes[name]))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func hashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func sortedUnique(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
