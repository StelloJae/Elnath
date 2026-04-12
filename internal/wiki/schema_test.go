package wiki

import (
	"reflect"
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

func TestFrontmatterExtraRoundTrip(t *testing.T) {
	raw := `---
title: Project Routing Preferences
type: concept
tags:
  - routing
preferred_workflows:
  question: research
avoid_workflows:
  - team
custom_flag: true
custom_nested:
  mode: strict
---

Body text.
`

	page, err := ParseFrontmatter([]byte(raw))
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}
	if page.Extra == nil {
		t.Fatal("expected Extra to be populated")
	}

	rendered, err := RenderFrontmatter(page)
	if err != nil {
		t.Fatalf("RenderFrontmatter: %v", err)
	}

	roundTrip, err := ParseFrontmatter(rendered)
	if err != nil {
		t.Fatalf("ParseFrontmatter(round trip): %v", err)
	}

	if !reflect.DeepEqual(roundTrip.Extra, page.Extra) {
		t.Fatalf("Extra = %#v, want %#v", roundTrip.Extra, page.Extra)
	}
	if roundTrip.Title != page.Title {
		t.Fatalf("Title = %q, want %q", roundTrip.Title, page.Title)
	}
	if strings.TrimSpace(roundTrip.Content) != "Body text." {
		t.Fatalf("Content = %q, want Body text.", roundTrip.Content)
	}
}
