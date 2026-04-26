package tools

import (
	"bytes"
	"strings"
	"testing"
)

func TestCappedOutput_BelowLimitKeepsEverything(t *testing.T) {
	w := newCappedOutput(100)
	if _, err := w.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if w.Truncated() {
		t.Error("small write should not be truncated")
	}
	if w.Render() != "hello" {
		t.Errorf("Render = %q, want hello", w.Render())
	}
	if w.RawBytes() != 5 {
		t.Errorf("RawBytes = %d, want 5", w.RawBytes())
	}
	if w.Dropped() != 0 {
		t.Errorf("Dropped = %d, want 0", w.Dropped())
	}
}

func TestCappedOutput_ExactLimitNotTruncated(t *testing.T) {
	w := newCappedOutput(10)
	data := []byte("1234567890") // exactly 10 bytes
	if _, err := w.Write(data); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if w.Truncated() {
		t.Errorf("exact-limit write should not report truncation; raw=%d kept=%d", w.RawBytes(), w.Kept())
	}
	if w.Render() != "1234567890" {
		t.Errorf("Render = %q", w.Render())
	}
}

func TestCappedOutput_AboveLimitKeepsHeadAndTail(t *testing.T) {
	w := newCappedOutput(16) // head=8, tail=8 (minimum viable split)
	w.Write([]byte("HEADHEAD"))
	w.Write([]byte(strings.Repeat("X", 50)))
	w.Write([]byte("TAILTAIL"))
	if !w.Truncated() {
		t.Fatal("expected truncated")
	}
	rendered := w.Render()
	if !strings.HasPrefix(rendered, "HEADHEAD") {
		t.Errorf("render %q does not start with HEADHEAD", rendered)
	}
	if !strings.HasSuffix(rendered, "TAILTAIL") {
		t.Errorf("render %q does not end with TAILTAIL", rendered)
	}
	if !strings.Contains(rendered, "output truncated") {
		t.Errorf("render %q missing truncation marker", rendered)
	}
	if !strings.Contains(rendered, "omitted 50 bytes") {
		t.Errorf("render %q should report 50 omitted bytes", rendered)
	}
	if w.RawBytes() != 66 {
		t.Errorf("RawBytes = %d, want 66", w.RawBytes())
	}
	if w.Kept() != 16 {
		t.Errorf("Kept = %d, want 16", w.Kept())
	}
	if w.Dropped() != 50 {
		t.Errorf("Dropped = %d, want 50", w.Dropped())
	}
}

