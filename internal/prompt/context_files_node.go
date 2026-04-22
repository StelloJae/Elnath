package prompt

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	maxContextFileBytes  = 8 * 1024
	maxContextTotalBytes = 24 * 1024
	maxContextFileLevels = 10
)

type ContextFilesNode struct {
	priority int
}

type contextFileGroup struct {
	label string
	names []string
}

type contextFileBlock struct {
	label string
	body  string
}

var contextFileGroups = []contextFileGroup{
	{label: ".elnath/project.yaml", names: []string{filepath.Join(".elnath", "project.yaml")}},
	{label: "CLAUDE.md", names: []string{"CLAUDE.md", "claude.md"}},
	{label: "AGENTS.md", names: []string{"AGENTS.md", "agents.md"}},
}

func NewContextFilesNode(priority int) *ContextFilesNode {
	return &ContextFilesNode{priority: priority}
}

func (n *ContextFilesNode) Name() string {
	return "context_files"
}

// CacheBoundary classifies context files as volatile: the files list
// expands as the session touches new paths.
func (n *ContextFilesNode) CacheBoundary() CacheBoundary { return CacheBoundaryVolatile }

func (n *ContextFilesNode) Priority() int {
	if n == nil {
		return 0
	}
	return n.priority
}

func (n *ContextFilesNode) Render(_ context.Context, state *RenderState) (string, error) {
	if n == nil || state == nil || state.BenchmarkMode {
		return "", nil
	}

	workDir := strings.TrimSpace(state.WorkDir)
	if workDir == "" {
		return "", nil
	}

	blocks := collectContextFileBlocks(contextFileSearchDirs(workDir))
	if len(blocks) == 0 {
		return "", nil
	}

	return renderContextFileBlocks(blocks), nil
}

func contextFileSearchDirs(workDir string) []string {
	dir := cleanWorkDir(workDir)
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}

	dirs := make([]string, 0, maxContextFileLevels)
	for i := 0; i < maxContextFileLevels; i++ {
		dirs = append(dirs, dir)
		if isGitRootDir(dir) {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return dirs
}

func isGitRootDir(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".git"))
	if err != nil {
		return false
	}
	return info.IsDir() || info.Mode().IsRegular()
}

func collectContextFileBlocks(dirs []string) []contextFileBlock {
	blocks := make([]contextFileBlock, 0, len(contextFileGroups))
	for _, group := range contextFileGroups {
		if block, ok := findContextFileBlock(dirs, group); ok {
			blocks = append(blocks, block)
		}
	}
	return blocks
}

func findContextFileBlock(dirs []string, group contextFileGroup) (contextFileBlock, bool) {
	for _, dir := range dirs {
		for _, name := range group.names {
			path := filepath.Join(dir, name)
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			if info.IsDir() {
				continue
			}

			body, ok := readContextFileBlock(path, group.label)
			if !ok {
				return contextFileBlock{}, false
			}
			return contextFileBlock{label: group.label, body: body}, true
		}
	}
	return contextFileBlock{}, false
}

func readContextFileBlock(path, label string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		slog.Debug("context_files: read failed", "path", path, "error", err)
		return "", false
	}
	content := truncateContextFile(string(data), maxContextFileBytes)
	cleaned, _ := ScanContent(content, label)
	return cleaned, true
}

func truncateContextFile(content string, maxBytes int) string {
	if maxBytes <= 0 || len(content) <= maxBytes {
		return content
	}

	truncated := []byte(content[:maxBytes])
	for len(truncated) > 0 && !utf8.Valid(truncated) {
		truncated = truncated[:len(truncated)-1]
	}
	return string(truncated) + "\n[truncated]"
}

func renderContextFileBlocks(blocks []contextFileBlock) string {
	for len(blocks) > 0 {
		rendered := formatContextFileBlocks(blocks)
		if len(rendered) <= maxContextTotalBytes {
			return rendered
		}
		blocks = blocks[:len(blocks)-1]
	}
	return ""
}

func formatContextFileBlocks(blocks []contextFileBlock) string {
	if len(blocks) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("<<context_files>>\n")
	for i, block := range blocks {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "--- %s ---\n", block.label)
		b.WriteString(block.body)
	}
	b.WriteString("\n<</context_files>>")
	return b.String()
}
