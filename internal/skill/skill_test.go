package skill

import (
	"reflect"
	"testing"

	"github.com/stello/elnath/internal/wiki"
)

func TestFromPage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		page *wiki.Page
		want *Skill
	}{
		{
			name: "skill page",
			page: &wiki.Page{
				Tags:    []string{"skill"},
				Content: "Review PR {pr_number}",
				Extra: map[string]any{
					"name":           "pr-review",
					"description":    "Review PRs",
					"trigger":        "/pr-review <pr_number>",
					"required_tools": []string{"bash", "read_file"},
					"model":          "gpt-5",
				},
			},
			want: &Skill{
				Name:          "pr-review",
				Description:   "Review PRs",
				Trigger:       "/pr-review <pr_number>",
				RequiredTools: []string{"bash", "read_file"},
				Model:         "gpt-5",
				Prompt:        "Review PR {pr_number}",
				Status:        "active",
			},
		},
		{
			name: "skill page with status and source",
			page: &wiki.Page{
				Tags:    []string{"skill"},
				Content: "Deploy {env}",
				Extra: map[string]any{
					"name":        "deploy-check",
					"description": "Check deployment status",
					"status":      "draft",
					"source":      "analyst",
				},
			},
			want: &Skill{
				Name:        "deploy-check",
				Description: "Check deployment status",
				Prompt:      "Deploy {env}",
				Status:      "draft",
				Source:      "analyst",
			},
		},
		{
			name: "missing skill tag",
			page: &wiki.Page{
				Tags: []string{"analysis"},
				Extra: map[string]any{
					"name": "pr-review",
				},
			},
		},
		{
			name: "missing name",
			page: &wiki.Page{
				Tags:  []string{"skill"},
				Extra: map[string]any{},
			},
		},
		{
			name: "empty name",
			page: &wiki.Page{
				Tags: []string{"skill"},
				Extra: map[string]any{
					"name": "",
				},
			},
		},
		{
			name: "required tools from any slice",
			page: &wiki.Page{
				Tags: []string{"skill"},
				Extra: map[string]any{
					"name":           "audit-security",
					"required_tools": []any{"bash", "read_file"},
				},
			},
			want: &Skill{
				Name:          "audit-security",
				RequiredTools: []string{"bash", "read_file"},
				Status:        "active",
			},
		},
		{
			name: "missing required tools",
			page: &wiki.Page{
				Tags: []string{"skill"},
				Extra: map[string]any{
					"name": "audit-security",
				},
			},
			want: &Skill{
				Name:   "audit-security",
				Status: "active",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := FromPage(tt.page)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("FromPage() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestRenderPrompt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		skill *Skill
		args  map[string]string
		want  string
	}{
		{
			name:  "single placeholder",
			skill: &Skill{Prompt: "Review PR #{pr_number}"},
			args:  map[string]string{"pr_number": "42"},
			want:  "Review PR #42",
		},
		{
			name:  "multiple placeholders",
			skill: &Skill{Prompt: "Compare {base} to {head}"},
			args:  map[string]string{"base": "main", "head": "feature"},
			want:  "Compare main to feature",
		},
		{
			name:  "missing placeholder stays",
			skill: &Skill{Prompt: "Review PR #{pr_number}"},
			args:  map[string]string{"issue_number": "42"},
			want:  "Review PR #{pr_number}",
		},
		{
			name:  "claude arguments placeholder",
			skill: &Skill{Prompt: "Review $ARGUMENTS with {target}"},
			args:  map[string]string{"ARGUMENTS": "PR 42", "target": "tests"},
			want:  "Review PR 42 with tests",
		},
		{
			name:  "claude named dollar placeholders",
			skill: &Skill{Prompt: "Review $pr_number on $branch; keep $branch_name literal"},
			args:  map[string]string{"pr_number": "42", "branch": "main"},
			want:  "Review 42 on main; keep $branch_name literal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.skill.RenderPrompt(tt.args); got != tt.want {
				t.Fatalf("RenderPrompt() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderRuntimePlaceholders(t *testing.T) {
	t.Parallel()

	got := renderRuntimePlaceholders(
		"Run ${CLAUDE_SKILL_DIR}/scripts/check.sh for ${CLAUDE_SESSION_ID}; mirror ${ELNATH_SKILL_DIR} ${ELNATH_SESSION_ID}.",
		"/tmp/skill",
		"session-123",
	)
	want := "Run /tmp/skill/scripts/check.sh for session-123; mirror /tmp/skill session-123."
	if got != want {
		t.Fatalf("renderRuntimePlaceholders() = %q, want %q", got, want)
	}
}

func TestRenderPromptWithRuntimeKeepsUserArgsLiteral(t *testing.T) {
	t.Parallel()

	sk := &Skill{
		Prompt: "Run ${CLAUDE_SKILL_DIR}/scripts/check.sh with {literal}.",
	}
	got := sk.RenderPromptWithRuntime(
		map[string]string{"literal": "${CLAUDE_SESSION_ID}"},
		"/tmp/skill",
		"session-123",
	)
	want := "Run /tmp/skill/scripts/check.sh with ${CLAUDE_SESSION_ID}."
	if got != want {
		t.Fatalf("RenderPromptWithRuntime() = %q, want %q", got, want)
	}
}
