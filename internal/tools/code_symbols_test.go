package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodeSymbolsToolDocumentSymbols(t *testing.T) {
	root := t.TempDir()
	writeCodeSymbolFile(t, filepath.Join(root, "worker.go"), `package demo

const DefaultLimit = 10

type Worker struct{}

type Runner interface {
	Run() error
}

func NewWorker() *Worker {
	return &Worker{}
}

func (w *Worker) Run() error {
	return nil
}
`)
	tool := NewCodeSymbolsTool(NewPathGuard(root, nil))

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"operation":"document_symbols","file_path":"worker.go"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}

	var out codeSymbolsOutput
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if out.Operation != "document_symbols" || out.Status != "success" || out.FilePath != "worker.go" {
		t.Fatalf("output header = %+v", out)
	}
	if out.Receipt.Tool != CodeSymbolsToolName || out.Receipt.ExecutionPolicy != "code_symbols_observation" {
		t.Fatalf("receipt = %+v, want code_symbols observation receipt", out.Receipt)
	}
	if !out.Receipt.ReadOnly || out.Receipt.Operation != "document_symbols" || out.Receipt.Status != "success" || out.Receipt.Count == 0 {
		t.Fatalf("receipt metadata = %+v, want read-only document_symbols success", out.Receipt)
	}
	seen := map[string]codeSymbolItem{}
	for _, sym := range out.Symbols {
		seen[sym.Name] = sym
	}
	for _, name := range []string{"DefaultLimit", "Worker", "Runner", "NewWorker", "Worker.Run"} {
		if _, ok := seen[name]; !ok {
			t.Fatalf("missing symbol %q in %+v", name, out.Symbols)
		}
	}
	if seen["DefaultLimit"].Kind != "constant" {
		t.Fatalf("DefaultLimit kind = %q, want constant", seen["DefaultLimit"].Kind)
	}
	if seen["Runner"].Kind != "interface" {
		t.Fatalf("Runner kind = %q, want interface", seen["Runner"].Kind)
	}
	if seen["Worker.Run"].Receiver != "Worker" {
		t.Fatalf("Worker.Run receiver = %q, want Worker", seen["Worker.Run"].Receiver)
	}
}

func TestCodeSymbolsToolWorkspaceSymbolsFiltersQueryAndCaps(t *testing.T) {
	root := t.TempDir()
	writeCodeSymbolFile(t, filepath.Join(root, "alpha.go"), `package demo

func AlphaOne() {}
func BetaOne() {}
`)
	writeCodeSymbolFile(t, filepath.Join(root, "nested", "beta.go"), `package demo

func AlphaTwo() {}
type AlphaType struct{}
`)
	writeCodeSymbolFile(t, filepath.Join(root, "nested", "ignored.txt"), `package demo
func AlphaIgnored() {}
`)
	tool := NewCodeSymbolsTool(NewPathGuard(root, nil))

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"operation":"workspace_symbols","query":"alpha","max_results":2}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}

	var out codeSymbolsOutput
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if out.Operation != "workspace_symbols" || out.Status != "success" || out.Count != 2 || !out.Truncated {
		t.Fatalf("output = %+v, want two truncated workspace matches", out)
	}
	for _, sym := range out.Symbols {
		if sym.FilePath == "" || sym.Line == 0 || sym.Column == 0 {
			t.Fatalf("symbol missing location: %+v", sym)
		}
		if sym.Name == "BetaOne" || sym.Name == "AlphaIgnored" {
			t.Fatalf("unexpected symbol in filtered output: %+v", sym)
		}
	}
}

func TestCodeSymbolsToolWorkspaceSymbolsReportsPartialParseErrors(t *testing.T) {
	root := t.TempDir()
	writeCodeSymbolFile(t, filepath.Join(root, "ok.go"), `package demo

func Alpha() {}
`)
	writeCodeSymbolFile(t, filepath.Join(root, "broken.go"), `package demo

func Broken( {
`)
	tool := NewCodeSymbolsTool(NewPathGuard(root, nil))

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"operation":"workspace_symbols","query":"alpha"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}

	var out codeSymbolsOutput
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if out.Status != "partial_success" || len(out.Errors) != 1 {
		t.Fatalf("output = %+v, want partial_success with one parse error", out)
	}
	if out.Receipt.ErrorCount != 1 || out.Receipt.Status != "partial_success" {
		t.Fatalf("receipt = %+v, want partial_success with one parse error", out.Receipt)
	}
	if out.Errors[0].FilePath != "broken.go" {
		t.Fatalf("error file = %q, want broken.go", out.Errors[0].FilePath)
	}
	if len(out.Symbols) != 1 || out.Symbols[0].Name != "Alpha" {
		t.Fatalf("symbols = %+v, want Alpha from ok.go", out.Symbols)
	}
}

func TestCodeSymbolsToolWorkspaceSymbolsReportsSymlinkEscape(t *testing.T) {
	root, sessionDir, ctx := b3b1Setup(t, "sess-code-symbols")
	outside := t.TempDir()
	writeCodeSymbolFile(t, filepath.Join(outside, "secret.go"), `package outside

func SecretOutside() {}
`)
	if err := os.Symlink(filepath.Join(outside, "secret.go"), filepath.Join(sessionDir, "secret_link.go")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	tool := NewCodeSymbolsTool(NewPathGuard(root, nil))

	res, err := tool.Execute(ctx, json.RawMessage(`{"operation":"workspace_symbols","query":"Secret"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}

	var out codeSymbolsOutput
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if out.Status != "partial_success" || len(out.Errors) != 1 {
		t.Fatalf("output = %+v, want partial_success with one symlink escape error", out)
	}
	if len(out.Symbols) != 0 {
		t.Fatalf("symbols = %+v, want no leaked outside symbols", out.Symbols)
	}
	if out.Errors[0].FilePath != "secret_link.go" {
		t.Fatalf("error file = %q, want secret_link.go", out.Errors[0].FilePath)
	}
	if !strings.Contains(out.Errors[0].Error, "escapes session workspace") {
		t.Fatalf("error = %q, want session escape", out.Errors[0].Error)
	}
}

func TestCodeSymbolsToolUnsupportedLanguageIsStructured(t *testing.T) {
	root := t.TempDir()
	writeCodeSymbolFile(t, filepath.Join(root, "notes.txt"), "not go")
	tool := NewCodeSymbolsTool(NewPathGuard(root, nil))

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"operation":"document_symbols","file_path":"notes.txt"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}

	var out codeSymbolsOutput
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if out.Status != "unsupported_language" || out.Count != 0 {
		t.Fatalf("output = %+v, want unsupported_language with no symbols", out)
	}
}

func TestCodeSymbolsToolMetadata(t *testing.T) {
	tool := NewCodeSymbolsTool(NewPathGuard(t.TempDir(), nil))
	if tool.Name() != CodeSymbolsToolName {
		t.Fatalf("Name = %q", tool.Name())
	}
	if !tool.IsConcurrencySafe(nil) || !tool.Reversible() || tool.ShouldCancelSiblingsOnError() {
		t.Fatalf("metadata = concurrency:%t reversible:%t cancel:%t", tool.IsConcurrencySafe(nil), tool.Reversible(), tool.ShouldCancelSiblingsOnError())
	}
	if !ShouldDeferToolSchema(tool) {
		t.Fatal("code_symbols should defer initial schema")
	}
}

func writeCodeSymbolFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}
