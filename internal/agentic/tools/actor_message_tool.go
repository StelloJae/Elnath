package agentictools

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/stello/elnath/internal/agentic"
	basetools "github.com/stello/elnath/internal/tools"
)

const (
	ActorMessageSendToolName = "agentic_message_send"
	ActorMessageListToolName = "agentic_message_list"
	actorMessageHandoffType  = "actor_message"
)

type actorMessageStore interface {
	GetAgenticTask(context.Context, int64) (*agentic.AgenticTask, error)
	GetAgentActor(context.Context, int64) (*agentic.AgentActor, error)
	UpdateAgentActor(context.Context, agentic.AgentActor) (*agentic.AgentActor, error)
	CreateActorHandoff(context.Context, agentic.ActorHandoff) (*agentic.ActorHandoff, error)
}

type ActorMessageSendTool struct {
	store actorMessageStore
	now   func() time.Time
}

func NewActorMessageSendTool(store actorMessageStore) *ActorMessageSendTool {
	return &ActorMessageSendTool{store: store, now: time.Now}
}

func (t *ActorMessageSendTool) Name() string { return ActorMessageSendToolName }

func (t *ActorMessageSendTool) Description() string {
	return "Send a bounded message between agentic actors by recording inbox/outbox mailbox entries"
}

func (t *ActorMessageSendTool) Schema() json.RawMessage {
	return basetools.Object(map[string]basetools.Property{
		"task_id":       basetools.Int("Agentic task id containing both actors."),
		"from_actor_id": basetools.Int("Sender actor id."),
		"to_actor_id":   basetools.Int("Recipient actor id."),
		"summary":       basetools.String("Optional short message preview."),
		"message":       basetools.String("Message text to record."),
	}, []string{"task_id", "from_actor_id", "to_actor_id", "message"})
}

func (t *ActorMessageSendTool) IsConcurrencySafe(json.RawMessage) bool { return false }

func (t *ActorMessageSendTool) Reversible() bool { return false }

func (t *ActorMessageSendTool) Scope(json.RawMessage) basetools.ToolScope {
	return basetools.ToolScope{Persistent: true}
}

func (t *ActorMessageSendTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *ActorMessageSendTool) DeferInitialToolSchema() bool { return true }

type actorMessageSendToolInput struct {
	TaskID      int64  `json:"task_id"`
	FromActorID int64  `json:"from_actor_id"`
	ToActorID   int64  `json:"to_actor_id"`
	Summary     string `json:"summary"`
	Message     string `json:"message"`
}

type actorMessageSendToolOutput struct {
	TaskID      int64              `json:"task_id"`
	FromActorID int64              `json:"from_actor_id"`
	ToActorID   int64              `json:"to_actor_id"`
	HandoffID   int64              `json:"handoff_id"`
	Summary     string             `json:"summary,omitempty"`
	Delivered   bool               `json:"delivered"`
	Boundary    string             `json:"boundary"`
	Receipt     agenticToolReceipt `json:"receipt"`
}

type actorMailboxMessage struct {
	HandoffID   int64  `json:"handoff_id"`
	FromActorID int64  `json:"from_actor_id"`
	ToActorID   int64  `json:"to_actor_id"`
	Summary     string `json:"summary,omitempty"`
	Text        string `json:"text"`
	Read        bool   `json:"read,omitempty"`
	CreatedAt   string `json:"created_at"`
}

