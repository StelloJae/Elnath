package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/fault"
	"github.com/stello/elnath/internal/fault/scenarios"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/tools"
)

type chaosRuntime struct {
	ctx    context.Context
	cfg    *config.Config
	out    io.Writer
	errOut io.Writer
	logger *slog.Logger
}

type chaosRunOptions struct {
	all          bool
	runs         int
	outDir       string
	forceEnable  bool
	scenarioName string
}

func cmdChaos(ctx context.Context, args []string) error {
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	rt := &chaosRuntime{
		ctx:    ctx,
		cfg:    cfg,
		out:    os.Stdout,
		errOut: os.Stderr,
		logger: slog.Default(),
	}
	return runChaos(rt, args)
}

func runChaos(rt *chaosRuntime, args []string) error {
	if len(args) == 0 {
		return printChaosHelp(rt.out)
	}
	switch args[0] {
	case "run":
		return runChaosRun(rt, args[1:])
	case "list":
		return runChaosList(rt, args[1:])
	case "report":
		return runChaosReport(rt, args[1:])
	case "help", "--help", "-h":
		return printChaosHelp(rt.out)
	default:
		return fmt.Errorf("unknown subcommand %q (try: run, list, report)", args[0])
	}
}

func printChaosHelp(w io.Writer) error {
	_, err := fmt.Fprintln(w, `Usage: elnath chaos <subcommand>

Subcommands:
  run <scenario-name> [--runs N] [--out DIR] [--config-enable]
  run --all [--runs N] [--out DIR] [--config-enable]
  list
  report <run-id|latest>`)
	return err
}

func runChaosList(rt *chaosRuntime, _ []string) error {
	reg := fault.NewRegistry(scenarios.All())
	for _, scenario := range reg.All() {
		if _, err := fmt.Fprintf(rt.out, "%s\t%s\t%s\tthreshold=%.2f/%d/%d\n",
			scenario.Name,
			scenario.Category,
			scenario.Description,
			scenario.Threshold.RecoveryRate,
			scenario.Threshold.MaxRuns,
			scenario.Threshold.MaxRecoveryAttempts,
		); err != nil {
			return err
		}
	}
	return nil
}

func runChaosReport(rt *chaosRuntime, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("report requires a run-id or latest")
	}
	baseDir := chaosBaseDir(rt.cfg, "")
	runDir, err := resolveRunDir(baseDir, args[0])
	if err != nil {
		return err
	}
	reportPath := filepath.Join(runDir, "report.md")
	data, err := os.ReadFile(reportPath)
	if err != nil {
		var out bytes.Buffer
		if renderErr := fault.NewMDReporter(filepath.Join(runDir, "runs.jsonl"), &out).Render(); renderErr != nil {
			return renderErr
		}
		_, err = io.Copy(rt.out, &out)
		return err
	}
	_, err = rt.out.Write(data)
	return err
}

func runChaosRun(rt *chaosRuntime, args []string) error {
	opts, err := parseChaosRunOptions(args)
	if err != nil {
		return err
	}
	if opts.forceEnable {
		rt.cfg.FaultInjection.Enabled = true
	}
	profile, err := fault.CheckGuards(fault.GuardConfig{Enabled: rt.cfg.FaultInjection.Enabled})
	if err != nil {
		return err
	}
	reg := fault.NewRegistry(scenarios.All())
	selected, err := selectScenarios(reg, profile, opts)
	if err != nil {
		return err
	}
	runID, err := newRunID()
	if err != nil {
		return err
	}
	if err := validateRunID(runID); err != nil {
		return err
	}
	runDir := filepath.Join(chaosBaseDir(rt.cfg, opts.outDir), runID)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return fmt.Errorf("create run dir: %w", err)
	}
	runsFile, err := os.OpenFile(filepath.Join(runDir, "runs.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open runs file: %w", err)
	}
	defer runsFile.Close()
	jsonl := fault.NewJSONLReporter(runsFile)
	for _, scenario := range selected {
		runs := opts.runs
		if runs <= 0 {
			runs = scenario.Threshold.MaxRuns
		}
		for i := 0; i < runs; i++ {
			rec := executeScenarioRun(rt.ctx, rt.logger, scenario, runID)
			if _, err := fmt.Fprintf(rt.out, "%s run %d/%d: %s\n", scenario.Name, i+1, runs, strings.ToUpper(rec.Outcome)); err != nil {
				return err
			}
			if err := jsonl.Record(rec); err != nil {
				fmt.Fprintf(rt.errOut, "WARN: fault reporter write failed: %v\n", err)
			}
		}
	}
	reportPath := filepath.Join(runDir, "report.md")
	reportFile, err := os.OpenFile(reportPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open report file: %w", err)
	}
	if err := fault.NewMDReporter(filepath.Join(runDir, "runs.jsonl"), reportFile).Render(); err != nil {
		fmt.Fprintf(rt.errOut, "WARN: render report failed: %v\n", err)
	}
	_ = reportFile.Close()
	_, err = fmt.Fprintf(rt.out, "report: %s\n", reportPath)
	return err
}

