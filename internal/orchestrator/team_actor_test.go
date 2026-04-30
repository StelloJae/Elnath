package orchestrator

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/agentic"
	agenticactors "github.com/stello/elnath/internal/agentic/actors"
	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/tools"

	_ "modernc.org/sqlite"
)

func TestTeamWorkflow_CreatesPlannerExecutorSynthesizerActors(t *testing.T) {
	ctx := context.Background()
	recorder := newRecordingActorRecorder()
	input := actorWorkflowInput(newActorWorkflowProvider(), recorder)

	result, err := NewTeamWorkflow().Run(ctx, input)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Workflow != "team" {
		t.Fatalf("workflow = %q, want team", result.Workflow)
	}

	actors := recorder.actorsByRole()
	if len(actors[agentic.ActorRolePlanner]) != 1 {
		t.Fatalf("planner actors = %+v, want 1", actors[agentic.ActorRolePlanner])
	}
	if len(actors[agentic.ActorRoleExecutor]) != 2 {
		t.Fatalf("executor actors = %+v, want 2", actors[agentic.ActorRoleExecutor])
	}
	if len(actors[agentic.ActorRoleSynthesizer]) != 1 {
		t.Fatalf("synthesizer actors = %+v, want 1", actors[agentic.ActorRoleSynthesizer])
	}
}

func TestTeamWorkflow_RecordsPlannerToExecutorHandoffs(t *testing.T) {
	ctx := context.Background()
	recorder := newRecordingActorRecorder()
	input := actorWorkflowInput(newActorWorkflowProvider(), recorder)

	if _, err := NewTeamWorkflow().Run(ctx, input); err != nil {
		t.Fatalf("Run: %v", err)
	}

	handoffs := recorder.handoffsByType("planner_to_executor")
	if len(handoffs) != 2 {
		t.Fatalf("planner_to_executor handoffs = %+v, want 2", handoffs)
	}
	for _, handoff := range handoffs {
		if handoff.TaskID != input.AgenticTaskID || handoff.FromActorID == 0 || handoff.ToActorID == 0 || handoff.PayloadJSON == "" {
			t.Fatalf("unexpected handoff: %+v", handoff)
		}
	}
}

func TestTeamWorkflow_RecordsExecutorToSynthesizerHandoffs(t *testing.T) {
	ctx := context.Background()
	recorder := newRecordingActorRecorder()
	input := actorWorkflowInput(newActorWorkflowProvider(), recorder)

	if _, err := NewTeamWorkflow().Run(ctx, input); err != nil {
		t.Fatalf("Run: %v", err)
	}

	handoffs := recorder.handoffsByType("executor_to_synthesizer")
	if len(handoffs) != 2 {
		t.Fatalf("executor_to_synthesizer handoffs = %+v, want 2", handoffs)
	}
	for _, handoff := range handoffs {
		if handoff.TaskID != input.AgenticTaskID || handoff.FromActorID == 0 || handoff.ToActorID == 0 || handoff.PayloadJSON == "" {
			t.Fatalf("unexpected handoff: %+v", handoff)
		}
	}
}

func TestTeamWorkflow_ActorStatusesMirrorSuccess(t *testing.T) {
	ctx := context.Background()
	recorder := newRecordingActorRecorder()
	input := actorWorkflowInput(newActorWorkflowProvider(), recorder)

	if _, err := NewTeamWorkflow().Run(ctx, input); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, actor := range recorder.allActors() {
		if actor.Status != agentic.ActorStatusSucceeded {
			t.Fatalf("actor %+v status = %q, want %q", actor, actor.Status, agentic.ActorStatusSucceeded)
		}
	}
}

