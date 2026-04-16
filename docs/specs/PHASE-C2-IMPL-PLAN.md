# Phase C-2: Skill Emergence MVP — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wiki-native skill CRUD + LLM 실시간 힌트(Layer 1) + prevalence 기반 자동 승격(Layer 3)을 구현하여 Elnath의 skill emergence MVP를 완성한다.

**Architecture:** W1(foundation)이 기존 파일 수정 + 핵심 타입/인터페이스를 정의하고, W2(Layer 1)와 W4(Layer 3)가 신규 파일만 생성하여 병렬 진행한다. Wiki가 skill의 single source of truth이고, JSONL tracker가 usage/pattern 데이터를 기록한다.

**Tech Stack:** Go 1.25+, SQLite (wiki FTS5), JSONL append-only, `internal/wiki.Store` CRUD API, `internal/tools.Tool` 7-method interface

**Worker 의존관계:** W1 먼저 완료 → W2, W4 병렬 시작

---

## Worker 1: Foundation (Skill CRUD + Creator + Tracker + Integration)

### Task 1: Extend Skill struct with Status and Source

**Files:**
- Modify: `internal/skill/skill.go:11-18` (Skill struct)
- Modify: `internal/skill/skill_test.go`

- [ ] **Step 1: Write failing test for Status/Source parsing**

```go
// internal/skill/skill_test.go — 기존 TestFromPage 테이블에 추가
{
    name: "page with status and source",
    page: &wiki.Page{
        Tags: []string{"skill"},
        Extra: map[string]any{
            "name":        "deploy-check",
            "description": "Check deployment status",
            "status":      "draft",
            "source":      "analyst",
        },
        Content: "Check deployment.",
    },
    wantName:   "deploy-check",
    wantStatus: "draft",
    wantSource: "analyst",
},
{
    name: "page without status defaults to active",
    page: &wiki.Page{
        Tags: []string{"skill"},
        Extra: map[string]any{
            "name": "simple",
        },
        Content: "Do something.",
    },
    wantName:   "simple",
    wantStatus: "active",
    wantSource: "",
},
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -race ./internal/skill/ -run TestFromPage -v
```
Expected: FAIL — `Skill` struct has no `Status`/`Source` fields.

- [ ] **Step 3: Add Status and Source fields to Skill struct**

```go
// internal/skill/skill.go
type Skill struct {
	Name          string
	Description   string
	Trigger       string
	RequiredTools []string
	Model         string
	Prompt        string
	Status        string // "active" (default) | "draft"
	Source        string // "user" | "hint" | "analyst" | "promoted"
}
```

Update `FromPage()` to parse these fields:

```go
func FromPage(page *wiki.Page) *Skill {
	if page == nil || !hasTag(page.Tags, "skill") {
		return nil
	}

	name, ok := stringExtra(page.Extra, "name")
	if !ok || name == "" {
		return nil
	}

	status := extraString(page.Extra, "status")
	if status == "" {
		status = "active"
	}

	return &Skill{
		Name:          name,
		Description:   extraString(page.Extra, "description"),
		Trigger:       extraString(page.Extra, "trigger"),
		RequiredTools: extraStrings(page.Extra, "required_tools"),
		Model:         extraString(page.Extra, "model"),
		Prompt:        page.Content,
		Status:        status,
		Source:        extraString(page.Extra, "source"),
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test -race ./internal/skill/ -run TestFromPage -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/skill/skill.go internal/skill/skill_test.go
git commit -m "feat(skill): add Status and Source fields to Skill struct"
```

---

### Task 2: Add draft filter to Registry.Load()

**Files:**
- Modify: `internal/skill/registry.go:34-56` (Load method)
- Modify: `internal/skill/registry_test.go`

- [ ] **Step 1: Write failing test — draft skills excluded from Load**

```go
// internal/skill/registry_test.go — 새 테스트 함수
func TestRegistryLoadSkipsDraft(t *testing.T) {
	dir := t.TempDir()
	store, err := wiki.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	activePage := &wiki.Page{
		Path: "skills/active-skill.md",
		Tags: []string{"skill"},
		Extra: map[string]any{
			"name":        "active-skill",
			"description": "An active skill",
			"status":      "active",
		},
		Content: "Do active things.",
	}
	draftPage := &wiki.Page{
		Path: "skills/draft-skill.md",
		Tags: []string{"skill"},
		Extra: map[string]any{
			"name":        "draft-skill",
			"description": "A draft skill",
			"status":      "draft",
		},
		Content: "Do draft things.",
	}

	if err := store.Create(activePage); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(draftPage); err != nil {
		t.Fatal(err)
	}

	reg := NewRegistry()
	if err := reg.Load(store); err != nil {
		t.Fatal(err)
	}

	if _, ok := reg.Get("active-skill"); !ok {
		t.Error("active skill should be loaded")
	}
	if _, ok := reg.Get("draft-skill"); ok {
		t.Error("draft skill should NOT be loaded")
	}
	if len(reg.List()) != 1 {
		t.Errorf("expected 1 skill, got %d", len(reg.List()))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -race ./internal/skill/ -run TestRegistryLoadSkipsDraft -v
```
Expected: FAIL — draft skill is loaded because no filter exists.

- [ ] **Step 3: Add draft filter to Load()**

```go
// internal/skill/registry.go — Load() 내부, skill := FromPage(page) 이후
func (r *Registry) Load(store *wiki.Store) error {
	pages, err := store.List()
	if err != nil {
		return err
	}

	if r.skills == nil {
		r.skills = make(map[string]*Skill)
	}

	for _, page := range pages {
		skill := FromPage(page)
		if skill == nil {
			continue
		}
		if skill.Status == "draft" {
			continue
		}
		if _, exists := r.skills[skill.Name]; exists {
			slog.Warn("duplicate skill definition", "name", skill.Name, "path", page.Path)
		}
		r.skills[skill.Name] = skill
	}

	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test -race ./internal/skill/ -run TestRegistryLoadSkipsDraft -v
```
Expected: PASS

