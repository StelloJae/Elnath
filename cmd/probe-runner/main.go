// Command probe-runner drives the Phase 8.1 baseline replay harness.
//
// It reads a probe corpus markdown (`.omc/research/probe-corpus-*.md`),
// runs each probe through two CLIs in series — `claude -p` for the CC
// baseline and `elnath run --non-interactive` for the Elnath side — and
// writes a comparison report with the 3 provider-agnostic HARD metrics
// + `mean_output_tokens_per_task` SOFT column per Phase 8 consensus
// amendment 2026-04-23.
//
// Scope fence: baseline measurement only. Not a production shim for
// Claude CLI (see amendment §6 row 2).
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// probe is one row from the corpus table + its verbatim prompt.
type probe struct {
	ID       string
	Category string
	Language string
	Prompt   string
}

// metrics captured per side per probe. Provider-agnostic fields carry
// direct comparisons; Anthropic-specific cache numbers are populated
// only when the CC side returns them (elnath side on Codex leaves
// them zero).
type metrics struct {
	Turns            int
	ToolUses         int
	ToolErrors       int
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	Duration         time.Duration
	RawResultLine    string
	Error            string
	// ReplyText holds the assistant-visible final text — populated only
	// when --head2head capture is requested. CC fills from the stream
	// json `result.result` field; Elnath fills by stripping log /
	// summary noise from stdout.
	ReplyText string
}

type result struct {
	Probe  probe
	CC     metrics
	Elnath metrics
	RunAt  time.Time
}

func main() {
	corpus := flag.String("corpus", ".omc/research/probe-corpus-2026-04-22.md", "probe corpus markdown path")
	output := flag.String("output", ".omc/research/claude-code-baseline-"+time.Now().Format("2006-01-02")+".md", "baseline report output path")
	only := flag.String("probe", "", "run only listed probe IDs (comma-separated, e.g. P02,P03,P09)")
	sides := flag.String("sides", "cc,elnath", "comma-separated sides: cc, elnath, or both")
	delaySec := flag.Int("delay", 10, "seconds to wait between probes (rate-limit protection)")
	elnathBinary := flag.String("elnath", "./elnath", "path to elnath binary")
	head2head := flag.String("head2head", "", "if set, also write side-by-side transcript markdown to this path")
	flag.Parse()

	probes, err := parseCorpus(*corpus)
	if err != nil {
		fatalf("probe-runner: parse corpus %s: %v", *corpus, err)
	}
	if *only != "" {
		probes = filterByIDs(probes, *only)
		if len(probes) == 0 {
			fatalf("probe-runner: no probe matched IDs %q", *only)
		}
	}
	captureReply := *head2head != ""

	runCC := strings.Contains(*sides, "cc")
	runElnath := strings.Contains(*sides, "elnath")
	if !runCC && !runElnath {
		fatalf("probe-runner: --sides must include cc and/or elnath")
	}

	results := make([]result, 0, len(probes))
	for i, p := range probes {
		fmt.Fprintf(os.Stderr, "probe-runner: [%d/%d] running %s (%s/%s)\n", i+1, len(probes), p.ID, p.Category, p.Language)
		r := result{Probe: p, RunAt: time.Now()}
		if runCC {
			r.CC = runCCProbe(p, captureReply)
		}
		if runElnath {
			r.Elnath = runElnathProbe(p, *elnathBinary, captureReply)
		}
		results = append(results, r)
		if i < len(probes)-1 && *delaySec > 0 {
			time.Sleep(time.Duration(*delaySec) * time.Second)
		}
	}

	if err := writeReport(*output, results); err != nil {
		fatalf("probe-runner: write report: %v", err)
	}
	fmt.Fprintf(os.Stderr, "probe-runner: done — %d results written to %s\n", len(results), *output)
	if captureReply {
		if err := writeHead2Head(*head2head, results); err != nil {
			fatalf("probe-runner: write head2head: %v", err)
		}
		fmt.Fprintf(os.Stderr, "probe-runner: head2head transcripts at %s\n", *head2head)
	}
}

