package userfacingerr

import (
	"errors"
	"fmt"
)

// UserFacingError carries a stable ELN-XXX code and the original error.
type UserFacingError struct {
	code    Code
	context string
	wrapped error
}

func Wrap(code Code, err error, context string) *UserFacingError {
	return &UserFacingError{code: code, context: context, wrapped: err}
}

func (e *UserFacingError) Error() string {
	entry, ok := Lookup(e.code)
	if !ok {
		if e.wrapped != nil {
			return fmt.Sprintf("%s: %s: %v", e.code, e.context, e.wrapped)
		}
		return fmt.Sprintf("%s: %s", e.code, e.context)
	}
	if e.wrapped != nil {
		return fmt.Sprintf("%s %s: %v\nHint: %s", e.code, entry.Title, e.wrapped, entry.HowToFix)
	}
	return fmt.Sprintf("%s %s\nHint: %s", e.code, entry.Title, entry.HowToFix)
}

func (e *UserFacingError) Unwrap() error { return e.wrapped }

func (e *UserFacingError) Is(target error) bool {
	var other *UserFacingError
	if errors.As(target, &other) {
		return e.code == other.code
	}
	return false
}

func (e *UserFacingError) Code() Code { return e.code }