- [ ] **Step 5: Run all existing skill tests to confirm no regression**

```bash
go test -race ./internal/skill/... -v
```
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
git add internal/skill/registry.go internal/skill/registry_test.go
git commit -m "feat(skill): filter draft skills from Registry.Load()"
```

---

### Task 3: Create interfaces.go (W2/W4 contracts)

**Files:**
- Create: `internal/skill/interfaces.go`

- [ ] **Step 1: Create interfaces file**

```go
// internal/skill/interfaces.go
package skill

import (
	"context"

	"github.com/stello/elnath/internal/llm"
)

// Analyst extracts skill patches from session trajectories.
// Implementations MUST create a fresh agent.New() per analysis call.
// Reusing an existing session's agent violates context isolation.
type Analyst interface {
	Analyze(ctx context.Context, sessions []SessionTrajectory) ([]SkillPatch, error)
}

// SessionTrajectory represents a completed session for analysis.
// Load via agent.LoadSessionMessages() — never parse raw JSONL.
type SessionTrajectory struct {
	SessionID string
	Messages  []llm.Message
	Success   bool
	Intent    string
}

// SkillPatch is a proposal to create or deepen a skill.
type SkillPatch struct {
	Action         string // "create" | "deepen"
	Params         CreateParams
	Evidence       []string // session IDs
	Confidence     float64
	PatchRationale string
}

// ConsolidationResult reports what the consolidator did.
type ConsolidationResult struct {
	Promoted []string
	Merged   []string
	Rejected []string
	Cleaned  []string // drafts deleted due to age
}

// NotifyFunc sends a notification (e.g., via Telegram).
type NotifyFunc func(ctx context.Context, message string) error
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/skill/...
```
Expected: Success

- [ ] **Step 3: Commit**

```bash
git add internal/skill/interfaces.go
git commit -m "feat(skill): add Analyst/Consolidator interfaces and shared types"
```

---

### Task 4: Create Tracker (JSONL usage + pattern logging)

**Files:**
- Create: `internal/skill/tracker.go`
- Create: `internal/skill/tracker_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/skill/tracker_test.go
package skill

import (
	"path/filepath"
	"testing"
	"time"
)

func TestTrackerRecordUsage(t *testing.T) {
	dir := t.TempDir()
	tracker := NewTracker(dir)

	err := tracker.RecordUsage(UsageRecord{
		SkillName: "pr-review",
		SessionID: "sess-1",
		Timestamp: time.Now(),
		Success:   true,
	})
	if err != nil {
		t.Fatal(err)
	}

	stats, err := tracker.UsageStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats["pr-review"] != 1 {
		t.Errorf("expected 1 usage, got %d", stats["pr-review"])
	}
}

func TestTrackerRecordPattern(t *testing.T) {
	dir := t.TempDir()
	tracker := NewTracker(dir)

	now := time.Now()
	err := tracker.RecordPattern(PatternRecord{
		ID:           "pat-1",
		Description:  "test then commit pattern",
		SessionIDs:   []string{"sess-1", "sess-2"},
		ToolSequence: []string{"bash", "read_file", "write_file"},
		FirstSeen:    now,
		LastSeen:     now,
	})
	if err != nil {
		t.Fatal(err)
	}

	patterns, err := tracker.LoadPatterns()
	if err != nil {
		t.Fatal(err)
	}
	if len(patterns) != 1 {
		t.Fatalf("expected 1 pattern, got %d", len(patterns))
	}
	if patterns[0].ID != "pat-1" {
		t.Errorf("expected pat-1, got %s", patterns[0].ID)
	}
}

func TestTrackerEmptyFiles(t *testing.T) {
	dir := t.TempDir()
	tracker := NewTracker(dir)

	stats, err := tracker.UsageStats()
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 0 {
		t.Errorf("expected empty stats, got %v", stats)
	}

	patterns, err := tracker.LoadPatterns()
	if err != nil {
		t.Fatal(err)
	}
	if len(patterns) != 0 {
		t.Errorf("expected empty patterns, got %v", patterns)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -race ./internal/skill/ -run TestTracker -v
```
Expected: FAIL — `NewTracker` not defined.

- [ ] **Step 3: Implement Tracker**

```go
// internal/skill/tracker.go
package skill

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type UsageRecord struct {
	SkillName string    `json:"skill_name"`
	SessionID string    `json:"session_id"`
	Timestamp time.Time `json:"timestamp"`
	Success   bool      `json:"success"`
}

type PatternRecord struct {
	ID           string    `json:"id"`
	Description  string    `json:"description"`
	SessionIDs   []string  `json:"session_ids"`
	ToolSequence []string  `json:"tool_sequence"`
	FirstSeen    time.Time `json:"first_seen"`
	LastSeen     time.Time `json:"last_seen"`
	DraftSkill   string    `json:"draft_skill,omitempty"`
}

type Tracker struct {
	usagePath   string
	patternPath string
}

func NewTracker(dataDir string) *Tracker {
	return &Tracker{
		usagePath:   filepath.Join(dataDir, "skill-usage.jsonl"),
		patternPath: filepath.Join(dataDir, "skill-patterns.jsonl"),
	}
}

func (t *Tracker) RecordUsage(record UsageRecord) error {
	return appendJSONL(t.usagePath, record)
}

func (t *Tracker) RecordPattern(record PatternRecord) error {
	return appendJSONL(t.patternPath, record)
}

func (t *Tracker) LoadPatterns() ([]PatternRecord, error) {
	return readJSONL[PatternRecord](t.patternPath)
}

func (t *Tracker) UsageStats() (map[string]int, error) {
	records, err := readJSONL[UsageRecord](t.usagePath)
	if err != nil {
		return nil, err
	}
	stats := make(map[string]int)
	for _, r := range records {
		stats[r.SkillName]++
	}
	return stats, nil
}

func appendJSONL[T any](path string, record T) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	data = append(data, '\n')
	_, err = f.Write(data)
	return err
}

func readJSONL[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	var results []T
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var item T
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			continue
		}
		results = append(results, item)
	}
	return results, scanner.Err()
}
```

- [ ] **Step 4: Run tests to verify pass**

```bash
go test -race ./internal/skill/ -run TestTracker -v
```
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/skill/tracker.go internal/skill/tracker_test.go
git commit -m "feat(skill): add JSONL Tracker for usage and pattern recording"
```