var tableRowRE = regexp.MustCompile(`^\|\s*(P\d+)\s*\|\s*([a-z/-]+)\s*\|\s*([a-z/.0-9+-]+)\s*\|`)
var headerRE = regexp.MustCompile(`^###\s+(P\d+)\s+—\s+`)

// parseCorpus reads the probe corpus markdown and returns one probe per
// row of the ID table that also has a matching `### P?? — …` prompt
// section.
func parseCorpus(path string) ([]probe, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(raw), "\n")

	meta := map[string]probe{}
	for _, l := range lines {
		if m := tableRowRE.FindStringSubmatch(l); len(m) == 4 {
			meta[m[1]] = probe{ID: m[1], Category: m[2], Language: m[3]}
		}
	}

	var currentID string
	var buf strings.Builder
	flush := func() {
		if currentID == "" {
			return
		}
		if m, ok := meta[currentID]; ok {
			m.Prompt = strings.TrimSpace(buf.String())
			meta[currentID] = m
		}
		currentID = ""
		buf.Reset()
	}
	for _, l := range lines {
		if m := headerRE.FindStringSubmatch(l); len(m) == 2 {
			flush()
			currentID = m[1]
			continue
		}
		if currentID == "" {
			continue
		}
		switch {
		case strings.HasPrefix(l, "> "):
			buf.WriteString(l[2:])
			buf.WriteByte('\n')
		case l == ">":
			buf.WriteByte('\n')
		}
	}
	flush()

	var out []probe
	for _, p := range meta {
		if p.Prompt == "" {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) == 0 {
		return nil, errors.New("no probes with both table entry and prompt body found")
	}
	return out, nil
}

// filterByIDs accepts a comma-separated list of probe IDs and returns
// the subset, preserving corpus order. Empty list => no filter.
func filterByIDs(probes []probe, csv string) []probe {
	wanted := map[string]bool{}
	for _, id := range strings.Split(csv, ",") {
		if id = strings.TrimSpace(id); id != "" {
			wanted[id] = true
		}
	}
	var out []probe
	for _, p := range probes {
		if wanted[p.ID] {
			out = append(out, p)
		}
	}
	return out
}

// runCCProbe executes `claude -p --output-format stream-json --verbose
// --no-session-persistence "$prompt"` and extracts the final
// `{"type":"result",...}` line which carries num_turns, duration, usage,
// and permission_denials in one structured payload. When captureReply
// is true, also fills metrics.ReplyText from the result event's
// `result` field for head-to-head rendering.
func runCCProbe(p probe, captureReply bool) metrics {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", "-p", "--output-format", "stream-json",
		"--verbose", "--no-session-persistence", p.Prompt)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	m := metrics{Duration: time.Since(start)}
	if err != nil {
		m.Error = fmt.Sprintf("cc run failed: %v; stderr=%s", err, stderr.String())
		return m
	}
	parseCCStreamJSON(&m, stdout, captureReply)
	return m
}

