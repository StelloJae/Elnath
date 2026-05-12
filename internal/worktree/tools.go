package worktree

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/stello/elnath/internal/tools"
)

const (
	EnterToolName = "enter_worktree"
	ListToolName  = "worktree_list"
	PruneToolName = "worktree_prune"
	ExitToolName  = "exit_worktree"
)

var worktreeNameSegmentRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

type Manager struct {
	baseDir string
	now     func() time.Time
}

func NewManager(baseDir string) *Manager {
	return &Manager{baseDir: baseDir, now: time.Now}
}

type registryFile struct {
	Worktrees []Record `json:"worktrees"`
}

type Record struct {
	Name           string `json:"name"`
	Slug           string `json:"slug"`
	Path           string `json:"path"`
	Branch         string `json:"branch"`
	OriginalBranch string `json:"original_branch,omitempty"`
	OriginalHead   string `json:"original_head"`
	CreatedAt      string `json:"created_at"`
}

type EnterTool struct {
	manager *Manager
}

func NewEnterTool(manager *Manager) *EnterTool {
	return &EnterTool{manager: manager}
}

func (t *EnterTool) Name() string { return EnterToolName }

func (t *EnterTool) Description() string {
	return "Create or reuse an Elnath-managed git worktree and record it in the local worktree registry"
}

func (t *EnterTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{
		"name": tools.String("Optional worktree name. Slash-separated segments are allowed; each segment may contain letters, digits, dot, underscore, or dash."),
	}, nil)
}

func (t *EnterTool) IsConcurrencySafe(json.RawMessage) bool { return false }

func (t *EnterTool) Reversible() bool { return false }

func (t *EnterTool) Scope(json.RawMessage) tools.ToolScope {
	return tools.ToolScope{Persistent: true}
}

func (t *EnterTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *EnterTool) DeferInitialToolSchema() bool { return true }

type EnterInput struct {
	Name string `json:"name"`
}

type EnterOutput struct {
	Name           string `json:"name"`
	Slug           string `json:"slug"`
	Path           string `json:"path"`
	Branch         string `json:"branch"`
	OriginalBranch string `json:"original_branch,omitempty"`
	OriginalHead   string `json:"original_head"`
	Existing       bool   `json:"existing"`
	RegistryPath   string `json:"registry_path"`
	EnteredSession bool   `json:"entered_session"`
}

func (t *EnterTool) Execute(ctx context.Context, params json.RawMessage) (*tools.Result, error) {
	var input EnterInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return tools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}
	if t == nil || t.manager == nil {
		return tools.ErrorResult("enter_worktree: manager unavailable"), nil
	}
	output, err := t.manager.Enter(ctx, input.Name)
	if err != nil {
		return tools.ErrorResult("enter_worktree: " + err.Error()), nil
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("enter_worktree: marshal output: %v", err)), nil
	}
	return tools.SuccessResult(string(raw)), nil
}

type ExitTool struct {
	manager *Manager
}

func NewExitTool(manager *Manager) *ExitTool {
	return &ExitTool{manager: manager}
}

func (t *ExitTool) Name() string { return ExitToolName }

func (t *ExitTool) Description() string {
	return "Keep or remove an Elnath-managed git worktree using registry-backed safety checks"
}

func (t *ExitTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{
		"name":            tools.String("Managed worktree name to exit. Required unless path is provided."),
		"path":            tools.String("Managed worktree path to exit. Required unless name is provided."),
		"action":          tools.StringEnum("Exit action. keep leaves the worktree on disk; remove deletes it after safety checks.", "keep", "remove"),
		"discard_changes": tools.Bool("Allow removing a dirty worktree and discarding uncommitted changes."),
	}, nil)
}

func (t *ExitTool) IsConcurrencySafe(json.RawMessage) bool { return false }

func (t *ExitTool) Reversible() bool { return false }

func (t *ExitTool) Scope(json.RawMessage) tools.ToolScope {
	return tools.ToolScope{Persistent: true}
}

func (t *ExitTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *ExitTool) DeferInitialToolSchema() bool { return true }

type ExitInput struct {
	Name           string `json:"name"`
	Path           string `json:"path"`
	Action         string `json:"action"`
	DiscardChanges bool   `json:"discard_changes"`
}

