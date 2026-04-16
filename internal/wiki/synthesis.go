package wiki

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// SynthesisIDPrefix is the stable prefix used for consolidation synthesis IDs.
const SynthesisIDPrefix = "synth-"

// SynthesisID derives a stable short identifier from the synthesis text. Two
// syntheses with identical text collapse to the same ID, which keeps
// MarkSuperseded idempotent across retries with the same LLM output.
func SynthesisID(text string) string {
	sum := sha256.Sum256([]byte(text))
	return SynthesisIDPrefix + hex.EncodeToString(sum[:])[:8]
}

// SynthesisSlug converts a topic string into a URL-safe slug for the synthesis
// page path. All non [a-z0-9] runes collapse to a single dash, leading and
// trailing dashes are trimmed, and empty results fall back to "misc".
func SynthesisSlug(topic string) string {
	s := strings.ToLower(strings.TrimSpace(topic))
	if s == "" {
		return "misc"
	}
	var b strings.Builder
	dashed := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dashed = false
		default:
			if !dashed && b.Len() > 0 {
				b.WriteRune('-')
				dashed = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "misc"
	}
	return slug
}

// BuildSynthesisPage assembles a wiki Page at
// synthesis/<slug>/<YYYY-MM-DD>-<idSuffix>.md with SourceConsolidation
// provenance. The synthesisID is stamped in the page's source_event so callers
// can correlate the wiki page with a Lesson.SupersededBy link.
//
// The idSuffix keeps the path unique when multiple syntheses share a topic
// slug on the same day. Tags default to [primaryTopic] when tags is empty.
func BuildSynthesisPage(synthesisID, primaryTopic, body string, tags []string, created time.Time) *Page {
	if synthesisID == "" {
		synthesisID = SynthesisID(body)
	}
	slug := SynthesisSlug(primaryTopic)
	createdUTC := created.UTC()
	idSuffix := strings.TrimPrefix(synthesisID, SynthesisIDPrefix)
	if len(idSuffix) > 8 {
		idSuffix = idSuffix[:8]
	}
	path := fmt.Sprintf("synthesis/%s/%s-%s.md", slug, createdUTC.Format("2006-01-02"), idSuffix)

	title := fmt.Sprintf("Synthesis: %s (%s)", primaryTopic, createdUTC.Format("2006-01-02"))
	if strings.TrimSpace(primaryTopic) == "" {
		title = fmt.Sprintf("Synthesis %s", createdUTC.Format("2006-01-02"))
	}

	cleanTags := make([]string, 0, len(tags))
	for _, t := range tags {
		if t = strings.TrimSpace(t); t != "" {
			cleanTags = append(cleanTags, t)
		}
	}
	if len(cleanTags) == 0 && strings.TrimSpace(primaryTopic) != "" {
		cleanTags = []string{primaryTopic}
	}

	page := &Page{
		Path:    path,
		Title:   title,
		Type:    PageTypeAnalysis,
		Tags:    cleanTags,
		Content: body,
		Extra:   make(map[string]any),
	}
	page.SetSource(SourceConsolidation, "", synthesisID)
	return page
}