func parseCCStreamJSON(m *metrics, body []byte, captureReply bool) {
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		var ev struct {
			Type    string          `json:"type"`
			Subtype string          `json:"subtype"`
			Message json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "assistant":
			m.Turns++
			if len(ev.Message) > 0 {
				var msg struct {
					Content []struct {
						Type string `json:"type"`
					} `json:"content"`
				}
				if err := json.Unmarshal(ev.Message, &msg); err == nil {
					for _, c := range msg.Content {
						if c.Type == "tool_use" {
							m.ToolUses++
						}
					}
				}
			}
		case "user":
			var msg struct {
				Message struct {
					Content []struct {
						Type    string `json:"type"`
						IsError bool   `json:"is_error"`
					} `json:"content"`
				} `json:"message"`
			}
			if err := json.Unmarshal(line, &msg); err == nil {
				for _, c := range msg.Message.Content {
					if c.Type == "tool_result" && c.IsError {
						m.ToolErrors++
					}
				}
			}
		case "result":
			var r struct {
				NumTurns       int    `json:"num_turns"`
				Duration       int    `json:"duration_ms"`
				Result         string `json:"result"`
				IsError        bool   `json:"is_error"`
				TerminalReason string `json:"terminal_reason"`
				Usage          struct {
					InputTokens              int `json:"input_tokens"`
					OutputTokens             int `json:"output_tokens"`
					CacheReadInputTokens     int `json:"cache_read_input_tokens"`
					CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal(line, &r); err == nil {
				if r.NumTurns > 0 {
					m.Turns = r.NumTurns
				}
				m.InputTokens = r.Usage.InputTokens
				m.OutputTokens = r.Usage.OutputTokens
				m.CacheReadTokens = r.Usage.CacheReadInputTokens
				m.CacheWriteTokens = r.Usage.CacheCreationInputTokens
				if r.Duration > 0 {
					m.Duration = time.Duration(r.Duration) * time.Millisecond
				}
				m.RawResultLine = string(line)
				if captureReply {
					m.ReplyText = r.Result
				}
				if r.IsError {
					m.Error = fmt.Sprintf("cc result is_error=true terminal=%s result=%s", r.TerminalReason, truncate(r.Result, 200))
				}
			}
		}
	}
}

// runElnathProbe pipes the prompt into `elnath run --non-interactive`
// and parses the structured log stream + token-summary line. Elnath's
// non-interactive mode exits on stdin EOF, so a single prompt completes
// the session cleanly. When captureReply is true, also extracts the
// assistant reply text by stripping log noise.
func runElnathProbe(p probe, binary string, captureReply bool) metrics {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "run", "--non-interactive")
	cmd.Stdin = strings.NewReader(p.Prompt + "\n")
	// ELNATH_LOG_LEVEL=error keeps the assistant reply readable for
	// head-to-head capture without losing the metric-bearing
	// `[tokens: ...]` summary line (which prints unconditionally).
	if captureReply {
		cmd.Env = append(os.Environ(), "ELNATH_LOG_LEVEL=error")
	}
	var combined strings.Builder
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	err := cmd.Run()
	m := metrics{Duration: time.Since(start)}
	if err != nil {
		m.Error = fmt.Sprintf("elnath run failed: %v", err)
	}
	parseElnathOutput(&m, combined.String())
	if captureReply {
		m.ReplyText = extractElnathReply(combined.String())
	}
	return m
}

// extractElnathReply strips Elnath's log/banner/marker noise from
// stdout and returns just the assistant's reply text.
func extractElnathReply(body string) string {
	var keep []string
	for _, l := range strings.Split(body, "\n") {
		t := strings.TrimSpace(l)
		switch {
		case t == "":
			keep = append(keep, "")
		case strings.HasPrefix(t, "time="):
		case strings.HasPrefix(t, "elnath "):
		case strings.HasPrefix(t, "Type your message"):
		case strings.HasPrefix(t, "> "):
		case t == ">":
		case strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") && (strings.Contains(t, "→") || strings.HasPrefix(t, "[tokens:")):
		case strings.Contains(l, "wiki git: committed"):
		case strings.Contains(l, "session auto-documented"):
		default:
			keep = append(keep, l)
		}
	}
	out := strings.TrimSpace(strings.Join(keep, "\n"))
	return out
}

var (
	tokenLineRE          = regexp.MustCompile(`\[tokens:\s+(\d[\d,]*)\s+in\s*/\s*(\d[\d,]*)\s+out`)
	toolsSegmentRE       = regexp.MustCompile(`\|\s*tools:\s*(\d+)(?:\s*\((\d+)\s*err\))?`)
	errorLineRE          = regexp.MustCompile(`(?i)^error:|tool.*error`)
	cacheAnthropicLineRE = regexp.MustCompile(`cache_read_input_tokens["']?:\s*(\d+)`)
)

func parseElnathOutput(m *metrics, body string) {
	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		l := scanner.Text()
		if mt := tokenLineRE.FindStringSubmatch(l); len(mt) == 3 {
			m.Turns++
			m.InputTokens += atoiComma(mt[1])
			m.OutputTokens += atoiComma(mt[2])
			// Tool count rides on the same summary line when the agent
			// actually invoked tools — `| tools: N` or `| tools: N (M err)`.
			if mts := toolsSegmentRE.FindStringSubmatch(l); len(mts) >= 2 {
				m.ToolUses += atoiComma(mts[1])
				if len(mts) == 3 && mts[2] != "" {
					m.ToolErrors += atoiComma(mts[2])
				}
			}
		}
		if errorLineRE.MatchString(l) {
			m.ToolErrors++
		}
		if mc := cacheAnthropicLineRE.FindStringSubmatch(l); len(mc) == 2 {
			m.CacheReadTokens += atoiComma(mc[1])
		}
	}
}

