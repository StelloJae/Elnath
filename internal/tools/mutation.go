package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const defaultMutationDiagnosticTimeout = 2 * time.Second

var pythonDiagnosticCommand = "python3"
var typescriptDiagnosticCommand = "node"
var pythonDiagnosticLinePattern = regexp.MustCompile(`(?i)\bline\s+(\d+)\b`)

const typescriptDiagnosticScript = `
const fs = require("fs");
const path = require("path");
const { createRequire } = require("module");

const tempPath = process.argv[1];
const fileName = process.argv[2] || tempPath;
let ts = null;
let error = "";
for (const req of [createRequire(path.resolve(fileName)), require]) {
  try {
    ts = req("typescript");
    break;
  } catch (err) {
    error = err && (err.code || err.message) ? String(err.code || err.message) : String(err);
  }
}
if (!ts) {
  console.log(JSON.stringify({ configured: false, error }));
  process.exit(0);
}

const source = fs.readFileSync(tempPath, "utf8");
const result = ts.transpileModule(source, {
  fileName,
  reportDiagnostics: true,
  compilerOptions: {
    target: ts.ScriptTarget.ES2020,
    module: ts.ModuleKind.CommonJS,
    jsx: ts.JsxEmit.ReactJSX,
    allowJs: true,
    noEmitOnError: false
  }
});

const diagnostics = (result.diagnostics || []).map((diag) => {
  let line = 1;
  let column = 1;
  if (diag.file && typeof diag.start === "number") {
    const pos = diag.file.getLineAndCharacterOfPosition(diag.start);
    line = pos.line + 1;
    column = pos.character + 1;
  }
  const severity =
    diag.category === ts.DiagnosticCategory.Error ? "error" :
    diag.category === ts.DiagnosticCategory.Warning ? "warning" :
    diag.category === ts.DiagnosticCategory.Suggestion ? "suggestion" :
    "info";
  const message = ts.flattenDiagnosticMessageText(diag.messageText, " ");
  return { line, column, severity, message, code: diag.code, source: "typescript/transpileModule" };
});

console.log(JSON.stringify({ configured: true, diagnostics }));
`

type typescriptDiagnosticOutput struct {
	Configured  bool                          `json:"configured"`
	Error       string                        `json:"error,omitempty"`
	Diagnostics []typescriptDiagnosticPayload `json:"diagnostics,omitempty"`
}

type typescriptDiagnosticPayload struct {
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Code     int    `json:"code"`
	Source   string `json:"source"`
}

type FileMutation struct {
	Operation               string                   `json:"operation"`
	Path                    string                   `json:"path"`
	Changed                 bool                     `json:"changed"`
	BeforeExists            bool                     `json:"before_exists"`
	AfterExists             bool                     `json:"after_exists"`
	BeforeHash              string                   `json:"before_hash,omitempty"`
	AfterHash               string                   `json:"after_hash,omitempty"`
	BeforeLines             int                      `json:"before_lines"`
	AfterLines              int                      `json:"after_lines"`
	LineDelta               int                      `json:"line_delta"`
	DiagnosticLanguage      string                   `json:"diagnostic_language,omitempty"`
	DiagnosticStatus        string                   `json:"diagnostic_status,omitempty"`
	NewDiagnosticCount      int                      `json:"new_diagnostic_count,omitempty"`
	ExistingDiagnosticCount int                      `json:"existing_diagnostic_count,omitempty"`
	ResolvedDiagnosticCount int                      `json:"resolved_diagnostic_count,omitempty"`
	NewDiagnostics          []FileMutationDiagnostic `json:"new_diagnostics,omitempty"`
	FailureFamily           string                   `json:"failure_family,omitempty"`
}

type FileMutationDiagnostic struct {
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	Error    string `json:"error"`
	Severity string `json:"severity,omitempty"`
	Source   string `json:"source,omitempty"`
}

