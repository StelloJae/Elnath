package skill

import (
	"fmt"
	"strings"
)

type skillTrustFilter struct {
	active bool
	allow  map[string]struct{}
}

func newSkillTrustFilter(levels []string) (skillTrustFilter, error) {
	if len(levels) == 0 {
		return skillTrustFilter{}, nil
	}
	filter := skillTrustFilter{active: true, allow: make(map[string]struct{}, len(levels))}
	for _, level := range levels {
		level = strings.ToLower(strings.TrimSpace(level))
		if !validSkillTrustLevel(level) {
			return skillTrustFilter{}, fmt.Errorf("skill: unsupported trust level %q", level)
		}
		filter.allow[level] = struct{}{}
	}
	return filter, nil
}

func validSkillTrustLevel(level string) bool {
	switch level {
	case "wiki", "local_compatible", "plugin_cache", "declared":
		return true
	default:
		return false
	}
}

func (f skillTrustFilter) allowsSkill(sk *Skill) bool {
	if !f.active {
		return true
	}
	if sk == nil {
		return false
	}
	_, ok := f.allow[sk.TrustLevel()]
	return ok
}

func (f skillTrustFilter) filterMatches(matches []ConditionalSkillMatch) []ConditionalSkillMatch {
	if !f.active || len(matches) == 0 {
		return matches
	}
	out := make([]ConditionalSkillMatch, 0, len(matches))
	for _, match := range matches {
		if _, ok := f.allow[match.TrustLevel]; ok {
			out = append(out, match)
		}
	}
	return out
}
