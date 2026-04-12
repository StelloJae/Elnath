package prompt

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type ProjectContextNode struct {
	priority int
}

func NewProjectContextNode(priority int) *ProjectContextNode {
	return &ProjectContextNode{priority: priority}
}

func (n *ProjectContextNode) Name() string {
	return "project_context"
}

func (n *ProjectContextNode) Priority() int {
	if n == nil {
		return 0
	}
	return n.priority
}

func (n *ProjectContextNode) Render(ctx context.Context, state *RenderState) (string, error) {
	if n == nil || state == nil || !state.ExistingCode {
		return "", nil
	}
	root := strings.TrimSpace(state.WorkDir)
	if root == "" {
		return "", nil
	}

	branch := gitBranch(ctx, root)
	remote := gitRemote(ctx, root)
	hints := likelyRepoFiles(root, state.UserInput, 8)
	if branch == "" && remote == "" && len(hints) == 0 {
		return "", nil
	}

	var b strings.Builder
	b.WriteString("Project context:\n")
	if branch != "" {
		b.WriteString("- Git branch: ")
		b.WriteString(branch)
		b.WriteString("\n")
	}
	if remote != "" {
		b.WriteString("- Git remote: ")
		b.WriteString(remote)
		b.WriteString("\n")
	}
	if len(hints) > 0 {
		b.WriteString("- Likely relevant files:\n")
		for _, hint := range hints {
			b.WriteString("  - ")
			b.WriteString(hint)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String()), nil
}

func gitBranch(ctx context.Context, root string) string {
	for _, args := range [][]string{{"symbolic-ref", "--short", "HEAD"}, {"rev-parse", "--abbrev-ref", "HEAD"}} {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = cleanWorkDir(root)
		out, err := cmd.Output()
		if err != nil {
			continue
		}
		branch := strings.TrimSpace(string(out))
		if branch != "" && branch != "HEAD" {
			return branch
		}
	}
	return ""
}

func gitRemote(ctx context.Context, root string) string {
	cmd := exec.CommandContext(ctx, "git", "config", "--get", "remote.origin.url")
	cmd.Dir = cleanWorkDir(root)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return sanitizeGitRemote(strings.TrimSpace(string(out)))
}

func sanitizeGitRemote(remote string) string {
	parsed, err := url.Parse(remote)
	if err != nil || parsed.Scheme == "" {
		return remote
	}
	parsed.User = nil
	return parsed.String()
}

func cleanWorkDir(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "."
	}
	return filepath.Clean(path)
}

func likelyRepoFiles(root, prompt string, limit int) []string {
	if root == "" || limit <= 0 {
		return nil
	}
	keywords := keywordHints(prompt)
	if len(keywords) == 0 {
		return nil
	}

	type candidate struct {
		path  string
		score int
	}
	var candidates []candidate
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "vendor" || name == "dist" || name == ".github" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		lower := strings.ToLower(rel)
		score := 0
		for _, kw := range keywords {
			if strings.Contains(lower, kw) {
				score += 2
			}
		}
		if strings.HasPrefix(lower, "test/") || strings.HasPrefix(lower, "examples/") {
			score -= 2
		}
		if strings.Contains(lower, "/fixtures/") {
			score -= 2
		}
		if strings.Contains(lower, "/runtime/") || strings.Contains(lower, "/worker") || strings.Contains(lower, "/workers/") {
			score += 2
		}
		if strings.HasSuffix(lower, ".go") || strings.HasSuffix(lower, ".ts") || strings.HasSuffix(lower, ".tsx") || strings.HasSuffix(lower, ".js") {
			score++
		}
		if score < 2 {
			score += scoreFileContents(path, keywords)
		}
		if score > 0 {
			candidates = append(candidates, candidate{path: rel, score: score})
		}
		return nil
	})

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].path < candidates[j].path
		}
		return candidates[i].score > candidates[j].score
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.path)
	}
	return out
}

func scoreFileContents(path string, keywords []string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	if len(data) > 8192 {
		data = data[:8192]
	}
	lower := strings.ToLower(string(data))
	score := 0
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			score++
		}
	}
	return score
}

func keywordHints(prompt string) []string {
	stop := map[string]struct{}{
		"the": {}, "and": {}, "with": {}, "into": {}, "without": {}, "existing": {}, "repository": {},
		"codebase": {}, "task": {}, "this": {}, "that": {}, "must": {}, "should": {}, "make": {},
		"smallest": {}, "correct": {}, "change": {}, "verify": {}, "verification": {}, "tests": {},
		"test": {}, "feature": {}, "brownfield": {}, "track": {}, "language": {}, "repo": {},
		"extend": {}, "current": {}, "behavior": {}, "regressing": {}, "emit": {}, "flow": {},
		"service": {},
	}
	fields := strings.FieldsFunc(strings.ToLower(prompt), func(r rune) bool {
		return !(r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	})
	seen := map[string]struct{}{}
	var out []string
	for _, field := range fields {
		if len(field) < 4 {
			continue
		}
		if _, ok := stop[field]; ok {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		out = append(out, field)
	}
	return out
}
