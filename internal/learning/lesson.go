package learning

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/stello/elnath/internal/self"
)

type Lesson struct {
	ID               string        `json:"id"`
	Text             string        `json:"text"`
	Topic            string        `json:"topic,omitempty"`
	Source           string        `json:"source"`
	Confidence       string        `json:"confidence"`
	PersonaDelta     []self.Lesson `json:"persona_delta,omitempty"`
	Rationale        string        `json:"rationale,omitempty"`
	Evidence         []string      `json:"evidence,omitempty"`
	PersonaParam     string        `json:"persona_param,omitempty"`
	PersonaDirection string        `json:"persona_direction,omitempty"`
	PersonaMagnitude string        `json:"persona_magnitude,omitempty"`
	Created          time.Time     `json:"created"`
}

func deriveID(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])[:8]
}