type MutationDiagnosticAdapterPolicy struct {
	Language  string `json:"language"`
	Status    string `json:"status"`
	Adapter   string `json:"adapter,omitempty"`
	Command   string `json:"command,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
	Scope     string `json:"scope,omitempty"`
	Notes     string `json:"notes,omitempty"`
}

func NewFileMutation(operation string, path string, before []byte, beforeExists bool, after []byte, afterExists bool) *FileMutation {
	beforeLines := countMutationLines(before, beforeExists)
	afterLines := countMutationLines(after, afterExists)
	return &FileMutation{
		Operation:    strings.TrimSpace(operation),
		Path:         cleanMutationPath(path),
		Changed:      beforeExists != afterExists || !bytes.Equal(before, after),
		BeforeExists: beforeExists,
		AfterExists:  afterExists,
		BeforeHash:   mutationHash(before, beforeExists),
		AfterHash:    mutationHash(after, afterExists),
		BeforeLines:  beforeLines,
		AfterLines:   afterLines,
		LineDelta:    afterLines - beforeLines,
	}
}

func MutationDisplayPath(ctx context.Context, guard *PathGuard, abs string, fallback string) string {
	if guard != nil {
		if base, err := SessionWorkDirFromContext(ctx, guard); err == nil && strings.TrimSpace(base) != "" {
			return relPath(base, abs)
		}
		if workDir := guard.WorkDir(); strings.TrimSpace(workDir) != "" {
			return relPath(workDir, abs)
		}
	}
	return cleanMutationPath(fallback)
}

func AnnotateMutationDiagnostics(mutation *FileMutation, absPath string, basePath string, before []byte, beforeExists bool, after []byte, afterExists bool) {
	if mutation == nil {
		return
	}
	language := mutationDiagnosticLanguage(absPath)
	if strings.TrimSpace(basePath) == "" {
		basePath = filepath.Dir(absPath)
	}
	baselineDiagnostics, baselineStatus, baselineOK := mutationDiagnosticsFromSource(absPath, basePath, before, beforeExists, language)
	currentDiagnostics, currentStatus, currentOK := mutationDiagnosticsFromSource(absPath, basePath, after, afterExists, language)
	if !baselineOK || !currentOK {
		mutation.DiagnosticLanguage = language
		mutation.DiagnosticStatus = firstNonEmptyMutationDiagnosticStatus(currentStatus, baselineStatus, "diagnostics_not_configured")
		return
	}
	currentRel := relPath(basePath, absPath)
	delta := compareCodeDiagnostics(baselineDiagnostics, currentDiagnostics, before, after, currentRel)
	mutation.DiagnosticLanguage = language
	mutation.DiagnosticStatus = "diagnostic_delta_clean"
	if delta.NewCount > 0 {
		mutation.DiagnosticStatus = "new_diagnostics_found"
	}
	mutation.NewDiagnosticCount = delta.NewCount
	mutation.ExistingDiagnosticCount = delta.ExistingCount
	mutation.ResolvedDiagnosticCount = delta.ResolvedCount
	mutation.NewDiagnostics = fileMutationDiagnosticsFromDelta(delta.New, 3)
}

func AnnotateGoMutationDiagnostics(mutation *FileMutation, absPath string, basePath string, before []byte, beforeExists bool, after []byte, afterExists bool) {
	AnnotateMutationDiagnostics(mutation, absPath, basePath, before, beforeExists, after, afterExists)
}

func MutationDiagnosticAdapterPolicies() []MutationDiagnosticAdapterPolicy {
	pythonStatus := "diagnostics_not_configured"
	if pythonDiagnosticCommandAvailable() {
		pythonStatus = "available"
	}
	typescriptStatus := "diagnostics_not_configured"
	if typescriptDiagnosticCommandAvailable() {
		typescriptStatus = "conditional"
	}
	return []MutationDiagnosticAdapterPolicy{
		{
			Language: "go",
			Status:   "available",
			Adapter:  "go/parser",
			Scope:    "syntax",
			Notes:    "in-process parser diagnostic delta",
		},
		{
			Language:  "python",
			Status:    pythonStatus,
			Adapter:   "python/py_compile",
			Command:   strings.TrimSpace(pythonDiagnosticCommand),
			TimeoutMS: int(defaultMutationDiagnosticTimeout / time.Millisecond),
			Scope:     "syntax",
			Notes:     "best-effort temp-file syntax diagnostic delta",
		},
		{
			Language:  "typescript",
			Status:    typescriptStatus,
			Adapter:   "typescript/transpileModule",
			Command:   strings.TrimSpace(typescriptDiagnosticCommand),
			TimeoutMS: int(defaultMutationDiagnosticTimeout / time.Millisecond),
			Scope:     "syntax",
			Notes:     "best-effort temp-file syntax diagnostic delta; requires a project-local or globally resolvable typescript module",
		},
		{
			Language:  "javascript",
			Status:    typescriptStatus,
			Adapter:   "typescript/transpileModule",
			Command:   strings.TrimSpace(typescriptDiagnosticCommand),
			TimeoutMS: int(defaultMutationDiagnosticTimeout / time.Millisecond),
			Scope:     "syntax",
			Notes:     "best-effort temp-file syntax diagnostic delta via TypeScript parser; requires a project-local or globally resolvable typescript module",
		},
	}
}

func mutationDiagnosticsFromSource(absPath string, basePath string, src []byte, exists bool, language string) ([]codeSymbolError, string, bool) {
	if !exists {
		return nil, "", true
	}
	switch language {
	case "go":
		return parseGoDiagnosticsFromSource(absPath, basePath, src), "", true
	case "python":
		return parsePythonDiagnosticsFromSource(absPath, basePath, src)
	case "typescript", "javascript":
		return parseTypeScriptFamilyDiagnosticsFromSource(absPath, basePath, src)
	default:
		return nil, "diagnostics_not_configured", false
	}
}

func parsePythonDiagnosticsFromSource(absPath string, basePath string, src []byte) ([]codeSymbolError, string, bool) {
	command := strings.TrimSpace(pythonDiagnosticCommand)
	if command == "" {
		return nil, "diagnostics_not_configured", false
	}
	exe, ok := pythonDiagnosticCommandPath(command)
	if !ok {
		return nil, "diagnostics_not_configured", false
	}
	tmpDir, err := os.MkdirTemp("", "elnath-python-diagnostics-*")
	if err != nil {
		return nil, "diagnostics_unavailable", false
	}
	defer os.RemoveAll(tmpDir)

	tmpPath := filepath.Join(tmpDir, filepath.Base(absPath))
	if err := os.WriteFile(tmpPath, src, 0o644); err != nil {
		return nil, "diagnostics_unavailable", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultMutationDiagnosticTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, exe, "-B", "-m", "py_compile", tmpPath)
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return nil, "diagnostics_unavailable", false
	}
	if err == nil {
		return nil, "", true
	}
	diagnostic := pythonDiagnosticFromCompileOutput(relPath(basePath, absPath), src, string(output), err)
	return []codeSymbolError{diagnostic}, "", true
}

func pythonDiagnosticCommandAvailable() bool {
	_, ok := pythonDiagnosticCommandPath(strings.TrimSpace(pythonDiagnosticCommand))
	return ok
}

func pythonDiagnosticCommandPath(command string) (string, bool) {
	if command == "" {
		return "", false
	}
	exe, err := exec.LookPath(command)
	if err != nil {
		return "", false
	}
	return exe, true
}

func parseTypeScriptFamilyDiagnosticsFromSource(absPath string, basePath string, src []byte) ([]codeSymbolError, string, bool) {
	command := strings.TrimSpace(typescriptDiagnosticCommand)
	if command == "" {
		return nil, "diagnostics_not_configured", false
	}
	exe, ok := typescriptDiagnosticCommandPath(command)
	if !ok {
		return nil, "diagnostics_not_configured", false
	}
	tmpDir, err := os.MkdirTemp("", "elnath-typescript-diagnostics-*")
	if err != nil {
		return nil, "diagnostics_unavailable", false
	}
	defer os.RemoveAll(tmpDir)

	tmpPath := filepath.Join(tmpDir, filepath.Base(absPath))
	if err := os.WriteFile(tmpPath, src, 0o644); err != nil {
		return nil, "diagnostics_unavailable", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultMutationDiagnosticTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, exe, "-e", typescriptDiagnosticScript, tmpPath, absPath)
	if strings.TrimSpace(basePath) != "" {
		cmd.Dir = basePath
	}
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return nil, "diagnostics_unavailable", false
	}
	if err != nil {
		return nil, "diagnostics_unavailable", false
	}
	var parsed typescriptDiagnosticOutput
	if err := json.Unmarshal(output, &parsed); err != nil {
		return nil, "diagnostics_unavailable", false
	}
	if !parsed.Configured {
		return nil, "diagnostics_not_configured", false
	}
	return typescriptDiagnosticsFromPayload(relPath(basePath, absPath), src, parsed.Diagnostics), "", true
}

func typescriptDiagnosticCommandAvailable() bool {
	_, ok := typescriptDiagnosticCommandPath(strings.TrimSpace(typescriptDiagnosticCommand))
	return ok
}

func typescriptDiagnosticCommandPath(command string) (string, bool) {
	if command == "" {
		return "", false
	}
	exe, err := exec.LookPath(command)
	if err != nil {
		return "", false
	}
	return exe, true
}

func typescriptDiagnosticsFromPayload(filePath string, src []byte, diagnostics []typescriptDiagnosticPayload) []codeSymbolError {
	if len(diagnostics) == 0 {
		return nil
	}
	out := make([]codeSymbolError, 0, len(diagnostics))
	for _, item := range diagnostics {
		line := item.Line
		if line <= 0 {
			line = 1
		}
		column := item.Column
		if column <= 0 {
			column = 1
		}
		severity := strings.TrimSpace(item.Severity)
		if severity == "" {
			severity = "error"
		}
		source := strings.TrimSpace(item.Source)
		if source == "" {
			source = "typescript/transpileModule"
		}
		message := strings.TrimSpace(item.Message)
		if message == "" {
			message = "typescript syntax check failed"
		}
		diagnostic := codeSymbolError{
			FilePath: filePath,
			Line:     line,
			Column:   column,
			Error:    message,
			Severity: severity,
			Source:   source,
			LineText: codeSymbolSourceLine(src, line),
		}
		diagnostic.Fingerprint = codeSymbolDiagnosticFingerprint(diagnostic)
		out = append(out, diagnostic)
	}
	return out
}

func pythonDiagnosticFromCompileOutput(filePath string, src []byte, output string, err error) codeSymbolError {
	line := 1
	if match := pythonDiagnosticLinePattern.FindStringSubmatch(output); len(match) == 2 {
		if parsed, parseErr := strconv.Atoi(match[1]); parseErr == nil && parsed > 0 {
			line = parsed
		}
	}
	column := pythonDiagnosticColumn(output)
	message := pythonDiagnosticMessage(output, err)
	diagnostic := codeSymbolError{
		FilePath: filePath,
		Line:     line,
		Column:   column,
		Error:    message,
		Severity: "error",
		Source:   "python/py_compile",
		LineText: codeSymbolSourceLine(src, line),
	}
	diagnostic.Fingerprint = codeSymbolDiagnosticFingerprint(diagnostic)
	return diagnostic
}

func pythonDiagnosticColumn(output string) int {
	for _, line := range strings.Split(output, "\n") {
		if idx := strings.Index(line, "^"); idx >= 0 {
			return idx + 1
		}
	}
	return 0
}

func pythonDiagnosticMessage(output string, err error) string {
	lines := strings.Split(output, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.Contains(line, "^") || strings.HasPrefix(line, "File ") {
			continue
		}
		return strings.TrimPrefix(line, "Sorry: ")
	}
	if err != nil {
		return err.Error()
	}
	return "python syntax check failed"
}

func mutationDiagnosticLanguage(absPath string) string {
	switch strings.ToLower(filepath.Ext(absPath)) {
	case ".go":
		return "go"
	case ".py", ".pyw":
		return "python"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".md", ".mdx":
		return "markdown"
	case ".sh", ".bash", ".zsh":
		return "shell"
	default:
		return "unknown"
	}
}

func firstNonEmptyMutationDiagnosticStatus(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func fileMutationDiagnosticsFromDelta(diagnostics []codeDiagnosticDelta, limit int) []FileMutationDiagnostic {
	if limit <= 0 || len(diagnostics) == 0 {
		return nil
	}
	if len(diagnostics) > limit {
		diagnostics = diagnostics[:limit]
	}
	out := make([]FileMutationDiagnostic, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		out = append(out, FileMutationDiagnostic{
			Line:     diagnostic.AfterLine,
			Column:   diagnostic.AfterColumn,
			Error:    diagnostic.Error,
			Severity: diagnostic.Severity,
			Source:   diagnostic.Source,
		})
	}
	return out
}

func mutationHash(data []byte, exists bool) string {
	if !exists {
		return ""
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func countMutationLines(data []byte, exists bool) int {
	if !exists || len(data) == 0 {
		return 0
	}
	count := bytes.Count(data, []byte("\n"))
	if !bytes.HasSuffix(data, []byte("\n")) {
		count++
	}
	return count
}

func cleanMutationPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(path))
}
