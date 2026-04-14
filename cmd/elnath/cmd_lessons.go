package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/learning"
)

const archiveScanMaxTokenSize = 8 * 1024 * 1024

func cmdLessons(_ context.Context, args []string) error {
	if len(args) == 0 {
		return printLessonsUsage()
	}
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		return printLessonsUsage()
	}

	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	activePath := filepath.Join(cfg.DataDir, "lessons.jsonl")
	store := learning.NewStore(activePath)

	switch args[0] {
	case "list":
		return lessonsList(store, args[1:])
	case "show":
		return lessonsShow(store, args[1:])
	case "clear":
		return lessonsClear(store, args[1:])
	case "rotate":
		return lessonsRotate(store, activePath, args[1:])
	case "stats":
		return lessonsStats(store, activePath, args[1:])
	case "help", "-h", "--help":
		return printLessonsUsage()
	default:
		return fmt.Errorf("unknown lessons subcommand: %s (try: elnath lessons help)", args[0])
	}
}

func printLessonsUsage() error {
	fmt.Println(`Usage: elnath lessons <subcommand> [args]

Subcommands:
  list               List stored lessons
  show <id>          Show one lesson in detail
  clear              Delete lessons by filter or clear all
  rotate             Move old lessons into the archive
  stats              Show lesson summary statistics
  help               Show this help`)
	return nil
}

func lessonsList(store *learning.Store, args []string) error {
	fs := flag.NewFlagSet("lessons-list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	topic := fs.String("topic", "", "")
	confidence := fs.String("confidence", "", "")
	source := fs.String("source", "", "")
	sinceRaw := fs.String("since", "", "")
	beforeRaw := fs.String("before", "", "")
	limit := fs.Int("limit", 50, "")
	newest := fs.Bool("newest", false, "")
	asJSON := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}

	since, err := parseTimeFlag(*sinceRaw)
	if err != nil {
		return err
	}
	before, err := parseTimeFlag(*beforeRaw)
	if err != nil {
		return err
	}

	lessons, err := store.ListFiltered(learning.Filter{
		Topic:      *topic,
		Confidence: *confidence,
		Source:     *source,
		Since:      since,
		Before:     before,
		Limit:      *limit,
		Reverse:    *newest,
	})
	if err != nil {
		return err
	}
	if *asJSON {
		if len(lessons) == 0 {
			return nil
		}
		enc := json.NewEncoder(os.Stdout)
		for _, lesson := range lessons {
			if err := enc.Encode(lesson); err != nil {
				return fmt.Errorf("encode lesson: %w", err)
			}
		}
		return nil
	}
	if len(lessons) == 0 {
		fmt.Println("No lessons found.")
		return nil
	}

	for _, lesson := range lessons {
		fmt.Printf("%s  [%s] %s  %s\n", lesson.Created.Format("2006-01-02"), lesson.Confidence, lesson.Topic, lesson.ID)
		fmt.Printf("  %s\n\n", lesson.Text)
	}
	return nil
}

func lessonsShow(store *learning.Store, args []string) error {
	fs := flag.NewFlagSet("lessons-show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: elnath lessons show <id>")
	}

	prefix := strings.TrimSpace(fs.Arg(0))
	lessons, err := store.ListFiltered(learning.Filter{IDs: []string{prefix}})
	if err != nil {
		return err
	}
	if len(lessons) == 0 {
		return fmt.Errorf("no lesson matched prefix %q", prefix)
	}
	if len(lessons) > 1 {
		return fmt.Errorf("ambiguous prefix %q: %d matches", prefix, len(lessons))
	}

	lesson := lessons[0]
	fmt.Printf("ID:         %s\n", lesson.ID)
	fmt.Printf("Created:    %s\n", lesson.Created.Format(time.RFC3339))
	fmt.Printf("Topic:      %s\n", lesson.Topic)
	fmt.Printf("Confidence: %s\n", lesson.Confidence)
	fmt.Printf("Source:     %s\n\n", lesson.Source)
	fmt.Println("Text:")
	fmt.Printf("  %s\n\n", lesson.Text)
	fmt.Println("Persona delta:")
	if len(lesson.PersonaDelta) == 0 {
		fmt.Println("  (none)")
		return nil
	}
	for _, delta := range lesson.PersonaDelta {
		fmt.Printf("  %s  %+.3f\n", delta.Param, delta.Delta)
	}
	return nil
}

