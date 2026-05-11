package skill

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ConditionalSkillMatch records why a conditional skill matched a file path.
type ConditionalSkillMatch struct {
	SkillName string
	Pattern   string
	Path      string
}

// ConditionalMatchesForPaths returns conditional skill matches for the given
// file paths without mutating the registry or activating skills.
func (r *Registry) ConditionalMatchesForPaths(filePaths []string, cwd string) []ConditionalSkillMatch {
	if r == nil || len(filePaths) == 0 {
		return nil
	}

	paths := normalizeConditionalInputPaths(filePaths, cwd)
	if len(paths) == 0 {
		return nil
	}

	var matches []ConditionalSkillMatch
	for _, sk := range r.List() {
		if sk == nil || len(sk.Paths) == 0 {
			continue
		}
		for _, pattern := range sk.Paths {
			pattern = normalizeConditionalPattern(pattern)
			if pattern == "" {
				continue
			}
			for _, path := range paths {
				if conditionalPatternMatches(pattern, path) {
					matches = append(matches, ConditionalSkillMatch{
						SkillName: sk.Name,
						Pattern:   pattern,
						Path:      path,
					})
				}
			}
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].SkillName != matches[j].SkillName {
			return matches[i].SkillName < matches[j].SkillName
		}
		if matches[i].Pattern != matches[j].Pattern {
			return matches[i].Pattern < matches[j].Pattern
		}
		return matches[i].Path < matches[j].Path
	})
	return matches
}

func normalizeConditionalInputPaths(filePaths []string, cwd string) []string {
	cwd = strings.TrimSpace(cwd)
	if cwd != "" {
		cwd = filepath.Clean(cwd)
	}

	out := make([]string, 0, len(filePaths))
	for _, path := range filePaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		path = filepath.Clean(path)
		if filepath.IsAbs(path) {
			if cwd == "" {
				continue
			}
			rel, err := filepath.Rel(cwd, path)
			if err != nil {
				continue
			}
			path = rel
		}
		path = filepath.ToSlash(path)
		path = strings.TrimPrefix(path, "./")
		if path == "" || path == "." || strings.HasPrefix(path, "../") || path == ".." || strings.HasPrefix(path, "/") {
			continue
		}
		out = append(out, path)
	}
	return out
}

func normalizeConditionalPattern(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	pattern = filepath.ToSlash(pattern)
	pattern = strings.TrimPrefix(pattern, "./")
	return strings.Trim(pattern, "/")
}

func conditionalPatternMatches(pattern, path string) bool {
	if pattern == "" || path == "" {
		return false
	}
	if !strings.ContainsAny(pattern, "*?[") {
		return path == pattern || strings.HasPrefix(path, pattern+"/")
	}
	re, err := regexp.Compile("^" + conditionalGlobToRegex(pattern) + "$")
	if err != nil {
		return false
	}
	return re.MatchString(path)
}

func conditionalGlobToRegex(pattern string) string {
	var b strings.Builder
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		switch ch {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				if i+2 < len(pattern) && pattern[i+2] == '/' {
					b.WriteString("(?:.*/)?")
					i += 2
				} else {
					b.WriteString(".*")
					i++
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	return b.String()
}
