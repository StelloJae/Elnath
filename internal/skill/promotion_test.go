package skill

import (
	"strings"
	"testing"
)

func TestFormatPromotionMessage(t *testing.T) {
	t.Parallel()

	msg := FormatPromotionMessage(&Skill{Name: "deploy-check"}, 3, 7)
	if msg == "" {
		t.Fatal("FormatPromotionMessage() = empty string, want non-empty")
	}

	for _, want := range []string{"deploy-check", "3", "7"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message %q does not contain %q", msg, want)
		}
	}
}
