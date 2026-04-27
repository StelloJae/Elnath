package tools

import "sync"

// decisionDenyCap caps the per-Run deny Decision retention. Sized at
// 64 events to cover ~32 distinct denied destinations attempted with
// TCP+HTTPS_CONNECT each. Pathological single-Run attempts exceeding
// 64 distinct denies surface via ViolationDropCount; the first 64
// retain forensic value (FIFO drop on overflow). The cap is reason-
// independent — adding new ProxyReason enum values does NOT auto-grow
// the cap. Operator signal for "cap exceeded" is non-zero
// ViolationDropCount in BashRunResult.
const decisionDenyCap = 64

// decisionAllowCap caps the per-Run allow Decision retention. Tracks
// the previous (v42-1b) constant value of 200 so summary + slog
// behavior is preserved byte-identically across the v42-2 drain-time
// enforcement migration.
const decisionAllowCap = 200

// boundedDecisionBuffer is the per-Run drain-time cap on Decision
// events parsed off the netproxy child's stdout. Owned by each
// substrate runner (Seatbelt + bwrap) as a pointer field. The two
// slices are independent: an allow flood cannot starve deny retention.
//
// Concurrency contract:
//   - Push is called from the drain goroutine ONLY (one goroutine per
//     runner; no cross-Run sharing because runners are single-Run).
//   - Drain is called from Run() on completion ONLY.
//   - mu protects all four data fields; held only briefly per call.
//
// Memory bound: O(decisionDenyCap + decisionAllowCap) = ~50KiB worst
// case (Decision struct in-memory <200 bytes). Constant regardless
// of producer volume.
type boundedDecisionBuffer struct {
	mu         sync.Mutex
	denyBuf    []Decision
	allowBuf   []Decision
	denyDrops  int
	allowDrops int

	denyCap  int
	allowCap int
}

// newDecisionBuffer returns a buffer pre-sized with production caps
// (decisionDenyCap=64, decisionAllowCap=200).
func newDecisionBuffer() *boundedDecisionBuffer {
	return &boundedDecisionBuffer{
		denyCap:  decisionDenyCap,
		allowCap: decisionAllowCap,
	}
}

// newDecisionBufferForTest returns a buffer with caller-specified
// caps. Used by netproxy_drain_test.go tests that exercise overflow
// and cap=0 scenarios. Negative caps are clamped to 0 to mirror
// projectAuditRecords semantics.
func newDecisionBufferForTest(denyCap, allowCap int) *boundedDecisionBuffer {
	if denyCap < 0 {
		denyCap = 0
	}
	if allowCap < 0 {
		allowCap = 0
	}
	return &boundedDecisionBuffer{
		denyCap:  denyCap,
		allowCap: allowCap,
	}
}

// Push routes d to the deny or allow slice based on d.Allow. When
// the target slice is at cap, the corresponding drop counter
// increments and d is silently dropped (FIFO retention: first N kept).
// Drop counts surface via Drain. Caller is the drain goroutine; Push
// MUST NOT block on telemetry per the EventSink invariant.
func (b *boundedDecisionBuffer) Push(d Decision) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if d.Allow {
		if len(b.allowBuf) >= b.allowCap {
			b.allowDrops++
			return
		}
		b.allowBuf = append(b.allowBuf, d)
		return
	}
	if len(b.denyBuf) >= b.denyCap {
		b.denyDrops++
		return
	}
	b.denyBuf = append(b.denyBuf, d)
}

// Drain returns the deny + allow slices accumulated since the last
// Drain call, plus the drop counts for each. All four state pieces
// are reset before returning so the next Run starts clean.
//
// Single-snapshot is load-bearing: both deny and allow Decisions
// plus both drop counts come from one mu-guarded snapshot, so a
// Decision push that races the Drain call lands either fully in
// this Run or fully in the next, never split.
func (b *boundedDecisionBuffer) Drain() (deny []Decision, allow []Decision, denyDrops, allowDrops int) {
	b.mu.Lock()
	deny = b.denyBuf
	allow = b.allowBuf
	denyDrops = b.denyDrops
	allowDrops = b.allowDrops
	b.denyBuf = nil
	b.allowBuf = nil
	b.denyDrops = 0
	b.allowDrops = 0
	b.mu.Unlock()
	return
}

// projectAuditRecordsFromAllowOnly is the post-v42-2 shape projection.
// Receives a slice of allow-only Decisions (already capped upstream
// by the drain buffer) and renders SandboxAuditRecord entries with
// the N6 retention policy enforced (only host/port/protocol/source/
// decision retained). Caller is responsible for filtering allow-only.
func projectAuditRecordsFromAllowOnly(allows []Decision) []SandboxAuditRecord {
	if len(allows) == 0 {
		return nil
	}
	out := make([]SandboxAuditRecord, 0, len(allows))
	for _, d := range allows {
		out = append(out, SandboxAuditRecord{
			Host:     sanitizeViolationField(d.Host),
			Port:     uint16(d.Port),
			Protocol: sanitizeViolationField(string(d.Protocol)),
			Source:   string(d.Source),
			Decision: "allow",
		})
	}
	return out
}
