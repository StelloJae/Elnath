package wiki

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/stello/elnath/internal/llm"
)

// Ingester creates wiki pages from external sources such as git logs,
// conversation turns, and arbitrary files.
type Ingester struct {
	store    *Store
	provider llm.Provider // optional; nil means no LLM-assisted extraction
}

// IngestEvent is a conversation transcript snapshot ready for wiki ingest.
type IngestEvent struct {
	SessionID string
	Messages  []llm.Message
	Reason    string // Free-form trigger label such as "task_completed".
	Principal string
	Resumes   []ResumeRecord
	StartedAt time.Time
	Duration  time.Duration
}

type ResumeRecord struct {
	Surface   string    `json:"surface"`
	Principal string    `json:"principal"`
	At        time.Time `json:"at"`
}

// NewIngester creates an Ingester. provider may be nil for plain ingest without summarisation.
func NewIngester(store *Store, provider llm.Provider) *Ingester {
	return &Ingester{store: store, provider: provider}
}

// IngestSession ingests a pre-snapshotted session transcript.
func (ing *Ingester) IngestSession(ctx context.Context, event IngestEvent) error {
	if ing == nil || ing.store == nil {
		return nil
	}
	if strings.TrimSpace(event.SessionID) == "" {
		return nil
	}
	if len(event.Messages) == 0 {
		return nil
	}

	transcript := renderTranscript(event.Messages)
	summary := ""
	if ing.provider != nil {
		if generated, err := ing.summarise(ctx, event, transcript); err == nil {
			summary = strings.TrimSpace(generated)
		}
	}

	page := &Page{
		Path:    fmt.Sprintf("sessions/%s.md", event.SessionID),
		Title:   fmt.Sprintf("Session %s", event.SessionID),
		Type:    PageTypeSource,
		Content: renderSessionPageContent(event, transcript, summary),
		Tags:    sessionTags(event),
	}
	page.SetSource(SourceIngest, event.SessionID, "ingest_session")
	if err := ing.store.Upsert(page); err != nil {
		return err
	}

	if ing.provider != nil {
		ke := NewKnowledgeExtractor(ing.store, ing.provider, slog.Default())
		if err := ke.ExtractFromConversation(ctx, event.SessionID, event.Messages); err != nil {
			slog.Default().Warn("knowledge extraction failed, source page still saved", "error", err)
		}
	}

	return nil
}

// gitCommit holds the parsed output of a single git log entry.
type gitCommit struct {
	Hash    string
	Subject string
	Author  string
	Date    time.Time
}

// IngestGitLog reads the git log of repoPath since the given time and creates
// or updates wiki pages for significant commits (non-merge, non-empty subject).
func (ing *Ingester) IngestGitLog(ctx context.Context, repoPath string, since time.Time) error {
	sinceStr := since.UTC().Format(time.RFC3339)
	args := []string{
		"-C", repoPath,
		"log",
		"--since=" + sinceStr,
		"--format=%H|%s|%an|%aI",
		"--no-merges",
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("wiki ingest: git log: %w", err)
	}

	commits, err := parseGitLog(string(out))
	if err != nil {
		return fmt.Errorf("wiki ingest: parse git log: %w", err)
	}

	for _, c := range commits {
		if err := ing.ingestCommit(c, repoPath); err != nil {
			// Non-fatal: log and continue.
			continue
		}
	}

	return nil
}

func parseGitLog(raw string) ([]gitCommit, error) {
	var commits []gitCommit
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) != 4 {
			continue
		}
		t, err := time.Parse(time.RFC3339, parts[3])
		if err != nil {
			t = time.Now().UTC()
		}
		commits = append(commits, gitCommit{
			Hash:    parts[0],
			Subject: parts[1],
			Author:  parts[2],
			Date:    t,
		})
	}
	return commits, scanner.Err()
}

func (ing *Ingester) ingestCommit(c gitCommit, repoPath string) error {
	repoName := filepath.Base(repoPath)
	pagePath := fmt.Sprintf("sources/git/%s/%s.md", repoName, c.Hash[:8])

	content := fmt.Sprintf("## Commit %s\n\n**Author:** %s  \n**Date:** %s  \n**Subject:** %s\n",
		c.Hash, c.Author, c.Date.Format(time.RFC3339), c.Subject)

	page := &Page{
		Path:    pagePath,
		Title:   c.Subject,
		Type:    PageTypeSource,
		Content: content,
		Tags:    []string{"git", "commit", repoName},
		Created: c.Date,
		Updated: c.Date,
	}
	page.SetSource(SourceIngest, "", "ingest_git_log")

	return ing.store.Upsert(page)
}

func renderTranscript(messages []llm.Message) string {
	var sb strings.Builder
	for _, message := range messages {
		sb.WriteString(message.Role)
		sb.WriteString(": ")
		sb.WriteString(message.TextContent())
		sb.WriteByte('\n')
	}
	return sb.String()
}

