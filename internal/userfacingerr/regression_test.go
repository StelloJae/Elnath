package userfacingerr

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestWrap_PreservesWrappedErrorChain(t *testing.T) {
	base := errors.New("provider detail")
	original := fmt.Errorf("no LLM provider configured: %w", base)
	wrapped := Wrap(ELN001, original, "build provider")

	if !errors.Is(wrapped, base) {
		t.Fatal("errors.Is did not reach original wrapped error")
	}
	if !strings.Contains(wrapped.Error(), "no LLM provider configured") {
		t.Fatalf("Error() = %q, want original message preserved", wrapped.Error())
	}
}
