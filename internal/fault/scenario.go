package fault

import "github.com/stello/elnath/internal/fault/faulttype"

type (
	Scenario  = faulttype.Scenario
	Threshold = faulttype.Threshold
	Category  = faulttype.Category
	FaultType = faulttype.FaultType
)

const (
	CategoryTool = faulttype.CategoryTool
	CategoryLLM  = faulttype.CategoryLLM
	CategoryIPC  = faulttype.CategoryIPC

	FaultTransientError = faulttype.FaultTransientError
	FaultPermDenied     = faulttype.FaultPermDenied
	FaultTimeout        = faulttype.FaultTimeout
	FaultMalformedJSON  = faulttype.FaultMalformedJSON
	FaultHTTP429Burst   = faulttype.FaultHTTP429Burst
	FaultSlowConn       = faulttype.FaultSlowConn
	FaultPacketDrop     = faulttype.FaultPacketDrop
	FaultBackpressure   = faulttype.FaultBackpressure
	FaultWorkerPanic    = faulttype.FaultWorkerPanic
)