type ExitOutput struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	Branch       string `json:"branch"`
	Action       string `json:"action"`
	Removed      bool   `json:"removed"`
	DirtyFiles   int    `json:"dirty_files"`
	AheadCommits int    `json:"ahead_commits"`
	RegistryPath string `json:"registry_path"`
}

type ListTool struct {
	manager *Manager
}

func NewListTool(manager *Manager) *ListTool {
	return &ListTool{manager: manager}
}

func (t *ListTool) Name() string { return ListToolName }

func (t *ListTool) Description() string {
	return "List Elnath-managed git worktrees from the local worktree registry"
}

func (t *ListTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{}, nil)
}

func (t *ListTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *ListTool) Reversible() bool { return true }

func (t *ListTool) Scope(json.RawMessage) tools.ToolScope { return tools.ToolScope{} }

func (t *ListTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *ListTool) DeferInitialToolSchema() bool { return true }

type ListOutput struct {
	RegistryPath string       `json:"registry_path"`
	Total        int          `json:"total"`
	Worktrees    []ListRecord `json:"worktrees"`
}

type ListRecord struct {
	Name           string `json:"name"`
	Slug           string `json:"slug"`
	Path           string `json:"path"`
	Branch         string `json:"branch"`
	OriginalBranch string `json:"original_branch,omitempty"`
	OriginalHead   string `json:"original_head"`
	CreatedAt      string `json:"created_at"`
	Exists         bool   `json:"exists"`
	DirtyFiles     int    `json:"dirty_files"`
	AheadCommits   int    `json:"ahead_commits"`
	StatusError    string `json:"status_error,omitempty"`
}

func (t *ListTool) Execute(ctx context.Context, _ json.RawMessage) (*tools.Result, error) {
	if t == nil || t.manager == nil {
		return tools.ErrorResult("worktree_list: manager unavailable"), nil
	}
	output, err := t.manager.List(ctx)
	if err != nil {
		return tools.ErrorResult("worktree_list: " + err.Error()), nil
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("worktree_list: marshal output: %v", err)), nil
	}
	return tools.SuccessResult(string(raw)), nil
}

func (m *Manager) List(ctx context.Context) (ListOutput, error) {
	repoRoot, err := m.repoRoot(ctx)
	if err != nil {
		return ListOutput{}, err
	}
	registry, err := m.readRegistryForRoot(repoRoot)
	if err != nil {
		return ListOutput{}, err
	}
	worktrees := make([]ListRecord, 0, len(registry.Worktrees))
	for _, record := range registry.Worktrees {
		worktrees = append(worktrees, m.listRecordStatus(ctx, repoRoot, record))
	}
	return ListOutput{
		RegistryPath: filepath.Join(repoRoot, ".elnath", "worktrees", "registry.json"),
		Total:        len(worktrees),
		Worktrees:    worktrees,
	}, nil
}

func (m *Manager) listRecordStatus(ctx context.Context, repoRoot string, record Record) ListRecord {
	out := ListRecord{
		Name:           record.Name,
		Slug:           record.Slug,
		Path:           record.Path,
		Branch:         record.Branch,
		OriginalBranch: record.OriginalBranch,
		OriginalHead:   record.OriginalHead,
		CreatedAt:      record.CreatedAt,
	}
	if err := ensureManagedPath(repoRoot, record.Path); err != nil {
		out.StatusError = err.Error()
		return out
	}
	if _, err := os.Stat(record.Path); err != nil {
		if os.IsNotExist(err) {
			out.StatusError = "worktree path missing"
		} else {
			out.StatusError = err.Error()
		}
		return out
	}
	out.Exists = true
	dirty, err := m.dirtyFileCount(ctx, record.Path)
	if err != nil {
		out.StatusError = err.Error()
		return out
	}
	out.DirtyFiles = dirty
	ahead, err := m.aheadCount(ctx, record.Path, record.OriginalHead)
	if err != nil {
		out.StatusError = err.Error()
		return out
	}
	out.AheadCommits = ahead
	return out
}

type PruneTool struct {
	manager *Manager
}

func NewPruneTool(manager *Manager) *PruneTool {
	return &PruneTool{manager: manager}
}

func (t *PruneTool) Name() string { return PruneToolName }

func (t *PruneTool) Description() string {
	return "Dry-run or remove stale Elnath-managed worktree registry entries whose paths are missing"
}

