package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// KeyFor derives a stable cross-surface key from principal identity and prompt.
func KeyFor(principal Principal, prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return ""
	}

	hash := sha256.New()
	_, _ = hash.Write([]byte(strings.TrimSpace(principal.UserID)))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(strings.TrimSpace(principal.ProjectID)))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(prompt))
	return hex.EncodeToString(hash.Sum(nil))[:16]
}