func renderSessionPageContent(event IngestEvent, transcript, summary string) string {
	reason := strings.TrimSpace(event.Reason)
	if reason == "" {
		reason = "unknown"
	}

	var sb strings.Builder
	sb.WriteString("## Session Metadata\n\n")
	sb.WriteString("- **Session ID**: ")
	sb.WriteString(event.SessionID)
	sb.WriteByte('\n')
	sb.WriteString("- **Reason**: ")
	sb.WriteString(reason)
	sb.WriteByte('\n')
	if principal := strings.TrimSpace(event.Principal); principal != "" {
		sb.WriteString("- **Principal**: ")
		sb.WriteString(principal)
		sb.WriteByte('\n')
	}
	for _, resume := range event.Resumes {
		principal := strings.TrimSpace(resume.Principal)
		if principal == "" {
			principal = strings.TrimSpace(resume.Surface)
		}
		if principal == "" {
			continue
		}
		sb.WriteString("- **Resumed by**: ")
		sb.WriteString(principal)
		if !resume.At.IsZero() {
			sb.WriteString(" (")
			sb.WriteString(resume.At.UTC().Format(time.RFC3339))
			sb.WriteString(")")
		}
		sb.WriteByte('\n')
	}
	if !event.StartedAt.IsZero() {
		sb.WriteString("- **Started**: ")
		sb.WriteString(event.StartedAt.UTC().Format(time.RFC3339))
		sb.WriteByte('\n')
	}
	if event.Duration > 0 {
		sb.WriteString("- **Duration**: ")
		sb.WriteString(event.Duration.String())
		sb.WriteByte('\n')
	}

	if summary != "" {
		sb.WriteString("\n## Summary\n\n")
		sb.WriteString(summary)
		sb.WriteByte('\n')
	}

	sb.WriteString("\n## Transcript\n\n```\n")
	sb.WriteString(transcript)
	if transcript == "" || !strings.HasSuffix(transcript, "\n") {
		sb.WriteByte('\n')
	}
	sb.WriteString("```\n")
	return sb.String()
}

func sessionTags(event IngestEvent) []string {
	tags := []string{"session"}
	if reason := strings.TrimSpace(event.Reason); reason != "" {
		tags = append(tags, reason)
	}
	if principal := strings.TrimSpace(event.Principal); principal != "" {
		tags = append(tags, "principal:"+principal)
	}
	return tags
}

// summarise calls the LLM provider to produce a brief, structured summary of
// a session transcript. The prompt embeds session context (principal, reason,
// started_at, duration) so the generated summary is anchored in topic/time
// rather than being free-floating bullets.
func (ing *Ingester) summarise(ctx context.Context, event IngestEvent, transcript string) (string, error) {
	prompt := buildSummarisePrompt(event, transcript)
	resp, err := ing.provider.Chat(ctx, llm.ChatRequest{
		Messages:  []llm.Message{llm.NewUserMessage(prompt)},
		MaxTokens: 512,
	})
	if err != nil {
		return "", fmt.Errorf("wiki ingest: summarise: %w", err)
	}
	return resp.Content, nil
}

// buildSummarisePrompt renders the session context plus structured-output
// instructions in front of the transcript. Missing metadata lines are
// omitted so the prompt stays tight for shorter or system-triggered events.
func buildSummarisePrompt(event IngestEvent, transcript string) string {
	var sb strings.Builder
	sb.WriteString("You are summarising a completed session for the Elnath wiki.\n")
	sb.WriteString("Anchor the summary in the session context below before reading the transcript.\n\n")
	sb.WriteString("## Session Context\n")
	if principal := strings.TrimSpace(event.Principal); principal != "" {
		fmt.Fprintf(&sb, "- Principal: %s\n", principal)
	}
	if reason := strings.TrimSpace(event.Reason); reason != "" {
		fmt.Fprintf(&sb, "- Reason: %s\n", reason)
	}
	if !event.StartedAt.IsZero() {
		fmt.Fprintf(&sb, "- Started: %s\n", event.StartedAt.UTC().Format(time.RFC3339))
	}
	if event.Duration > 0 {
		fmt.Fprintf(&sb, "- Duration: %s\n", event.Duration)
	}
	sb.WriteString("\n## Instructions\n")
	sb.WriteString("Write a compact markdown summary with exactly three labelled sections:\n")
	sb.WriteString("- **Topic**: one sentence naming what the session was about.\n")
	sb.WriteString("- **Decisions**: up to 3 bullets capturing concrete decisions or commitments (write `- none` if no firm decision was made).\n")
	sb.WriteString("- **Outcomes**: up to 3 bullets capturing changed artifacts, follow-ups, or unresolved items.\n")
	sb.WriteString("Keep bullets atomic. Do not restate the session ID or transcript verbatim.\n\n")
	sb.WriteString("## Transcript\n")
	sb.WriteString(transcript)
	return sb.String()
}

// IngestFile reads a file and creates a wiki page of type "source".
func (ing *Ingester) IngestFile(ctx context.Context, filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("wiki ingest: read file %q: %w", filePath, err)
	}

	base := filepath.Base(filePath)
	title := strings.TrimSuffix(base, filepath.Ext(base))
	pagePath := fmt.Sprintf("sources/files/%s.md", base)

	content := fmt.Sprintf("## Source: %s\n\n```\n%s\n```\n", filePath, string(data))

	page := &Page{
		Path:    pagePath,
		Title:   title,
		Type:    PageTypeSource,
		Content: content,
		Tags:    []string{"file", filepath.Ext(filePath)},
	}
	page.SetSource(SourceIngest, "", "ingest_file")

	return ing.store.Upsert(page)
}
