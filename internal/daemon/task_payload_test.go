package daemon

import (
	"testing"

	"github.com/stello/elnath/internal/identity"
)

func TestParseTaskPayloadPlainText(t *testing.T) {
	got := ParseTaskPayload("tell me a joke")
	if got.Prompt != "tell me a joke" {
		t.Fatalf("Prompt = %q, want plain text payload", got.Prompt)
	}
	if got.SessionID != "" {
		t.Fatalf("SessionID = %q, want empty", got.SessionID)
	}
	if got.Surface != "" {
		t.Fatalf("Surface = %q, want empty", got.Surface)
	}
}

func TestEncodeTaskPayloadRoundTripsStructuredPayload(t *testing.T) {
	payload := TaskPayload{
		Prompt:    "continue the fix and summarize the result",
		SessionID: "sess-123",
		Surface:   "telegram",
		Principal: identity.Principal{UserID: "user-1", CanonicalUserID: "stello@host", ProjectID: "proj-1", Surface: "telegram"},
	}

	raw := EncodeTaskPayload(payload)
	if raw == payload.Prompt {
		t.Fatalf("EncodeTaskPayload returned plain prompt for structured payload: %q", raw)
	}

	got := ParseTaskPayload(raw)
	if got != payload {
		t.Fatalf("round trip payload = %+v, want %+v", got, payload)
	}
}

func TestParseTaskPayloadBackfillsPrincipalSurfaceFromLegacySurfaceField(t *testing.T) {
	got := ParseTaskPayload(`{"prompt":"hello","surface":"telegram"}`)
	if got.Principal.Surface != "telegram" {
		t.Fatalf("Principal.Surface = %q, want telegram", got.Principal.Surface)
	}
}
