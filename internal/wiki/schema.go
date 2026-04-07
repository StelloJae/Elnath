package wiki

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// PageType classifies the nature of a wiki page.
type PageType string

const (
	PageTypeEntity   PageType = "entity"
	PageTypeConcept  PageType = "concept"
	PageTypeSource   PageType = "source"
	PageTypeAnalysis PageType = "analysis"
	PageTypeMap      PageType = "map"
)

// Page represents a single wiki page parsed from a markdown file with YAML frontmatter.
type Page struct {
	Path       string    // relative path within wiki dir (e.g. "entities/foo.md")
	Title      string
	Type       PageType
	Content    string // markdown body without frontmatter
	Tags       []string
	Created    time.Time
	Updated    time.Time
	TTL        string // e.g. "7d", "30d", "" for permanent
	Confidence string // "high", "medium", "low"
}

// frontmatterYAML is the on-disk representation of the YAML block.
type frontmatterYAML struct {
	Title      string   `yaml:"title"`
	Type       PageType `yaml:"type"`
	Tags       []string `yaml:"tags,omitempty"`
	Created    string   `yaml:"created,omitempty"`
	Updated    string   `yaml:"updated,omitempty"`
	TTL        string   `yaml:"ttl,omitempty"`
	Confidence string   `yaml:"confidence,omitempty"`
}

const timeLayout = time.RFC3339

// ParseFrontmatter parses a raw markdown file that begins with a YAML frontmatter block.
// The frontmatter must be delimited by "---\n" lines.
func ParseFrontmatter(raw []byte) (*Page, error) {
	// Normalise line endings.
	content := strings.ReplaceAll(string(raw), "\r\n", "\n")

	if !strings.HasPrefix(content, "---\n") {
		return nil, fmt.Errorf("wiki: missing opening frontmatter delimiter")
	}

	// Find the closing "---" after the opening one.
	rest := content[4:] // skip opening "---\n"
	closingIdx := strings.Index(rest, "\n---\n")
	if closingIdx == -1 {
		// Also accept EOF-terminated closing delimiter.
		if strings.HasSuffix(rest, "\n---") {
			closingIdx = len(rest) - 4
		} else {
			return nil, fmt.Errorf("wiki: missing closing frontmatter delimiter")
		}
	}

	yamlBlock := rest[:closingIdx]
	body := ""
	endDelimPos := closingIdx + len("\n---\n")
	if endDelimPos <= len(rest) {
		body = strings.TrimPrefix(rest[endDelimPos:], "\n")
	}

	var fm frontmatterYAML
	if err := yaml.Unmarshal([]byte(yamlBlock), &fm); err != nil {
		return nil, fmt.Errorf("wiki: parse frontmatter yaml: %w", err)
	}

	if fm.Title == "" {
		return nil, fmt.Errorf("wiki: frontmatter missing required field 'title'")
	}

	page := &Page{
		Title:      fm.Title,
		Type:       fm.Type,
		Tags:       fm.Tags,
		TTL:        fm.TTL,
		Confidence: fm.Confidence,
		Content:    body,
	}

	if fm.Created != "" {
		t, err := time.Parse(timeLayout, fm.Created)
		if err != nil {
			return nil, fmt.Errorf("wiki: parse 'created' time %q: %w", fm.Created, err)
		}
		page.Created = t
	}

	if fm.Updated != "" {
		t, err := time.Parse(timeLayout, fm.Updated)
		if err != nil {
			return nil, fmt.Errorf("wiki: parse 'updated' time %q: %w", fm.Updated, err)
		}
		page.Updated = t
	}

	return page, nil
}

// RenderFrontmatter serialises a Page back to its markdown-with-frontmatter representation.
func RenderFrontmatter(page *Page) ([]byte, error) {
	if page.Title == "" {
		return nil, fmt.Errorf("wiki: page title must not be empty")
	}

	now := time.Now().UTC()
	if page.Created.IsZero() {
		page.Created = now
	}
	if page.Updated.IsZero() {
		page.Updated = now
	}

	fm := frontmatterYAML{
		Title:      page.Title,
		Type:       page.Type,
		Tags:       page.Tags,
		Created:    page.Created.UTC().Format(timeLayout),
		Updated:    page.Updated.UTC().Format(timeLayout),
		TTL:        page.TTL,
		Confidence: page.Confidence,
	}

	var buf bytes.Buffer
	buf.WriteString("---\n")

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(fm); err != nil {
		return nil, fmt.Errorf("wiki: marshal frontmatter yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("wiki: close yaml encoder: %w", err)
	}

	buf.WriteString("---\n")

	if page.Content != "" {
		buf.WriteByte('\n')
		buf.WriteString(page.Content)
		if !strings.HasSuffix(page.Content, "\n") {
			buf.WriteByte('\n')
		}
	}

	return buf.Bytes(), nil
}