func (t *ActorMessageSendTool) Execute(ctx context.Context, params json.RawMessage) (*basetools.Result, error) {
	if t == nil || t.store == nil {
		return basetools.ErrorResult("agentic_message_send: store unavailable"), nil
	}
	var input actorMessageSendToolInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return basetools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}
	if input.TaskID == 0 {
		return basetools.ErrorResult("agentic_message_send: task_id is required"), nil
	}
	if input.FromActorID == 0 || input.ToActorID == 0 {
		return basetools.ErrorResult("agentic_message_send: from_actor_id and to_actor_id are required"), nil
	}
	if input.FromActorID == input.ToActorID {
		return basetools.ErrorResult("agentic_message_send: recipient must differ from sender"), nil
	}
	message := strings.TrimSpace(input.Message)
	if message == "" {
		return basetools.ErrorResult("agentic_message_send: message is required"), nil
	}
	if _, err := t.store.GetAgenticTask(ctx, input.TaskID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return basetools.ErrorResult(fmt.Sprintf("agentic_message_send: task %d not found", input.TaskID)), nil
		}
		return basetools.ErrorResult(fmt.Sprintf("agentic_message_send: task %d: %v", input.TaskID, err)), nil
	}
	from, err := t.store.GetAgentActor(ctx, input.FromActorID)
	if err != nil {
		return basetools.ErrorResult(fmt.Sprintf("agentic_message_send: from_actor_id %d: %v", input.FromActorID, err)), nil
	}
	to, err := t.store.GetAgentActor(ctx, input.ToActorID)
	if err != nil {
		return basetools.ErrorResult(fmt.Sprintf("agentic_message_send: to_actor_id %d: %v", input.ToActorID, err)), nil
	}
	if from.TaskID != input.TaskID || to.TaskID != input.TaskID {
		return basetools.ErrorResult("agentic_message_send: both actors must belong to task"), nil
	}

	summary := strings.TrimSpace(input.Summary)
	payload, err := json.Marshal(map[string]string{
		"summary": summary,
		"message": message,
	})
	if err != nil {
		return basetools.ErrorResult(fmt.Sprintf("agentic_message_send: marshal payload: %v", err)), nil
	}
	handoff, err := t.store.CreateActorHandoff(ctx, agentic.ActorHandoff{
		TaskID:      input.TaskID,
		FromActorID: from.ID,
		ToActorID:   to.ID,
		HandoffType: actorMessageHandoffType,
		PayloadJSON: string(payload),
		Status:      agentic.ActorStatusCreated,
	})
	if err != nil {
		return basetools.ErrorResult(fmt.Sprintf("agentic_message_send: create handoff: %v", err)), nil
	}

	msg := actorMailboxMessage{
		HandoffID:   handoff.ID,
		FromActorID: from.ID,
		ToActorID:   to.ID,
		Summary:     summary,
		Text:        message,
		Read:        false,
		CreatedAt:   formatActorGraphTime(t.observeTime()),
	}
	if err := appendActorMessage(&from.OutboxJSON, msg); err != nil {
		return basetools.ErrorResult(fmt.Sprintf("agentic_message_send: append sender outbox: %v", err)), nil
	}
	if err := appendActorMessage(&to.InboxJSON, msg); err != nil {
		return basetools.ErrorResult(fmt.Sprintf("agentic_message_send: append recipient inbox: %v", err)), nil
	}
	if _, err := t.store.UpdateAgentActor(ctx, *from); err != nil {
		return basetools.ErrorResult(fmt.Sprintf("agentic_message_send: update sender: %v", err)), nil
	}
	if _, err := t.store.UpdateAgentActor(ctx, *to); err != nil {
		return basetools.ErrorResult(fmt.Sprintf("agentic_message_send: update recipient: %v", err)), nil
	}

	output := actorMessageSendToolOutput{
		TaskID:      input.TaskID,
		FromActorID: from.ID,
		ToActorID:   to.ID,
		HandoffID:   handoff.ID,
		Summary:     summary,
		Delivered:   true,
		Boundary:    "mailbox record only; no actor is resumed or executed automatically",
		Receipt: agenticToolReceipt{
			Tool:            ActorMessageSendToolName,
			Action:          "send",
			ReadOnly:        false,
			Persistent:      true,
			ExecutionPolicy: "agentic_actor_message_send",
			TaskID:          input.TaskID,
			FromActorID:     from.ID,
			ToActorID:       to.ID,
			HandoffID:       handoff.ID,
			Delivered:       true,
		},
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return basetools.ErrorResult(fmt.Sprintf("agentic_message_send: marshal output: %v", err)), nil
	}
	return basetools.SuccessResult(string(raw)), nil
}

func (t *ActorMessageSendTool) observeTime() time.Time {
	if t != nil && t.now != nil {
		return t.now()
	}
	return time.Now()
}

type actorMessageListStore interface {
	GetAgenticTask(context.Context, int64) (*agentic.AgenticTask, error)
	GetAgentActor(context.Context, int64) (*agentic.AgentActor, error)
}

type ActorMessageListTool struct {
	store actorMessageListStore
}

func NewActorMessageListTool(store actorMessageListStore) *ActorMessageListTool {
	return &ActorMessageListTool{store: store}
}

func (t *ActorMessageListTool) Name() string { return ActorMessageListToolName }

func (t *ActorMessageListTool) Description() string {
	return "List bounded actor mailbox messages from an agentic actor inbox or outbox"
}