---

### Task 5: Create Creator (CRUD + Promote with hot-reload)

**Files:**
- Create: `internal/skill/creator.go`
- Create: `internal/skill/creator_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/skill/creator_test.go
package skill

import (
	"testing"

	"github.com/stello/elnath/internal/wiki"
)

func TestCreatorCreate(t *testing.T) {
	dir := t.TempDir()
	store, _ := wiki.NewStore(dir)
	tracker := NewTracker(t.TempDir())
	creator := NewCreator(store, tracker, nil)

	params := CreateParams{
		Name:          "deploy-check",
		Description:   "Check deployment status",
		Trigger:       "/deploy-check <env>",
		RequiredTools: []string{"bash"},
		Prompt:        "Check deployment for {env}.",
		Status:        "active",
		Source:        "user",
	}
	sk, err := creator.Create(params)
	if err != nil {
		t.Fatal(err)
	}
	if sk.Name != "deploy-check" {
		t.Errorf("expected deploy-check, got %s", sk.Name)
	}

	page, err := store.Read("skills/deploy-check.md")
	if err != nil {
		t.Fatal("skill page not found in wiki")
	}
	if page.Extra["status"] != "active" {
		t.Errorf("expected status active, got %v", page.Extra["status"])
	}
}

func TestCreatorCreateDuplicate(t *testing.T) {
	dir := t.TempDir()
	store, _ := wiki.NewStore(dir)
	creator := NewCreator(store, NewTracker(t.TempDir()), nil)

	params := CreateParams{Name: "dup", Prompt: "test", Status: "active"}
	if _, err := creator.Create(params); err != nil {
		t.Fatal(err)
	}
	if _, err := creator.Create(params); err == nil {
		t.Error("expected error for duplicate skill")
	}
}

func TestCreatorDelete(t *testing.T) {
	dir := t.TempDir()
	store, _ := wiki.NewStore(dir)
	creator := NewCreator(store, NewTracker(t.TempDir()), nil)

	creator.Create(CreateParams{Name: "to-delete", Prompt: "test", Status: "active"})
	if err := creator.Delete("to-delete"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read("skills/to-delete.md"); err == nil {
		t.Error("expected page to be deleted")
	}
}

func TestCreatorPromoteWithHotReload(t *testing.T) {
	dir := t.TempDir()
	store, _ := wiki.NewStore(dir)
	reg := NewRegistry()
	creator := NewCreator(store, NewTracker(t.TempDir()), reg)

	creator.Create(CreateParams{
		Name:   "promoted-skill",
		Prompt: "do things",
		Status: "draft",
		Source: "analyst",
	})

	if _, ok := reg.Get("promoted-skill"); ok {
		t.Error("draft skill should not be in registry")
	}

	if err := creator.Promote("promoted-skill"); err != nil {
		t.Fatal(err)
	}

	page, _ := store.Read("skills/promoted-skill.md")
	if page.Extra["status"] != "active" {
		t.Errorf("expected status active after promote, got %v", page.Extra["status"])
	}
	if page.Extra["source"] != "promoted" {
		t.Errorf("expected source promoted, got %v", page.Extra["source"])
	}

	if _, ok := reg.Get("promoted-skill"); !ok {
		t.Error("promoted skill should be in registry via hot-reload")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test -race ./internal/skill/ -run TestCreator -v
```
Expected: FAIL — `NewCreator` not defined.

- [ ] **Step 3: Implement Creator**

```go
// internal/skill/creator.go
package skill

import (
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/wiki"
)

type CreateParams struct {
	Name           string
	Description    string
	Trigger        string
	RequiredTools  []string
	Model          string
	Prompt         string
	Status         string
	Source         string
	SourceSessions []string
}

type Creator struct {
	store    *wiki.Store
	tracker  *Tracker
	registry *Registry
}

func NewCreator(store *wiki.Store, tracker *Tracker, registry *Registry) *Creator {
	return &Creator{store: store, tracker: tracker, registry: registry}
}

func (c *Creator) Create(params CreateParams) (*Skill, error) {
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return nil, fmt.Errorf("skill name must not be empty")
	}

	if params.Status == "" {
		params.Status = "active"
	}

	path := "skills/" + name + ".md"
	extra := map[string]any{
		"name":        name,
		"description": params.Description,
		"trigger":     params.Trigger,
		"status":      params.Status,
		"source":      params.Source,
	}
	if len(params.RequiredTools) > 0 {
		tools := make([]any, len(params.RequiredTools))
		for i, t := range params.RequiredTools {
			tools[i] = t
		}
		extra["required_tools"] = tools
	}
	if params.Model != "" {
		extra["model"] = params.Model
	}
	if len(params.SourceSessions) > 0 {
		sessions := make([]any, len(params.SourceSessions))
		for i, s := range params.SourceSessions {
			sessions[i] = s
		}
		extra["source_sessions"] = sessions
	}

	page := &wiki.Page{
		Path:    path,
		Title:   name,
		Tags:    []string{"skill"},
		Extra:   extra,
		Content: params.Prompt,
	}

	if err := c.store.Create(page); err != nil {
		return nil, fmt.Errorf("create skill %q: %w", name, err)
	}

	sk := FromPage(page)
	if sk == nil {
		return nil, fmt.Errorf("created page did not parse as skill")
	}

	if sk.Status == "active" && c.registry != nil {
		c.registry.Add(sk)
	}

	return sk, nil
}

func (c *Creator) Delete(name string) error {
	path := "skills/" + name + ".md"
	return c.store.Delete(path)
}

func (c *Creator) Promote(name string) error {
	path := "skills/" + name + ".md"
	page, err := c.store.Read(path)
	if err != nil {
		return fmt.Errorf("promote skill %q: %w", name, err)
	}

	if page.Extra == nil {
		page.Extra = make(map[string]any)
	}
	page.Extra["status"] = "active"
	page.Extra["source"] = "promoted"

	if err := c.store.Update(page); err != nil {
		return fmt.Errorf("promote skill %q: %w", name, err)
	}

	if c.registry != nil {
		sk := FromPage(page)
		if sk != nil {
			c.registry.Add(sk)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify pass**

```bash
go test -race ./internal/skill/ -run TestCreator -v
```
Expected: All PASS

- [ ] **Step 5: Run all skill tests for regression**

```bash
go test -race ./internal/skill/... -v
```
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
git add internal/skill/creator.go internal/skill/creator_test.go
git commit -m "feat(skill): add Creator with CRUD, Promote, and Registry hot-reload"
```

