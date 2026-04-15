package faulttype

import "time"

type Category string

const (
	CategoryTool Category = "tool"
	CategoryLLM  Category = "llm"
	CategoryIPC  Category = "ipc"
)

type FaultType string

const (
	FaultTransientError FaultType = "transient_error"
	FaultPermDenied     FaultType = "perm_denied"
	FaultTimeout        FaultType = "timeout"
	FaultMalformedJSON  FaultType = "malformed_json"
	FaultHTTP429Burst   FaultType = "http_429_burst"
	FaultSlowConn       FaultType = "slow_conn"
	FaultPacketDrop     FaultType = "packet_drop"
	FaultBackpressure   FaultType = "backpressure"
	FaultWorkerPanic    FaultType = "worker_panic"
)

type Threshold struct {
	RecoveryRate        float64
	MaxRuns             int
	MaxRecoveryAttempts int
}

type Scenario struct {
	Name          string
	Category      Category
	FaultType     FaultType
	Description   string
	FaultRate     float64
	FaultDuration time.Duration
	Threshold     Threshold
	TargetTool    string
	BurstLimit    int
}
