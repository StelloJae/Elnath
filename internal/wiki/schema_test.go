package wiki

import (
	"strings"
	"testing"
)

func TestParseFrontmatter(t *testing.T) {
	raw := `---
title: My Concept
type: concept
tags:
  - go
  - testing
confidence: high
---

This is the body of the page.
`
	page, err := ParseFrontmatter([]byte(raw))
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}

	if page.Title != "My Concept" {
		t.Errorf("Title = %q, want %q", page.Title, "My Concept")
	}
	if page.Type != PageTypeConcept {
		t.Errorf("Type = %q, want %q", page.Type, PageTypeConcept)
	}
	if page.Confidence != "high" {
		t.Errorf("Confidence = %q, want %q", page.Confidence, "high")
	}
	if len(page.Tags) != 2 {
		t.Errorf("Tags = %v, want 2 elements", page.Tags)
	}
	if !strings.Contains(page.Content, "body of the page") {
		t.Errorf("Content = %q, expected body text", page.Content)
	}
}

func TestParseFrontmatterMissing(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{
			name: "no frontmatter delimiter",
			raw:  "Just plain markdown without any frontmatter.\n",
		},
		{
			name: "missing closing delimiter",
			raw:  "---\ntitle: Broken\n",
		},
		{
			name: "missing required title field",
			raw:  "---\ntype: concept\n---\nbody\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseFrontmatter([]byte(tc.raw))
			if err == nil {
				t.Errorf("ParseFrontmatter(%q) = nil error, want error", tc.name)
			}
		})
	}
}