---

### Task 6: Create CLI commands (cmd_skill.go)

**Files:**
- Create: `cmd/elnath/cmd_skill.go`
- Modify: `cmd/elnath/commands.go:35-50` (add "skill" entry)

- [ ] **Step 1: Create cmd_skill.go**

```go
// cmd/elnath/cmd_skill.go
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/skill"
	"github.com/stello/elnath/internal/wiki"
)

func cmdSkill(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return cmdSkillList(ctx, nil)
	}

	sub := args[0]
	rest := args[1:]

	switch sub {
	case "list":
		return cmdSkillList(ctx, rest)
	case "show":
		return cmdSkillShow(ctx, rest)
	case "create":
		return cmdSkillCreate(ctx, rest)
	case "delete":
		return cmdSkillDelete(ctx, rest)
	case "edit":
		return cmdSkillEdit(ctx, rest)
	case "stats":
		return cmdSkillStats(ctx, rest)
	default:
		return fmt.Errorf("unknown skill subcommand: %q (try: list, show, create, delete, edit, stats)", sub)
	}
}

func cmdSkillList(_ context.Context, args []string) error {
	cfg, err := config.Load(extractConfigFlag(os.Args))
	if err != nil {
		return err
	}
	store, err := wiki.NewStore(cfg.WikiDir)
	if err != nil {
		return err
	}
	reg := skill.NewRegistry()
	showAll := hasFlag(args, "--all")

	pages, err := store.List()
	if err != nil {
		return err
	}

	var skills []*skill.Skill
	for _, page := range pages {
		sk := skill.FromPage(page)
		if sk == nil {
			continue
		}
		if !showAll && sk.Status == "draft" {
			continue
		}
		skills = append(skills, sk)
	}

	_ = reg
	if len(skills) == 0 {
		fmt.Println("No skills found.")
		return nil
	}
	for _, sk := range skills {
		status := ""
		if sk.Status == "draft" {
			status = " [draft]"
		}
		fmt.Printf("  /%s — %s%s\n", sk.Name, sk.Description, status)
	}
	return nil
}

func cmdSkillShow(_ context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: elnath skill show <name>")
	}
	cfg, err := config.Load(extractConfigFlag(os.Args))
	if err != nil {
		return err
	}
	store, err := wiki.NewStore(cfg.WikiDir)
	if err != nil {
		return err
	}

	page, err := store.Read("skills/" + args[0] + ".md")
	if err != nil {
		return fmt.Errorf("skill %q not found", args[0])
	}
	sk := skill.FromPage(page)
	if sk == nil {
		return fmt.Errorf("page is not a valid skill")
	}

	fmt.Printf("Name:        %s\n", sk.Name)
	fmt.Printf("Description: %s\n", sk.Description)
	fmt.Printf("Trigger:     %s\n", sk.Trigger)
	fmt.Printf("Status:      %s\n", sk.Status)
	fmt.Printf("Source:      %s\n", sk.Source)
	fmt.Printf("Tools:       %s\n", strings.Join(sk.RequiredTools, ", "))
	fmt.Printf("Model:       %s\n", sk.Model)
	fmt.Printf("\n--- Prompt ---\n%s\n", sk.Prompt)
	return nil
}

func cmdSkillCreate(_ context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: elnath skill create <name>")
	}
	cfg, err := config.Load(extractConfigFlag(os.Args))
	if err != nil {
		return err
	}
	store, err := wiki.NewStore(cfg.WikiDir)
	if err != nil {
		return err
	}
	tracker := skill.NewTracker(cfg.DataDir)
	creator := skill.NewCreator(store, tracker, nil)

	name := args[0]
	var desc, trigger, prompt string

	fmt.Print("Description: ")
	fmt.Scanln(&desc)
	fmt.Print("Trigger (e.g. /name <arg>): ")
	fmt.Scanln(&trigger)
	fmt.Print("Prompt: ")
	fmt.Scanln(&prompt)

	sk, err := creator.Create(skill.CreateParams{
		Name:        name,
		Description: desc,
		Trigger:     trigger,
		Prompt:      prompt,
		Status:      "active",
		Source:      "user",
	})
	if err != nil {
		return err
	}
	fmt.Printf("Created skill: /%s\n", sk.Name)
	return nil
}

func cmdSkillDelete(_ context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: elnath skill delete <name>")
	}
	cfg, err := config.Load(extractConfigFlag(os.Args))
	if err != nil {
		return err
	}
	store, err := wiki.NewStore(cfg.WikiDir)
	if err != nil {
		return err
	}
	tracker := skill.NewTracker(cfg.DataDir)
	creator := skill.NewCreator(store, tracker, nil)

	fmt.Printf("Delete skill %q? (y/N): ", args[0])
	var confirm string
	fmt.Scanln(&confirm)
	if confirm != "y" && confirm != "Y" {
		fmt.Println("Cancelled.")
		return nil
	}
	if err := creator.Delete(args[0]); err != nil {
		return err
	}
	fmt.Printf("Deleted skill: %s\n", args[0])
	return nil
}

func cmdSkillEdit(_ context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: elnath skill edit <name>")
	}
	cfg, err := config.Load(extractConfigFlag(os.Args))
	if err != nil {
		return err
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	path := cfg.WikiDir + "/skills/" + args[0] + ".md"
	return core.RunEditor(editor, path)
}

func cmdSkillStats(_ context.Context, _ []string) error {
	cfg, err := config.Load(extractConfigFlag(os.Args))
	if err != nil {
		return err
	}
	tracker := skill.NewTracker(cfg.DataDir)
	stats, err := tracker.UsageStats()
	if err != nil {
		return err
	}
	if len(stats) == 0 {
		fmt.Println("No skill usage recorded yet.")
		return nil
	}
	fmt.Println("Skill usage:")
	for name, count := range stats {
		fmt.Printf("  %-20s %d invocations\n", name, count)
	}
	return nil
}
```

