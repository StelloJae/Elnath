package daemon

import "testing"

func TestTaskPayload_RoundTripsAgenticGatewayEnforcement(t *testing.T) {
	payload := TaskPayload{
		Prompt:             "run with explicit gateway",
		AgenticEnforcement: "gateway",
	}

	raw := EncodeTaskPayload(payload)
	if raw == payload.Prompt {
		t.Fatalf("EncodeTaskPayload returned plain prompt for agentic-enforced payload: %q", raw)
	}
	got := ParseTaskPayload(raw)
	if got.AgenticEnforcement != "gateway" {
		t.Fatalf("AgenticEnforcement = %q, want gateway", got.AgenticEnforcement)
	}
	if got.Prompt != payload.Prompt {
		t.Fatalf("Prompt = %q, want %q", got.Prompt, payload.Prompt)
	}
}

func TestTaskPayload_LegacyPlainTextHasNoAgenticEnforcement(t *testing.T) {
	got := ParseTaskPayload("legacy plain task")

	if got.AgenticEnforcement != "" {
		t.Fatalf("AgenticEnforcement = %q, want empty", got.AgenticEnforcement)
	}
}
