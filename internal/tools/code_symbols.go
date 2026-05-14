package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	CodeSymbolsToolName         = "code_symbols"
	defaultCodeSymbolMaxResults = 50
	maxCodeSymbolResults        = 200
)

// CodeSymbolsTool provides a small Go-native code intelligence surface.
// It intentionally covers only symbol listing and exact-name definitions for
// now instead of claiming full LSP parity.
type CodeSymbolsTool struct{ guard *PathGuard }

func NewCodeSymbolsTool(guard *PathGuard) *CodeSymbolsTool {
	return &CodeSymbolsTool{guard: guard}
}

func (t *CodeSymbolsTool) Name() string { return CodeSymbolsToolName }

func (t *CodeSymbolsTool) Description() string {
	return "Inspect Go code symbols and file outlines without starting a language server. Supports document_symbols for one Go file, workspace_symbols across Go files, and exact-name definition lookup."
}

func (t *CodeSymbolsTool) Schema() json.RawMessage {
	return Object(map[string]Property{
		"operation":   StringEnum("Operation to perform.", "document_symbols", "workspace_symbols", "definition"),
		"file_path":   String("Go file path for document_symbols."),
		"path":        String("Base directory for workspace_symbols or definition. Defaults to current workspace."),
		"query":       String("Optional case-insensitive symbol name filter for workspace_symbols; required exact symbol name for definition."),
		"max_results": Int("Maximum symbols to return. Defaults to 50 and caps at 200."),
	}, []string{"operation"})
}

func (t *CodeSymbolsTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *CodeSymbolsTool) Reversible() bool { return true }

func (t *CodeSymbolsTool) Scope(params json.RawMessage) ToolScope {
	var input codeSymbolsInput
	if err := json.Unmarshal(params, &input); err != nil {
		return ConservativeScope()
	}
	switch strings.ToLower(strings.TrimSpace(input.Operation)) {
	case "document_symbols", "workspace_symbols", "definition":
		return ToolScope{ReadPaths: []string{t.guard.WorkDir()}}
	default:
		return ConservativeScope()
	}
}

func (t *CodeSymbolsTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *CodeSymbolsTool) DeferInitialToolSchema() bool { return true }

type codeSymbolsInput struct {
	Operation  string `json:"operation"`
	FilePath   string `json:"file_path"`
	Path       string `json:"path"`
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
}

type codeSymbolsOutput struct {
	Operation string             `json:"operation"`
	Status    string             `json:"status"`
	Language  string             `json:"language"`
	FilePath  string             `json:"file_path,omitempty"`
	Path      string             `json:"path,omitempty"`
	Query     string             `json:"query,omitempty"`
	Count     int                `json:"count"`
	Truncated bool               `json:"truncated"`
	Errors    []codeSymbolError  `json:"errors,omitempty"`
	Symbols   []codeSymbolItem   `json:"symbols"`
	Receipt   codeSymbolsReceipt `json:"receipt"`
}

type codeSymbolsReceipt struct {
	Tool            string `json:"tool"`
	Action          string `json:"action"`
	ReadOnly        bool   `json:"read_only"`
	Persistent      bool   `json:"persistent"`
	ExecutionPolicy string `json:"execution_policy"`
	Operation       string `json:"operation"`
	Status          string `json:"status"`
	Language        string `json:"language"`
	FilePath        string `json:"file_path,omitempty"`
	Path            string `json:"path,omitempty"`
	Query           string `json:"query,omitempty"`
	Count           int    `json:"count"`
	Truncated       bool   `json:"truncated"`
	ErrorCount      int    `json:"error_count"`
}

type codeSymbolError struct {
	FilePath string `json:"file_path"`
	Error    string `json:"error"`
}