func (t *PruneTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{
		"dry_run": tools.Bool("When true or omitted, report stale registry entries without modifying the registry. Set false to remove only missing managed-path entries."),
	}, nil)
}

func (t *PruneTool) IsConcurrencySafe(json.RawMessage) bool { return false }

func (t *PruneTool) Reversible() bool { return false }

func (t *PruneTool) Scope(json.RawMessage) tools.ToolScope {
	return tools.ToolScope{Persistent: true}
}

func (t *PruneTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *PruneTool) DeferInitialToolSchema() bool { return true }

type PruneInput struct {
	DryRun *bool `json:"dry_run"`
}

type PruneOutput struct {
	RegistryPath string        `json:"registry_path"`
	DryRun       bool          `json:"dry_run"`
	Total        int           `json:"total"`
	StaleCount   int           `json:"stale_count"`
	RemovedCount int           `json:"removed_count"`
	KeptCount    int           `json:"kept_count"`
	Entries      []PruneRecord `json:"entries"`
}

type PruneRecord struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Reason      string `json:"reason"`
	WouldRemove bool   `json:"would_remove"`
	Removed     bool   `json:"removed"`
}

func (t *PruneTool) Execute(ctx context.Context, params json.RawMessage) (*tools.Result, error) {
	var input PruneInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return tools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}
	if t == nil || t.manager == nil {
		return tools.ErrorResult("worktree_prune: manager unavailable"), nil
	}
	output, err := t.manager.Prune(ctx, input)
	if err != nil {
		return tools.ErrorResult("worktree_prune: " + err.Error()), nil
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("worktree_prune: marshal output: %v", err)), nil
	}
	return tools.SuccessResult(string(raw)), nil
}

func (m *Manager) Prune(ctx context.Context, input PruneInput) (PruneOutput, error) {
	repoRoot, err := m.repoRoot(ctx)
	if err != nil {
		return PruneOutput{}, err
	}
	registry, err := m.readRegistryForRoot(repoRoot)
	if err != nil {
		return PruneOutput{}, err
	}
	dryRun := true
	if input.DryRun != nil {
		dryRun = *input.DryRun
	}
	output := PruneOutput{
		RegistryPath: filepath.Join(repoRoot, ".elnath", "worktrees", "registry.json"),
		DryRun:       dryRun,
		Total:        len(registry.Worktrees),
	}
	kept := make([]Record, 0, len(registry.Worktrees))
	for _, record := range registry.Worktrees {
		reason, stale := staleRegistryReason(repoRoot, record.Path)
		if !stale {
			kept = append(kept, record)
			continue
		}
		output.StaleCount++
		entry := PruneRecord{
			Name:        record.Name,
			Path:        record.Path,
			Reason:      reason,
			WouldRemove: true,
			Removed:     !dryRun,
		}
		output.Entries = append(output.Entries, entry)
		if dryRun {
			kept = append(kept, record)
		} else {
			output.RemovedCount++
		}
	}
	output.KeptCount = len(kept)
	if !dryRun && output.RemovedCount > 0 {
		registry.Worktrees = kept
		if err := m.writeRegistryForRoot(repoRoot, registry); err != nil {
			return PruneOutput{}, err
		}
	}
	return output, nil
}

func (t *ExitTool) Execute(ctx context.Context, params json.RawMessage) (*tools.Result, error) {
	var input ExitInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return tools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}
	if t == nil || t.manager == nil {
		return tools.ErrorResult("exit_worktree: manager unavailable"), nil
	}
	output, err := t.manager.Exit(ctx, input)
	if err != nil {
		return tools.ErrorResult("exit_worktree: " + err.Error()), nil
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("exit_worktree: marshal output: %v", err)), nil
	}
	return tools.SuccessResult(string(raw)), nil
}

