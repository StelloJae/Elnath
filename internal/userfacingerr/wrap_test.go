package userfacingerr

import (
	"errors"
	"strings"
	"testing"
)

func TestWrap_ErrorIncludesCodeAndHint(t *testing.T) {
	err := Wrap(ELN001, errors.New("provider missing"), "test")
	text := err.Error()
	if !strings.Contains(text, "ELN-001") {
		t.Fatalf("Error() = %q, want ELN-001", text)
	}
	if !strings.Contains(text, "Run 'elnath setup --quickstart'") {
		t.Fatalf("Error() = %q, want HowToFix hint", text)
	}

	var target *UserFacingError
	if !errors.As(err, &target) {
		t.Fatal("errors.As did not match UserFacingError")
	}
	if target.Code() != ELN001 {
		t.Fatalf("Code() = %q, want %q", target.Code(), ELN001)
	}
}

func TestWrap_HandlesNilWrappedError(t *testing.T) {
	err := Wrap(ELN001, nil, "test")
	text := err.Error()
	if !strings.Contains(text, "ELN-001") {
		t.Fatalf("Error() = %q, want ELN-001", text)
	}
	if strings.Contains(text, "%!s(<nil>)") {
		t.Fatalf("Error() = %q, should not format nil badly", text)
	}
}

func TestWrap_UnknownCodeFallsBack(t *testing.T) {
	err := Wrap(Code("ELN-999"), errors.New("boom"), "test")
	text := err.Error()
	if !strings.Contains(text, "ELN-999") || !strings.Contains(text, "test") {
		t.Fatalf("Error() = %q, want fallback with code and context", text)
	}
}
