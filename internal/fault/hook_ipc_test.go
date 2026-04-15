package fault

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/stello/elnath/internal/fault/faulttype"
)

type mockConn struct {
	writes int
	buf    bytes.Buffer
}

func (c *mockConn) Read(b []byte) (int, error)       { return 0, nil }
func (c *mockConn) Write(b []byte) (int, error)      { c.writes++; return c.buf.Write(b) }
func (c *mockConn) Close() error                     { return nil }
func (c *mockConn) LocalAddr() net.Addr              { return nil }
func (c *mockConn) RemoteAddr() net.Addr             { return nil }
func (c *mockConn) SetDeadline(time.Time) error      { return nil }
func (c *mockConn) SetReadDeadline(time.Time) error  { return nil }
func (c *mockConn) SetWriteDeadline(time.Time) error { return nil }

func TestIPCFaultConnSlowConnAddsLatency(t *testing.T) {
	inner := &mockConn{}
	inj := &toolHookInjector{active: true, shouldFault: true}
	s := testScenario("ipc-socket-slow", faulttype.CategoryIPC, faulttype.FaultSlowConn)
	s.FaultDuration = 20 * time.Millisecond
	conn := NewIPCFaultConn(inner, inj, s)

	start := time.Now()
	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatalf("Write() error = %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed < s.FaultDuration {
		t.Fatalf("Write() elapsed = %s, want >= %s", elapsed, s.FaultDuration)
	}
}

func TestIPCFaultConnPacketDropReturnsSuccessWithoutInnerWrite(t *testing.T) {
	inner := &mockConn{}
	inj := &toolHookInjector{active: true, shouldFault: true}
	s := testScenario("ipc-socket-drop", faulttype.CategoryIPC, faulttype.FaultPacketDrop)
	conn := NewIPCFaultConn(inner, inj, s)

	n, err := conn.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write() error = %v, want nil", err)
	}
	if n != len("hello") {
		t.Fatalf("Write() n = %d, want %d", n, len("hello"))
	}
	if inner.writes != 0 {
		t.Fatalf("inner writes = %d, want 0", inner.writes)
	}
}

func TestIPCFaultConnDelegatesWhenInactive(t *testing.T) {
	inner := &mockConn{}
	conn := NewIPCFaultConn(inner, &toolHookInjector{}, testScenario("ipc-socket-slow", faulttype.CategoryIPC, faulttype.FaultSlowConn))

	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatalf("Write() error = %v, want nil", err)
	}
	if inner.writes != 1 {
		t.Fatalf("inner writes = %d, want 1", inner.writes)
	}
}

func TestIPCFaultConnSkipsWrongCategory(t *testing.T) {
	inner := &mockConn{}
	inj := &toolHookInjector{active: true, shouldFault: true}
	conn := NewIPCFaultConn(inner, inj, testScenario("tool-bash-transient-fail", faulttype.CategoryTool, faulttype.FaultTransientError))

	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatalf("Write() error = %v, want nil", err)
	}
	if inner.writes != 1 {
		t.Fatalf("inner writes = %d, want 1", inner.writes)
	}
}
