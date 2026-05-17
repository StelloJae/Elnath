package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

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
	if filepath.Ext(absPath) != ".go" {
		mutation.DiagnosticLanguage = language
		mutation.DiagnosticStatus = "diagnostics_not_configured"
		return
	}
	if strings.TrimSpace(basePath) == "" {
		basePath = filepath.Dir(absPath)
	}
	var baselineDiagnostics []codeSymbolError
	if beforeExists {
		baselineDiagnostics = parseGoDiagnosticsFromSource(absPath, basePath, before)
	}
	var currentDiagnostics []codeSymbolError
	if afterExists {
		currentDiagnostics = parseGoDiagnosticsFromSource(absPath, basePath, after)
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
