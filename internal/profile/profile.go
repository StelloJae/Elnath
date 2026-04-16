package profile

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/stello/elnath/internal/wiki"
)

type Profile struct {
	Name          string
	Model         string
	Tools         []string
	MaxIterations int
	SystemExtra   string
}

func FromPage(page *wiki.Page) *Profile {
	if page == nil || !hasTag(page.Tags, "profile") {
		return nil
	}

	name, ok := stringExtra(page.Extra, "name")
	if !ok || name == "" {
		return nil
	}

	return &Profile{
		Name:          name,
		Model:         extraString(page.Extra, "model"),
		Tools:         extraStrings(page.Extra, "tools"),
		MaxIterations: extraInt(page.Extra, "max_iterations"),
		SystemExtra:   page.Content,
	}
}

func LoadAll(store *wiki.Store) (map[string]*Profile, error) {
	pages, err := store.List()
	if err != nil {
		return nil, fmt.Errorf("list pages: %w", err)
	}

	profiles := make(map[string]*Profile)
	for _, page := range pages {
		p := FromPage(page)
		if p == nil {
			continue
		}
		profiles[p.Name] = p
	}
	return profiles, nil
}

func hasTag(tags []string, want string) bool {
	for _, tag := range tags {
		if tag == want {
			return true
		}
	}
	return false
}

func extraString(extra map[string]any, key string) string {
	value, _ := stringExtra(extra, key)
	return value
}

func stringExtra(extra map[string]any, key string) (string, bool) {
	if extra == nil {
		return "", false
	}
	value, ok := extra[key].(string)
	return value, ok
}

func extraStrings(extra map[string]any, key string) []string {
	if extra == nil {
		return nil
	}

	raw, ok := extra[key]
	if !ok {
		return nil
	}

	switch values := raw.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			out = append(out, fmt.Sprintf("%v", value))
		}
		return out
	default:
		return nil
	}
}

func extraInt(extra map[string]any, key string) int {
	if extra == nil {
		return 0
	}

	raw, ok := extra[key]
	if !ok {
		return 0
	}

	switch v := raw.(type) {
	case int:
		return v
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(v)
		return n
	default:
		return 0
	}
}

// SortedNames returns profile names in alphabetical order.
func SortedNames(profiles map[string]*Profile) []string {
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
