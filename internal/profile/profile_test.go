package profile

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
		want *Profile
	}{
		{
			name: "valid profile page",
			page: &wiki.Page{
				Tags:    []string{"profile"},
				Content: "You are a strict code reviewer.",
				Extra: map[string]any{
					"name":           "code-reviewer",
					"model":          "claude-sonnet-4-6",
					"tools":          []string{"read_file", "grep", "bash"},
					"max_iterations": 20,
				},
			},
			want: &Profile{
				Name:          "code-reviewer",
				Model:         "claude-sonnet-4-6",
				Tools:         []string{"read_file", "grep", "bash"},
				MaxIterations: 20,
				SystemExtra:   "You are a strict code reviewer.",
			},
		},
		{
			name: "missing profile tag",
			page: &wiki.Page{
				Tags: []string{"skill"},
				Extra: map[string]any{
					"name": "code-reviewer",
				},
			},
		},
		{
			name: "missing name extra",
			page: &wiki.Page{
				Tags:  []string{"profile"},
				Extra: map[string]any{},
			},
		},
		{
			name: "empty name",
			page: &wiki.Page{
				Tags: []string{"profile"},
				Extra: map[string]any{
					"name": "",
				},
			},
		},
		{
			name: "empty tools",
			page: &wiki.Page{
				Tags: []string{"profile"},
				Extra: map[string]any{
					"name":  "minimal",
					"tools": []string{},
				},
			},
			want: &Profile{
				Name: "minimal",
			},
		},
		{
			name: "max_iterations as float64",
			page: &wiki.Page{
				Tags: []string{"profile"},
				Extra: map[string]any{
					"name":           "researcher",
					"max_iterations": float64(50),
				},
			},
			want: &Profile{
				Name:          "researcher",
				MaxIterations: 50,
			},
		},
		{
			name: "max_iterations as string",
			page: &wiki.Page{
				Tags: []string{"profile"},
				Extra: map[string]any{
					"name":           "researcher",
					"max_iterations": "30",
				},
			},
			want: &Profile{
				Name:          "researcher",
				MaxIterations: 30,
			},
		},
		{
			name: "tools from any slice",
			page: &wiki.Page{
				Tags: []string{"profile"},
				Extra: map[string]any{
					"name":  "researcher",
					"tools": []any{"bash", "read_file", "web_fetch"},
				},
			},
			want: &Profile{
				Name:  "researcher",
				Tools: []string{"bash", "read_file", "web_fetch"},
			},
		},
		{
			name: "body content populates SystemExtra",
			page: &wiki.Page{
				Tags:    []string{"profile"},
				Content: "You are a thorough researcher.\nCite sources.",
				Extra: map[string]any{
					"name": "researcher",
				},
			},
			want: &Profile{
				Name:        "researcher",
				SystemExtra: "You are a thorough researcher.\nCite sources.",
			},
		},
		{
			name: "nil page",
			page: nil,
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

func TestLoadAll(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := wiki.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	profilePage := &wiki.Page{
		Path:    "profiles/code-reviewer.md",
		Title:   "Code Reviewer",
		Tags:    []string{"profile"},
		Content: "Review code.",
		Extra: map[string]any{
			"name":           "code-reviewer",
			"model":          "claude-sonnet-4-6",
			"tools":          []string{"read_file", "bash"},
			"max_iterations": 20,
		},
	}
	nonProfilePage := &wiki.Page{
		Path:    "skills/deploy.md",
		Title:   "Deploy",
		Tags:    []string{"skill"},
		Content: "Deploy stuff.",
		Extra: map[string]any{
			"name": "deploy",
		},
	}

	if err := store.Create(profilePage); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(nonProfilePage); err != nil {
		t.Fatal(err)
	}

	profiles, err := LoadAll(store)
	if err != nil {
		t.Fatal(err)
	}

	if len(profiles) != 1 {
		t.Fatalf("LoadAll() returned %d profiles, want 1", len(profiles))
	}

	p, ok := profiles["code-reviewer"]
	if !ok {
		t.Fatal("LoadAll() missing code-reviewer profile")
	}
	if p.Model != "claude-sonnet-4-6" {
		t.Fatalf("profile model = %q, want %q", p.Model, "claude-sonnet-4-6")
	}
	if p.MaxIterations != 20 {
		t.Fatalf("profile max_iterations = %d, want 20", p.MaxIterations)
	}
}

func TestSortedNames(t *testing.T) {
	t.Parallel()

	profiles := map[string]*Profile{
		"zebra":    {Name: "zebra"},
		"alpha":    {Name: "alpha"},
		"middle":   {Name: "middle"},
	}
	got := SortedNames(profiles)
	want := []string{"alpha", "middle", "zebra"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SortedNames() = %v, want %v", got, want)
	}
}