func parseChaosRunOptions(args []string) (chaosRunOptions, error) {
	var opts chaosRunOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--all":
			opts.all = true
		case "--runs":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--runs requires a value")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n <= 0 {
				return opts, fmt.Errorf("invalid --runs value %q", args[i+1])
			}
			opts.runs = n
			i++
		case "--out":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--out requires a value")
			}
			opts.outDir = args[i+1]
			i++
		case "--config-enable":
			opts.forceEnable = true
		default:
			if strings.HasPrefix(args[i], "--") {
				return opts, fmt.Errorf("unknown flag %q", args[i])
			}
			if opts.scenarioName != "" {
				return opts, fmt.Errorf("multiple scenario names provided")
			}
			opts.scenarioName = args[i]
		}
	}
	return opts, nil
}

func selectScenarios(reg *fault.ScenarioRegistry, profile string, opts chaosRunOptions) ([]*fault.Scenario, error) {
	if opts.all {
		return reg.All(), nil
	}
	name := opts.scenarioName
	if name == "" {
		name = profile
	}
	if name == "" {
		return nil, fmt.Errorf("run requires a scenario name or --all")
	}
	scenario, ok := reg.Get(name)
	if !ok {
		return nil, fmt.Errorf("scenario not found: %s", name)
	}
	return []*fault.Scenario{scenario}, nil
}

func executeScenarioRun(ctx context.Context, logger *slog.Logger, scenario *fault.Scenario, runID string) fault.RunRecord {
	start := time.Now()
	var (
		outcome  = "pass"
		detail   string
		attempts int
	)
	switch scenario.Category {
	case fault.CategoryIPC:
		outcome, detail, attempts = runIPCScenario(ctx, logger, scenario)
	default:
		outcome, detail, attempts = runAgentScenario(ctx, scenario)
	}
	return fault.RunRecord{
		Timestamp:        time.Now().UTC(),
		Scenario:         scenario.Name,
		FaultType:        scenario.FaultType,
		RunID:            runID,
		Outcome:          outcome,
		DurationMS:       time.Since(start).Milliseconds(),
		RecoveryAttempts: attempts,
		ErrorDetail:      detail,
	}
}

func runAgentScenario(ctx context.Context, scenario *fault.Scenario) (string, string, int) {
	for attempt := 0; attempt < 100; attempt++ {
		outcome, detail, recoveryAttempts, faultInjected := runAgentScenarioOnce(ctx, scenario)
		if faultInjected {
			return outcome, detail, recoveryAttempts
		}
	}
	return "error", "no fault injected after 100 attempts", 0
}

