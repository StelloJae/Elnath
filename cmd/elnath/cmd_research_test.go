package main

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/identity"
	"github.com/stello/elnath/internal/onboarding"
	researchpkg "github.com/stello/elnath/internal/research"
)

func TestCmdResearchUsage(t *testing.T) {
	stdout, _ := captureOutput(t, func() {
		if err := cmdResearch(context.Background(), nil); err != nil {
			t.Fatalf("cmdResearch usage: %v", err)
		}
	})
	if !strings.Contains(stdout, "Usage: elnath research") {
		t.Fatalf("stdout = %q, want research usage", stdout)
	}
}

func TestCmdResearchUnknownSubcommand(t *testing.T) {
	err := cmdResearch(context.Background(), []string{"bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown research subcommand: bogus") {
		t.Fatalf("cmdResearch(bogus) err = %v, want unknown subcommand", err)
	}
}

func TestResearchStartRequiresTopic(t *testing.T) {
	err := researchStart(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "topic required") {
		t.Fatalf("researchStart() err = %v, want topic required", err)
	}
}

func TestResearchStartQueuesResearchPayload(t *testing.T) {
	t.Setenv("USER", "stello")
	socketPath := testSocketPath(t, "research-start")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	var queuedPayload string
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		var req daemon.IPCRequest
		if err := json.NewDecoder(conn).Decode(&req); err != nil {
			return
		}
		_ = json.Unmarshal(req.Payload, &queuedPayload)
		_ = json.NewEncoder(conn).Encode(daemon.IPCResponse{
			OK:   true,
			Data: map[string]any{"task_id": 42, "existed": false},
		})
	}()

	cfgPath := writeDaemonTestConfig(t, onboarding.En, socketPath)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, _ := captureOutput(t, func() {
		if err := researchStart(context.Background(), []string{"go", "idiomatic", "error", "handling"}); err != nil {
			t.Fatalf("researchStart: %v", err)
		}
	})
	if !strings.Contains(stdout, "Research task queued: 42") {
		t.Fatalf("stdout = %q, want queued output", stdout)
	}
	<-done

	payload := daemon.ParseTaskPayload(queuedPayload)
	if payload.Type != daemon.TaskTypeResearch {
		t.Fatalf("payload.Type = %q, want research", payload.Type)
	}
	if payload.Prompt != "go idiomatic error handling" {
		t.Fatalf("payload.Prompt = %q", payload.Prompt)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	wantPrincipal := identity.ResolveCLIPrincipal(nil, "", cwd)
	if payload.Principal != wantPrincipal {
		t.Fatalf("payload.Principal = %+v, want %+v", payload.Principal, wantPrincipal)
	}
}

func TestResearchStartReportsDeduplicatedTask(t *testing.T) {
	socketPath := testSocketPath(t, "research-dedup")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		var req daemon.IPCRequest
		if err := json.NewDecoder(conn).Decode(&req); err != nil {
			return
		}
		_ = json.NewEncoder(conn).Encode(daemon.IPCResponse{
			OK:   true,
			Data: map[string]any{"task_id": 42, "existed": true},
		})
	}()

	cfgPath := writeDaemonTestConfig(t, onboarding.En, socketPath)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, _ := captureOutput(t, func() {
		if err := researchStart(context.Background(), []string{"go", "idiomatic", "error", "handling"}); err != nil {
			t.Fatalf("researchStart: %v", err)
		}
	})
	if !strings.Contains(stdout, "already running") {
		t.Fatalf("stdout = %q, want deduplicated output", stdout)
	}
	<-done
}

func TestResearchStatusFiltersNonResearchTasks(t *testing.T) {
	socketPath := testSocketPath(t, "research-status")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	researchPayload := daemon.EncodeTaskPayload(daemon.TaskPayload{Type: daemon.TaskTypeResearch, Prompt: "ambient research loop"})
	now := time.Now().UnixMilli()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		var req daemon.IPCRequest
		if err := json.NewDecoder(conn).Decode(&req); err != nil {
			return
		}
		_ = json.NewEncoder(conn).Encode(daemon.IPCResponse{
			OK: true,
			Data: map[string]any{"tasks": []any{
				map[string]any{"id": 1, "status": "done", "payload": "tell me a joke", "updated_at": now},
				map[string]any{"id": 2, "status": "running", "payload": researchPayload, "updated_at": now},
			}},
		})
	}()

	cfgPath := writeDaemonTestConfig(t, onboarding.En, socketPath)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, _ := captureOutput(t, func() {
		if err := researchStatus(context.Background(), nil); err != nil {
			t.Fatalf("researchStatus: %v", err)
		}
	})
	if !strings.Contains(stdout, "ambient research loop") {
		t.Fatalf("stdout = %q, want research topic", stdout)
	}
	if strings.Contains(stdout, "tell me a joke") {
		t.Fatalf("stdout = %q, want non-research task filtered out", stdout)
	}
	<-done
}

func TestResearchResultPrintsDecodedResearchResult(t *testing.T) {
	socketPath := testSocketPath(t, "research-result")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	researchPayload := daemon.EncodeTaskPayload(daemon.TaskPayload{Type: daemon.TaskTypeResearch, Prompt: "ambient research loop"})
	rawResult, err := json.Marshal(researchpkg.ResearchResult{
		Topic:     "ambient research loop",
		Summary:   "Research summary",
		TotalCost: 1.25,
		Rounds: []researchpkg.RoundResult{{
			Round:      0,
			Hypothesis: researchpkg.Hypothesis{Statement: "Test hypothesis"},
			Result:     researchpkg.ExperimentResult{Findings: "Found something", Confidence: "high", Supported: true},
		}},
	})
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		var req daemon.IPCRequest
		if err := json.NewDecoder(conn).Decode(&req); err != nil {
			return
		}
		_ = json.NewEncoder(conn).Encode(daemon.IPCResponse{
			OK: true,
			Data: map[string]any{"tasks": []any{
				map[string]any{"id": 7, "status": "done", "payload": researchPayload, "summary": "Research summary", "result": string(rawResult)},
			}},
		})
	}()

	cfgPath := writeDaemonTestConfig(t, onboarding.En, socketPath)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, _ := captureOutput(t, func() {
		if err := researchResult(context.Background(), []string{"7"}); err != nil {
			t.Fatalf("researchResult: %v", err)
		}
	})
	if !strings.Contains(stdout, "Topic: ambient research loop") {
		t.Fatalf("stdout = %q, want topic", stdout)
	}
	if !strings.Contains(stdout, "Research summary") {
		t.Fatalf("stdout = %q, want summary", stdout)
	}
	if !strings.Contains(stdout, "Rounds: 1") {
		t.Fatalf("stdout = %q, want rounds count", stdout)
	}
	<-done
}
