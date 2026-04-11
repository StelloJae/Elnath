package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const grepMaxMatches = 100

// ---------------------------------------------------------------------------
// ReadTool
// ---------------------------------------------------------------------------

// ReadTool reads file contents, optionally with line offset and limit.
type ReadTool struct{ guard *PathGuard }

func NewReadTool(guard *PathGuard) *ReadTool { return &ReadTool{guard: guard} }

func (t *ReadTool) Name() string { return "read_file" }
func (t *ReadTool) Description() string {
	return "Read the contents of a file with optional line range."
}

func (t *ReadTool) Schema() json.RawMessage {
	return Object(map[string]Property{
		"file_path": String("Path to the file (absolute, relative, or ~/)."),
		"offset":    Int("Starting line number (1-based, optional)."),
		"limit":     Int("Maximum number of lines to return (optional)."),
	}, []string{"file_path"})
}

func (t *ReadTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *ReadTool) Reversible() bool { return true }

func (t *ReadTool) Scope(params json.RawMessage) ToolScope {
	var p readParams
	if err := json.Unmarshal(params, &p); err != nil {
		return ConservativeScope()
	}
	return resolvedReadScope(t.guard, p.FilePath)
}

type readParams struct {
	FilePath string `json:"file_path"`
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
}

func (t *ReadTool) Execute(_ context.Context, params json.RawMessage) (*Result, error) {
	var p readParams
	if err := json.Unmarshal(params, &p); err != nil {
		return ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
	}
	abs, err := t.guard.Resolve(p.FilePath)
	if err != nil {
		return ErrorResult(err.Error()), nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return ErrorResult(fmt.Sprintf("read_file: %v", err)), nil
	}

	// Binary file detection: look for a null byte in the first 8 KiB.
	check := data
	if len(check) > 8192 {
		check = check[:8192]
	}
	if bytes.IndexByte(check, 0) >= 0 {
		return ErrorResult(fmt.Sprintf("read_file: %s appears to be a binary file", p.FilePath)), nil
	}

	lines := strings.Split(string(data), "\n")

	// Apply offset (1-based).
	start := 0
	if p.Offset > 0 {
		start = p.Offset - 1
	}
	if start >= len(lines) {
		return SuccessResult(""), nil
	}
	lines = lines[start:]

	// Apply limit.
	if p.Limit > 0 && len(lines) > p.Limit {
		lines = lines[:p.Limit]
	}

	// Format as cat -n (line numbers).
	var sb strings.Builder
	for i, line := range lines {
		fmt.Fprintf(&sb, "%6d\t%s\n", start+i+1, line)
	}
	return SuccessResult(truncateOutput(sb.String(), toolMaxOutputBytes)), nil
}

// ---------------------------------------------------------------------------
// WriteTool
// ---------------------------------------------------------------------------

// WriteTool creates or overwrites a file atomically.
type WriteTool struct{ guard *PathGuard }

func NewWriteTool(guard *PathGuard) *WriteTool { return &WriteTool{guard: guard} }

func (t *WriteTool) Name() string        { return "write_file" }
func (t *WriteTool) Description() string { return "Create or overwrite a file with the given content." }

func (t *WriteTool) Schema() json.RawMessage {
	return Object(map[string]Property{
		"file_path": String("Path to the file (absolute, relative, or ~/)."),
		"content":   String("Content to write."),
	}, []string{"file_path", "content"})
}

func (t *WriteTool) IsConcurrencySafe(json.RawMessage) bool { return false }

func (t *WriteTool) Reversible() bool { return false }

func (t *WriteTool) Scope(params json.RawMessage) ToolScope {
	var p writeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return ConservativeScope()
	}
	return resolvedWriteScope(t.guard, p.FilePath)
}

type writeParams struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

func (t *WriteTool) Execute(_ context.Context, params json.RawMessage) (*Result, error) {
	var p writeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
	}
	if err := t.guard.CheckScope(t.Scope(params)); err != nil {
		return ErrorResult(err.Error()), nil
	}
	abs, err := t.guard.Resolve(p.FilePath)
	if err != nil {
		return ErrorResult(err.Error()), nil
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return ErrorResult(fmt.Sprintf("write_file mkdir: %v", err)), nil
	}

	// Atomic write: write to a temp file in the same directory, then rename.
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".write_tmp_*")
	if err != nil {
		return ErrorResult(fmt.Sprintf("write_file temp: %v", err)), nil
	}
	tmpName := tmp.Name()
	_, writeErr := tmp.WriteString(p.Content)
	closeErr := tmp.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(tmpName)
		return ErrorResult(fmt.Sprintf("write_file: %v", firstErr(writeErr, closeErr))), nil
	}
	if err := os.Rename(tmpName, abs); err != nil {
		_ = os.Remove(tmpName)
		return ErrorResult(fmt.Sprintf("write_file rename: %v", err)), nil
	}
	return SuccessResult(fmt.Sprintf("wrote %s", p.FilePath)), nil
}