func TestTeamWorkflow_ActorStatusesMirrorPartialFailure(t *testing.T) {
	ctx := context.Background()
	recorder := newRecordingActorRecorder()
	provider := &partialFailProvider{
		planner: `[
			{"id":1,"title":"Step A","instruction":"do step A"},
			{"id":2,"title":"Step B","instruction":"do step B"},
			{"id":3,"title":"Step C","instruction":"do step C"}
		]`,
		synth:              "synthesised answer covering A and C, with B unavailable",
		failingInstruction: "do step B",
		successResults: map[string]string{
			"do step A": "result A",
			"do step C": "result C",
		},
	}
	input := actorWorkflowInput(provider, recorder)

	result, err := NewTeamWorkflow().Run(ctx, input)
	if err != nil {
		t.Fatalf("partial failure must preserve current team behavior; got error: %v", err)
	}
	if result.FinishReason != string(agent.FinishReasonPartialSuccess) {
		t.Fatalf("finish reason = %q, want %q", result.FinishReason, agent.FinishReasonPartialSuccess)
	}

	executors := recorder.actorsByRole()[agentic.ActorRoleExecutor]
	var succeeded, failed int
	for _, actor := range executors {
		switch actor.Status {
		case agentic.ActorStatusSucceeded:
			succeeded++
		case agentic.ActorStatusFailed:
			failed++
		}
	}
	if succeeded != 2 || failed != 1 {
		t.Fatalf("executor statuses succeeded=%d failed=%d actors=%+v, want 2/1", succeeded, failed, executors)
	}
	if synth := recorder.actorsByRole()[agentic.ActorRoleSynthesizer]; len(synth) != 1 || synth[0].Status != agentic.ActorStatusSucceeded {
		t.Fatalf("synthesizer actors = %+v, want one succeeded", synth)
	}
}

func TestTeamWorkflow_ActorRecorderFailureObservable(t *testing.T) {
	ctx := context.Background()
	recorder := newRecordingActorRecorder()
	recorder.failCreateRole = agentic.ActorRolePlanner
	provider := newActorWorkflowProvider()
	input := actorWorkflowInput(provider, recorder)
	var streamed strings.Builder
	input.Sink = event.OnTextToSink(func(text string) { streamed.WriteString(text) })

	result, err := NewTeamWorkflow().Run(ctx, input)
	if err != nil {
		t.Fatalf("actor recorder failure should degrade observability without failing team execution: %v", err)
	}
	if result.Workflow != "team" {
		t.Fatalf("workflow = %q, want team", result.Workflow)
	}
	if !strings.Contains(streamed.String(), "actor recorder degraded") || !strings.Contains(streamed.String(), agentic.ActorRolePlanner) {
		t.Fatalf("stream = %q, want observable planner actor degradation", streamed.String())
	}
}

func TestTeamWorkflow_ExecutorActorFailureDoesNotSkipSubtask(t *testing.T) {
	ctx := context.Background()
	recorder := newRecordingActorRecorder()
	recorder.failCreateRole = agentic.ActorRoleExecutor
	provider := newActorWorkflowProvider()
	input := actorWorkflowInput(provider, recorder)
	var streamed strings.Builder
	input.Sink = event.OnTextToSink(func(text string) { streamed.WriteString(text) })

	result, err := NewTeamWorkflow().Run(ctx, input)
	if err != nil {
		t.Fatalf("executor actor failure should not skip existing subtask execution: %v", err)
	}
	if result.Workflow != "team" || !strings.Contains(result.Summary, "Combined") {
		t.Fatalf("unexpected team result after executor actor degradation: %+v", result)
	}
	if provider.CallCount() != 4 {
		t.Fatalf("provider calls = %d, want planner + 2 executors + synthesizer", provider.CallCount())
	}
	if !strings.Contains(streamed.String(), "actor recorder degraded") || !strings.Contains(streamed.String(), agentic.ActorRoleExecutor) {
		t.Fatalf("stream = %q, want observable executor actor degradation", streamed.String())
	}
}

func TestTeamWorkflow_ActorPlannerEmptyPlanFallbackTerminal(t *testing.T) {
	ctx := context.Background()
	recorder := newRecordingActorRecorder()
	provider := newTestProvider(
		"[]",
		"Direct single answer",
	)
	input := actorWorkflowInput(provider, recorder)

	result, err := NewTeamWorkflow().Run(ctx, input)
	if err != nil {
		t.Fatalf("Run fallback: %v", err)
	}
	if result.Workflow != "single" {
		t.Fatalf("workflow = %q, want single fallback", result.Workflow)
	}
	planners := recorder.actorsByRole()[agentic.ActorRolePlanner]
	if len(planners) != 1 {
		t.Fatalf("planner actors = %+v, want one", planners)
	}
	if planners[0].Status != agentic.ActorStatusCanceled {
		t.Fatalf("planner actor status = %q, want %q after empty-plan fallback", planners[0].Status, agentic.ActorStatusCanceled)
	}
}

