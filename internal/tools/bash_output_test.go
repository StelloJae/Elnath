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

func TestFormatBashOutput_EmptyStreams(t *testing.T) {
	stdout := newCappedOutput(1024)
	stderr := newCappedOutput(1024)
	body := formatBashOutput(stdout, stderr)

	if !strings.Contains(body, "[stdout: 0 bytes]") {
		t.Errorf("missing zero-byte stdout header; body=%q", body)
	}
	if !strings.Contains(body, "[stderr: 0 bytes]") {
		t.Errorf("missing zero-byte stderr header; body=%q", body)
	}
	if strings.Contains(body, "STDOUT:") {
		t.Errorf("empty stdout must not emit a STDOUT section; body=%q", body)
	}
	if strings.Contains(body, "STDERR:") {
		t.Errorf("empty stderr must not emit a STDERR section; body=%q", body)
	}
}

func TestFormatBashOutput_StdoutOnly(t *testing.T) {
	stdout := newCappedOutput(1024)
	stdout.Write([]byte("hello"))
	stderr := newCappedOutput(1024)
	body := formatBashOutput(stdout, stderr)

	if !strings.Contains(body, "[stdout: 5 bytes]") {
		t.Errorf("stdout header missing; body=%q", body)
	}
	if !strings.Contains(body, "STDOUT:\nhello") {
		t.Errorf("stdout section missing; body=%q", body)
	}
	if strings.Contains(body, "STDERR:") {
		t.Errorf("stderr absent but STDERR section printed; body=%q", body)
	}
}

func TestFormatBashOutput_TruncationAnnotated(t *testing.T) {
	stdout := newCappedOutput(20) // head=10, tail=10
	stdout.Write([]byte(strings.Repeat("x", 1000)))
	stderr := newCappedOutput(1024)
	body := formatBashOutput(stdout, stderr)

	if !strings.Contains(body, "[stdout: 1000 bytes, truncated (20 shown)]") {
		t.Errorf("truncation header missing/wrong; body=%q", body)
	}
	if !strings.Contains(body, "output truncated") {
		t.Errorf("stream-level truncation marker missing; body=%q", body)
	}
}

func TestFormatBashOutput_BothStreamsTruncatedIndependently(t *testing.T) {
	stdout := newCappedOutput(20)
	stdout.Write([]byte(strings.Repeat("a", 500)))
	stderr := newCappedOutput(20)
	stderr.Write([]byte(strings.Repeat("b", 700)))

	body := formatBashOutput(stdout, stderr)
	if !strings.Contains(body, "[stdout: 500 bytes, truncated (20 shown)]") {
		t.Errorf("stdout header wrong; body=%q", body)
	}
	if !strings.Contains(body, "[stderr: 700 bytes, truncated (20 shown)]") {
		t.Errorf("stderr header wrong; body=%q", body)
	}
}