- [ ] **Step 2: Register "skill" command in commands.go**

Add to the commands map in `cmd/elnath/commands.go`:

```go
"skill": cmdSkill,
```

- [ ] **Step 3: Verify build**

```bash
cd /Users/stello/elnath && make build
```
Expected: Success

- [ ] **Step 4: Test CLI manually**

```bash
./elnath skill list
./elnath skill list --all
```
Expected: Lists existing skills (pr-review, refactor-tests, audit-security)

- [ ] **Step 5: Commit**

```bash
git add cmd/elnath/cmd_skill.go cmd/elnath/commands.go
git commit -m "feat: add elnath skill CLI (list/show/create/edit/delete/stats)"
```

---

### Task 7: Wire integration in runtime.go + scheduler + telegram

**Files:**
- Modify: `cmd/elnath/runtime.go:266-277` (wire Creator, Tracker)
- Modify: `internal/scheduler/task.go:36` (add "skill-promote" type)
- Modify: `internal/telegram/shell.go` (skill commands)

- [ ] **Step 1: Add "skill-promote" to scheduler task types**

In `internal/scheduler/task.go:36`, change:

```go
if t.Type != "" && t.Type != "agent" && t.Type != "research" {
```

to:

```go
if t.Type != "" && t.Type != "agent" && t.Type != "research" && t.Type != "skill-promote" {
```

- [ ] **Step 2: Wire Creator and Tracker in runtime.go**

After the `skillReg` creation block (line ~277), add:

```go
skillTracker := skill.NewTracker(cfg.DataDir)
skillCreator := skill.NewCreator(wikiStore, skillTracker, skillReg)
```

Add `skillCreator` and `skillTracker` to the `executionRuntime` struct fields.

- [ ] **Step 3: Add Telegram skill commands in shell.go**

Add to the `handleCommand` switch in `shell.go`:

```go
case "/skill-list":
    return s.handleSkillList()
case "/skill-create":
    if len(fields) < 2 {
        return "Usage: /skill-create <name>", nil
    }
    return s.handleSkillCreate(fields[1])
```

Add to the `/help` output:

```go
"• <code>/skill-list</code> — 등록된 skill 목록\n"
"• <code>/skill-create</code> — 새 skill 생성\n"
```

- [ ] **Step 4: Build and verify**

```bash
cd /Users/stello/elnath && make build && go vet ./...
```
Expected: Success, no warnings

- [ ] **Step 5: Run all tests**

```bash
go test -race ./internal/skill/... ./internal/scheduler/... ./cmd/elnath/... -count=1
```
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
git add cmd/elnath/runtime.go internal/scheduler/task.go internal/telegram/shell.go
git commit -m "feat: wire skill Creator/Tracker into runtime, scheduler, and Telegram"
```

---

## Worker 2: Layer 1 (create_skill Tool + Guidance)

**의존성:** W1 Task 5 (Creator) 완료 후 시작

### Task 8: Create create_skill tool

**Files:**
- Create: `internal/tools/skill_tool.go`
- Create: `internal/tools/skill_tool_test.go`

- [ ] **Step 1: Write failing test**

```go
// internal/tools/skill_tool_test.go
package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stello/elnath/internal/skill"
	"github.com/stello/elnath/internal/wiki"
)

func TestSkillToolCreate(t *testing.T) {
	dir := t.TempDir()
	store, _ := wiki.NewStore(dir)
	tracker := skill.NewTracker(t.TempDir())
	creator := skill.NewCreator(store, tracker, nil)
	reg := skill.NewRegistry()
	tool := NewSkillTool(creator, reg)

	if tool.Name() != "create_skill" {
		t.Errorf("expected create_skill, got %s", tool.Name())
	}

	input := map[string]any{
		"action":         "create",
		"name":           "test-skill",
		"description":    "A test skill",
		"trigger":        "/test-skill <arg>",
		"required_tools": []string{"bash"},
		"prompt":         "Do test with {arg}.",
	}
	params, _ := json.Marshal(input)
	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Output)
	}

	page, err := store.Read("skills/test-skill.md")
	if err != nil {
		t.Fatal("skill page should exist after create")
	}
	if page.Extra["source"] != "hint" {
		t.Errorf("expected source hint, got %v", page.Extra["source"])
	}
}

func TestSkillToolList(t *testing.T) {
	dir := t.TempDir()
	store, _ := wiki.NewStore(dir)
	tracker := skill.NewTracker(t.TempDir())
	creator := skill.NewCreator(store, tracker, nil)
	reg := skill.NewRegistry()
	tool := NewSkillTool(creator, reg)

	creator.Create(skill.CreateParams{
		Name: "existing", Prompt: "test", Status: "active", Source: "user",
	})
	reg.Add(&skill.Skill{Name: "existing", Description: "test", Status: "active"})

	params, _ := json.Marshal(map[string]any{"action": "list"})
	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Output)
	}
}

