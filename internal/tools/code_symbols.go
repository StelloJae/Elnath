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
	"unicode/utf8"
)

const (
	CodeSymbolsToolName         = "code_symbols"
	defaultCodeSymbolMaxResults = 50
	maxCodeSymbolResults        = 200
)

// CodeSymbolsTool provides a small Go-native code intelligence surface.
// It intentionally covers symbol listing, exact-name definitions, identifier
// references, and basic hover signatures instead of claiming full LSP parity.
type CodeSymbolsTool struct{ guard *PathGuard }

func NewCodeSymbolsTool(guard *PathGuard) *CodeSymbolsTool {
	return &CodeSymbolsTool{guard: guard}
}

func (t *CodeSymbolsTool) Name() string { return CodeSymbolsToolName }

func (t *CodeSymbolsTool) Description() string {
	return "Inspect Go code symbols and file outlines without starting a language server. Supports document_symbols for one Go file, workspace_symbols across Go files, exact-name definition lookup, Go identifier references, and basic Go hover signatures."
}

func (t *CodeSymbolsTool) Schema() json.RawMessage {
	return Object(map[string]Property{
		"operation":   StringEnum("Operation to perform.", "document_symbols", "workspace_symbols", "definition", "references", "hover"),
		"file_path":   String("Go file path for document_symbols."),
		"path":        String("Base directory for workspace_symbols, definition, references, or hover. Defaults to current workspace."),
		"query":       String("Optional case-insensitive symbol name filter for workspace_symbols; required exact symbol name for definition, references, or hover unless file_path, line, and column identify a Go symbol."),
		"line":        Int("Optional 1-based line used with file_path and column to derive a Go identifier for definition, references, or hover."),
		"column":      Int("Optional 1-based column used with file_path and line to derive a Go identifier for definition, references, or hover."),
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
	case "document_symbols", "workspace_symbols", "definition", "references", "hover":
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
	Line       int    `json:"line"`
	Column     int    `json:"column"`
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
	case "references":
		return t.executeReferences(ctx, input)
	case "hover":
		return t.executeHover(ctx, input)
	default:
		return ErrorResult("code_symbols: operation must be document_symbols, workspace_symbols, definition, references, or hover"), nil
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
	if strings.TrimSpace(input.Query) == "" && strings.TrimSpace(input.FilePath) == "" && input.Line == 0 && input.Column == 0 {
		return ErrorResult("code_symbols: query is required for definition"), nil
	}
	out, err := t.collectDefinitions(ctx, sessionBase, searchBase, input)
	if err != nil {
		return ErrorResult("code_symbols: " + err.Error()), nil
	}
	return codeSymbolsJSON(out)
}

func (t *CodeSymbolsTool) executeHover(ctx context.Context, input codeSymbolsInput) (*Result, error) {
	sessionBase, err := SessionWorkDirFromContext(ctx, t.guard)
	if err != nil {
		return ErrorResult(fmt.Sprintf("code_symbols: session workspace: %v", err)), nil
	}
	searchBase, err := resolveSearchBase(t.guard, ctx, sessionBase, input.Path)
	if err != nil {
		return ErrorResult("code_symbols: " + err.Error()), nil
	}
	query, err := t.resolveCodeSymbolQuery(ctx, sessionBase, input, "hover")
	if err != nil {
		return ErrorResult("code_symbols: " + err.Error()), nil
	}
	if query == "" {
		return ErrorResult("code_symbols: query is required for hover"), nil
	}
	defInput := input
	defInput.Query = query
	defOutput, err := t.collectDefinitions(ctx, sessionBase, searchBase, defInput)
	if err != nil {
		return ErrorResult("code_symbols: " + err.Error()), nil
	}
	defOutput.Operation = "hover"
	defOutput.Query = query
	return codeSymbolsJSON(defOutput)
}

func (t *CodeSymbolsTool) executeReferences(ctx context.Context, input codeSymbolsInput) (*Result, error) {
	sessionBase, err := SessionWorkDirFromContext(ctx, t.guard)
	if err != nil {
		return ErrorResult(fmt.Sprintf("code_symbols: session workspace: %v", err)), nil
	}
	searchBase, err := resolveSearchBase(t.guard, ctx, sessionBase, input.Path)
	if err != nil {
		return ErrorResult("code_symbols: " + err.Error()), nil
	}
	query, err := t.resolveCodeSymbolQuery(ctx, sessionBase, input, "references")
	if err != nil {
		return ErrorResult("code_symbols: " + err.Error()), nil
	}
	if query == "" {
		return ErrorResult("code_symbols: query is required for references"), nil
	}
	maxResults := normalizeCodeSymbolMax(input.MaxResults)
	var candidates []codeSymbolFileCandidate
	var references []codeSymbolItem
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
		fileReferences, err := parseGoReferences(candidate.AbsPath, sessionBase, query)
		if err != nil {
			parseErrors = append(parseErrors, codeSymbolError{FilePath: candidate.RelPath, Error: err.Error()})
			continue
		}
		for _, ref := range fileReferences {
			if len(references) >= maxResults {
				truncated = true
				break
			}
			references = append(references, ref)
		}
		if truncated {
			break
		}
	}
	sortCodeSymbols(references)
	status := "success"
	if len(parseErrors) > 0 {
		status = "partial_success"
	}
	if len(references) == 0 && len(parseErrors) == 0 {
		status = "not_found"
	}
	return codeSymbolsJSON(codeSymbolsOutput{
		Operation: "references",
		Status:    status,
		Language:  "go",
		Path:      relPath(sessionBase, searchBase),
		Query:     query,
		Count:     len(references),
		Truncated: truncated,
		Errors:    parseErrors,
		Symbols:   references,
	})
}

func (t *CodeSymbolsTool) collectDefinitions(ctx context.Context, sessionBase string, searchBase string, input codeSymbolsInput) (codeSymbolsOutput, error) {
	query, err := t.resolveCodeSymbolQuery(ctx, sessionBase, input, "definition")
	if err != nil {
		return codeSymbolsOutput{}, err
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
		return codeSymbolsOutput{}, fmt.Errorf("walk: %v", walkErr)
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
	return codeSymbolsOutput{
		Operation: "definition",
		Status:    status,
		Language:  "go",
		Path:      relPath(sessionBase, searchBase),
		Query:     query,
		Count:     len(symbols),
		Truncated: truncated,
		Errors:    parseErrors,
		Symbols:   symbols,
	}, nil
}

func (t *CodeSymbolsTool) resolveCodeSymbolQuery(ctx context.Context, sessionBase string, input codeSymbolsInput, operation string) (string, error) {
	if query := strings.TrimSpace(input.Query); query != "" {
		return query, nil
	}
	if strings.TrimSpace(input.FilePath) == "" && input.Line == 0 && input.Column == 0 {
		return "", nil
	}
	if strings.TrimSpace(input.FilePath) == "" || input.Line <= 0 || input.Column <= 0 {
		return "", fmt.Errorf("query or file_path, line, and column are required for %s", operation)
	}
	abs, err := resolveFileTarget(t.guard, ctx, input.FilePath)
	if err != nil {
		return "", err
	}
	if filepath.Ext(abs) != ".go" {
		return "", fmt.Errorf("cursor query is only supported for Go files")
	}
	scoped, err := t.guard.ResolveSessionScoped(sessionBase, relPath(sessionBase, abs))
	if err != nil {
		return "", err
	}
	query, err := goIdentifierAtPosition(scoped, input.Line, input.Column)
	if err != nil {
		return "", err
	}
	return query, nil
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

func parseGoReferences(absPath, basePath, query string) ([]codeSymbolItem, error) {
	src, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, absPath, src, 0)
	if err != nil {
		return nil, err
	}
	target := referenceSymbolName(query)
	var references []codeSymbolItem
	ast.Inspect(file, func(node ast.Node) bool {
		ident, ok := node.(*ast.Ident)
		if !ok || ident.Name != target {
			return true
		}
		pos := fset.Position(ident.Pos())
		references = append(references, codeSymbolItem{
			Name:      ident.Name,
			Kind:      "reference",
			FilePath:  relPath(basePath, absPath),
			Line:      pos.Line,
			Column:    pos.Column,
			Signature: codeSymbolSourceLine(src, pos.Line),
		})
		return true
	})
	sortCodeSymbols(references)
	return references, nil
}

func goIdentifierAtPosition(absPath string, line, column int) (string, error) {
	src, err := os.ReadFile(absPath)
	if err != nil {
		return "", err
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, absPath, src, 0)
	if err != nil {
		return "", err
	}
	var found string
	ast.Inspect(file, func(node ast.Node) bool {
		if found != "" {
			return false
		}
		ident, ok := node.(*ast.Ident)
		if !ok {
			return true
		}
		pos := fset.Position(ident.Pos())
		if pos.Line != line {
			return true
		}
		start := pos.Column
		end := start + utf8.RuneCountInString(ident.Name)
		if column >= start && column < end {
			found = ident.Name
			return false
		}
		return true
	})
	if found == "" {
		return "", fmt.Errorf("no Go identifier at %s:%d:%d", relPath(filepath.Dir(absPath), absPath), line, column)
	}
	return found, nil
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

func referenceSymbolName(query string) string {
	query = strings.TrimSpace(query)
	if idx := strings.LastIndex(query, "."); idx >= 0 && idx < len(query)-1 {
		return query[idx+1:]
	}
	return query
}

func codeSymbolSourceLine(src []byte, line int) string {
	if line <= 0 {
		return ""
	}
	lines := bytes.Split(src, []byte{'\n'})
	if line > len(lines) {
		return ""
	}
	return strings.TrimSpace(string(lines[line-1]))
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
