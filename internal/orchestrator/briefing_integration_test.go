package orchestrator

import (
	"context"
	"testing"

	"github.com/stello/elnath/internal/conversation"
	"github.com/stello/elnath/internal/llm"
)

// projectStubProvider always returns intent=project, simulating an LLM that
// (primed by ongoing session history about an in-flight project) over-labels
// declarative briefings as project intent. The imperative gate inside the
// classifier should demote declarative inputs to chat before routing.
type projectStubProvider struct{}

func (p *projectStubProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Content: `{"intent":"project","confidence":0.9}`}, nil
}

func (p *projectStubProvider) Stream(_ context.Context, _ llm.ChatRequest, _ func(llm.StreamEvent)) error {
	return nil
}

func (p *projectStubProvider) Name() string           { return "project-stub" }
func (p *projectStubProvider) Models() []llm.ModelInfo { return nil }

// TestBriefingDeclarativeRoutesToSingle is the end-to-end regression fence for
// FU-RouterImperativeGate P2. It exercises classifier → router as a pipeline
// and proves that declarative project-context statements, which the LLM
// over-labels as intent=project, get demoted to chat and routed to single —
// NOT autopilot. The guard case keeps explicit creation imperatives on the
// autopilot path so we do not regress the legitimate "Build me X" flow.
//
// Dogfood repro (2026-04-20, session 11, tasks #297-#299): identical prompts
// were routed to autopilot before the imperative gate, triggering unwanted
// Ruby code generation and Telegram briefing UX failure.
func TestBriefingDeclarativeRoutesToSingle(t *testing.T) {
	cases := []struct {
		name         string
		message      string
		wantIntent   conversation.Intent
		wantWorkflow string
	}{
		{
			name:         "declarative_features_list_routes_single",
			message:      "Core features: daily habit check-in, weekly review, streak tracking.",
			wantIntent:   conversation.IntentChat,
			wantWorkflow: "single",
		},
		{
			name:         "declarative_app_name_routes_single",
			message:      "The name of the app is HabitForge.",
			wantIntent:   conversation.IntentChat,
			wantWorkflow: "single",
		},
		{
			name:         "declarative_platform_routes_single",
			message:      "Primary platform: iOS, with a web companion later.",
			wantIntent:   conversation.IntentChat,
			wantWorkflow: "single",
		},
		{
			name:         "english_imperative_stays_on_autopilot",
			message:      "Build me a habit tracker app.",
			wantIntent:   conversation.IntentProject,
			wantWorkflow: "autopilot",
		},
	}

	classifier := conversation.NewLLMClassifier()
	provider := &projectStubProvider{}
	router := newTestRouter()

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			intent, err := classifier.Classify(context.Background(), provider, tc.message, nil)
			if err != nil {
				t.Fatalf("classify %q: %v", tc.message, err)
			}
			if intent != tc.wantIntent {
				t.Errorf("intent for %q = %q, want %q", tc.message, intent, tc.wantIntent)
			}
			wf := router.Route(intent, nil, nil)
			if wf.Name() != tc.wantWorkflow {
				t.Errorf("workflow for %q (intent=%q) = %q, want %q",
					tc.message, intent, wf.Name(), tc.wantWorkflow)
			}
		})
	}
}
