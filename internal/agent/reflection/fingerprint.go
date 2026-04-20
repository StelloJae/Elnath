// Package reflection implements Phase 0 observe-only self-healing infrastructure.
// Components: fingerprint (task-identity hashing), trigger (gate),
// engine (LLM reflection call), store (JSONL append), pool (async dispatch).
// See docs/superpowers/specs/2026-04-20-self-healing-observe-only-phase0-design.md.
package reflection

import (
	"crypto/sha256"
	"encoding/base32"
	"sort"
	"strings"
)

// Fingerprint is a 12-char base32 digest of (normalized subject, sorted tool names).
// Stable and concurrency-safe; see spec §2.1.
type Fingerprint string

// ComputeFingerprint returns a deterministic 12-character base32 fingerprint.
// The subject is lower-cased and trimmed; tool names are sorted to make the
// hash order-invariant. A nil or empty toolNames slice is equivalent.
func ComputeFingerprint(subject string, toolNames []string) Fingerprint {
	normSubject := strings.ToLower(strings.TrimSpace(subject))

	sorted := make([]string, len(toolNames))
	copy(sorted, toolNames)
	sort.Strings(sorted)

	var b strings.Builder
	b.WriteString(normSubject)
	b.WriteByte(0x00)
	for i, name := range sorted {
		if i > 0 {
			b.WriteByte(0x1f)
		}
		b.WriteString(name)
	}

	sum := sha256.Sum256([]byte(b.String()))
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:])
	return Fingerprint(encoded[:12])
}