func TestSkillToolDelete(t *testing.T) {
	dir := t.TempDir()
	store, _ := wiki.NewStore(dir)
	tracker := skill.NewTracker(t.TempDir())
	creator := skill.NewCreator(store, tracker, nil)
	reg := skill.NewRegistry()
	tool := NewSkillTool(creator, reg)

	creator.Create(skill.CreateParams{
		Name: "to-delete", Prompt: "test", Status: "active",
	})

	params, _ := json.Marshal(map[string]any{"action": "delete", "name": "to-delete"})
	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Output)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -race ./internal/tools/ -run TestSkillTool -v
```
Expected: FAIL — `NewSkillTool` not defined.

- [ ] **Step 3: Implement SkillTool**

```go
// internal/tools/skill_tool.go
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/skill"
)

type SkillTool struct {
	creator  *skill.Creator
	registry *skill.Registry
}

func NewSkillTool(creator *skill.Creator, registry *skill.Registry) *SkillTool {
	return &SkillTool{creator: creator, registry: registry}
}

func (t *SkillTool) Name() string { return "create_skill" }

func (t *SkillTool) Description() string {
	return "Create, list, or delete a wiki-native skill"
}

func (t *SkillTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {"type": "string", "enum": ["create", "list", "delete"], "description": "Action to perform"},
			"name": {"type": "string", "description": "Skill name (lowercase, hyphens)"},
			"description": {"type": "string", "description": "What the skill does"},
			"trigger": {"type": "string", "description": "Trigger format, e.g. /deploy-check <env>"},
			"required_tools": {"type": "array", "items": {"type": "string"}, "description": "Tools the skill needs"},
			"prompt": {"type": "string", "description": "Skill prompt with {arg} placeholders"}
		},
		"required": ["action"]
	}`)
}

type skillToolInput struct {
	Action        string   `json:"action"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Trigger       string   `json:"trigger"`
	RequiredTools []string `json:"required_tools"`
	Prompt        string   `json:"prompt"`
}

func (t *SkillTool) Execute(_ context.Context, params json.RawMessage) (*Result, error) {
	var input skillToolInput
	if err := json.Unmarshal(params, &input); err != nil {
		return ErrorResult(fmt.Sprintf("invalid input: %v", err)), nil
	}

	switch input.Action {
	case "create":
		return t.executeCreate(input)
	case "list":
		return t.executeList()
	case "delete":
		return t.executeDelete(input)
	default:
		return ErrorResult(fmt.Sprintf("unknown action: %q", input.Action)), nil
	}
}

