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
}

// NewIngester creates an Ingester. provider may be nil for plain ingest without summarisation.
func NewIngester(store *Store, provider llm.Provider) *Ingester {
	return &Ingester{store: store, provider: provider}
}

// IngestEvent ingests a pre-snapshotted conversation transcript.
func (ing *Ingester) IngestEvent(ctx context.Context, event IngestEvent) error {
	if ing == nil || ing.store == nil {
		return nil
	}
	if strings.TrimSpace(event.SessionID) == "" {
		return nil
	}
	if len(event.Messages) == 0 {
		return nil
	}
	return ing.IngestConversation(ctx, event.SessionID, event.Messages)
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

	return ing.store.Upsert(page)
}

// IngestConversation extracts notable facts from a list of conversation turns
// and creates wiki pages for each. If a provider is available, it is used for
// LLM-assisted extraction; otherwise a simple heuristic is applied.
func (ing *Ingester) IngestConversation(ctx context.Context, sessionID string, turns []llm.Message) error {
	if len(turns) == 0 {
		return nil
	}

	// Concatenate all turns into a readable transcript.
	var sb strings.Builder
	for _, t := range turns {
		sb.WriteString(t.Role)
		sb.WriteString(": ")
		sb.WriteString(t.TextContent())
		sb.WriteByte('\n')
	}
	transcript := sb.String()

	// Derive a simple summary page from the session transcript.
	title := fmt.Sprintf("Session %s", sessionID)
	pagePath := fmt.Sprintf("sources/conversations/%s.md", sessionID)

	content := fmt.Sprintf("## Conversation Transcript\n\n```\n%s\n```\n", transcript)

	if ing.provider != nil {
		summary, err := ing.summarise(ctx, transcript)
		if err == nil && summary != "" {
			content = fmt.Sprintf("## Summary\n\n%s\n\n## Transcript\n\n```\n%s\n```\n", summary, transcript)
		}
	}

	page := &Page{
		Path:    pagePath,
		Title:   title,
		Type:    PageTypeSource,
		Content: content,
		Tags:    []string{"conversation", sessionID},
	}

	if err := ing.store.Upsert(page); err != nil {
		return err
	}

	// Knowledge extraction: create structured entity/concept pages from the conversation.
	if ing.provider != nil {
		ke := NewKnowledgeExtractor(ing.store, ing.provider, slog.Default())
		if err := ke.ExtractFromConversation(ctx, sessionID, turns); err != nil {
			slog.Default().Warn("knowledge extraction failed, source page still saved", "error", err)
		}
	}

	return nil
}

// summarise calls the LLM provider to produce a brief summary of a transcript.
func (ing *Ingester) summarise(ctx context.Context, transcript string) (string, error) {
	prompt := "Summarise the following conversation in 3-5 bullet points:\n\n" + transcript
	resp, err := ing.provider.Chat(ctx, llm.ChatRequest{
		Messages:  []llm.Message{llm.NewUserMessage(prompt)},
		MaxTokens: 512,
	})
	if err != nil {
		return "", fmt.Errorf("wiki ingest: summarise: %w", err)
	}
	return resp.Content, nil
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

	return ing.store.Upsert(page)
}