func (m *Manager) Enter(ctx context.Context, rawName string) (EnterOutput, error) {
	repoRoot, err := m.repoRoot(ctx)
	if err != nil {
		return EnterOutput{}, err
	}
	name := strings.TrimSpace(rawName)
	if name == "" {
		name = "session-" + m.now().UTC().Format("20060102-150405")
	}
	slug, err := normalizeName(name)
	if err != nil {
		return EnterOutput{}, err
	}
	base := filepath.Join(repoRoot, ".elnath", "worktrees")
	path := filepath.Join(base, slug)
	branch := "elnath-worktree-" + slug
	registryPath := filepath.Join(base, "registry.json")

	originalHead, err := m.git(ctx, repoRoot, "rev-parse", "HEAD")
	if err != nil {
		return EnterOutput{}, fmt.Errorf("read original HEAD: %w", err)
	}
	originalBranch, _ := m.git(ctx, repoRoot, "branch", "--show-current")

	registry, err := m.readRegistryForRoot(repoRoot)
	if err != nil {
		return EnterOutput{}, err
	}
	if record, ok := registry.findByNameOrPath(name, path); ok {
		return EnterOutput{
			Name:           record.Name,
			Slug:           record.Slug,
			Path:           record.Path,
			Branch:         record.Branch,
			OriginalBranch: record.OriginalBranch,
			OriginalHead:   record.OriginalHead,
			Existing:       true,
			RegistryPath:   registryPath,
			EnteredSession: false,
		}, nil
	}

	if err := os.MkdirAll(base, 0o755); err != nil {
		return EnterOutput{}, fmt.Errorf("create worktree base: %w", err)
	}
	if _, err := os.Stat(path); err == nil {
		return EnterOutput{}, fmt.Errorf("worktree path already exists but is not registered: %s", path)
	} else if !os.IsNotExist(err) {
		return EnterOutput{}, fmt.Errorf("stat worktree path: %w", err)
	}
	if _, err := m.git(ctx, repoRoot, "worktree", "add", "-b", branch, path, "HEAD"); err != nil {
		return EnterOutput{}, err
	}

	record := Record{
		Name:           name,
		Slug:           slug,
		Path:           path,
		Branch:         branch,
		OriginalBranch: strings.TrimSpace(originalBranch),
		OriginalHead:   strings.TrimSpace(originalHead),
		CreatedAt:      m.now().UTC().Format(time.RFC3339),
	}
	registry.Worktrees = append(registry.Worktrees, record)
	if err := m.writeRegistryForRoot(repoRoot, registry); err != nil {
		return EnterOutput{}, err
	}

	return EnterOutput{
		Name:           record.Name,
		Slug:           record.Slug,
		Path:           record.Path,
		Branch:         record.Branch,
		OriginalBranch: record.OriginalBranch,
		OriginalHead:   record.OriginalHead,
		Existing:       false,
		RegistryPath:   registryPath,
		EnteredSession: false,
	}, nil
}

func (m *Manager) Exit(ctx context.Context, input ExitInput) (ExitOutput, error) {
	repoRoot, err := m.repoRoot(ctx)
	if err != nil {
		return ExitOutput{}, err
	}
	action := strings.TrimSpace(input.Action)
	if action == "" {
		action = "keep"
	}
	if action != "keep" && action != "remove" {
		return ExitOutput{}, fmt.Errorf("action must be keep or remove")
	}
	registry, err := m.readRegistryForRoot(repoRoot)
	if err != nil {
		return ExitOutput{}, err
	}
	record, ok := registry.findByNameOrPath(strings.TrimSpace(input.Name), strings.TrimSpace(input.Path))
	if !ok {
		return ExitOutput{}, fmt.Errorf("managed worktree not found")
	}
	if err := ensureManagedPath(repoRoot, record.Path); err != nil {
		return ExitOutput{}, err
	}
	dirty, err := m.dirtyFileCount(ctx, record.Path)
	if err != nil {
		return ExitOutput{}, err
	}
	ahead, err := m.aheadCount(ctx, record.Path, record.OriginalHead)
	if err != nil {
		return ExitOutput{}, err
	}
	output := ExitOutput{
		Name:         record.Name,
		Path:         record.Path,
		Branch:       record.Branch,
		Action:       action,
		Removed:      false,
		DirtyFiles:   dirty,
		AheadCommits: ahead,
		RegistryPath: filepath.Join(repoRoot, ".elnath", "worktrees", "registry.json"),
	}
	if action == "keep" {
		return output, nil
	}
	if dirty > 0 && !input.DiscardChanges {
		return ExitOutput{}, fmt.Errorf("worktree has uncommitted changes; set discard_changes=true to remove")
	}
	if ahead > 0 && !input.DiscardChanges {
		return ExitOutput{}, fmt.Errorf("worktree has commits not reachable from original head; set discard_changes=true to remove")
	}
	args := []string{"worktree", "remove"}
	if input.DiscardChanges {
		args = append(args, "--force")
	}
	args = append(args, record.Path)
	if _, err := m.git(ctx, repoRoot, args...); err != nil {
		return ExitOutput{}, err
	}
	registry.remove(record)
	if err := m.writeRegistryForRoot(repoRoot, registry); err != nil {
		return ExitOutput{}, err
	}
	output.Removed = true
	return output, nil
}