func atoiComma(s string) int {
	s = strings.ReplaceAll(s, ",", "")
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func writeReport(path string, results []result) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()

	now := time.Now().UTC().Format(time.RFC3339)
	fmt.Fprintf(w, "# Claude Code baseline — %s\n\n", now)
	fmt.Fprintf(w, "Probes: %d. See `.omc/plans/phase-8-consensus-amendment-2026-04-23.md` for metric definitions.\n\n", len(results))

	fmt.Fprintln(w, "## Per-probe metrics (CC ↔ Elnath)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "| Probe | Side | Turns | ToolUses | ToolErr | InTok | OutTok | MeanOut | CacheRead | Dur | Error |")
	fmt.Fprintln(w, "|-------|------|-------|----------|---------|-------|--------|---------|-----------|-----|-------|")
	for _, r := range results {
		writeRow(w, r.Probe.ID+" "+r.Probe.Category, "cc", r.CC)
		writeRow(w, "", "elnath", r.Elnath)
	}
	fmt.Fprintln(w, "")

	ccTurns, elTurns := collectTurns(results)
	fmt.Fprintln(w, "## HARD metric summary")
	fmt.Fprintln(w, "")
	fmt.Fprintf(w, "- **Turns-to-complete**: CC median=%d, Elnath median=%d, Δ=%s (target: ≤ +10%%)\n",
		medianInt(ccTurns), medianInt(elTurns), fmtPctDelta(medianInt(ccTurns), medianInt(elTurns)))
	fmt.Fprintf(w, "- **Tool-error-rate**: CC total=%d, Elnath total=%d (target: Elnath ≤ CC + 1pp)\n",
		sumToolErr(results, true), sumToolErr(results, false))
	fmt.Fprintln(w, "- **Re-ask rate**: partner-scored; fill in after manual review of each probe's transcript.")
	fmt.Fprintln(w, "")

	fmt.Fprintln(w, "## SOFT observability")
	fmt.Fprintln(w, "")
	ccOut, elOut := collectMeanOut(results)
	fmt.Fprintf(w, "- **Mean output tokens / task**: CC=%d, Elnath=%d (target: within ±20%%)\n", ccOut, elOut)
	fmt.Fprintf(w, "- **CC cache-hit rate** (Anthropic only): %s\n", cacheHitLine(results))
	fmt.Fprintln(w, "")

	fmt.Fprintln(w, "## Open calibration items")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "- Partner to self-score re-ask rate per probe and append to the summary.")
	fmt.Fprintln(w, "- Cross-provider re-ask threshold (amendment §7 Risk D) — compare observed Codex vs Anthropic delta before finalizing ≤15% gate.")
	fmt.Fprintln(w, "- If CC total cost > $40 across 20 probes, flag for partner review (current per-probe cache-creation cost ≈ $0.30–1.00).")
	return nil
}

