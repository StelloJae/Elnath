package magicdocs

import (
	"crypto/sha256"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/stello/elnath/internal/wiki"
)

type WikiWriter struct {
	store  *wiki.Store
	logger *slog.Logger
}

func NewWikiWriter(store *wiki.Store, logger *slog.Logger) *WikiWriter {
	return &WikiWriter{store: store, logger: logger}
}

func (w *WikiWriter) Apply(actions []PageAction, sessionID, trigger string) (created, updated int) {
	for _, a := range actions {
		var err error
		switch a.Action {
		case "create":
			err = w.createPage(a, sessionID, trigger)
			if err == nil {
				created++
			}
		case "update":
			wasUpdate, e := w.updateOwnedPage(a, sessionID, trigger)
			err = e
			if err == nil {
				if wasUpdate {
					updated++
				} else {
					created++
				}
			}
		default:
			w.logger.Warn("magic-docs unknown action", "action", a.Action, "path", a.Path)
			continue
		}
		if err != nil {
			w.logger.Error("magic-docs wiki write failed",
				"action", a.Action,
				"path", a.Path,
				"error", err,
			)
		}
	}
	return
}

func (w *WikiWriter) createPage(a PageAction, sessionID, trigger string) error {
	page := &wiki.Page{
		Path:       a.Path,
		Title:      a.Title,
		Type:       wiki.PageType(a.Type),
		Content:    a.Content,
		Confidence: a.Confidence,
		Tags:       a.Tags,
	}
	page.SetSource(wiki.SourceMagicDocs, sessionID, trigger)
	return w.store.Create(page)
}

func (w *WikiWriter) updateOwnedPage(a PageAction, sessionID, trigger string) (wasUpdate bool, err error) {
	existing, err := w.store.Read(a.Path)
	if err != nil {
		return false, w.createPage(a, sessionID, trigger)
	}

	if !existing.IsOwnedBy(wiki.SourceMagicDocs) {
		return false, w.createLinkedPage(a, existing, sessionID, trigger)
	}

	existing.Content = a.Content
	existing.Confidence = a.Confidence
	existing.Tags = a.Tags
	existing.Extra["source_session"] = sessionID
	existing.Extra["source_event"] = trigger
	return true, w.store.Upsert(existing)
}

func (w *WikiWriter) createLinkedPage(a PageAction, target *wiki.Page, sessionID, trigger string) error {
	hash := shortHash(sessionID + a.Path)
	dir := filepath.Dir(a.Path)
	base := strings.TrimSuffix(filepath.Base(a.Path), ".md")
	linkedPath := filepath.Join(dir, base+"-auto-"+hash+".md")

	page := &wiki.Page{
		Path:       linkedPath,
		Title:      a.Title,
		Type:       wiki.PageType(a.Type),
		Content:    fmt.Sprintf("Related: [%s](%s)\n\n%s", target.Title, target.Path, a.Content),
		Confidence: a.Confidence,
		Tags:       a.Tags,
	}
	page.SetSource(wiki.SourceMagicDocs, sessionID, trigger)
	page.Extra["related_to"] = target.Path
	return w.store.Create(page)
}

func shortHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h[:4])
}
