package scenarios

import (
	"regexp"
	"testing"
	"time"

	"github.com/stello/elnath/internal/fault/faulttype"
)

func TestAllReturnsTenScenarios(t *testing.T) {
	if got := len(All()); got != 10 {
		t.Fatalf("len(All()) = %d, want 10", got)
	}
}

func TestScenarioThresholdsValid(t *testing.T) {
	for _, scenario := range All() {
		if scenario.Threshold.MaxRuns <= 0 {
			t.Fatalf("%s MaxRuns = %d, want > 0", scenario.Name, scenario.Threshold.MaxRuns)
		}
		if scenario.Threshold.RecoveryRate <= 0 {
			t.Fatalf("%s RecoveryRate = %f, want > 0", scenario.Name, scenario.Threshold.RecoveryRate)
		}
	}
}

func TestScenarioNamesAreSlugs(t *testing.T) {
	re := regexp.MustCompile(`^[a-z0-9-]+$`)
	for _, scenario := range All() {
		if !re.MatchString(scenario.Name) {
			t.Fatalf("%s does not match slug pattern", scenario.Name)
		}
	}
}

func TestScenarioFaultRateRange(t *testing.T) {
	for _, scenario := range All() {
		if scenario.FaultRate < 0 || scenario.FaultRate > 1 {
			t.Fatalf("%s FaultRate = %f, want [0,1]", scenario.Name, scenario.FaultRate)
		}
	}
}

func TestScenarioRecoveryRateSane(t *testing.T) {
	for _, scenario := range All() {
		if scenario.Threshold.RecoveryRate <= scenario.FaultRate*0.5 {
			t.Fatalf("%s RecoveryRate = %f, FaultRate = %f, want RecoveryRate > FaultRate*0.5", scenario.Name, scenario.Threshold.RecoveryRate, scenario.FaultRate)
		}
	}
}

func TestIPCSleepCapValidator(t *testing.T) {
	for _, scenario := range All() {
		if (scenario.FaultType == faulttype.FaultSlowConn || scenario.FaultType == faulttype.FaultBackpressure) && scenario.FaultDuration > 5*time.Second {
			t.Fatalf("%s FaultDuration = %s, want <= 5s", scenario.Name, scenario.FaultDuration)
		}
	}
}

func TestScenarioNumbersMatchSpec(t *testing.T) {
	want := map[string]struct {
		category            faulttype.Category
		faultType           faulttype.FaultType
		faultRate           float64
		faultDuration       time.Duration
		burstLimit          int
		recoveryRate        float64
		maxRuns             int
		maxRecoveryAttempts int
		targetTool          string
	}{
		"tool-bash-transient-fail":   {category: faulttype.CategoryTool, faultType: faulttype.FaultTransientError, faultRate: 0.20, recoveryRate: 0.95, maxRuns: 20, maxRecoveryAttempts: 3, targetTool: "bash"},
		"tool-file-read-perm-denied": {category: faulttype.CategoryTool, faultType: faulttype.FaultPermDenied, faultRate: 0.10, recoveryRate: 0.90, maxRuns: 20, maxRecoveryAttempts: 2, targetTool: "read_file"},
		"tool-web-timeout":           {category: faulttype.CategoryTool, faultType: faulttype.FaultTimeout, faultRate: 0.10, recoveryRate: 0.90, maxRuns: 20, maxRecoveryAttempts: 3, targetTool: "web_fetch"},
		"llm-anthropic-429-burst":    {category: faulttype.CategoryLLM, faultType: faulttype.FaultHTTP429Burst, faultRate: 1.00, burstLimit: 3, recoveryRate: 0.95, maxRuns: 15, maxRecoveryAttempts: 5},
		"llm-codex-malformed-json":   {category: faulttype.CategoryLLM, faultType: faulttype.FaultMalformedJSON, faultRate: 0.15, recoveryRate: 0.85, maxRuns: 20, maxRecoveryAttempts: 3},
		"llm-provider-timeout":       {category: faulttype.CategoryLLM, faultType: faulttype.FaultTimeout, faultRate: 0.30, recoveryRate: 0.80, maxRuns: 15, maxRecoveryAttempts: 3},
		"ipc-socket-slow":            {category: faulttype.CategoryIPC, faultType: faulttype.FaultSlowConn, faultRate: 1.00, faultDuration: 50 * time.Millisecond, recoveryRate: 0.98, maxRuns: 20, maxRecoveryAttempts: 1},
		"ipc-socket-drop":            {category: faulttype.CategoryIPC, faultType: faulttype.FaultPacketDrop, faultRate: 0.05, recoveryRate: 0.90, maxRuns: 20, maxRecoveryAttempts: 3},
		"ipc-queue-backpressure":     {category: faulttype.CategoryIPC, faultType: faulttype.FaultBackpressure, faultRate: 1.00, faultDuration: 500 * time.Millisecond, recoveryRate: 0.90, maxRuns: 15, maxRecoveryAttempts: 2},
		"ipc-worker-panic-recover":   {category: faulttype.CategoryIPC, faultType: faulttype.FaultWorkerPanic, faultRate: 0.10, recoveryRate: 0.95, maxRuns: 20, maxRecoveryAttempts: 1},
	}

	for _, scenario := range All() {
		exp, ok := want[scenario.Name]
		if !ok {
			t.Fatalf("unexpected scenario %q", scenario.Name)
		}
		if scenario.Category != exp.category || scenario.FaultType != exp.faultType || scenario.FaultRate != exp.faultRate || scenario.FaultDuration != exp.faultDuration || scenario.BurstLimit != exp.burstLimit || scenario.Threshold.RecoveryRate != exp.recoveryRate || scenario.Threshold.MaxRuns != exp.maxRuns || scenario.Threshold.MaxRecoveryAttempts != exp.maxRecoveryAttempts || scenario.TargetTool != exp.targetTool {
			t.Fatalf("scenario %s = %#v, want %#v", scenario.Name, scenario, exp)
		}
	}
}
