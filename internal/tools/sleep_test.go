package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSleepToolWaitsWithoutShellProcessAndReturnsReceipt(t *testing.T) {
	tool := NewSleepTool()

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"duration_ms":1,"reason":"poll_spacing"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error: %s", result.Output)
	}

	var output sleepToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, result.Output)
	}
	if output.RequestedMS != 1 || output.SleptMS != 1 || output.Capped || output.Reason != "poll_spacing" {
		t.Fatalf("output = %+v", output)
	}
	if output.Receipt.Tool != SleepToolName || output.Receipt.Action != sleepActionWait || !output.Receipt.ReadOnly || output.Receipt.Persistent {
		t.Fatalf("receipt identity = %+v", output.Receipt)
	}
	if !output.Receipt.ExecutionAvailable || output.Receipt.ExecutionPolicy != sleepPolicyTimer || output.Receipt.Status != sleepStatusSlept || output.Receipt.TimeoutMS != 1 {
		t.Fatalf("receipt execution = %+v", output.Receipt)
	}
}

func TestSleepToolCapsDuration(t *testing.T) {
	tool := &SleepTool{maxDuration: time.Millisecond}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"duration_ms":1000}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error: %s", result.Output)
	}

	var output sleepToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, result.Output)
	}
	if output.RequestedMS != 1000 || output.SleptMS != 1 || !output.Capped || output.Receipt.TimeoutMS != 1 {
		t.Fatalf("output = %+v", output)
	}
}

func TestSleepToolRejectsMissingOrInvalidDuration(t *testing.T) {
	tool := NewSleepTool()

	for _, params := range []json.RawMessage{nil, json.RawMessage(`{}`), json.RawMessage(`{"duration_ms":0}`)} {
		result, err := tool.Execute(context.Background(), params)
		if err != nil {
			t.Fatalf("Execute(%s): %v", params, err)
		}
		if !result.IsError || !strings.Contains(result.Output, "sleep:") {
			t.Fatalf("Execute(%s) = %+v, want sleep error", params, result)
		}
	}
}

func TestSleepToolHonorsContextCancellation(t *testing.T) {
	tool := NewSleepTool()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := tool.Execute(ctx, json.RawMessage(`{"duration_ms":1000}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.IsError || !strings.Contains(result.Output, "interrupted") {
		t.Fatalf("Execute canceled = %+v, want interrupted error", result)
	}
}

func TestSleepToolMetadataIsDeferredAndReadOnly(t *testing.T) {
	tool := NewSleepTool()

	if tool.Name() != SleepToolName {
		t.Fatalf("Name() = %q", tool.Name())
	}
	if !tool.IsConcurrencySafe(nil) || !tool.Reversible() || tool.ShouldCancelSiblingsOnError() {
		t.Fatalf("metadata = safe:%t reversible:%t cancel:%t", tool.IsConcurrencySafe(nil), tool.Reversible(), tool.ShouldCancelSiblingsOnError())
	}
	if got := tool.Scope(nil); len(got.ReadPaths) != 0 || len(got.WritePaths) != 0 || got.Network || got.Persistent {
		t.Fatalf("Scope(nil) = %+v, want empty read-only scope", got)
	}
	if !tool.DeferInitialToolSchema() || !ShouldDeferToolSchema(tool) {
		t.Fatal("sleep should defer initial schema")
	}
}