func writeRow(w *bufio.Writer, label, side string, m metrics) {
	meanOut := 0
	if m.Turns > 0 {
		meanOut = m.OutputTokens / m.Turns
	}
	fmt.Fprintf(w, "| %s | %s | %d | %d | %d | %d | %d | %d | %d | %s | %s |\n",
		label, side, m.Turns, m.ToolUses, m.ToolErrors,
		m.InputTokens, m.OutputTokens, meanOut, m.CacheReadTokens,
		m.Duration.Truncate(time.Second), truncate(m.Error, 60))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func fmtPctDelta(base, other int) string {
	if base == 0 {
		return "n/a (base=0)"
	}
	return fmt.Sprintf("%+.1f%%", float64(other-base)/float64(base)*100)
}

func medianInt(v []int) int {
	if len(v) == 0 {
		return 0
	}
	s := append([]int(nil), v...)
	sort.Ints(s)
	return s[len(s)/2]
}

func collectTurns(results []result) (cc, el []int) {
	for _, r := range results {
		if r.CC.Turns > 0 {
			cc = append(cc, r.CC.Turns)
		}
		if r.Elnath.Turns > 0 {
			el = append(el, r.Elnath.Turns)
		}
	}
	return
}

func sumToolErr(results []result, ccSide bool) int {
	n := 0
	for _, r := range results {
		if ccSide {
			n += r.CC.ToolErrors
		} else {
			n += r.Elnath.ToolErrors
		}
	}
	return n
}

func collectMeanOut(results []result) (cc, el int) {
	ccOut, ccT, elOut, elT := 0, 0, 0, 0
	for _, r := range results {
		ccOut += r.CC.OutputTokens
		ccT += r.CC.Turns
		elOut += r.Elnath.OutputTokens
		elT += r.Elnath.Turns
	}
	if ccT > 0 {
		cc = ccOut / ccT
	}
	if elT > 0 {
		el = elOut / elT
	}
	return
}

// writeHead2Head emits a single markdown file with each probe rendered
// as its own section: the prompt, the CC reply, the Elnath reply, and
// a short metric line. Designed for partner read-through judgment of
// the qualitative gap between the two sides.
func writeHead2Head(path string, results []result) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()
	fmt.Fprintf(w, "# Head-to-head transcripts — %s\n\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(w, "Probes: %d. Read each section to judge whether Elnath's reply matches CC's quality.\n\n", len(results))
	for _, r := range results {
		fmt.Fprintf(w, "---\n\n## %s — %s / %s\n\n", r.Probe.ID, r.Probe.Category, r.Probe.Language)
		fmt.Fprintln(w, "### Prompt")
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, indent(r.Probe.Prompt, "> "))
		fmt.Fprintln(w, "")
		fmt.Fprintf(w, "### Claude Code (Anthropic Opus 4.7 [1m]) — %d turns / %d tools / %s\n\n",
			r.CC.Turns, r.CC.ToolUses, r.CC.Duration.Truncate(time.Second))
		fmt.Fprintln(w, replyBlock(r.CC.ReplyText, r.CC.Error))
		fmt.Fprintln(w, "")
		fmt.Fprintf(w, "### Elnath (Codex / current default) — %d turns / %d tools / %s\n\n",
			r.Elnath.Turns, r.Elnath.ToolUses, r.Elnath.Duration.Truncate(time.Second))
		fmt.Fprintln(w, replyBlock(r.Elnath.ReplyText, r.Elnath.Error))
		fmt.Fprintln(w, "")
	}
	fmt.Fprintln(w, "---\n## Partner judgment template")
	fmt.Fprintln(w, "Per probe, fill one of:")
	fmt.Fprintln(w, "- **OK** — Elnath quality acceptable")
	fmt.Fprintln(w, "- **VETO: <reason>** — what's missing or wrong")
	fmt.Fprintln(w, "")
	for _, r := range results {
		fmt.Fprintf(w, "- %s: \n", r.Probe.ID)
	}
	return nil
}

func indent(text, prefix string) string {
	var sb strings.Builder
	for i, l := range strings.Split(text, "\n") {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(prefix)
		sb.WriteString(l)
	}
	return sb.String()
}

func replyBlock(text, errStr string) string {
	if errStr != "" {
		return "**(run failed: " + errStr + ")**"
	}
	t := strings.TrimSpace(text)
	if t == "" {
		return "_(empty reply — model produced no text content)_"
	}
	return t
}

func cacheHitLine(results []result) string {
	read, created := 0, 0
	for _, r := range results {
		read += r.CC.CacheReadTokens
		created += r.CC.CacheWriteTokens
	}
	total := read + created
	if total == 0 {
		return "n/a (no cache activity recorded)"
	}
	return fmt.Sprintf("%.1f%% (%d read / %d written)", float64(read)*100/float64(total), read, created)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