func runAgentScenarioOnce(ctx context.Context, scenario *fault.Scenario) (string, string, int, bool) {
	workDir, cleanup, toolInput, providerName := prepareAgentScenarioResources(scenario)
	defer cleanup()
	reg := buildToolRegistry(tools.NewPathGuard(workDir, nil))
	if scenario.Category == fault.CategoryTool {
		reg = newChaosToolRegistry(scenario)
	}
	inj := fault.NewScenarioInjector(scenario, time.Now().UnixNano())
	inj.ResetForRun()
	provider := &chaosProvider{scenario: scenario, providerName: providerName, toolInput: toolInput}
	wrappedProvider := llm.Provider(provider)
	if scenario.Category == fault.CategoryLLM {
		wrappedProvider = fault.NewLLMFaultHook(provider, inj, scenario)
	}
	exec := tools.Executor(reg)
	if scenario.Category == fault.CategoryTool {
		exec = fault.NewToolFaultHook(reg, inj, scenario)
	}
	runCtx, cancel := context.WithTimeout(ctx, scenarioTimeout(scenario))
	defer cancel()
	a := agent.New(wrappedProvider, reg,
		agent.WithPermission(agent.NewPermission(agent.WithMode(agent.ModeBypass))),
		agent.WithToolExecutor(exec),
		agent.WithMaxIterations(scenario.Threshold.MaxRecoveryAttempts+3),
	)
	result, err := a.Run(runCtx, []llm.Message{llm.NewUserMessage("run chaos scenario")}, nil)
	if err != nil {
		return "error", err.Error(), scenario.Threshold.MaxRecoveryAttempts, inj.FaultCount() > 0
	}
	attempts := 0
	if result.Iterations > 0 {
		attempts = result.Iterations - 1
	}
	last := ""
	if len(result.Messages) > 0 {
		last = result.Messages[len(result.Messages)-1].Text()
	}
	if strings.Contains(last, "scenario complete") && attempts <= scenario.Threshold.MaxRecoveryAttempts {
		return "pass", "", attempts, inj.FaultCount() > 0
	}
	if attempts > scenario.Threshold.MaxRecoveryAttempts {
		return "fail", fmt.Sprintf("recovery attempts %d exceeded max %d", attempts, scenario.Threshold.MaxRecoveryAttempts), attempts, inj.FaultCount() > 0
	}
	if last == "" {
		last = "scenario did not complete successfully"
	}
	return "fail", last, attempts, inj.FaultCount() > 0
}

func runIPCScenario(ctx context.Context, logger *slog.Logger, scenario *fault.Scenario) (string, string, int) {
	dataDir := filepath.Join(os.TempDir(), "elnath-chaos-ipc", time.Now().Format("20060102150405.000000000"))
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "error", err.Error(), 0
	}
	db, err := core.OpenDB(dataDir)
	if err != nil {
		return "error", err.Error(), 0
	}
	defer db.Close()
	queue, err := daemon.NewQueue(db.Main)
	if err != nil {
		return "error", err.Error(), 0
	}
	socketPath := filepath.Join(dataDir, "daemon.sock")
	inj := fault.NewScenarioInjector(scenario, time.Now().UnixNano())
	inj.ResetForRun()
	runner := chaosDaemonRunner(scenario)
	d := daemon.New(queue, socketPath, 1, runner, logger)
	d.WithFaultInjection(inj, scenario)
	d.WithFaultGuardConfig(fault.GuardConfig{Enabled: true})
	d.MarkFaultGuardChecked()
	startCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d.Start(startCtx) }()
	if err := waitForSocket(socketPath); err != nil {
		cancel()
		<-done
		return "error", err.Error(), 0
	}
	task, err := submitChaosTask(socketPath, queue, scenarioTimeout(scenario))
	cancel()
	if err != nil {
		<-done
		return "error", err.Error(), 0
	}
	if scenario.FaultType == fault.FaultWorkerPanic && task.Status == daemon.StatusFailed && strings.Contains(task.Result, "fault: injected worker panic") {
		secondTask, retryErr := submitChaosTask(socketPath, queue, scenarioTimeout(scenario))
		<-done
		if retryErr != nil {
			return "error", retryErr.Error(), 1
		}
		if secondTask.Status == daemon.StatusDone && strings.Contains(secondTask.Result, "scenario complete") {
			return "pass", "", 1
		}
		if secondTask.Status == daemon.StatusFailed {
			return "fail", secondTask.Result, 1
		}
		return "fail", secondTask.Result, 1
	}
	<-done
	if task.Status == daemon.StatusDone && strings.Contains(task.Result, "scenario complete") {
		return "pass", "", 0
	}
	if task.Status == daemon.StatusFailed {
		return "fail", task.Result, 1
	}
	return "fail", task.Result, 0
}

