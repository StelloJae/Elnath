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

func TestTaskPayload_RoundTripsAgenticCompletionGate(t *testing.T) {
	payload := TaskPayload{
		Prompt:                "run with explicit verifier completion gate",
		AgenticCompletionGate: "verification",
	}

	raw := EncodeTaskPayload(payload)
	if raw == payload.Prompt {
		t.Fatalf("EncodeTaskPayload returned plain prompt for completion-gated payload: %q", raw)
	}
	got := ParseTaskPayload(raw)
	if got.AgenticCompletionGate != "verification" {
		t.Fatalf("AgenticCompletionGate = %q, want verification", got.AgenticCompletionGate)
	}
	if got.Prompt != payload.Prompt {
		t.Fatalf("Prompt = %q, want %q", got.Prompt, payload.Prompt)
	}
}

func TestTaskPayload_CanonicalizesAgenticModes(t *testing.T) {
	raw := `{"prompt":"run with explicit modes","agentic_enforcement":" GATEWAY ","agentic_completion_gate":" VERIFICATION "}`

	got := ParseTaskPayload(raw)
	if got.AgenticEnforcement != "gateway" {
		t.Fatalf("AgenticEnforcement = %q, want gateway", got.AgenticEnforcement)
	}
	if got.AgenticCompletionGate != "verification" {
		t.Fatalf("AgenticCompletionGate = %q, want verification", got.AgenticCompletionGate)
	}

	roundTrip := ParseTaskPayload(EncodeTaskPayload(got))
	if roundTrip.AgenticEnforcement != "gateway" {
		t.Fatalf("roundTrip AgenticEnforcement = %q, want gateway", roundTrip.AgenticEnforcement)
	}
	if roundTrip.AgenticCompletionGate != "verification" {
		t.Fatalf("roundTrip AgenticCompletionGate = %q, want verification", roundTrip.AgenticCompletionGate)
	}
}

func TestTaskPayload_LegacyPlainTextHasNoAgenticEnforcement(t *testing.T) {
	got := ParseTaskPayload("legacy plain task")

	if got.AgenticEnforcement != "" {
		t.Fatalf("AgenticEnforcement = %q, want empty", got.AgenticEnforcement)
	}
	if got.AgenticCompletionGate != "" {
		t.Fatalf("AgenticCompletionGate = %q, want empty", got.AgenticCompletionGate)
	}
}
