package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

type FileMutation struct {
	Operation     string `json:"operation"`
	Path          string `json:"path"`
	Changed       bool   `json:"changed"`
	BeforeExists  bool   `json:"before_exists"`
	AfterExists   bool   `json:"after_exists"`
	BeforeHash    string `json:"before_hash,omitempty"`
	AfterHash     string `json:"after_hash,omitempty"`
	BeforeLines   int    `json:"before_lines"`
	AfterLines    int    `json:"after_lines"`
	LineDelta     int    `json:"line_delta"`
	FailureFamily string `json:"failure_family,omitempty"`
}

func NewFileMutation(operation string, path string, before []byte, beforeExists bool, after []byte, afterExists bool) *FileMutation {
	beforeLines := countMutationLines(before, beforeExists)
	afterLines := countMutationLines(after, afterExists)
	return &FileMutation{
		Operation:    strings.TrimSpace(operation),
		Path:         cleanMutationPath(path),
		Changed:      beforeExists != afterExists || !bytes.Equal(before, after),
		BeforeExists: beforeExists,
		AfterExists:  afterExists,
		BeforeHash:   mutationHash(before, beforeExists),
		AfterHash:    mutationHash(after, afterExists),
		BeforeLines:  beforeLines,
		AfterLines:   afterLines,
		LineDelta:    afterLines - beforeLines,
	}
}

func MutationDisplayPath(ctx context.Context, guard *PathGuard, abs string, fallback string) string {
	if guard != nil {
		if base, err := SessionWorkDirFromContext(ctx, guard); err == nil && strings.TrimSpace(base) != "" {
			return relPath(base, abs)
		}
		if workDir := guard.WorkDir(); strings.TrimSpace(workDir) != "" {
			return relPath(workDir, abs)
		}
	}
	return cleanMutationPath(fallback)
}

func mutationHash(data []byte, exists bool) string {
	if !exists {
		return ""
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func countMutationLines(data []byte, exists bool) int {
	if !exists || len(data) == 0 {
		return 0
	}
	count := bytes.Count(data, []byte("\n"))
	if !bytes.HasSuffix(data, []byte("\n")) {
		count++
	}
	return count
}

func cleanMutationPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(path))
}