func (m *Manager) readRegistry(ctx context.Context) (registryFile, error) {
	repoRoot, err := m.repoRoot(ctx)
	if err != nil {
		return registryFile{}, err
	}
	return m.readRegistryForRoot(repoRoot)
}

func (m *Manager) repoRoot(ctx context.Context) (string, error) {
	if m == nil {
		return "", fmt.Errorf("manager unavailable")
	}
	base := strings.TrimSpace(m.baseDir)
	if base == "" {
		var err error
		base, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	root, err := m.git(ctx, base, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not inside a git repository: %w", err)
	}
	return strings.TrimSpace(root), nil
}

func (m *Manager) readRegistryForRoot(repoRoot string) (registryFile, error) {
	path := filepath.Join(repoRoot, ".elnath", "worktrees", "registry.json")
	var registry registryFile
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return registry, nil
		}
		return registry, fmt.Errorf("read registry: %w", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return registry, nil
	}
	if err := json.Unmarshal(data, &registry); err != nil {
		return registry, fmt.Errorf("parse registry: %w", err)
	}
	return registry, nil
}

func (m *Manager) writeRegistryForRoot(repoRoot string, registry registryFile) error {
	path := filepath.Join(repoRoot, ".elnath", "worktrees", "registry.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create registry dir: %w", err)
	}
	raw, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal registry: %w", err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("write registry: %w", err)
	}
	return nil
}

func (m *Manager) dirtyFileCount(ctx context.Context, path string) (int, error) {
	out, err := m.git(ctx, path, "status", "--porcelain")
	if err != nil {
		return 0, err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return 0, nil
	}
	return len(strings.Split(out, "\n")), nil
}

func (m *Manager) aheadCount(ctx context.Context, path string, originalHead string) (int, error) {
	originalHead = strings.TrimSpace(originalHead)
	if originalHead == "" {
		return 0, nil
	}
	out, err := m.git(ctx, path, "rev-list", "--count", originalHead+"..HEAD")
	if err != nil {
		return 0, err
	}
	var count int
	if _, err := fmt.Sscanf(strings.TrimSpace(out), "%d", &count); err != nil {
		return 0, fmt.Errorf("parse ahead count: %w", err)
	}
	return count, nil
}

func (m *Manager) git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func normalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("invalid name: empty")
	}
	if len(name) > 64 {
		return "", fmt.Errorf("invalid name: max length is 64")
	}
	segments := strings.Split(name, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." || !worktreeNameSegmentRE.MatchString(segment) {
			return "", fmt.Errorf("invalid name: use slash-separated [A-Za-z0-9._-] segments")
		}
	}
	return strings.ReplaceAll(name, "/", "+"), nil
}

func ensureManagedPath(repoRoot string, path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	base, err := filepath.Abs(filepath.Join(repoRoot, ".elnath", "worktrees"))
	if err != nil {
		return err
	}
	if absPath == base || !strings.HasPrefix(absPath, base+string(os.PathSeparator)) {
		return fmt.Errorf("path is outside managed worktree root")
	}
	return nil
}

func staleRegistryReason(repoRoot string, path string) (string, bool) {
	if err := ensureManagedPath(repoRoot, path); err != nil {
		return err.Error(), false
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return "worktree path missing", true
		}
		return err.Error(), false
	}
	return "", false
}

func (r registryFile) findByNameOrPath(name string, path string) (Record, bool) {
	for _, record := range r.Worktrees {
		if name != "" && record.Name == name {
			return record, true
		}
		if path != "" && samePath(record.Path, path) {
			return record, true
		}
	}
	return Record{}, false
}

func (r *registryFile) remove(target Record) {
	if r == nil {
		return
	}
	out := r.Worktrees[:0]
	for _, record := range r.Worktrees {
		if record.Name == target.Name && samePath(record.Path, target.Path) {
			continue
		}
		out = append(out, record)
	}
	r.Worktrees = out
}

func samePath(a string, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA == nil && errB == nil {
		return aa == bb
	}
	return a == b
}
