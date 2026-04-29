package signals

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/ambient"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/scheduler"
)

const (
	SourceAmbient   = "ambient"
	SourceManual    = "manual"
	SourceScheduler = "scheduler"

	TypeAmbientBootTask = "ambient_boot_task"
	TypeDaemonSubmit    = "daemon_submit"
	TypeScheduledTask   = "scheduled_task"
)

type Bridge struct {
	store *agentic.Store
}

func NewBridge(store *agentic.Store) *Bridge {
	return &Bridge{store: store}
}

func (b *Bridge) RecordScheduledSignal(ctx context.Context, task scheduler.ScheduledTask, queueTaskID int64, existed bool, enqueueErr error) error {
	if b == nil || b.store == nil {
		return nil
	}
	observedAt := time.Now()
	watcher, err := b.sourceWatcher(ctx, SourceScheduler)
	if err != nil {
		return err
	}
	taskHash := hashString(task.Name)
	cursor := occurrenceCursor("scheduler", taskHash, queueTaskID, existed, enqueueErr, observedAt)
	payload := map[string]any{
		"task_name_hash": hashString(task.Name),
		"type":           task.Type,
		"prompt_hash":    hashString(task.Prompt),
		"prompt_len":     len(task.Prompt),
		"queue_task_id":  queueTaskID,
		"existed":        existed,
	}
	if enqueueErr != nil {
		payload["enqueue_error"] = true
		payload["enqueue_error_hash"] = hashString(enqueueErr.Error())
	}
	if err := b.record(ctx, agentic.GoalSignal{
		WatcherID:   watcher.ID,
		Source:      SourceScheduler,
		Type:        TypeScheduledTask,
		PayloadJSON: mustPayloadJSON(payload),
		Severity:    1,
		Status:      agentic.SignalStatusNew,
		DedupeKey:   cursor,
		ObservedAt:  observedAt,
	}); err != nil {
		return err
	}
	_, err = b.store.UpdateSignalWatcherCursor(ctx, watcher.ID, cursor)
	return err
}

func (b *Bridge) RecordAmbientSignal(ctx context.Context, task ambient.BootTask) error {
	if b == nil || b.store == nil {
		return nil
	}
	observedAt := time.Now()
	watcher, err := b.sourceWatcher(ctx, SourceAmbient)
	if err != nil {
		return err
	}
	identity := task.Path
	if identity == "" {
		identity = task.Title
	}
	cursor := fmt.Sprintf("ambient:%s:%d", hashString(identity), observedAt.UnixNano())
	payload := map[string]any{
		"path_hash":     hashString(task.Path),
		"title_hash":    hashString(task.Title),
		"prompt_hash":   hashString(task.Prompt),
		"prompt_len":    len(task.Prompt),
		"schedule":      task.Schedule.Type,
		"silent":        task.Silent,
		"tag_count":     len(task.Tags),
		"identity_hash": hashString(identity),
	}
	if err := b.record(ctx, agentic.GoalSignal{
		WatcherID:   watcher.ID,
		Source:      SourceAmbient,
		Type:        TypeAmbientBootTask,
		PayloadJSON: mustPayloadJSON(payload),
		Severity:    1,
		Status:      agentic.SignalStatusNew,
		DedupeKey:   cursor,
		ObservedAt:  observedAt,
	}); err != nil {
		return err
	}
	_, err = b.store.UpdateSignalWatcherCursor(ctx, watcher.ID, cursor)
	return err
}

func (b *Bridge) RecordManualSubmitSignal(ctx context.Context, payload string, queueTaskID int64, existed bool) error {
	if b == nil || b.store == nil {
		return nil
	}
	observedAt := time.Now()
	watcher, err := b.sourceWatcher(ctx, SourceManual)
	if err != nil {
		return err
	}
	parsed := daemon.ParseTaskPayload(payload)
	prompt := parsed.Prompt
	if prompt == "" {
		prompt = payload
	}
	cursor := manualCursor(queueTaskID, existed, observedAt)
	body := map[string]any{
		"prompt_hash":   hashString(prompt),
		"prompt_len":    len(prompt),
		"task_type":     parsed.Type,
		"queue_task_id": queueTaskID,
		"existed":       existed,
	}
	if err := b.record(ctx, agentic.GoalSignal{
		WatcherID:   watcher.ID,
		Source:      SourceManual,
		Type:        TypeDaemonSubmit,
		PayloadJSON: mustPayloadJSON(body),
		Severity:    1,
		Status:      agentic.SignalStatusNew,
		DedupeKey:   cursor,
		ObservedAt:  observedAt,
	}); err != nil {
		return err
	}
	_, err = b.store.UpdateSignalWatcherCursor(ctx, watcher.ID, cursor)
	return err
}

func (b *Bridge) record(ctx context.Context, signal agentic.GoalSignal) error {
	if signal.Fingerprint == "" {
		signal.Fingerprint = fingerprint(signal.Source, signal.Type, signal.DedupeKey, signal.PayloadJSON)
	}
	_, _, err := b.store.CreateOrGetGoalSignal(ctx, signal)
	return err
}

func (b *Bridge) sourceWatcher(ctx context.Context, source string) (*agentic.SignalWatcher, error) {
	watcher, _, err := b.store.CreateOrGetSignalWatcher(ctx, agentic.SignalWatcher{
		Source:     source,
		ConfigJSON: fmt.Sprintf(`{"bridge":"agentic_pr3","source":%q}`, source),
		Enabled:    true,
	})
	return watcher, err
}

func occurrenceCursor(source, identityHash string, queueTaskID int64, existed bool, eventErr error, observedAt time.Time) string {
	if queueTaskID > 0 && !existed {
		return fmt.Sprintf("%s:%s:queue:%d", source, identityHash, queueTaskID)
	}
	if queueTaskID > 0 {
		return fmt.Sprintf("%s:%s:queue:%d:observed:%d", source, identityHash, queueTaskID, observedAt.UnixNano())
	}
	if eventErr != nil {
		return fmt.Sprintf("%s:%s:error:%s:%d", source, identityHash, hashString(eventErr.Error()), observedAt.UnixNano())
	}
	return fmt.Sprintf("%s:%s:observed:%d", source, identityHash, observedAt.UnixNano())
}

func manualCursor(queueTaskID int64, existed bool, observedAt time.Time) string {
	if queueTaskID > 0 && !existed {
		return fmt.Sprintf("manual:queue:%d", queueTaskID)
	}
	if queueTaskID > 0 {
		return fmt.Sprintf("manual:queue:%d:observed:%d", queueTaskID, observedAt.UnixNano())
	}
	return fmt.Sprintf("manual:observed:%d", observedAt.UnixNano())
}

func mustPayloadJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{}`
	}
	return string(b)
}

func hashString(value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func fingerprint(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
