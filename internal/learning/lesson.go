package learning

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/stello/elnath/internal/self"
)

type Lesson struct {
	ID           string        `json:"id"`
	Text         string        `json:"text"`
	Topic        string        `json:"topic,omitempty"`
	Source       string        `json:"source"`
	Confidence   string        `json:"confidence"`
	PersonaDelta []self.Lesson `json:"persona_delta,omitempty"`
	Created      time.Time     `json:"created"`
}

func deriveID(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])[:8]
}
