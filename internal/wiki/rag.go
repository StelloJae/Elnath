package wiki

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

const ragContentLimit = 500

// BuildRAGContext searches the wiki index for content relevant to the query
// and returns a formatted string suitable for injection into the system prompt.
func BuildRAGContext(ctx context.Context, idx *Index, query string, maxResults int) string {
	if idx == nil || query == "" {
		return ""
	}
	results, err := idx.Search(ctx, SearchOpts{Query: query, Limit: maxResults})
	if err != nil || len(results) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("Relevant knowledge from wiki:\n\n")
	for _, r := range results {
		fmt.Fprintf(&sb, "### %s (%s)\n", r.Page.Title, r.Page.Path)
		content := r.Page.Content
		if len(content) > ragContentLimit {
			content = content[:ragContentLimit]
			// Ensure we don't split a multi-byte UTF-8 character.
			for !utf8.ValidString(content) && len(content) > 0 {
				content = content[:len(content)-1]
			}
			content += "..."
		}
		sb.WriteString(content)
		sb.WriteString("\n\n")
	}
	return sb.String()
}