func firstErr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// EditTool
// ---------------------------------------------------------------------------

// EditTool performs an exact string replacement in a file.
type EditTool struct{ guard *PathGuard }

func NewEditTool(guard *PathGuard) *EditTool { return &EditTool{guard: guard} }

func (t *EditTool) Name() string        { return "edit_file" }
func (t *EditTool) Description() string { return "Replace an exact string in a file with new content." }

func (t *EditTool) Schema() json.RawMessage {
	return Object(map[string]Property{
		"file_path":   String("Path to the file (absolute, relative, or ~/)."),
		"old_string":  String("Exact string to find and replace."),
		"new_string":  String("Replacement string."),
		"replace_all": Bool("Replace all occurrences (default: false, requires unique match)."),
	}, []string{"file_path", "old_string", "new_string"})
}

func (t *EditTool) IsConcurrencySafe(json.RawMessage) bool { return false }

func (t *EditTool) Reversible() bool { return false }

func (t *EditTool) Scope(params json.RawMessage) ToolScope {
	var p editParams
	if err := json.Unmarshal(params, &p); err != nil {
		return ConservativeScope()
	}
	return resolvedWriteScope(t.guard, p.FilePath)
}

type editParams struct {
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

func (t *EditTool) Execute(_ context.Context, params json.RawMessage) (*Result, error) {
	var p editParams
	if err := json.Unmarshal(params, &p); err != nil {
		return ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
	}
	if err := t.guard.CheckScope(t.Scope(params)); err != nil {
		return ErrorResult(err.Error()), nil
	}
	abs, err := t.guard.Resolve(p.FilePath)
	if err != nil {
		return ErrorResult(err.Error()), nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return ErrorResult(fmt.Sprintf("edit_file read: %v", err)), nil
	}
	original := string(data)

	count := strings.Count(original, p.OldString)
	if count == 0 {
		return ErrorResult(fmt.Sprintf("edit_file: old_string not found in %s", p.FilePath)), nil
	}
	if !p.ReplaceAll && count > 1 {
		return ErrorResult(fmt.Sprintf(
			"edit_file: old_string found %d times in %s (use replace_all=true or make it unique)",
			count, p.FilePath,
		)), nil
	}

	n := 1
	if p.ReplaceAll {
		n = -1
	}
	updated := strings.Replace(original, p.OldString, p.NewString, n)

	if err := os.WriteFile(abs, []byte(updated), 0o644); err != nil {
		return ErrorResult(fmt.Sprintf("edit_file write: %v", err)), nil
	}
	return SuccessResult(fmt.Sprintf("edited %s", p.FilePath)), nil
}

// ---------------------------------------------------------------------------
// GlobTool
// ---------------------------------------------------------------------------

// GlobTool lists files matching a glob pattern, sorted by modification time.
type GlobTool struct{ guard *PathGuard }

func NewGlobTool(guard *PathGuard) *GlobTool { return &GlobTool{guard: guard} }

func (t *GlobTool) Name() string        { return "glob" }
func (t *GlobTool) Description() string { return "List files matching a glob pattern." }

func (t *GlobTool) Schema() json.RawMessage {
	return Object(map[string]Property{
		"pattern": String("Glob pattern (supports ** for recursive matching)."),
		"path":    String("Base path to search (default: working directory)."),
	}, []string{"pattern"})
}

func (t *GlobTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *GlobTool) Reversible() bool { return true }

func (t *GlobTool) Scope(params json.RawMessage) ToolScope {
	var p globParams
	if err := json.Unmarshal(params, &p); err != nil {
		return ConservativeScope()
	}
	return resolvedBaseReadScope(t.guard, p.Path)
}

type globParams struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
}

type fileEntry struct {
	path    string
	modTime int64
}

func (t *GlobTool) Execute(_ context.Context, params json.RawMessage) (*Result, error) {
	var p globParams
	if err := json.Unmarshal(params, &p); err != nil {
		return ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
	}

	searchBase := t.guard.WorkDir()
	if p.Path != "" {
		abs, err := t.guard.Resolve(p.Path)
		if err != nil {
			return ErrorResult(err.Error()), nil
		}
		searchBase = abs
	}

	var entries []fileEntry
	if strings.Contains(p.Pattern, "**") {
		entries = recursiveGlob(searchBase, t.guard.WorkDir(), p.Pattern)
	} else {
		absPattern := filepath.Join(searchBase, p.Pattern)
		found, err := filepath.Glob(absPattern)
		if err != nil {
			return ErrorResult(fmt.Sprintf("glob: %v", err)), nil
		}
		for _, f := range found {
			info, err := os.Lstat(f)
			if err != nil || info.IsDir() {
				continue
			}
			rel, _ := filepath.Rel(t.guard.WorkDir(), f)
			entries = append(entries, fileEntry{path: rel, modTime: info.ModTime().UnixNano()})
		}
	}

	if len(entries) == 0 {
		return SuccessResult("(no matches)"), nil
	}

	// Sort by modification time descending (most recently modified first).
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].modTime > entries[j].modTime
	})

	paths := make([]string, len(entries))
	for i, e := range entries {
		paths[i] = e.path
	}
	return SuccessResult(strings.Join(paths, "\n")), nil
}

