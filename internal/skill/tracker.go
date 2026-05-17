package skill

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	SkillVerificationUnknown = "unknown"
	SkillVerificationNotRun  = "not_run"
	SkillVerificationPassed  = "passed"
	SkillVerificationFailed  = "failed"
)

type UsageRecord struct {
	SkillName               string    `json:"skill_name"`
	SessionID               string    `json:"session_id"`
	Timestamp               time.Time `json:"timestamp"`
	Success                 bool      `json:"success"`
	RequiredTools           []string  `json:"required_tools,omitempty"`
	VerificationResult      string    `json:"verification_result,omitempty"`
	UserOutcome             string    `json:"user_outcome,omitempty"`
	PromotionCandidate      bool      `json:"promotion_candidate,omitempty"`
	ImprovementProposalPath string    `json:"improvement_proposal_path,omitempty"`
}

type UsageSummary struct {
	SkillName                   string    `json:"skill_name"`
	Invocations                 int       `json:"invocations"`
	Successes                   int       `json:"successes"`
	Failures                    int       `json:"failures"`
	LastUsedAt                  time.Time `json:"last_used_at,omitempty"`
	RequiredTools               []string  `json:"required_tools,omitempty"`
	VerificationPassed          int       `json:"verification_passed,omitempty"`
	VerificationFailed          int       `json:"verification_failed,omitempty"`
	VerificationNotRun          int       `json:"verification_not_run,omitempty"`
	VerificationUnknown         int       `json:"verification_unknown,omitempty"`
	PromotionCandidates         int       `json:"promotion_candidates,omitempty"`
	LastUserOutcome             string    `json:"last_user_outcome,omitempty"`
	LastImprovementProposalPath string    `json:"last_improvement_proposal_path,omitempty"`
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
	proposalDir string
}

func NewTracker(dataDir string) *Tracker {
	return &Tracker{
		usagePath:   filepath.Join(dataDir, "skill-usage.jsonl"),
		patternPath: filepath.Join(dataDir, "skill-patterns.jsonl"),
		proposalDir: filepath.Join(dataDir, "skill-improvement-proposals"),
	}
}

func (t *Tracker) RecordUsage(record UsageRecord) error {
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now().UTC()
	}
	record.RequiredTools = normalizeUsageTools(record.RequiredTools)
	record.VerificationResult = normalizeSkillVerificationResult(record.VerificationResult)
	if strings.TrimSpace(record.UserOutcome) == "" {
		if record.Success {
			record.UserOutcome = "completed"
		} else {
			record.UserOutcome = "failed"
		}
	}
	return appendJSONL(t.usagePath, record)
}

func (t *Tracker) RecordPattern(record PatternRecord) error {
	now := time.Now().UTC()
	if record.FirstSeen.IsZero() {
		record.FirstSeen = now
	}
	if record.LastSeen.IsZero() {
		record.LastSeen = now
	}
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
	stats := make(map[string]int, len(records))
	for _, record := range records {
		stats[record.SkillName]++
	}
	return stats, nil
}

func (t *Tracker) UsageSummaries() (map[string]UsageSummary, error) {
	records, err := readJSONL[UsageRecord](t.usagePath)
	if err != nil {
		return nil, err
	}
	summaries := make(map[string]UsageSummary, len(records))
	for _, record := range records {
		if record.SkillName == "" {
			continue
		}
		summary := summaries[record.SkillName]
		summary.SkillName = record.SkillName
		summary.Invocations++
		if record.Success {
			summary.Successes++
		} else {
			summary.Failures++
		}
		summary.RequiredTools = mergeUsageTools(summary.RequiredTools, record.RequiredTools)
		switch normalizeSkillVerificationResult(record.VerificationResult) {
		case SkillVerificationPassed:
			summary.VerificationPassed++
		case SkillVerificationFailed:
			summary.VerificationFailed++
		case SkillVerificationNotRun:
			summary.VerificationNotRun++
		default:
			summary.VerificationUnknown++
		}
		if record.PromotionCandidate {
			summary.PromotionCandidates++
		}
		if record.Timestamp.After(summary.LastUsedAt) {
			summary.LastUsedAt = record.Timestamp
			summary.LastUserOutcome = record.UserOutcome
			summary.LastImprovementProposalPath = record.ImprovementProposalPath
		}
		summaries[record.SkillName] = summary
	}
	return summaries, nil
}

type ImprovementProposal struct {
	SkillName       string    `json:"skill_name"`
	SessionID       string    `json:"session_id,omitempty"`
	Reason          string    `json:"reason"`
	Evidence        []string  `json:"evidence,omitempty"`
	SuggestedChange string    `json:"suggested_change"`
	CreatedAt       time.Time `json:"created_at"`
}

func (t *Tracker) WriteImprovementProposal(proposal ImprovementProposal) (string, error) {
	if t == nil {
		return "", fmt.Errorf("skill tracker is not configured")
	}
	proposal.SkillName = strings.TrimSpace(proposal.SkillName)
	if err := ValidateSkillName(proposal.SkillName); err != nil {
		return "", err
	}
	if strings.TrimSpace(proposal.Reason) == "" {
		return "", fmt.Errorf("skill improvement proposal reason must not be empty")
	}
	if strings.TrimSpace(proposal.SuggestedChange) == "" {
		return "", fmt.Errorf("skill improvement proposal suggested change must not be empty")
	}
	if proposal.CreatedAt.IsZero() {
		proposal.CreatedAt = time.Now().UTC()
	}
	if err := os.MkdirAll(t.proposalDir, 0o755); err != nil {
		return "", fmt.Errorf("create proposal dir: %w", err)
	}
	basePath := filepath.Join(t.proposalDir, fmt.Sprintf("%s-%s.md",
		proposal.CreatedAt.UTC().Format("20060102T150405Z"),
		proposal.SkillName,
	))
	path := basePath
	for i := 2; ; i++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		} else if err != nil {
			return "", fmt.Errorf("stat skill improvement proposal path: %w", err)
		}
		path = strings.TrimSuffix(basePath, ".md") + fmt.Sprintf("-%d.md", i)
	}
	if err := os.WriteFile(path, []byte(formatImprovementProposal(proposal)), 0o600); err != nil {
		return "", fmt.Errorf("write skill improvement proposal: %w", err)
	}
	return path, nil
}

