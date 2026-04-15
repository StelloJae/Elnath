package fault

import (
	"net"
	"time"

	"github.com/stello/elnath/internal/fault/faulttype"
)

type IPCFaultConn struct {
	net.Conn
	injector Injector
	scenario *faulttype.Scenario
}

func NewIPCFaultConn(c net.Conn, inj Injector, s *faulttype.Scenario) *IPCFaultConn {
	return &IPCFaultConn{Conn: c, injector: inj, scenario: s}
}

func (c *IPCFaultConn) Write(b []byte) (int, error) {
	if !c.injector.Active() {
		return c.Conn.Write(b)
	}
	if c.scenario == nil || c.scenario.Category != faulttype.CategoryIPC {
		return c.Conn.Write(b)
	}
	if c.scenario.FaultType == faulttype.FaultWorkerPanic {
		return c.Conn.Write(b)
	}
	if c.injector.ShouldFault(c.scenario) {
		switch c.scenario.FaultType {
		case faulttype.FaultSlowConn:
			time.Sleep(c.scenario.FaultDuration)
		case faulttype.FaultPacketDrop:
			return len(b), nil
		case faulttype.FaultBackpressure:
			time.Sleep(c.scenario.FaultDuration)
		}
	}
	return c.Conn.Write(b)
}