func lessonsClear(store *learning.Store, args []string) error {
	fs := flag.NewFlagSet("lessons-clear", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var ids stringList
	fs.Var(&ids, "id", "")
	topic := fs.String("topic", "", "")
	confidence := fs.String("confidence", "", "")
	beforeRaw := fs.String("before", "", "")
	all := fs.Bool("all", false, "")
	dryRun := fs.Bool("dry-run", false, "")
	yes := fs.Bool("y", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}

	hasOtherFilters := len(ids) > 0 || strings.TrimSpace(*topic) != "" || strings.TrimSpace(*confidence) != "" || strings.TrimSpace(*beforeRaw) != ""
	hasNonIDFilters := strings.TrimSpace(*topic) != "" || strings.TrimSpace(*confidence) != "" || strings.TrimSpace(*beforeRaw) != ""
	if *all && hasOtherFilters {
		return fmt.Errorf("--all cannot be combined with --id, --topic, --confidence, or --before")
	}
	if !*all && !hasOtherFilters {
		return fmt.Errorf("specify --id, --topic, --confidence, --before, or --all")
	}

	before, err := parseTimeFlag(*beforeRaw)
	if err != nil {
		return err
	}

	hadIDs := len(ids) > 0
	resolvedIDs, err := resolveLessonIDs(store, ids)
	if err != nil {
		return err
	}
	if hadIDs && len(resolvedIDs) == 0 && !hasNonIDFilters {
		if *dryRun {
			fmt.Printf("Would delete %d lesson(s). Run without --dry-run to apply.\n", 0)
			return nil
		}
		fmt.Printf("Deleted %d lesson(s).\n", 0)
		return nil
	}
	filter := learning.Filter{
		Topic:      *topic,
		Confidence: *confidence,
		Before:     before,
		IDs:        resolvedIDs,
	}

	if *all {
		lessons, err := store.List()
		if err != nil {
			return err
		}
		if *dryRun {
			fmt.Printf("Would delete %d lesson(s). Run without --dry-run to apply.\n", len(lessons))
			return nil
		}
		if !*yes {
			if !isTTY(os.Stdin) {
				return fmt.Errorf("refusing to clear all lessons without -y when stdin is not a TTY")
			}
			confirmed, err := confirmClearAll(len(lessons))
			if err != nil {
				return err
			}
			if !confirmed {
				return fmt.Errorf("clear all cancelled")
			}
		}
		removed, err := store.Clear()
		if err != nil {
			return err
		}
		fmt.Printf("Deleted %d lesson(s).\n", removed)
		return nil
	}

	matches, err := store.ListFiltered(filter)
	if err != nil {
		return err
	}
	if *dryRun {
		fmt.Printf("Would delete %d lesson(s). Run without --dry-run to apply.\n", len(matches))
		return nil
	}
	removed, err := store.DeleteMatching(filter)
	if err != nil {
		return err
	}
	fmt.Printf("Deleted %d lesson(s).\n", removed)
	return nil
}

func lessonsRotate(store *learning.Store, activePath string, args []string) error {
	fs := flag.NewFlagSet("lessons-rotate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	keep := fs.Int("keep", 0, "")
	maxBytesRaw := fs.String("max-bytes", "", "")
	if err := fs.Parse(args); err != nil {
		return err
	}

	maxBytes, err := parseBytesFlag(*maxBytesRaw)
	if err != nil {
		return err
	}
	if *keep <= 0 && maxBytes == 0 {
		return fmt.Errorf("rotate requires --keep or --max-bytes")
	}

	removed, err := store.Rotate(learning.RotateOpts{KeepLast: *keep, MaxBytes: maxBytes})
	if err != nil {
		return err
	}
	stats, err := store.Summary()
	if err != nil {
		return err
	}
	archiveLines, archiveBytes, err := archiveMetrics(activePath)
	if err != nil {
		return err
	}
	fmt.Printf("Rotated %d lesson(s). Active: %s (%d entries, %s). Archive: %s (total %d entries, %s).\n",
		removed,
		activePath,
		stats.Total,
		formatBytes(stats.FileBytes),
		lessonsArchivePath(activePath),
		archiveLines,
		formatBytes(archiveBytes),
	)
	return nil
}

func lessonsStats(store *learning.Store, activePath string, args []string) error {
	fs := flag.NewFlagSet("lessons-stats", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	asJSON := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}

	stats, err := store.Summary()
	if err != nil {
		return err
	}
	archiveLines, archiveBytes, err := archiveMetrics(activePath)
	if err != nil {
		return err
	}

	if *asJSON {
		payload := struct {
			learning.Stats
			ArchiveBytes int64 `json:"archive_bytes"`
			ArchiveLines int   `json:"archive_lines"`
		}{
			Stats:        stats,
			ArchiveBytes: archiveBytes,
			ArchiveLines: archiveLines,
		}
		return json.NewEncoder(os.Stdout).Encode(payload)
	}

	fmt.Printf("Active file: %s (%s)\n", activePath, formatBytes(stats.FileBytes))
	fmt.Printf("Archive: %s (%d entries, %s)\n", lessonsArchivePath(activePath), archiveLines, formatBytes(archiveBytes))
	if stats.Total == 0 {
		fmt.Println("Total: 0 lessons")
		fmt.Println("Range: -")
	} else {
		fmt.Printf("Total: %d lessons\n", stats.Total)
		fmt.Printf("Range: %s -> %s\n", stats.OldestAt.Format("2006-01-02"), stats.NewestAt.Format("2006-01-02"))
	}

	fmt.Println()
	fmt.Println("By confidence:")
	for _, confidence := range []string{"high", "medium", "low"} {
		fmt.Printf("  %-6s %d\n", confidence, stats.ByConfidence[confidence])
	}

	fmt.Println()
	fmt.Println("By source:")
	sources := make([]sourceCount, 0, len(stats.BySource))
	for src, count := range stats.BySource {
		label := src
		if label == "" {
			label = "(empty)"
		}
		sources = append(sources, sourceCount{Source: label, Count: count})
	}
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].Count == sources[j].Count {
			return sources[i].Source < sources[j].Source
		}
		return sources[i].Count > sources[j].Count
	})
	for _, source := range sources {
		fmt.Printf("  %-18s %d\n", source.Source, source.Count)
	}

	fmt.Println()
	fmt.Println("By topic (top 10):")
	topics := make([]topicCount, 0, len(stats.ByTopic))
	for topic, count := range stats.ByTopic {
		topics = append(topics, topicCount{Topic: topic, Count: count})
	}
	sort.Slice(topics, func(i, j int) bool {
		if topics[i].Count == topics[j].Count {
			return topics[i].Topic < topics[j].Topic
		}
		return topics[i].Count > topics[j].Count
	})
	if len(topics) > 10 {
		topics = topics[:10]
	}
	for _, topic := range topics {
		fmt.Printf("  %-16s %d\n", topic.Topic, topic.Count)
	}
	return nil
}

