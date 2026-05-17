package agent

import (
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/tools"
)

const mutationVerifierFooterHeader = "[Filesystem mutation verifier]"

func formatMutationVerifierFooter(mutations []*tools.FileMutation) string {
	mutations = filterMutationRecords(mutations)
	if len(mutations) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(mutationVerifierFooterHeader)
	b.WriteByte('\n')
	for _, mutation := range mutations {
		fmt.Fprintf(&b,
			"- %s %s changed=%t before_exists=%t after_exists=%t lines=%d->%d line_delta=%+d",
			emptyAsUnknown(mutation.Operation),
			emptyAsUnknown(mutation.Path),
			mutation.Changed,
			mutation.BeforeExists,
			mutation.AfterExists,
			mutation.BeforeLines,
			mutation.AfterLines,
			mutation.LineDelta,
		)
		if mutation.BeforeHash != "" || mutation.AfterHash != "" {
			fmt.Fprintf(&b, " hash=%s->%s", emptyAsNone(mutation.BeforeHash), emptyAsNone(mutation.AfterHash))
		}
		if mutation.DiagnosticStatus != "" {
			fmt.Fprintf(&b,
				" diagnostics=%s language=%s new=%d existing=%d resolved=%d",
				mutation.DiagnosticStatus,
				emptyAsUnknown(mutation.DiagnosticLanguage),
				mutation.NewDiagnosticCount,
				mutation.ExistingDiagnosticCount,
				mutation.ResolvedDiagnosticCount,
			)
			for i, diagnostic := range mutation.NewDiagnostics {
				if i >= 3 {
					break
				}
				fmt.Fprintf(&b, " new_diag_%d=%s:%d:%d:%s",
					i+1,
					emptyAsUnknown(diagnostic.Source),
					diagnostic.Line,
					diagnostic.Column,
					compactMutationDiagnosticError(diagnostic.Error),
				)
			}
		}
		if mutation.FailureFamily != "" {
			fmt.Fprintf(&b, " failure_family=%s", mutation.FailureFamily)
		}
		b.WriteByte('\n')
	}
	b.WriteString("Use this verified disk-delta evidence before claiming file changes. If an intended edit has no matching changed=true mutation, continue with the smallest corrective action or report incomplete work.")
	return b.String()
}

func filterMutationRecords(mutations []*tools.FileMutation) []*tools.FileMutation {
	out := make([]*tools.FileMutation, 0, len(mutations))
	for _, mutation := range mutations {
		if mutation == nil {
			continue
		}
		if strings.TrimSpace(mutation.Operation) == "" && strings.TrimSpace(mutation.Path) == "" {
			continue
		}
		out = append(out, mutation)
	}
	return out
}

func emptyAsUnknown(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "(unknown)"
	}
	return value
}

func emptyAsNone(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "none"
	}
	return value
}

func compactMutationDiagnosticError(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\t", " ")
	if value == "" {
		return "(unknown)"
	}
	const maxDiagnosticErrorLen = 96
	if len(value) <= maxDiagnosticErrorLen {
		return value
	}
	return value[:maxDiagnosticErrorLen] + "..."
}