func chaosDaemonRunner(scenario *fault.Scenario) daemon.AgentTaskRunner {
	return func(ctx context.Context, payload string, _ func(string)) (daemon.TaskResult, error) {
		_ = payload
		reg := buildToolRegistry(tools.NewPathGuard(os.TempDir(), nil))
		provider := &chaosProvider{scenario: scenario, providerName: "anthropic"}
		a := agent.New(provider, reg,
			agent.WithPermission(agent.NewPermission(agent.WithMode(agent.ModeBypass))),
			agent.WithMaxIterations(4),
		)
		result, err := a.Run(ctx, []llm.Message{llm.NewUserMessage("run daemon scenario")}, nil)
		if err != nil {
			return daemon.TaskResult{}, err
		}
		text := ""
		if len(result.Messages) > 0 {
			text = result.Messages[len(result.Messages)-1].Text()
		}
		return daemon.TaskResult{Result: text, Summary: text}, nil
	}
}

type chaosProvider struct {
	scenario     *fault.Scenario
	providerName string
	toolInput    string
}

type chaosTool struct{ name string }

func (t *chaosTool) Name() string                           { return t.name }
func (t *chaosTool) Description() string                    { return t.name }
func (t *chaosTool) Schema() json.RawMessage                { return json.RawMessage(`{"type":"object"}`) }
func (t *chaosTool) IsConcurrencySafe(json.RawMessage) bool { return false }
func (t *chaosTool) Reversible() bool                       { return true }
func (t *chaosTool) Scope(json.RawMessage) tools.ToolScope  { return tools.ConservativeScope() }
func (t *chaosTool) ShouldCancelSiblingsOnError() bool      { return false }
func (t *chaosTool) Execute(context.Context, json.RawMessage) (*tools.Result, error) {
	return tools.SuccessResult("scenario complete"), nil
}

func (p *chaosProvider) Name() string { return p.providerName }

func (p *chaosProvider) Models() []llm.ModelInfo { return nil }

func (p *chaosProvider) Chat(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}

func (p *chaosProvider) Stream(_ context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
	if p.scenario.Category == fault.CategoryTool {
		results := gatherToolResults(req.Messages)
		if len(results) == 0 {
			emitChaosToolCall(cb, "tool-1", p.scenario.TargetTool, p.toolInput)
			return nil
		}
		last := results[len(results)-1]
		if last.IsError && len(results) <= p.scenario.Threshold.MaxRecoveryAttempts {
			emitChaosToolCall(cb, fmt.Sprintf("tool-%d", len(results)+1), p.scenario.TargetTool, p.toolInput)
			return nil
		}
		text := "scenario complete"
		if last.IsError {
			text = "scenario failed"
		}
		cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: text})
		cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 1, OutputTokens: 1}})
		return nil
	}
	cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: "scenario complete"})
	cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 1, OutputTokens: 1}})
	return nil
}

func gatherToolResults(messages []llm.Message) []llm.ToolResultBlock {
	var results []llm.ToolResultBlock
	for _, msg := range messages {
		for _, block := range msg.Content {
			if tr, ok := block.(llm.ToolResultBlock); ok {
				results = append(results, tr)
			}
		}
	}
	return results
}

func emitChaosToolCall(cb func(llm.StreamEvent), id, name, input string) {
	cb(llm.StreamEvent{Type: llm.EventToolUseStart, ToolCall: &llm.ToolUseEvent{ID: id, Name: name}})
	cb(llm.StreamEvent{Type: llm.EventToolUseDone, ToolCall: &llm.ToolUseEvent{ID: id, Name: name, Input: input}})
	cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 1, OutputTokens: 1}})
}

func prepareAgentScenarioResources(scenario *fault.Scenario) (string, func(), string, string) {
	workDir, _ := os.MkdirTemp("", "elnath-chaos-agent-")
	cleanup := func() { _ = os.RemoveAll(workDir) }
	providerName := "anthropic"
	toolInput := `{}`
	switch scenario.Name {
	case "tool-bash-transient-fail":
		toolInput = `{"command":"pwd"}`
	case "tool-file-read-perm-denied":
		path := filepath.Join(workDir, "input.txt")
		_ = os.WriteFile(path, []byte("hello fault"), 0o600)
		toolInput = fmt.Sprintf(`{"file_path":%q}`, path)
	case "tool-web-timeout":
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "ok")
		}))
		cleanup = func() {
			server.Close()
			_ = os.RemoveAll(workDir)
		}
		toolInput = fmt.Sprintf(`{"url":%q}`, server.URL)
	case "llm-codex-malformed-json":
		providerName = "codex"
	}
	return workDir, cleanup, toolInput, providerName
}

