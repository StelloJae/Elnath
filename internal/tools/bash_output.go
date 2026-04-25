package tools

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

// bashOutputCapPerStream sets the default per-stream capture limit
// for bash invocations. Capture is bounded at write-time so a runaway
// command cannot exhaust the host's memory before the timeout fires.
const bashOutputCapPerStream = 256 * 1024

// cappedOutput is an io.Writer that keeps the first headCap bytes
// and the last tailCap bytes of everything written, dropping the
// middle once the total exceeds the limit. Writes never fail: the
// policy is drop/truncate, never flow-control the command.
//
// The tail is held in a fixed-size ring buffer (tail, tailStart,
// tailLen) so tiny writes after the buffer fills do not trigger any
// allocation or capacity-grow path. Earlier revisions used
// `tail = tail[overflow:]` followed by `append`; once the underlying
// array's free capacity ran out, every additional 1-byte write forced
// a reallocation, giving an O(N * tailCap) worst case for tiny-write
// streams. The ring keeps Write at amortized O(len(p)).
type cappedOutput struct {
	headCap int
	tailCap int

	head []byte

	tail      []byte
	tailStart int
	tailLen   int

	raw int64
}

// newCappedOutput returns a writer that retains at most `limit` bytes
// split evenly between head and tail.
func newCappedOutput(limit int) *cappedOutput {
	if limit < 16 {
		limit = 16
	}
	headCap := limit / 2
	tailCap := limit - headCap
	return &cappedOutput{
		headCap: headCap,
		tailCap: tailCap,
		head:    make([]byte, 0, headCap),
		tail:    make([]byte, tailCap),
	}
}

// Write always returns len(p), nil. Bytes beyond the head quota are
// funneled into the tail ring; bytes beyond head+tail are counted in
// raw but dropped so Render() can report the omission.
func (w *cappedOutput) Write(p []byte) (int, error) {
	n := len(p)
	w.raw += int64(n)

	if len(w.head) < w.headCap {
		room := w.headCap - len(w.head)
		if len(p) <= room {
			w.head = append(w.head, p...)
			return n, nil
		}
		w.head = append(w.head, p[:room]...)
		p = p[room:]
	}

	if w.tailCap == 0 || len(p) == 0 {
		return n, nil
	}

	// Single chunk that already overshoots the tail capacity: keep
	// only the last tailCap bytes, repacked so tailStart=0 keeps the
	// rendering path contiguous.
	if len(p) >= w.tailCap {
		copy(w.tail, p[len(p)-w.tailCap:])
		w.tailStart = 0
		w.tailLen = w.tailCap
		return n, nil
	}

	// Smaller chunk: walk into the ring in at most two contiguous
	// copies (one before the wrap, one after) so each Write costs
	// O(len(p)) regardless of how full the buffer already is.
	for len(p) > 0 {
		writeIdx := (w.tailStart + w.tailLen) % w.tailCap
		chunk := w.tailCap - writeIdx
		if chunk > len(p) {
			chunk = len(p)
		}
		copy(w.tail[writeIdx:writeIdx+chunk], p[:chunk])
		p = p[chunk:]

		if w.tailLen+chunk <= w.tailCap {
			w.tailLen += chunk
			continue
		}
		overflow := w.tailLen + chunk - w.tailCap
		w.tailStart = (w.tailStart + overflow) % w.tailCap
		w.tailLen = w.tailCap
	}
	return n, nil
}

// RawBytes returns the total number of bytes seen by the writer,
// including those discarded by truncation. The counter is int64 so
// streams above ~2 GiB do not silently wrap on 32-bit GOARCH builds.
func (w *cappedOutput) RawBytes() int64 { return w.raw }

// Kept reports how many bytes are retained in head+tail combined.
func (w *cappedOutput) Kept() int { return len(w.head) + w.tailLen }

// Dropped reports how many bytes were discarded between head and tail.
func (w *cappedOutput) Dropped() int64 {
	kept := int64(w.Kept())
	if w.raw <= kept {
		return 0
	}
	return w.raw - kept
}

// Truncated reports whether any bytes were dropped.
func (w *cappedOutput) Truncated() bool { return w.Dropped() > 0 }

// tailBytes materialises the ring's logical contents in arrival order.
// Allocation here is fine; the hot path is Write, which stays
// allocation-free once the buffer is sized.
func (w *cappedOutput) tailBytes() []byte {
	if w.tailLen == 0 {
		return nil
	}
	if w.tailStart == 0 {
		return w.tail[:w.tailLen]
	}
	out := make([]byte, w.tailLen)
	first := w.tailCap - w.tailStart
	if first > w.tailLen {
		first = w.tailLen
	}
	copy(out, w.tail[w.tailStart:w.tailStart+first])
	if w.tailLen > first {
		copy(out[first:], w.tail[:w.tailLen-first])
	}
	return out
}

// Render returns head + elision marker + tail. The marker is inserted
// only when bytes were dropped. Bytes are returned as-is (no UTF-8
// fixup) so binary output survives the round trip.
func (w *cappedOutput) Render() string {
	if !w.Truncated() {
		return string(w.head) + string(w.tailBytes())
	}
	marker := fmt.Sprintf("\n... [output truncated: omitted %d bytes] ...\n", w.Dropped())
	return string(w.head) + marker + string(w.tailBytes())
}

// Ensure interface conformance at compile time.
var _ io.Writer = (*cappedOutput)(nil)

// formatBashOutput builds the LLM-facing body for a bash invocation.
// It prepends a metadata header with the raw/kept byte counts for
// each stream and appends STDOUT/STDERR sections only when those
// streams produced output. B1 BashResult metadata will replace this
// shape with a structured payload; this is the minimum surface
// needed for P0-3.
func formatBashOutput(stdout, stderr *cappedOutput) string {
	var sb strings.Builder
	writeStreamHeader(&sb, "stdout", stdout)
	writeStreamHeader(&sb, "stderr", stderr)

	stdoutText := stdout.Render()
	stderrText := stderr.Render()
	if stdout.RawBytes() > 0 {
		sb.WriteString("\nSTDOUT:\n")
		sb.WriteString(stdoutText)
		if !strings.HasSuffix(stdoutText, "\n") {
			sb.WriteString("\n")
		}
	}
	if stderr.RawBytes() > 0 {
		sb.WriteString("\nSTDERR:\n")
		sb.WriteString(stderrText)
		if !strings.HasSuffix(stderrText, "\n") {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func writeStreamHeader(sb *strings.Builder, name string, w *cappedOutput) {
	sb.WriteString("[")
	sb.WriteString(name)
	sb.WriteString(": ")
	sb.WriteString(strconv.FormatInt(w.RawBytes(), 10))
	sb.WriteString(" bytes")
	if w.Truncated() {
		sb.WriteString(", truncated (")
		sb.WriteString(strconv.Itoa(w.Kept()))
		sb.WriteString(" shown)")
	}
	sb.WriteString("]\n")
}