// recursiveGlob walks searchBase and matches each file's relative path against pattern.
func recursiveGlob(searchBase, baseDir, pattern string) []fileEntry {
	parts := strings.SplitN(pattern, "**", 2)
	prefix := filepath.Clean(parts[0])
	suffix := ""
	if len(parts) == 2 {
		suffix = strings.TrimPrefix(parts[1], string(filepath.Separator))
	}

	var results []fileEntry
	walkRoot := filepath.Join(searchBase, prefix)
	_ = filepath.WalkDir(walkRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(baseDir, path)
		if err != nil {
			return nil
		}
		if suffix != "" {
			if ok, _ := filepath.Match(suffix, filepath.Base(path)); !ok {
				return nil
			}
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		results = append(results, fileEntry{path: rel, modTime: info.ModTime().UnixNano()})
		return nil
	})
	return results
}

// ---------------------------------------------------------------------------
// GrepTool
// ---------------------------------------------------------------------------

// GrepTool searches for a regex pattern across files in a directory.
type GrepTool struct{ guard *PathGuard }

func NewGrepTool(guard *PathGuard) *GrepTool { return &GrepTool{guard: guard} }

func (t *GrepTool) Name() string        { return "grep" }
func (t *GrepTool) Description() string { return "Search for a regex pattern in files." }

func (t *GrepTool) Schema() json.RawMessage {
	return Object(map[string]Property{
		"pattern": String("Regular expression to search for."),
		"path":    String("Base path to search (default: working directory)."),
		"include": String("Optional glob filter for file names (e.g. '*.go')."),
	}, []string{"pattern"})
}

func (t *GrepTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *GrepTool) Reversible() bool { return true }

func (t *GrepTool) Scope(params json.RawMessage) ToolScope {
	var p grepParams
	if err := json.Unmarshal(params, &p); err != nil {
		return ConservativeScope()
	}
	return resolvedBaseReadScope(t.guard, p.Path)
}

type grepParams struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
	Include string `json:"include"`
}

func resolvedReadScope(guard *PathGuard, rawPath string) ToolScope {
	abs, err := guard.Resolve(rawPath)
	if err != nil {
		return ConservativeScope()
	}
	return ToolScope{ReadPaths: []string{abs}}
}

func resolvedWriteScope(guard *PathGuard, rawPath string) ToolScope {
	abs, err := guard.Resolve(rawPath)
	if err != nil {
		return ConservativeScope()
	}
	return ToolScope{WritePaths: []string{abs}, Persistent: true}
}

func resolvedBaseReadScope(guard *PathGuard, rawPath string) ToolScope {
	if rawPath == "" {
		return ToolScope{ReadPaths: []string{guard.WorkDir()}}
	}
	return resolvedReadScope(guard, rawPath)
}

func (t *GrepTool) Execute(_ context.Context, params json.RawMessage) (*Result, error) {
	var p grepParams
	if err := json.Unmarshal(params, &p); err != nil {
		return ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
	}

	re, err := regexp.Compile(p.Pattern)
	if err != nil {
		return ErrorResult(fmt.Sprintf("grep: invalid pattern: %v", err)), nil
	}

	searchRoot := t.guard.WorkDir()
	if p.Path != "" {
		abs, err := t.guard.Resolve(p.Path)
		if err != nil {
			return ErrorResult(err.Error()), nil
		}
		searchRoot = abs
	}

	var sb strings.Builder
	matchCount := 0

	_ = filepath.WalkDir(searchRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if p.Include != "" {
			if ok, _ := filepath.Match(p.Include, filepath.Base(path)); !ok {
				return nil
			}
		}
		if matchCount >= grepMaxMatches {
			return fs.SkipAll
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		rel, _ := filepath.Rel(t.guard.WorkDir(), path)
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if matchCount >= grepMaxMatches {
				break
			}
			if re.MatchString(line) {
				fmt.Fprintf(&sb, "%s:%d: %s\n", rel, i+1, line)
				matchCount++
			}
		}
		return nil
	})

	if matchCount == 0 {
		return SuccessResult("(no matches)"), nil
	}
	if matchCount >= grepMaxMatches {
		sb.WriteString(fmt.Sprintf("... (output truncated at %d matches)\n", grepMaxMatches))
	}
	return SuccessResult(sb.String()), nil
}