type codeSymbolItem struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Receiver  string `json:"receiver,omitempty"`
	FilePath  string `json:"file_path"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	Signature string `json:"signature,omitempty"`
}

type codeSymbolFileCandidate struct {
	AbsPath string
	RelPath string
}

func (t *CodeSymbolsTool) Execute(ctx context.Context, params json.RawMessage) (*Result, error) {
	var input codeSymbolsInput
	if err := json.Unmarshal(params, &input); err != nil {
		return ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
	}
	switch strings.ToLower(strings.TrimSpace(input.Operation)) {
	case "document_symbols":
		return t.executeDocumentSymbols(ctx, input)
	case "workspace_symbols":
		return t.executeWorkspaceSymbols(ctx, input)
	case "definition":
		return t.executeDefinition(ctx, input)
	default:
		return ErrorResult("code_symbols: operation must be document_symbols, workspace_symbols, or definition"), nil
	}
}

func (t *CodeSymbolsTool) executeDocumentSymbols(ctx context.Context, input codeSymbolsInput) (*Result, error) {
	if strings.TrimSpace(input.FilePath) == "" {
		return ErrorResult("code_symbols: file_path is required for document_symbols"), nil
	}
	abs, err := resolveFileTarget(t.guard, ctx, input.FilePath)
	if err != nil {
		return ErrorResult("code_symbols: " + err.Error()), nil
	}
	sessionBase, err := SessionWorkDirFromContext(ctx, t.guard)
	if err != nil {
		return ErrorResult(fmt.Sprintf("code_symbols: session workspace: %v", err)), nil
	}
	if filepath.Ext(abs) != ".go" {
		return codeSymbolsJSON(codeSymbolsOutput{
			Operation: "document_symbols",
			Status:    "unsupported_language",
			FilePath:  relPath(sessionBase, abs),
			Language:  "unsupported",
		})
	}
	symbols, err := parseGoSymbols(abs, sessionBase)
	if err != nil {
		return ErrorResult(fmt.Sprintf("code_symbols: parse %s: %v", input.FilePath, err)), nil
	}
	return codeSymbolsJSON(codeSymbolsOutput{
		Operation: "document_symbols",
		Status:    "success",
		Language:  "go",
		FilePath:  relPath(sessionBase, abs),
		Count:     len(symbols),
		Symbols:   symbols,
	})
}

func (t *CodeSymbolsTool) executeWorkspaceSymbols(ctx context.Context, input codeSymbolsInput) (*Result, error) {
	sessionBase, err := SessionWorkDirFromContext(ctx, t.guard)
	if err != nil {
		return ErrorResult(fmt.Sprintf("code_symbols: session workspace: %v", err)), nil
	}
	searchBase, err := resolveSearchBase(t.guard, ctx, sessionBase, input.Path)
	if err != nil {
		return ErrorResult("code_symbols: " + err.Error()), nil
	}
	maxResults := normalizeCodeSymbolMax(input.MaxResults)
	query := strings.ToLower(strings.TrimSpace(input.Query))
	var candidates []codeSymbolFileCandidate
	var symbols []codeSymbolItem
	var parseErrors []codeSymbolError
	truncated := false
	walkErr := filepath.WalkDir(searchBase, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			parseErrors = append(parseErrors, codeSymbolError{FilePath: relPath(sessionBase, path), Error: err.Error()})
			return nil
		}
		if d.IsDir() {
			if shouldSkipCodeSymbolDir(path, searchBase, d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		rel := relPath(sessionBase, path)
		scoped, err := t.guard.ResolveSessionScoped(sessionBase, rel)
		if err != nil {
			parseErrors = append(parseErrors, codeSymbolError{FilePath: rel, Error: err.Error()})
			return nil
		}
		candidates = append(candidates, codeSymbolFileCandidate{AbsPath: scoped, RelPath: rel})
		return nil
	})
	if walkErr != nil {
		return ErrorResult(fmt.Sprintf("code_symbols: walk: %v", walkErr)), nil
	}
	gitIgnored := gitIgnoredCodeSymbolPaths(ctx, sessionBase, candidates)
	for _, candidate := range candidates {
		if gitIgnored[filepath.ToSlash(candidate.RelPath)] {
			continue
		}
		fileSymbols, err := parseGoSymbols(candidate.AbsPath, sessionBase)
		if err != nil {
			parseErrors = append(parseErrors, codeSymbolError{FilePath: candidate.RelPath, Error: err.Error()})
			continue
		}
		for _, sym := range fileSymbols {
			if query != "" && !strings.Contains(strings.ToLower(sym.Name), query) {
				continue
			}
			if len(symbols) >= maxResults {
				truncated = true
				break
			}
			symbols = append(symbols, sym)
		}
		if truncated {
			break
		}
	}
	sortCodeSymbols(symbols)
	status := "success"
	if len(parseErrors) > 0 {
		status = "partial_success"
	}
	return codeSymbolsJSON(codeSymbolsOutput{
		Operation: "workspace_symbols",
		Status:    status,
		Language:  "go",
		Path:      relPath(sessionBase, searchBase),
		Query:     strings.TrimSpace(input.Query),
		Count:     len(symbols),
		Truncated: truncated,
		Errors:    parseErrors,
		Symbols:   symbols,
	})
}

func (t *CodeSymbolsTool) executeDefinition(ctx context.Context, input codeSymbolsInput) (*Result, error) {
	sessionBase, err := SessionWorkDirFromContext(ctx, t.guard)
	if err != nil {
		return ErrorResult(fmt.Sprintf("code_symbols: session workspace: %v", err)), nil
	}
	searchBase, err := resolveSearchBase(t.guard, ctx, sessionBase, input.Path)
	if err != nil {
		return ErrorResult("code_symbols: " + err.Error()), nil
	}
	query := strings.TrimSpace(input.Query)
	if query == "" {
		return ErrorResult("code_symbols: query is required for definition"), nil
	}
	maxResults := normalizeCodeSymbolMax(input.MaxResults)
	var candidates []codeSymbolFileCandidate
	var symbols []codeSymbolItem
	var parseErrors []codeSymbolError
	truncated := false
	walkErr := filepath.WalkDir(searchBase, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			parseErrors = append(parseErrors, codeSymbolError{FilePath: relPath(sessionBase, path), Error: err.Error()})
			return nil
		}
		if d.IsDir() {
			if shouldSkipCodeSymbolDir(path, searchBase, d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		rel := relPath(sessionBase, path)
		scoped, err := t.guard.ResolveSessionScoped(sessionBase, rel)
		if err != nil {
			parseErrors = append(parseErrors, codeSymbolError{FilePath: rel, Error: err.Error()})
			return nil
		}
		candidates = append(candidates, codeSymbolFileCandidate{AbsPath: scoped, RelPath: rel})
		return nil
	})
	if walkErr != nil {
		return ErrorResult(fmt.Sprintf("code_symbols: walk: %v", walkErr)), nil
	}
	gitIgnored := gitIgnoredCodeSymbolPaths(ctx, sessionBase, candidates)
	for _, candidate := range candidates {
		if gitIgnored[filepath.ToSlash(candidate.RelPath)] {
			continue
		}
		fileSymbols, err := parseGoSymbols(candidate.AbsPath, sessionBase)
		if err != nil {
			parseErrors = append(parseErrors, codeSymbolError{FilePath: candidate.RelPath, Error: err.Error()})
			continue
		}
		for _, sym := range fileSymbols {
			if !matchesDefinitionSymbol(sym.Name, query) {
				continue
			}
			if len(symbols) >= maxResults {
				truncated = true
				break
			}
			symbols = append(symbols, sym)
		}
		if truncated {
			break
		}
	}
	sortCodeSymbols(symbols)
	status := "success"
	if len(parseErrors) > 0 {
		status = "partial_success"
	}
	if len(symbols) == 0 && len(parseErrors) == 0 {
		status = "not_found"
	}
	return codeSymbolsJSON(codeSymbolsOutput{
		Operation: "definition",
		Status:    status,
		Language:  "go",
		Path:      relPath(sessionBase, searchBase),
		Query:     query,
		Count:     len(symbols),
		Truncated: truncated,
		Errors:    parseErrors,
		Symbols:   symbols,
	})
}

func gitIgnoredCodeSymbolPaths(ctx context.Context, base string, candidates []codeSymbolFileCandidate) map[string]bool {
	if len(candidates) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(candidates))
	var stdin bytes.Buffer
	for _, candidate := range candidates {
		rel := filepath.ToSlash(candidate.RelPath)
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}
		stdin.WriteString(rel)
		stdin.WriteByte(0)
	}
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, "git", "-C", base, "check-ignore", "--stdin", "-z")
	cmd.Stdin = &stdin
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil
		}
		return nil
	}
	ignored := make(map[string]bool)
	for _, part := range bytes.Split(output, []byte{0}) {
		rel := strings.TrimSpace(string(part))
		if rel == "" {
			continue
		}
		ignored[filepath.ToSlash(rel)] = true
	}
	return ignored
}

func parseGoSymbols(absPath, basePath string) ([]codeSymbolItem, error) {
	src, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, absPath, src, 0)
	if err != nil {
		return nil, err
	}
	var symbols []codeSymbolItem
	for _, decl := range file.Decls {
		switch node := decl.(type) {
		case *ast.FuncDecl:
			pos := fset.Position(node.Name.Pos())
			receiver := receiverName(node.Recv)
			symbols = append(symbols, codeSymbolItem{
				Name:      functionSymbolName(receiver, node.Name.Name),
				Kind:      "function",
				Receiver:  receiver,
				FilePath:  relPath(basePath, absPath),
				Line:      pos.Line,
				Column:    pos.Column,
				Signature: formatFuncSignature(node),
			})
		case *ast.GenDecl:
			for _, spec := range node.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					pos := fset.Position(s.Name.Pos())
					symbols = append(symbols, codeSymbolItem{
						Name:      s.Name.Name,
						Kind:      typeSymbolKind(s.Type),
						FilePath:  relPath(basePath, absPath),
						Line:      pos.Line,
						Column:    pos.Column,
						Signature: "type " + s.Name.Name,
					})
				case *ast.ValueSpec:
					for _, name := range s.Names {
						pos := fset.Position(name.Pos())
						symbols = append(symbols, codeSymbolItem{
							Name:      name.Name,
							Kind:      valueSymbolKind(node.Tok),
							FilePath:  relPath(basePath, absPath),
							Line:      pos.Line,
							Column:    pos.Column,
							Signature: formatValueSignature(s, name.Name),
						})
					}
				}
			}
		}
	}
	sortCodeSymbols(symbols)
	return symbols, nil
}

func codeSymbolsJSON(out codeSymbolsOutput) (*Result, error) {
	out.Receipt = codeSymbolsReceipt{
		Tool:            CodeSymbolsToolName,
		Action:          out.Operation,
		ReadOnly:        true,
		Persistent:      false,
		ExecutionPolicy: "code_symbols_observation",
		Operation:       out.Operation,
		Status:          out.Status,
		Language:        out.Language,
		FilePath:        out.FilePath,
		Path:            out.Path,
		Query:           out.Query,
		Count:           out.Count,
		Truncated:       out.Truncated,
		ErrorCount:      len(out.Errors),
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return ErrorResult(fmt.Sprintf("code_symbols: marshal output: %v", err)), nil
	}
	return SuccessResult(string(raw)), nil
}

func normalizeCodeSymbolMax(n int) int {
	if n <= 0 {
		return defaultCodeSymbolMaxResults
	}
	if n > maxCodeSymbolResults {
		return maxCodeSymbolResults
	}
	return n
}

func shouldSkipCodeSymbolDir(path, searchBase, name string) bool {
	if path == searchBase {
		return false
	}
	switch name {
	case ".git", ".elnath", ".omc", "node_modules", "vendor":
		return true
	default:
		return strings.HasPrefix(name, ".")
	}
}

func relPath(base, path string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return path
	}
	return rel
}

func sortCodeSymbols(symbols []codeSymbolItem) {
	sort.Slice(symbols, func(i, j int) bool {
		if symbols[i].FilePath != symbols[j].FilePath {
			return symbols[i].FilePath < symbols[j].FilePath
		}
		if symbols[i].Line != symbols[j].Line {
			return symbols[i].Line < symbols[j].Line
		}
		if symbols[i].Column != symbols[j].Column {
			return symbols[i].Column < symbols[j].Column
		}
		return symbols[i].Name < symbols[j].Name
	})
}

func matchesDefinitionSymbol(symbolName, query string) bool {
	if symbolName == query {
		return true
	}
	return !strings.Contains(query, ".") && strings.HasSuffix(symbolName, "."+query)
}

func functionSymbolName(receiver, name string) string {
	if receiver == "" {
		return name
	}
	return receiver + "." + name
}

func receiverName(fields *ast.FieldList) string {
	if fields == nil || len(fields.List) == 0 {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(formatNode(fields.List[0].Type)), "*")
}

func typeSymbolKind(expr ast.Expr) string {
	switch expr.(type) {
	case *ast.InterfaceType:
		return "interface"
	case *ast.StructType:
		return "struct"
	default:
		return "type"
	}
}

func valueSymbolKind(tok token.Token) string {
	switch tok {
	case token.CONST:
		return "constant"
	case token.VAR:
		return "variable"
	default:
		return "value"
	}
}

func formatFuncSignature(fn *ast.FuncDecl) string {
	if fn == nil {
		return ""
	}
	fnType := strings.TrimPrefix(formatNode(fn.Type), "func")
	if receiver := receiverName(fn.Recv); receiver != "" {
		return "func (" + receiver + ") " + fn.Name.Name + fnType
	}
	return "func " + fn.Name.Name + fnType
}

func formatValueSignature(node *ast.ValueSpec, name string) string {
	if node == nil {
		return name
	}
	if node.Type != nil {
		return name + " " + formatNode(node.Type)
	}
	return name
}

func formatNode(node any) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, token.NewFileSet(), node); err != nil {
		return ""
	}
	return buf.String()
}