func TestTeamWorkflow_NoActorRecorderKeepsLegacyBehavior(t *testing.T) {
	ctx := context.Background()
	provider := newActorWorkflowProvider()
	input := actorWorkflowInput(provider, nil)
	input.AgenticTaskID = 42

	result, err := NewTeamWorkflow().Run(ctx, input)
	if err != nil {
		t.Fatalf("Run without actor recorder: %v", err)
	}
	if result.Workflow != "team" || !strings.Contains(result.Summary, "Combined") {
		t.Fatalf("unexpected legacy result: %+v", result)
	}
	if provider.CallCount() != 4 {
		t.Fatalf("provider calls = %d, want legacy planner + 2 executors + synthesizer", provider.CallCount())
	}
}

func TestTeamWorkflow_DoesNotCreateAutonomousSideEffects(t *testing.T) {
	ctx := context.Background()
	db, store := newActorWorkflowStore(t)
	task := createActorWorkflowTask(t, ctx, store)
	recorder := agenticactors.NewRecorder(store)
	input := actorWorkflowInput(newActorWorkflowProvider(), recorder)
	input.AgenticTaskID = task.ID

	if _, err := NewTeamWorkflow().Run(ctx, input); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, table := range []string{"policy_decisions", "tool_action_receipts", "verification_runs", "memory_updates", "followups"} {
		if got := actorWorkflowRowCount(t, db, table); got != 0 {
			t.Fatalf("%s rows = %d, want 0", table, got)
		}
	}
	if got := actorWorkflowRowCount(t, db, "agent_actors"); got != 4 {
		t.Fatalf("agent_actors rows = %d, want planner + 2 executors + synthesizer", got)
	}
	if got := actorWorkflowRowCount(t, db, "actor_handoffs"); got != 4 {
		t.Fatalf("actor_handoffs rows = %d, want 2 planner->executor + 2 executor->synthesizer", got)
	}
}

func TestTeamWorkflow_RecordsInheritedBudgetMetadata(t *testing.T) {
	ctx := context.Background()
	recorder := newRecordingActorRecorder()
	input := actorWorkflowInput(newActorWorkflowProvider(), recorder)
	input.Config.MaxIterations = 12

	if _, err := NewTeamWorkflow().Run(ctx, input); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, actor := range recorder.allActors() {
		if !strings.Contains(actor.BudgetJSON, `"max_iterations"`) || !strings.Contains(actor.BudgetJSON, `"max_actor_depth"`) {
			t.Fatalf("actor budget metadata = %q, want inherited budget/depth fields", actor.BudgetJSON)
		}
	}
}

func TestTeamWorkflow_RedactsSecretsFromActorPayloads(t *testing.T) {
	ctx := context.Background()
	recorder := newRecordingActorRecorder()
	input := actorWorkflowInput(newActorWorkflowProvider(), recorder)
	input.Message = "Design API using key=AKIAIOSFODNN7EXAMPLE"

	if _, err := NewTeamWorkflow().Run(ctx, input); err != nil {
		t.Fatalf("Run: %v", err)
	}

	payloads := recorder.serializedPayloads()
	for _, payload := range payloads {
		if strings.Contains(payload, "AKIAIOSFODNN7EXAMPLE") {
			t.Fatalf("actor payload leaked secret: %s", payload)
		}
	}
	if !strings.Contains(strings.Join(payloads, "\n"), "[REDACTED:aws-access-key]") {
		t.Fatalf("actor payloads did not preserve redaction evidence: %v", payloads)
	}
}

func TestTeamWorkflow_DoesNotChangeToolGatewayOrPermissionBehavior(t *testing.T) {
	ctx := context.Background()
	reg := tools.NewRegistry()
	reg.Register(&testTool{name: "read"})
	if result, err := reg.Execute(ctx, "read", nil); err != nil || result.IsError {
		t.Fatalf("plain registry execute before actor workflow = result %+v error %v", result, err)
	}

	recorder := newRecordingActorRecorder()
	input := actorWorkflowInput(newActorWorkflowProvider(), recorder)
	input.Tools = reg
	if _, err := NewTeamWorkflow().Run(ctx, input); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result, err := reg.Execute(ctx, "read", nil); err != nil || result.IsError {
		t.Fatalf("plain registry execute after actor workflow = result %+v error %v", result, err)
	}
}