func newChaosToolRegistry(scenario *fault.Scenario) *tools.Registry {
	reg := tools.NewRegistry()
	name := scenario.TargetTool
	if name == "" {
		name = "bash"
	}
	reg.Register(&chaosTool{name: name})
	return reg
}

func scenarioTimeout(scenario *fault.Scenario) time.Duration {
	switch scenario.FaultType {
	case fault.FaultTimeout:
		return 300 * time.Millisecond
	case fault.FaultBackpressure:
		return 3 * time.Second
	default:
		return 2 * time.Second
	}
}

func chaosBaseDir(cfg *config.Config, override string) string {
	if override != "" {
		return override
	}
	if cfg != nil && cfg.FaultInjection.OutputDir != "" {
		return cfg.FaultInjection.OutputDir
	}
	if cfg != nil && cfg.DataDir != "" {
		return filepath.Join(cfg.DataDir, "fault")
	}
	return filepath.Join(config.DefaultDataDir(), "fault")
}

func resolveRunDir(baseDir, arg string) (string, error) {
	if arg == "latest" {
		entries, err := os.ReadDir(baseDir)
		if err != nil {
			return "", err
		}
		type entryInfo struct {
			name string
			mod  time.Time
		}
		var dirs []entryInfo
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			dirs = append(dirs, entryInfo{name: entry.Name(), mod: info.ModTime()})
		}
		if len(dirs) == 0 {
			return "", fmt.Errorf("no fault runs found")
		}
		sort.Slice(dirs, func(i, j int) bool { return dirs[i].mod.After(dirs[j].mod) })
		arg = dirs[0].name
	}
	if err := validateRunID(arg); err != nil {
		return "", err
	}
	return filepath.Join(baseDir, arg), nil
}

func newRunID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	hexText := hex.EncodeToString(b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexText[:8], hexText[8:12], hexText[12:16], hexText[16:20], hexText[20:]), nil
}

var uuidV4Pattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)

func validateRunID(runID string) error {
	if !uuidV4Pattern.MatchString(runID) {
		return fmt.Errorf("invalid run-id %q", runID)
	}
	return nil
}

func mustMarshalString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

func sendChaosIPC(socketPath string, req daemon.IPCRequest, timeout time.Duration) (*daemon.IPCResponse, error) {
	conn, err := net.DialTimeout("unix", socketPath, timeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return nil, err
	}
	var resp daemon.IPCResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func waitForSocket(socketPath string) error {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", socketPath)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("socket did not become ready: %s", socketPath)
}

func extractChaosTaskID(resp *daemon.IPCResponse) (int64, error) {
	data, err := json.Marshal(resp.Data)
	if err != nil {
		return 0, err
	}
	var parsed struct {
		TaskID float64 `json:"task_id"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return 0, err
	}
	if parsed.TaskID <= 0 {
		return 0, fmt.Errorf("missing task_id in response")
	}
	return int64(parsed.TaskID), nil
}

func waitForTask(queue *daemon.Queue, taskID int64, timeout time.Duration) (*daemon.Task, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, err := queue.Get(context.Background(), taskID)
		if err != nil {
			return nil, err
		}
		if task.Status == daemon.StatusDone || task.Status == daemon.StatusFailed {
			return task, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil, fmt.Errorf("task %d did not finish before timeout", taskID)
}

func submitChaosTask(socketPath string, queue *daemon.Queue, timeout time.Duration) (*daemon.Task, error) {
	resp, err := sendChaosIPC(socketPath, daemon.IPCRequest{Command: "submit", Payload: mustMarshalString("say hello")}, timeout)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("%s", resp.Err)
	}
	taskID, err := extractChaosTaskID(resp)
	if err != nil {
		return nil, err
	}
	return waitForTask(queue, taskID, timeout)
}