func (t *SkillTool) executeCreate(input skillToolInput) (*Result, error) {
	if input.Name == "" {
		return ErrorResult("name is required for create"), nil
	}
	if input.Prompt == "" {
		return ErrorResult("prompt is required for create"), nil
	}
	sk, err := t.creator.Create(skill.CreateParams{
		Name:          input.Name,
		Description:   input.Description,
		Trigger:       input.Trigger,
		RequiredTools: input.RequiredTools,
		Prompt:        input.Prompt,
		Status:        "active",
		Source:        "hint",
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to create skill: %v", err)), nil
	}
	return SuccessResult(fmt.Sprintf("Created skill /%s — %s", sk.Name, sk.Description)), nil
}

func (t *SkillTool) executeList() (*Result, error) {
	skills := t.registry.List()
	if len(skills) == 0 {
		return SuccessResult("No skills registered."), nil
	}
	var b strings.Builder
	b.WriteString("Registered skills:\n")
	for _, sk := range skills {
		fmt.Fprintf(&b, "  /%s — %s\n", sk.Name, sk.Description)
	}
	return SuccessResult(b.String()), nil
}

func (t *SkillTool) executeDelete(input skillToolInput) (*Result, error) {
	if input.Name == "" {
		return ErrorResult("name is required for delete"), nil
	}
	if err := t.creator.Delete(input.Name); err != nil {
		return ErrorResult(fmt.Sprintf("failed to delete: %v", err)), nil
	}
	return SuccessResult(fmt.Sprintf("Deleted skill /%s", input.Name)), nil
}

func (t *SkillTool) IsConcurrencySafe(_ json.RawMessage) bool { return true }
func (t *SkillTool) Reversible() bool                         { return true }
func (t *SkillTool) ShouldCancelSiblingsOnError() bool        { return false }

func (t *SkillTool) Scope(params json.RawMessage) ToolScope {
	var input skillToolInput
	if err := json.Unmarshal(params, &input); err != nil {
		return ConservativeScope()
	}
	if input.Action == "list" {
		return ToolScope{Read: true}
	}
	return ToolScope{Read: true, Write: true}
}
```

- [ ] **Step 4: Run tests to verify pass**

```bash
go test -race ./internal/tools/ -run TestSkillTool -v
```
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tools/skill_tool.go internal/tools/skill_tool_test.go
git commit -m "feat: add create_skill tool for LLM-driven skill creation"
```

---

### Task 9: Create SkillGuidanceNode

**Files:**
- Create: `internal/prompt/skill_guidance_node.go`
- Create: `internal/prompt/skill_guidance_node_test.go`

- [ ] **Step 1: Write failing test**

```go
// internal/prompt/skill_guidance_node_test.go
package prompt

import (
	"context"
	"testing"
)

func TestSkillGuidanceNodeRender(t *testing.T) {
	node := NewSkillGuidanceNode(64)
	out, err := node.Render(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out == "" {
		t.Error("expected non-empty guidance")
	}
	if node.Priority() != 64 {
		t.Errorf("expected priority 64, got %d", node.Priority())
	}
	if node.Name() != "skill_guidance" {
		t.Errorf("expected skill_guidance, got %s", node.Name())
	}
}

func TestSkillGuidanceNodeBenchmarkMode(t *testing.T) {
	node := NewSkillGuidanceNode(64)
	state := &RenderState{BenchmarkMode: true}
	out, err := node.Render(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Errorf("expected empty in benchmark mode, got %q", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -race ./internal/prompt/ -run TestSkillGuidanceNode -v
```
Expected: FAIL

- [ ] **Step 3: Implement SkillGuidanceNode**

```go
// internal/prompt/skill_guidance_node.go
package prompt

import "context"

type SkillGuidanceNode struct {
	priority int
}

func NewSkillGuidanceNode(priority int) *SkillGuidanceNode {
	return &SkillGuidanceNode{priority: priority}
}

func (n *SkillGuidanceNode) Name() string     { return "skill_guidance" }
func (n *SkillGuidanceNode) Priority() int    { return n.priority }

func (n *SkillGuidanceNode) Render(_ context.Context, state *RenderState) (string, error) {
	if state != nil && state.BenchmarkMode {
		return "", nil
	}
	return `You have a create_skill tool. Use it when:
- You notice a repeated pattern across sessions
- The user says "make this a skill" or similar
- A multi-step workflow could be reusable

When suggesting a skill, briefly explain what it would do before creating it.
Do not suggest skills for one-time tasks.`, nil
}
```

- [ ] **Step 4: Run test to verify pass**

```bash
go test -race ./internal/prompt/ -run TestSkillGuidanceNode -v
```
Expected: All PASS

- [ ] **Step 5: Register in runtime.go (W1 adds this line)**

W1 adds to `buildExecutionRuntime` after SkillCatalogNode registration:

```go
b.Register(prompt.NewSkillGuidanceNode(64))
```

- [ ] **Step 6: Commit**

```bash
git add internal/prompt/skill_guidance_node.go internal/prompt/skill_guidance_node_test.go
git commit -m "feat: add SkillGuidanceNode for LLM skill creation hints"
```

---

## Worker 4: Layer 3 (Consolidation + Promotion)

**의존성:** W1 Task 4 (Tracker) + Task 5 (Creator) 완료 후 시작

### Task 10: Create Consolidator

**Files:**
- Create: `internal/skill/consolidator.go`
- Create: `internal/skill/consolidator_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/skill/consolidator_test.go
package skill

import (
	"context"
	"testing"
	"time"

	"github.com/stello/elnath/internal/wiki"
)

func TestConsolidatorPromotesMeetingThreshold(t *testing.T) {
	dir := t.TempDir()
	store, _ := wiki.NewStore(dir)
	tracker := NewTracker(t.TempDir())
	reg := NewRegistry()
	creator := NewCreator(store, tracker, reg)

	creator.Create(CreateParams{
		Name:           "candidate",
		Prompt:         "do things",
		Status:         "draft",
		Source:         "analyst",
		SourceSessions: []string{"s1", "s2", "s3"},
	})

	for i := 0; i < 3; i++ {
		tracker.RecordPattern(PatternRecord{
			ID:           fmt.Sprintf("p%d", i),
			SessionIDs:   []string{fmt.Sprintf("s%d", i+1)},
			ToolSequence: []string{"bash", "read_file"},
			FirstSeen:    time.Now(),
			LastSeen:     time.Now(),
			DraftSkill:   "candidate",
		})
	}

	consolidator := NewConsolidator(creator, tracker, reg, store, ConsolidatorConfig{
		MinSessions:   3,
		MinPrevalence: 2,
		MaxDraftAge:   90 * 24 * time.Hour,
	})

	result, err := consolidator.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Promoted) != 1 || result.Promoted[0] != "candidate" {
		t.Errorf("expected candidate promoted, got %v", result.Promoted)
	}
	if _, ok := reg.Get("candidate"); !ok {
		t.Error("promoted skill should be in registry")
	}
}

func TestConsolidatorSkipsBelowThreshold(t *testing.T) {
	dir := t.TempDir()
	store, _ := wiki.NewStore(dir)
	tracker := NewTracker(t.TempDir())
	reg := NewRegistry()
	creator := NewCreator(store, tracker, reg)

	creator.Create(CreateParams{
		Name:   "weak-candidate",
		Prompt: "do things",
		Status: "draft",
		Source: "analyst",
	})

	tracker.RecordPattern(PatternRecord{
		ID:         "p1",
		SessionIDs: []string{"s1"},
		DraftSkill: "weak-candidate",
		FirstSeen:  time.Now(),
		LastSeen:   time.Now(),
	})

	consolidator := NewConsolidator(creator, tracker, reg, store, ConsolidatorConfig{
		MinSessions:   5,
		MinPrevalence: 2,
		MaxDraftAge:   90 * 24 * time.Hour,
	})

	result, err := consolidator.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Promoted) != 0 {
		t.Errorf("expected no promotions, got %v", result.Promoted)
	}
}

func TestConsolidatorCleansOldDrafts(t *testing.T) {
	dir := t.TempDir()
	store, _ := wiki.NewStore(dir)
	tracker := NewTracker(t.TempDir())
	reg := NewRegistry()
	creator := NewCreator(store, tracker, reg)

	creator.Create(CreateParams{
		Name:   "old-draft",
		Prompt: "old things",
		Status: "draft",
		Source: "analyst",
	})

	consolidator := NewConsolidator(creator, tracker, reg, store, ConsolidatorConfig{
		MinSessions:   5,
		MinPrevalence: 2,
		MaxDraftAge:   0, // expire immediately for test
	})

	result, err := consolidator.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Cleaned) != 1 || result.Cleaned[0] != "old-draft" {
		t.Errorf("expected old-draft cleaned, got %v", result.Cleaned)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test -race ./internal/skill/ -run TestConsolidator -v
```
Expected: FAIL

- [ ] **Step 3: Implement Consolidator**

```go
// internal/skill/consolidator.go
package skill

import (
	"context"
	"log/slog"
	"time"

	"github.com/stello/elnath/internal/wiki"
)

type ConsolidatorConfig struct {
	MinSessions   int
	MinPrevalence int
	MaxDraftAge   time.Duration
}

func DefaultConsolidatorConfig() ConsolidatorConfig {
	return ConsolidatorConfig{
		MinSessions:   5,
		MinPrevalence: 2,
		MaxDraftAge:   90 * 24 * time.Hour,
	}
}

type Consolidator struct {
	creator  *Creator
	tracker  *Tracker
	registry *Registry
	store    *wiki.Store
	config   ConsolidatorConfig
}

func NewConsolidator(
	creator *Creator,
	tracker *Tracker,
	registry *Registry,
	store *wiki.Store,
	config ConsolidatorConfig,
) *Consolidator {
	return &Consolidator{
		creator:  creator,
		tracker:  tracker,
		registry: registry,
		store:    store,
		config:   config,
	}
}

func (c *Consolidator) Run(ctx context.Context) (*ConsolidationResult, error) {
	drafts, err := c.loadDrafts()
	if err != nil {
		return nil, err
	}

	patterns, err := c.tracker.LoadPatterns()
	if err != nil {
		return nil, err
	}

	result := &ConsolidationResult{}

	for _, draft := range drafts {
		prevalence := c.countPrevalence(draft.Name, patterns)
		totalSessions := c.countTotalSessions(draft.Name, patterns)

		if prevalence >= c.config.MinPrevalence && totalSessions >= c.config.MinSessions {
			if err := c.creator.Promote(draft.Name); err != nil {
				slog.Warn("skill promote failed", "name", draft.Name, "error", err)
				continue
			}
			result.Promoted = append(result.Promoted, draft.Name)
			slog.Info("skill promoted", "name", draft.Name, "prevalence", prevalence, "sessions", totalSessions)
			continue
		}

		if c.isDraftExpired(draft) {
			if err := c.creator.Delete(draft.Name); err != nil {
				slog.Warn("skill cleanup failed", "name", draft.Name, "error", err)
				continue
			}
			result.Cleaned = append(result.Cleaned, draft.Name)
			slog.Info("expired draft cleaned", "name", draft.Name)
		}
	}

	return result, nil
}

func (c *Consolidator) loadDrafts() ([]*Skill, error) {
	pages, err := c.store.List()
	if err != nil {
		return nil, err
	}

	var drafts []*Skill
	for _, page := range pages {
		sk := FromPage(page)
		if sk != nil && sk.Status == "draft" {
			drafts = append(drafts, sk)
		}
	}
	return drafts, nil
}

func (c *Consolidator) countPrevalence(draftName string, patterns []PatternRecord) int {
	sessionSet := make(map[string]struct{})
	for _, p := range patterns {
		if p.DraftSkill != draftName {
			continue
		}
		for _, sid := range p.SessionIDs {
			sessionSet[sid] = struct{}{}
		}
	}
	return len(sessionSet)
}

func (c *Consolidator) countTotalSessions(draftName string, patterns []PatternRecord) int {
	return c.countPrevalence(draftName, patterns)
}

func (c *Consolidator) isDraftExpired(draft *Skill) bool {
	if c.config.MaxDraftAge <= 0 {
		return true
	}
	page, err := c.store.Read("skills/" + draft.Name + ".md")
	if err != nil {
		return false
	}
	return time.Since(page.Created) > c.config.MaxDraftAge
}
```

- [ ] **Step 4: Run tests to verify pass**

```bash
go test -race ./internal/skill/ -run TestConsolidator -v
```
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/skill/consolidator.go internal/skill/consolidator_test.go
git commit -m "feat(skill): add Consolidator for prevalence-weighted draft promotion"
```

---

### Task 11: Create Promotion notification formatter

**Files:**
- Create: `internal/skill/promotion.go`
- Create: `internal/skill/promotion_test.go`

- [ ] **Step 1: Write failing test**

```go
// internal/skill/promotion_test.go
package skill

import "testing"

func TestFormatPromotionMessage(t *testing.T) {
	msg := FormatPromotionMessage(&Skill{
		Name:        "deploy-check",
		Description: "Check deployment status",
	}, 3, 7)

	if msg == "" {
		t.Error("expected non-empty message")
	}

	tests := []string{"deploy-check", "3", "7"}
	for _, want := range tests {
		if !containsStr(msg, want) {
			t.Errorf("message should contain %q: %s", want, msg)
		}
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstring(s, sub))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Implement FormatPromotionMessage**

```go
// internal/skill/promotion.go
package skill

import "fmt"

func FormatPromotionMessage(sk *Skill, prevalence int, totalSessions int) string {
	return fmt.Sprintf(
		"New skill /%s activated (%d sessions, %d independent patterns). Use /skill-list to review.",
		sk.Name, totalSessions, prevalence,
	)
}
```

- [ ] **Step 3: Run test**

```bash
go test -race ./internal/skill/ -run TestFormatPromotionMessage -v
```
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/skill/promotion.go internal/skill/promotion_test.go
git commit -m "feat(skill): add promotion notification formatter"
```

---

## Final Integration (after W1 + W2 + W4 merge)

### Task 12: Register all components and verify end-to-end

- [ ] **Step 1: Register SkillGuidanceNode and create_skill tool in runtime.go**

In `buildExecutionRuntime`, after SkillCatalogNode:

```go
b.Register(prompt.NewSkillGuidanceNode(64))
```

After tool registry setup:

```go
reg.Register(tools.NewSkillTool(skillCreator, skillReg))
```

- [ ] **Step 2: Full build and test**

```bash
cd /Users/stello/elnath && make build && go test -race ./... -count=1
```
Expected: Build success, all tests PASS

- [ ] **Step 3: Manual smoke test**

```bash
# CLI skill management
./elnath skill list
./elnath skill create test-manual
./elnath skill show test-manual
./elnath skill delete test-manual

# Verify create_skill tool appears in tool catalog
./elnath run  # In interactive mode, check system prompt includes skill guidance
```

- [ ] **Step 4: Verify go vet**

```bash
go vet ./...
```
Expected: No warnings

- [ ] **Step 5: Final commit**

```bash
git add -A
git commit -m "feat: Phase C-2 Skill Emergence MVP — CRUD + Layer 1 + Layer 3"
```
