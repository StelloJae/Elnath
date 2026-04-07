package core

import (
	"errors"
	"fmt"
	"strings"
)

// UserError wraps an internal error with a user-friendly message.
type UserError struct {
	Message string
	Err     error
}

func (e *UserError) Error() string {
	return fmt.Sprintf("%s: %v", e.Message, e.Err)
}

func (e *UserError) Unwrap() error {
	return e.Err
}

func NewUserError(msg string, err error) *UserError {
	return &UserError{Message: msg, Err: err}
}

// FormatUserError extracts the user-friendly message if available.
// Otherwise returns the raw error string.
func FormatUserError(err error) string {
	var ue *UserError
	if errors.As(err, &ue) {
		return ue.Message
	}
	// Strip common internal prefixes for cleaner output.
	msg := err.Error()
	prefixes := []string{"load config: ", "init app: ", "build provider: "}
	for _, p := range prefixes {
		if strings.HasPrefix(msg, p) {
			msg = strings.TrimPrefix(msg, p)
			break
		}
	}
	return msg
}
