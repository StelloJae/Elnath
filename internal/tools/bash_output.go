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
type cappedOutput struct {
	headCap int
	tailCap int
	head    []byte
	tail    []byte
	raw     int
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
		tail:    make([]byte, 0, tailCap),
	}
}

// Write always returns len(p), nil. Bytes beyond the head quota are
// funneled through a tail ring; bytes beyond head+tail are counted
// but dropped so Render() can report the omission.
func (w *cappedOutput) Write(p []byte) (int, error) {
	n := len(p)
	w.raw += n

	if len(w.head) < w.headCap {
		room := w.headCap - len(w.head)
		if len(p) <= room {
			w.head = append(w.head, p...)
			return n, nil
		}
		w.head = append(w.head, p[:room]...)
		p = p[room:]
	}

	if len(p) >= w.tailCap {
		w.tail = append(w.tail[:0], p[len(p)-w.tailCap:]...)
		return n, nil
	}
	if len(w.tail)+len(p) > w.tailCap {
		overflow := len(w.tail) + len(p) - w.tailCap
		w.tail = w.tail[overflow:]
	}
	w.tail = append(w.tail, p...)
	return n, nil
}

// RawBytes returns the total number of bytes seen by the writer,
// including those discarded by truncation.
func (w *cappedOutput) RawBytes() int { return w.raw }

// Kept reports how many bytes are retained in head+tail combined.
func (w *cappedOutput) Kept() int { return len(w.head) + len(w.tail) }

// Dropped reports how many bytes were discarded between head and tail.
func (w *cappedOutput) Dropped() int {
	if w.raw <= w.Kept() {
		return 0
	}
	return w.raw - w.Kept()
}

// Truncated reports whether any bytes were dropped.
func (w *cappedOutput) Truncated() bool { return w.Dropped() > 0 }

// Render returns head + elision marker + tail. The marker is inserted
// only when bytes were dropped. Bytes are returned as-is (no UTF-8
// fixup) so binary output survives the round trip.
func (w *cappedOutput) Render() string {
	if !w.Truncated() {
		return string(w.head) + string(w.tail)
	}
	marker := fmt.Sprintf("\n... [output truncated: omitted %d bytes] ...\n", w.Dropped())
	return string(w.head) + marker + string(w.tail)
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
	sb.WriteString(strconv.Itoa(w.RawBytes()))
	sb.WriteString(" bytes")
	if w.Truncated() {
		sb.WriteString(", truncated (")
		sb.WriteString(strconv.Itoa(w.Kept()))
		sb.WriteString(" shown)")
	}
	sb.WriteString("]\n")
}
