package scenarios

import (
	"time"

	"github.com/stello/elnath/internal/fault/faulttype"
)

func All() []*faulttype.Scenario {
	ss := []*faulttype.Scenario{
		toolBashTransientFail(),
		toolFileReadPermDenied(),
		toolWebTimeout(),
		llmAnthropic429Burst(),
		llmCodexMalformedJSON(),
		llmProviderTimeout(),
		ipcSocketSlow(),
		ipcSocketDrop(),
		ipcQueueBackpressure(),
		ipcWorkerPanicRecover(),
	}
	for _, scenario := range ss {
		if (scenario.FaultType == faulttype.FaultSlowConn || scenario.FaultType == faulttype.FaultBackpressure) && scenario.FaultDuration > 5*time.Second {
			panic("fault: scenario " + scenario.Name + " FaultDuration exceeds 5s cap")
		}
	}
	return ss
}

func toolBashTransientFail() *faulttype.Scenario {
	return &faulttype.Scenario{
		Name:        "tool-bash-transient-fail",
		Category:    faulttype.CategoryTool,
		FaultType:   faulttype.FaultTransientError,
		Description: "Inject transient failures into bash tool execution.",
		FaultRate:   0.20,
		Threshold:   faulttype.Threshold{RecoveryRate: 0.95, MaxRuns: 20, MaxRecoveryAttempts: 3},
		TargetTool:  "bash",
	}
}

func toolFileReadPermDenied() *faulttype.Scenario {
	return &faulttype.Scenario{
		Name:        "tool-file-read-perm-denied",
		Category:    faulttype.CategoryTool,
		FaultType:   faulttype.FaultPermDenied,
		Description: "Inject permission denied failures into read_file.",
		FaultRate:   0.10,
		Threshold:   faulttype.Threshold{RecoveryRate: 0.90, MaxRuns: 20, MaxRecoveryAttempts: 2},
		TargetTool:  "read_file",
	}
}

func toolWebTimeout() *faulttype.Scenario {
	return &faulttype.Scenario{
		Name:        "tool-web-timeout",
		Category:    faulttype.CategoryTool,
		FaultType:   faulttype.FaultTimeout,
		Description: "Inject timeout failures into web_fetch.",
		FaultRate:   0.10,
		Threshold:   faulttype.Threshold{RecoveryRate: 0.90, MaxRuns: 20, MaxRecoveryAttempts: 3},
		TargetTool:  "web_fetch",
	}
}

func llmAnthropic429Burst() *faulttype.Scenario {
	return &faulttype.Scenario{
		Name:        "llm-anthropic-429-burst",
		Category:    faulttype.CategoryLLM,
		FaultType:   faulttype.FaultHTTP429Burst,
		Description: "Inject a burst of HTTP 429 responses into llm streaming.",
		FaultRate:   1.00,
		Threshold:   faulttype.Threshold{RecoveryRate: 0.95, MaxRuns: 15, MaxRecoveryAttempts: 5},
		BurstLimit:  3,
	}
}

func llmCodexMalformedJSON() *faulttype.Scenario {
	return &faulttype.Scenario{
		Name:        "llm-codex-malformed-json",
		Category:    faulttype.CategoryLLM,
		FaultType:   faulttype.FaultMalformedJSON,
		Description: "Inject malformed JSON failures into llm streaming.",
		FaultRate:   0.15,
		Threshold:   faulttype.Threshold{RecoveryRate: 0.85, MaxRuns: 20, MaxRecoveryAttempts: 3},
	}
}

func llmProviderTimeout() *faulttype.Scenario {
	return &faulttype.Scenario{
		Name:        "llm-provider-timeout",
		Category:    faulttype.CategoryLLM,
		FaultType:   faulttype.FaultTimeout,
		Description: "Inject provider timeout failures into llm streaming.",
		FaultRate:   0.30,
		Threshold:   faulttype.Threshold{RecoveryRate: 0.80, MaxRuns: 15, MaxRecoveryAttempts: 3},
	}
}

func ipcSocketSlow() *faulttype.Scenario {
	return &faulttype.Scenario{
		Name:          "ipc-socket-slow",
		Category:      faulttype.CategoryIPC,
		FaultType:     faulttype.FaultSlowConn,
		Description:   "Inject 50ms latency into IPC writes.",
		FaultRate:     1.00,
		FaultDuration: 50 * time.Millisecond,
		Threshold:     faulttype.Threshold{RecoveryRate: 0.98, MaxRuns: 20, MaxRecoveryAttempts: 1},
	}
}

func ipcSocketDrop() *faulttype.Scenario {
	return &faulttype.Scenario{
		Name:        "ipc-socket-drop",
		Category:    faulttype.CategoryIPC,
		FaultType:   faulttype.FaultPacketDrop,
		Description: "Inject dropped IPC writes.",
		FaultRate:   0.05,
		Threshold:   faulttype.Threshold{RecoveryRate: 0.90, MaxRuns: 20, MaxRecoveryAttempts: 3},
		// daemon에 retransmit 로직이 없으면 baseline fail 가능 - spec §13 참조.
	}
}

func ipcQueueBackpressure() *faulttype.Scenario {
	return &faulttype.Scenario{
		Name:          "ipc-queue-backpressure",
		Category:      faulttype.CategoryIPC,
		FaultType:     faulttype.FaultBackpressure,
		Description:   "Inject 500ms backpressure into IPC writes.",
		FaultRate:     1.00,
		FaultDuration: 500 * time.Millisecond,
		Threshold:     faulttype.Threshold{RecoveryRate: 0.90, MaxRuns: 15, MaxRecoveryAttempts: 2},
	}
}

func ipcWorkerPanicRecover() *faulttype.Scenario {
	return &faulttype.Scenario{
		Name:        "ipc-worker-panic-recover",
		Category:    faulttype.CategoryIPC,
		FaultType:   faulttype.FaultWorkerPanic,
		Description: "Inject worker panic faults into daemon workers.",
		FaultRate:   0.10,
		Threshold:   faulttype.Threshold{RecoveryRate: 0.95, MaxRuns: 20, MaxRecoveryAttempts: 1},
	}
}
