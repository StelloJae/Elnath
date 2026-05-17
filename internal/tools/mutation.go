package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
var pythonDiagnosticLinePattern = regexp.MustCompile(`(?i)\bline\s+(\d+)\b`)

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

func mutationDiagnosticsFromSource(absPath string, basePath string, src []byte, exists bool, language string) ([]codeSymbolError, string, bool) {
	if !exists {
		return nil, "", true
	}
	switch language {
	case "go":
		return parseGoDiagnosticsFromSource(absPath, basePath, src), "", true
	case "python":
		return parsePythonDiagnosticsFromSource(absPath, basePath, src)
	default:
		return nil, "diagnostics_not_configured", false
	}
}

func parsePythonDiagnosticsFromSource(absPath string, basePath string, src []byte) ([]codeSymbolError, string, bool) {
	command := strings.TrimSpace(pythonDiagnosticCommand)
	if command == "" {
		return nil, "diagnostics_not_configured", false
	}
	exe, err := exec.LookPath(command)
	if err != nil {
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