func TestCappedOutput_MultipleWritesAggregate(t *testing.T) {
	w := newCappedOutput(10)
	writes := []string{"HE", "AD", "LOSTLOST", "LOST", "TA", "IL"}
	for _, s := range writes {
		if _, err := w.Write([]byte(s)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if !w.Truncated() {
		t.Fatal("expected truncated across many writes")
	}
	rendered := w.Render()
	if !strings.HasPrefix(rendered, "HEAD") {
		t.Errorf("render %q should preserve the first bytes HEAD", rendered)
	}
	if !strings.HasSuffix(rendered, "TAIL") {
		t.Errorf("render %q should preserve the last bytes TAIL", rendered)
	}
}

func TestCappedOutput_WriteNeverErrors(t *testing.T) {
	w := newCappedOutput(4)
	for i := 0; i < 100; i++ {
		n, err := w.Write([]byte("abcdefghij"))
		if err != nil {
			t.Fatalf("Write should never return err, got %v", err)
		}
		if n != 10 {
			t.Fatalf("Write should report all bytes accepted; n=%d", n)
		}
	}
	if w.RawBytes() != 1000 {
		t.Errorf("RawBytes = %d, want 1000", w.RawBytes())
	}
}

func TestCappedOutput_BinarySafe(t *testing.T) {
	w := newCappedOutput(8)
	payload := []byte{0xff, 0x00, 0xfe, 0x01, 0xfd, 0x02, 0xfc, 0x03}
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !bytes.Equal([]byte(w.Render()), payload) {
		t.Errorf("binary round trip failed; got %x want %x", []byte(w.Render()), payload)
	}
}

func TestCappedOutput_MinimumLimit(t *testing.T) {
	// limit <= 16 is clamped up to 16 so head+tail can always hold a
	// meaningful sample. This prevents degenerate zero-size renders.
	w := newCappedOutput(4)
	w.Write([]byte(strings.Repeat("x", 100)))
	if w.Kept() != 16 {
		t.Errorf("Kept = %d, want 16 (clamped min)", w.Kept())
	}
}

func successMeta(stdout, stderr *cappedOutput) bashResultMeta {
	ec := 0
	return bashResultMeta{
		Status:           "success",
		ExitCode:         &ec,
		CWD:              ".",
		StdoutRawBytes:   stdout.RawBytes(),
		StdoutShownBytes: int64(stdout.Kept()),
		StdoutTruncated:  stdout.Truncated(),
		StderrRawBytes:   stderr.RawBytes(),
		StderrShownBytes: int64(stderr.Kept()),
		StderrTruncated:  stderr.Truncated(),
		Classification:   "success",
	}
}

func TestFormatBashResult_EmptyStreams(t *testing.T) {
	stdout := newCappedOutput(1024)
	stderr := newCappedOutput(1024)
	body := formatBashResult(successMeta(stdout, stderr), stdout, stderr)

	if !strings.HasPrefix(body, "BASH RESULT\n") {
		t.Errorf("body must start with metadata header; body=%q", body)
	}
	for _, want := range []string{
		"status: success",
		"stdout_bytes_raw: 0",
		"stdout_truncated: false",
		"stderr_bytes_raw: 0",
		"stderr_truncated: false",
		"classification: success",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing metadata line %q; body=%q", want, body)
		}
	}
	if strings.Contains(body, "STDOUT:") {
		t.Errorf("empty stdout must not emit a STDOUT section; body=%q", body)
	}
	if strings.Contains(body, "STDERR:") {
		t.Errorf("empty stderr must not emit a STDERR section; body=%q", body)
	}
}

func TestFormatBashResult_StdoutOnly(t *testing.T) {
	stdout := newCappedOutput(1024)
	stdout.Write([]byte("hello"))
	stderr := newCappedOutput(1024)
	body := formatBashResult(successMeta(stdout, stderr), stdout, stderr)

	if !strings.Contains(body, "stdout_bytes_raw: 5") {
		t.Errorf("stdout raw count missing; body=%q", body)
	}
	if !strings.Contains(body, "stdout_bytes_shown: 5") {
		t.Errorf("stdout shown count missing; body=%q", body)
	}
	if !strings.Contains(body, "STDOUT:\nhello") {
		t.Errorf("stdout section missing; body=%q", body)
	}
	if strings.Contains(body, "STDERR:") {
		t.Errorf("stderr absent but STDERR section printed; body=%q", body)
	}
}

func TestFormatBashResult_TruncationAnnotated(t *testing.T) {
	stdout := newCappedOutput(20) // head=10, tail=10
	stdout.Write([]byte(strings.Repeat("x", 1000)))
	stderr := newCappedOutput(1024)
	body := formatBashResult(successMeta(stdout, stderr), stdout, stderr)

	for _, want := range []string{
		"stdout_bytes_raw: 1000",
		"stdout_bytes_shown: 20",
		"stdout_truncated: true",
		"output truncated",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q; body=%q", want, body)
		}
	}
}

func TestFormatBashResult_BothStreamsTruncatedIndependently(t *testing.T) {
	stdout := newCappedOutput(20)
	stdout.Write([]byte(strings.Repeat("a", 500)))
	stderr := newCappedOutput(20)
	stderr.Write([]byte(strings.Repeat("b", 700)))

	body := formatBashResult(successMeta(stdout, stderr), stdout, stderr)
	for _, want := range []string{
		"stdout_bytes_raw: 500",
		"stdout_bytes_shown: 20",
		"stdout_truncated: true",
		"stderr_bytes_raw: 700",
		"stderr_bytes_shown: 20",
		"stderr_truncated: true",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q; body=%q", want, body)
		}
	}
}

// TestCappedOutput_TinyWritesDoNotGrowTailCapacity asserts the ring
// buffer stays at its initial capacity even when the writer is fed
// many tiny chunks after the tail has filled. Pre-fix the tail used
// reslice+append, which forced repeated realloc once the underlying
// array's free space ran out — the regression this commit closes.
func TestCappedOutput_TinyWritesDoNotGrowTailCapacity(t *testing.T) {
	w := newCappedOutput(64)
	startCap := cap(w.tail)
	if startCap == 0 {
		t.Fatalf("tail must be pre-allocated, got cap 0")
	}

	for i := 0; i < 10000; i++ {
		n, err := w.Write([]byte{byte(i)})
		if err != nil {
			t.Fatalf("Write[%d]: %v", i, err)
		}
		if n != 1 {
			t.Fatalf("Write[%d] returned n=%d, want 1", i, n)
		}
	}

	if cap(w.tail) != startCap {
		t.Errorf("tail cap grew from %d to %d under tiny writes", startCap, cap(w.tail))
	}
	if w.Kept() > 64 {
		t.Errorf("Kept = %d, must not exceed limit 64", w.Kept())
	}
	if !w.Truncated() {
		t.Errorf("expected truncated after 10000 writes")
	}
}