func (t *Tracker) ReadImprovementProposal(path string) (ImprovementProposal, error) {
	if t == nil {
		return ImprovementProposal{}, fmt.Errorf("skill tracker is not configured")
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return ImprovementProposal{}, fmt.Errorf("skill improvement proposal path must not be empty")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(t.proposalDir, path)
	}
	cleanDir := filepath.Clean(t.proposalDir)
	cleanPath := filepath.Clean(path)
	rel, err := filepath.Rel(cleanDir, cleanPath)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
		return ImprovementProposal{}, fmt.Errorf("skill improvement proposal must be under %s", cleanDir)
	}
	raw, err := os.ReadFile(cleanPath)
	if err != nil {
		return ImprovementProposal{}, fmt.Errorf("read skill improvement proposal: %w", err)
	}
	proposal, err := parseImprovementProposalMarkdown(string(raw))
	if err != nil {
		return ImprovementProposal{}, err
	}
	return proposal, nil
}

func normalizeSkillVerificationResult(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case SkillVerificationPassed:
		return SkillVerificationPassed
	case SkillVerificationFailed:
		return SkillVerificationFailed
	case SkillVerificationNotRun:
		return SkillVerificationNotRun
	case "", SkillVerificationUnknown:
		return SkillVerificationUnknown
	default:
		return SkillVerificationUnknown
	}
}

func normalizeUsageTools(tools []string) []string {
	if len(tools) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(tools))
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			continue
		}
		if _, ok := seen[tool]; ok {
			continue
		}
		seen[tool] = struct{}{}
		out = append(out, tool)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

func mergeUsageTools(existing, next []string) []string {
	if len(existing) == 0 {
		return normalizeUsageTools(next)
	}
	merged := append(append([]string(nil), existing...), next...)
	return normalizeUsageTools(merged)
}

func formatImprovementProposal(proposal ImprovementProposal) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("type: skill-improvement-proposal\n")
	b.WriteString(fmt.Sprintf("skill: %s\n", proposal.SkillName))
	b.WriteString(fmt.Sprintf("created_at: %s\n", proposal.CreatedAt.UTC().Format(time.RFC3339)))
	if proposal.SessionID != "" {
		b.WriteString(fmt.Sprintf("session_id: %s\n", proposal.SessionID))
	}
	b.WriteString("---\n\n")
	b.WriteString("# Skill Improvement Proposal\n\n")
	b.WriteString("## Reason\n\n")
	b.WriteString(strings.TrimSpace(proposal.Reason))
	b.WriteString("\n\n")
	if len(proposal.Evidence) > 0 {
		b.WriteString("## Evidence\n\n")
		for _, item := range proposal.Evidence {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			b.WriteString("- ")
			b.WriteString(item)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("## Suggested Change\n\n")
	b.WriteString(strings.TrimSpace(proposal.SuggestedChange))
	b.WriteString("\n")
	return b.String()
}

func parseImprovementProposalMarkdown(text string) (ImprovementProposal, error) {
	proposal := ImprovementProposal{
		SkillName:       frontmatterValue(text, "skill"),
		SessionID:       frontmatterValue(text, "session_id"),
		Reason:          markdownSection(text, "## Reason"),
		SuggestedChange: markdownSection(text, "## Suggested Change"),
	}
	if created := frontmatterValue(text, "created_at"); created != "" {
		if parsed, err := time.Parse(time.RFC3339, created); err == nil {
			proposal.CreatedAt = parsed
		}
	}
	if err := ValidateSkillName(proposal.SkillName); err != nil {
		return ImprovementProposal{}, err
	}
	if strings.TrimSpace(proposal.Reason) == "" {
		return ImprovementProposal{}, fmt.Errorf("skill improvement proposal reason must not be empty")
	}
	if strings.TrimSpace(proposal.SuggestedChange) == "" {
		return ImprovementProposal{}, fmt.Errorf("skill improvement proposal suggested change must not be empty")
	}
	proposal.Evidence = markdownListSection(text, "## Evidence")
	return proposal, nil
}

func frontmatterValue(text, key string) string {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	prefix := key + ":"
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "---" {
			break
		}
		if strings.HasPrefix(line, prefix) {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, prefix)), "\"'")
		}
	}
	return ""
}

func markdownSection(text, header string) string {
	lines := strings.Split(text, "\n")
	var out []string
	inSection := false
	for _, line := range lines {
		if strings.TrimSpace(line) == header {
			inSection = true
			continue
		}
		if inSection && strings.HasPrefix(strings.TrimSpace(line), "## ") {
			break
		}
		if inSection {
			out = append(out, line)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func markdownListSection(text, header string) []string {
	section := markdownSection(text, header)
	if section == "" {
		return nil
	}
	var out []string
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func appendJSONL[T any](path string, record T) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create jsonl dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(record); err != nil {
		return fmt.Errorf("encode jsonl record: %w", err)
	}
	return nil
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
			return nil, fmt.Errorf("decode jsonl record: %w", err)
		}
		results = append(results, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan jsonl: %w", err)
	}
	return results, nil
}