func newActorWorkflowProvider() *testProvider {
	return newTestProvider(
		`[
			{"id":1,"title":"Research API","instruction":"List REST API patterns"},
			{"id":2,"title":"Draft schema","instruction":"Design the data model"}
		]`,
		"API patterns: REST, GraphQL",
		"Schema: users, posts tables",
		"Combined: REST API with users and posts tables",
	)
}

func actorWorkflowInput(provider llmProvider, recorder AgenticActorRecorder) WorkflowInput {
	input := testInput("Design a blog API", provider)
	input.AgenticTaskID = 42
	input.ActorRecorder = recorder
	return input
}

type llmProvider interface {
	Name() string
	Models() []llm.ModelInfo
	Chat(context.Context, llm.ChatRequest) (*llm.ChatResponse, error)
	Stream(context.Context, llm.ChatRequest, func(llm.StreamEvent)) error
}

type recordingActorRecorder struct {
	mu             sync.Mutex
	nextActorID    int64
	nextHandoffID  int64
	actors         map[int64]agentic.AgentActor
	handoffs       []agentic.ActorHandoff
	failCreateRole string
}

func newRecordingActorRecorder() *recordingActorRecorder {
	return &recordingActorRecorder{
		nextActorID:   1,
		nextHandoffID: 1,
		actors:        make(map[int64]agentic.AgentActor),
	}
}

func (r *recordingActorRecorder) CreateActor(_ context.Context, actor agentic.AgentActor) (*agentic.AgentActor, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if actor.Role == r.failCreateRole {
		return nil, errors.New("forced actor create failure")
	}
	actor.ID = r.nextActorID
	r.nextActorID++
	r.actors[actor.ID] = actor
	return &actor, nil
}

func (r *recordingActorRecorder) UpdateActor(_ context.Context, actor agentic.AgentActor) (*agentic.AgentActor, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if actor.ID == 0 {
		return nil, errors.New("missing actor id")
	}
	r.actors[actor.ID] = actor
	return &actor, nil
}

func (r *recordingActorRecorder) CreateHandoff(_ context.Context, handoff agentic.ActorHandoff) (*agentic.ActorHandoff, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	handoff.ID = r.nextHandoffID
	r.nextHandoffID++
	r.handoffs = append(r.handoffs, handoff)
	return &handoff, nil
}

func (r *recordingActorRecorder) allActors() []agentic.AgentActor {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]agentic.AgentActor, 0, len(r.actors))
	for _, actor := range r.actors {
		out = append(out, actor)
	}
	return out
}

func (r *recordingActorRecorder) actorsByRole() map[string][]agentic.AgentActor {
	out := make(map[string][]agentic.AgentActor)
	for _, actor := range r.allActors() {
		out[actor.Role] = append(out[actor.Role], actor)
	}
	return out
}

func (r *recordingActorRecorder) handoffsByType(handoffType string) []agentic.ActorHandoff {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []agentic.ActorHandoff
	for _, handoff := range r.handoffs {
		if handoff.HandoffType == handoffType {
			out = append(out, handoff)
		}
	}
	return out
}

func (r *recordingActorRecorder) serializedPayloads() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.actors)*5+len(r.handoffs))
	for _, actor := range r.actors {
		out = append(out, actor.StateJSON, actor.InboxJSON, actor.OutboxJSON, actor.ToolAllowlistJSON, actor.BudgetJSON)
	}
	for _, handoff := range r.handoffs {
		out = append(out, handoff.PayloadJSON)
	}
	return out
}

func newActorWorkflowStore(t *testing.T) (*sql.DB, *agentic.Store) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	if err := agentic.InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	return db, agentic.NewStore(db)
}

func createActorWorkflowTask(t *testing.T, ctx context.Context, store *agentic.Store) *agentic.AgenticTask {
	t.Helper()
	task, err := store.CreateAgenticTask(ctx, agentic.AgenticTask{
		Title:              "Team task",
		Prompt:             "Design a blog API",
		Status:             agentic.TaskStatusPending,
		RiskLevel:          agentic.RiskLevelLow,
		AutonomyDecision:   agentic.PolicyDecisionObserve,
		VerificationStatus: agentic.VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask: %v", err)
	}
	return task
}

func actorWorkflowRowCount(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}