func parseTimeFlag(raw string) (time.Time, error) {
	original := strings.TrimSpace(raw)
	if original == "" {
		return time.Time{}, nil
	}
	if d, err := time.ParseDuration(original); err == nil && d > 0 {
		return time.Now().UTC().Add(-d), nil
	}
	if parsed, err := time.Parse(time.RFC3339, original); err == nil {
		return parsed.UTC(), nil
	}
	if strings.HasSuffix(original, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(original, "d"))
		if err == nil && n > 0 {
			return time.Now().UTC().Add(-time.Duration(n) * 24 * time.Hour), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid time %q (expected RFC3339 or duration like 7d/24h)", original)
}

func parseBytesFlag(raw string) (int64, error) {
	original := strings.TrimSpace(raw)
	normalized := strings.ToUpper(original)
	if normalized == "" {
		return 0, nil
	}

	multiplier := int64(1)
	switch {
	case strings.HasSuffix(normalized, "KB"):
		multiplier = 1024
		normalized = strings.TrimSuffix(normalized, "KB")
	case strings.HasSuffix(normalized, "MB"):
		multiplier = 1024 * 1024
		normalized = strings.TrimSuffix(normalized, "MB")
	case strings.HasSuffix(normalized, "GB"):
		multiplier = 1024 * 1024 * 1024
		normalized = strings.TrimSuffix(normalized, "GB")
	}

	n, err := strconv.ParseInt(strings.TrimSpace(normalized), 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid byte size %q", original)
	}
	return n * multiplier, nil
}

type stringList []string

func (s *stringList) String() string {
	return strings.Join(*s, ",")
}

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func resolveLessonIDs(store *learning.Store, prefixes []string) ([]string, error) {
	if len(prefixes) == 0 {
		return nil, nil
	}

	resolved := make([]string, 0, len(prefixes))
	seen := make(map[string]struct{}, len(prefixes))
	for _, prefix := range prefixes {
		trimmed := strings.TrimSpace(prefix)
		if trimmed == "" {
			continue
		}
		matches, err := store.ListFiltered(learning.Filter{IDs: []string{trimmed}})
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			continue
		}
		if len(matches) > 1 {
			return nil, fmt.Errorf("ambiguous prefix %q: %d matches", trimmed, len(matches))
		}
		if _, ok := seen[matches[0].ID]; ok {
			continue
		}
		seen[matches[0].ID] = struct{}{}
		resolved = append(resolved, matches[0].ID)
	}
	return resolved, nil
}

func confirmClearAll(count int) (bool, error) {
	fmt.Printf("Delete ALL %d lessons? Archive is untouched. [y/N] ", count)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, fmt.Errorf("read confirmation: %w", err)
	}
	return strings.EqualFold(strings.TrimSpace(line), "y"), nil
}

func lessonsArchivePath(activePath string) string {
	if strings.HasSuffix(activePath, ".jsonl") {
		return strings.TrimSuffix(activePath, ".jsonl") + ".archive.jsonl"
	}
	return activePath + ".archive"
}

func archiveMetrics(activePath string) (int, int64, error) {
	archivePath := lessonsArchivePath(activePath)
	file, err := os.Open(archivePath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("open archive: %w", err)
	}
	defer file.Close()

	count := 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), archiveScanMaxTokenSize)
	for scanner.Scan() {
		count++
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, fmt.Errorf("scan archive: %w", err)
	}
	fi, err := file.Stat()
	if err != nil {
		return 0, 0, fmt.Errorf("stat archive: %w", err)
	}
	return count, fi.Size(), nil
}

type topicCount struct {
	Topic string
	Count int
}

type sourceCount struct {
	Source string
	Count  int
}

func formatBytes(size int64) string {
	switch {
	case size >= 1024*1024*1024:
		return fmt.Sprintf("%.1f GB", float64(size)/(1024*1024*1024))
	case size >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
	case size >= 1024:
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	default:
		return fmt.Sprintf("%d B", size)
	}
}