// TestCappedOutput_TailOrderPreservedAcrossWrap forces the ring to
// wrap (tailStart > 0) and asserts the rendered tail still reflects
// arrival order. A naive ring would render tail[0:tailLen] which
// would scramble the bytes once tailStart != 0.
func TestCappedOutput_TailOrderPreservedAcrossWrap(t *testing.T) {
	w := newCappedOutput(20) // head=10, tail=10
	w.Write([]byte("HEADHEADHE"))
	// Fill tail exactly: tailStart=0, tailLen=10.
	w.Write([]byte("0123456789"))
	// Wrap: each subsequent byte advances tailStart, dropping the
	// oldest tail byte. After feeding "ABCDE" the logical tail
	// content must be "56789ABCDE" (in that order).
	w.Write([]byte("ABCDE"))

	rendered := w.Render()
	if !strings.HasPrefix(rendered, "HEADHEADHE") {
		t.Errorf("render %q lost head", rendered)
	}
	if !strings.HasSuffix(rendered, "56789ABCDE") {
		t.Errorf("render %q does not end with logical tail '56789ABCDE'", rendered)
	}
	if !strings.Contains(rendered, "output truncated") {
		t.Errorf("render %q missing truncation marker", rendered)
	}
}

// TestCappedOutput_RingPacksOversizedSingleWrite confirms that a
// single Write larger than the tail capacity collapses to the last
// tailCap bytes with tailStart=0, so subsequent renders are
// contiguous and predictable.
func TestCappedOutput_RingPacksOversizedSingleWrite(t *testing.T) {
	w := newCappedOutput(20) // head=10, tail=10
	w.Write([]byte("HEADHEADHE"))
	// Single chunk much larger than tailCap; the ring should keep
	// only the last 10 bytes ("STUVWXYZAB") packed at tailStart=0.
	w.Write([]byte("0123456789KLMNOPQRSTUVWXYZAB"))

	if w.tailStart != 0 {
		t.Errorf("tailStart = %d, want 0 after oversized single write", w.tailStart)
	}
	if w.tailLen != 10 {
		t.Errorf("tailLen = %d, want 10", w.tailLen)
	}
	if !strings.HasSuffix(w.Render(), "STUVWXYZAB") {
		t.Errorf("render does not end with last 10 bytes: %q", w.Render())
	}
}

// TestCappedOutput_RawBytesReturnsInt64 pins the public type of
// RawBytes(): callers (including future B1 metadata) must be able to
// rely on int64 rather than the platform-dependent int.
func TestCappedOutput_RawBytesReturnsInt64(t *testing.T) {
	w := newCappedOutput(16)
	w.Write([]byte("hello"))
	var got int64 = w.RawBytes()
	if got != 5 {
		t.Errorf("RawBytes = %d, want 5", got)
	}
}

// v42-1b: appendAuditSummaryLine surfaces the count of permitted
// connections in the LLM-facing BASH RESULT body without dumping the
// host list. The summary section is omitted entirely when there are
// no records and no drops so quiet Runs stay terse.

func TestAppendAuditSummaryLine_OmitsSectionWhenEmpty(t *testing.T) {
	const original = "BASH RESULT\nstatus: success\n"
	out := appendAuditSummaryLine(original, nil, 0)
	if out != original {
		t.Errorf("output mutated when records and dropCount both zero; got %q want %q", out, original)
	}
	if strings.Contains(out, "PERMITTED") {
		t.Errorf("PERMITTED substring must not appear when section is omitted; got %q", out)
	}
}

func TestAppendAuditSummaryLine_RendersCountWithoutHostList(t *testing.T) {
	records := []SandboxAuditRecord{
		{
			Host:     "api.example.com",
			Port:     443,
			Protocol: string(ProtocolHTTPSConnect),
			Source:   string(SourceNetworkProxy),
			Decision: "allow",
		},
	}
	out := appendAuditSummaryLine("BASH RESULT\n", records, 0)
	want := "PERMITTED CONNECTIONS: 1 allowed (0 dropped)"
	if !strings.Contains(out, want) {
		t.Errorf("output missing summary line %q; got %q", want, out)
	}
	if strings.Contains(out, "api.example.com") {
		t.Errorf("host list MUST NOT appear in BASH RESULT body; got %q", out)
	}
}
