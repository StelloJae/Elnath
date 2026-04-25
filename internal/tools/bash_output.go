package tools

import (
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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

	if len(p) >= w.tailCap {
		copy(w.tail, p[len(p)-w.tailCap:])
		w.tailStart = 0
		w.tailLen = w.tailCap
		return n, nil
	}

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

// bashResultMeta is the structured summary B1 emits alongside captured
// stdout/stderr. Fields are LLM-facing and intentionally avoid host
// details: cwd is session-relative, classification is a coarse hint
// (NOT a security policy), and bytes are reported in raw / shown /
// truncated form so the agent can reason about whether it saw the
// full stream.
type bashResultMeta struct {
	Status           string
	ExitCode         *int
	Duration         time.Duration
	CWD              string
	TimedOut         bool
	Canceled         bool
	StdoutRawBytes   int64
	StdoutShownBytes int64
	StdoutTruncated  bool
	StderrRawBytes   int64
	StderrShownBytes int64
	StderrTruncated  bool
	Classification   string
}

// classifyExitCode maps a process exit code to one of the coarse
// labels exposed by bashResultMeta.Classification. The mapping is a
// rule-based hint for the agent; it is NOT consulted by the runtime
// for any policy decision.
func classifyExitCode(exitCode int) string {
	switch exitCode {
	case 127:
		return "command_not_found"
	case 126:
		return "permission_denied"
	default:
		return "unknown_nonzero"
	}
}

// displayCWD reports the working directory relative to the session
// root so LLM-facing output never leaks the absolute host path.
// When workDir equals the session root the result is ".". Anything
// that resolves outside the session (which should not happen given
// PathGuard validation) collapses to "." rather than leaking "..".
func displayCWD(sessionRoot, workDir string) string {
	if workDir == sessionRoot {
		return "."
	}
	rel, err := filepath.Rel(sessionRoot, workDir)
	if err != nil {
		return "."
	}
	if rel == "." {
		return "."
	}
	if rel == ".." || strings.HasPrefix(rel, "../") || strings.HasPrefix(rel, "..\\") {
		return "."
	}
	return rel
}

// formatBashResult emits the LLM-facing body for a bash invocation:
// a metadata header followed by STDOUT/STDERR sections. Sections are
// only emitted when the corresponding stream produced bytes, so empty
// streams stay quiet.
func formatBashResult(meta bashResultMeta, stdout, stderr *cappedOutput) string {
	var sb strings.Builder
	sb.WriteString("BASH RESULT\n")
	sb.WriteString("status: ")
	sb.WriteString(meta.Status)
	sb.WriteByte('\n')
	sb.WriteString("exit_code: ")
	if meta.ExitCode != nil {
		sb.WriteString(strconv.Itoa(*meta.ExitCode))
	} else {
		sb.WriteString("null")
	}
	sb.WriteByte('\n')
	sb.WriteString("duration_ms: ")
	sb.WriteString(strconv.FormatInt(meta.Duration.Milliseconds(), 10))
	sb.WriteByte('\n')
	sb.WriteString("cwd: ")
	sb.WriteString(meta.CWD)
	sb.WriteByte('\n')
	sb.WriteString("timed_out: ")
	sb.WriteString(strconv.FormatBool(meta.TimedOut))
	sb.WriteByte('\n')
	sb.WriteString("canceled: ")
	sb.WriteString(strconv.FormatBool(meta.Canceled))
	sb.WriteByte('\n')
	writeBytesLine(&sb, "stdout_bytes_raw", meta.StdoutRawBytes)
	writeBytesLine(&sb, "stdout_bytes_shown", meta.StdoutShownBytes)
	sb.WriteString("stdout_truncated: ")
	sb.WriteString(strconv.FormatBool(meta.StdoutTruncated))
	sb.WriteByte('\n')
	writeBytesLine(&sb, "stderr_bytes_raw", meta.StderrRawBytes)
	writeBytesLine(&sb, "stderr_bytes_shown", meta.StderrShownBytes)
	sb.WriteString("stderr_truncated: ")
	sb.WriteString(strconv.FormatBool(meta.StderrTruncated))
	sb.WriteByte('\n')
	sb.WriteString("classification: ")
	sb.WriteString(meta.Classification)
	sb.WriteByte('\n')

	stdoutText := stdout.Render()
	stderrText := stderr.Render()
	if stdout.RawBytes() > 0 {
		sb.WriteString("\nSTDOUT:\n")
		sb.WriteString(stdoutText)
		if !strings.HasSuffix(stdoutText, "\n") {
			sb.WriteByte('\n')
		}
	}
	if stderr.RawBytes() > 0 {
		sb.WriteString("\nSTDERR:\n")
		sb.WriteString(stderrText)
		if !strings.HasSuffix(stderrText, "\n") {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

func writeBytesLine(sb *strings.Builder, key string, n int64) {
	sb.WriteString(key)
	sb.WriteString(": ")
	sb.WriteString(strconv.FormatInt(n, 10))
	sb.WriteByte('\n')
}