func (t *ActorMessageListTool) Schema() json.RawMessage {
	return basetools.Object(map[string]basetools.Property{
		"task_id":  basetools.Int("Agentic task id containing the actor."),
		"actor_id": basetools.Int("Actor id whose mailbox should be listed."),
		"box":      basetools.StringEnum("Mailbox to list. Defaults to inbox.", "inbox", "outbox"),
	}, []string{"task_id", "actor_id"})
}

func (t *ActorMessageListTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *ActorMessageListTool) Reversible() bool { return true }

func (t *ActorMessageListTool) Scope(json.RawMessage) basetools.ToolScope {
	return basetools.ToolScope{}
}

func (t *ActorMessageListTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *ActorMessageListTool) DeferInitialToolSchema() bool { return true }

type actorMessageListToolInput struct {
	TaskID  int64  `json:"task_id"`
	ActorID int64  `json:"actor_id"`
	Box     string `json:"box"`
}

type actorMessageListToolOutput struct {
	TaskID   int64                 `json:"task_id"`
	ActorID  int64                 `json:"actor_id"`
	Box      string                `json:"box"`
	Total    int                   `json:"total"`
	Messages []actorMailboxMessage `json:"messages"`
	Receipt  agenticToolReceipt    `json:"receipt"`
}

func (t *ActorMessageListTool) Execute(ctx context.Context, params json.RawMessage) (*basetools.Result, error) {
	if t == nil || t.store == nil {
		return basetools.ErrorResult("agentic_message_list: store unavailable"), nil
	}
	var input actorMessageListToolInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return basetools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}
	if input.TaskID == 0 {
		return basetools.ErrorResult("agentic_message_list: task_id is required"), nil
	}
	if input.ActorID == 0 {
		return basetools.ErrorResult("agentic_message_list: actor_id is required"), nil
	}
	box, ok := normalizeActorMessageBox(input.Box)
	if !ok {
		return basetools.ErrorResult("agentic_message_list: box must be inbox or outbox"), nil
	}
	if _, err := t.store.GetAgenticTask(ctx, input.TaskID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return basetools.ErrorResult(fmt.Sprintf("agentic_message_list: task %d not found", input.TaskID)), nil
		}
		return basetools.ErrorResult(fmt.Sprintf("agentic_message_list: task %d: %v", input.TaskID, err)), nil
	}
	actor, err := t.store.GetAgentActor(ctx, input.ActorID)
	if err != nil {
		return basetools.ErrorResult(fmt.Sprintf("agentic_message_list: actor_id %d: %v", input.ActorID, err)), nil
	}
	if actor.TaskID != input.TaskID {
		return basetools.ErrorResult("agentic_message_list: actor must belong to task"), nil
	}

	rawBox := actor.InboxJSON
	if box == "outbox" {
		rawBox = actor.OutboxJSON
	}
	messages, err := parseActorMessages(rawBox)
	if err != nil {
		return basetools.ErrorResult(fmt.Sprintf("agentic_message_list: parse %s: %v", box, err)), nil
	}
	output := actorMessageListToolOutput{
		TaskID:   input.TaskID,
		ActorID:  actor.ID,
		Box:      box,
		Total:    len(messages),
		Messages: messages,
		Receipt: agenticToolReceipt{
			Tool:            ActorMessageListToolName,
			Action:          "list",
			ReadOnly:        true,
			Persistent:      false,
			ExecutionPolicy: "agentic_actor_message_observation",
			TaskID:          input.TaskID,
			ActorID:         actor.ID,
			Box:             box,
			Total:           len(messages),
		},
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return basetools.ErrorResult(fmt.Sprintf("agentic_message_list: marshal output: %v", err)), nil
	}
	return basetools.SuccessResult(string(raw)), nil
}

func appendActorMessage(raw *string, msg actorMailboxMessage) error {
	messages, err := parseActorMessages(*raw)
	if err != nil {
		return err
	}
	messages = append(messages, msg)
	encoded, err := json.Marshal(messages)
	if err != nil {
		return err
	}
	*raw = string(encoded)
	return nil
}

func parseActorMessages(raw string) ([]actorMailboxMessage, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "[]"
	}
	var messages []actorMailboxMessage
	if err := json.Unmarshal([]byte(raw), &messages); err != nil {
		return nil, err
	}
	if messages == nil {
		messages = []actorMailboxMessage{}
	}
	return messages, nil
}

func normalizeActorMessageBox(box string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(box)) {
	case "", "inbox":
		return "inbox", true
	case "outbox":
		return "outbox", true
	default:
		return "", false
	}
}
