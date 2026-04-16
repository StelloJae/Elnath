package magicdocs

import (
	"time"

	"github.com/stello/elnath/internal/event"
)

type ExtractionRequest struct {
	Events    []event.Event
	SessionID string
	Trigger   string
	Timestamp time.Time
}

type ExtractionResult struct {
	Pages []PageAction `json:"pages"`
}

type PageAction struct {
	Action     string   `json:"action"`
	Path       string   `json:"path"`
	Title      string   `json:"title"`
	Type       string   `json:"type"`
	Content    string   `json:"content"`
	Confidence string   `json:"confidence"`
	Tags       []string `json:"tags"`
}

type classification int

const (
	drop    classification = iota
	pass
	context_
)
