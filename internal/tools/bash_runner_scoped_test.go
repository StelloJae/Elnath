package tools

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

type scopedAttributionFakeRunner struct {
	ready       *sync.WaitGroup
	release     <-chan struct{}
	decisionBuf *boundedDecisionBuffer
}

func (r *scopedAttributionFakeRunner) Name() string { return "scoped-attribution-fake" }

func (r *scopedAttributionFakeRunner) Probe(context.Context) BashRunnerProbe {
	return BashRunnerProbe{
		Available:       true,
		Name:            r.Name(),
		ExecutionMode:   "test_scoped",
		PolicyName:      "test",
		SandboxEnforced: true,
	}
}

func (r *scopedAttributionFakeRunner) Run(ctx context.Context, req BashRunRequest) (BashRunResult, error) {
	sessionID := SessionIDFrom(ctx)
	deny, err := NewDeny(SourceNetworkProxy, ReasonNotInAllowlist, sessionID+"-blocked.example", 443, ProtocolHTTPSConnect)
	if err != nil {
		return BashRunResult{}, err
	}
	allow, err := NewAllow(SourceNetworkProxy, sessionID+"-allowed.example", 443, ProtocolHTTPSConnect)
	if err != nil {
		return BashRunResult{}, err
	}
	r.decisionBuf.Push(deny)
	r.decisionBuf.Push(allow)

	r.ready.Done()
	<-r.release

	denyDecisions, allowDecisions, denyDrops, allowDrops := r.decisionBuf.Drain()
	res := BashRunResult{
		Output:               "BASH RESULT\nstatus: success\n",
		Classification:       "success",
		CWD:                  req.DisplayCWD,
		ViolationDropCount:   denyDrops,
		AuditRecordDropCount: allowDrops,
	}
	for _, d := range denyDecisions {
		res.Violations = append(res.Violations, SandboxViolation{
			Source:   string(d.Source),
			Host:     d.Host,
			Port:     uint16(d.Port),
			Protocol: string(d.Protocol),
			Reason:   string(d.Reason),
		})
	}
	res.AuditRecords = projectAuditRecordsFromAllowOnly(allowDecisions)
	return res, nil
}

func (r *scopedAttributionFakeRunner) Close(context.Context) error { return nil }

func TestScopedBashRunner_ConcurrentRunsDoNotShareNetworkProxyViolations(t *testing.T) {
	resA, resB := runConcurrentScopedAttributionRuns(t)

	assertOnlyViolationHost(t, resA, "sess-A-blocked.example")
	assertOnlyViolationHost(t, resB, "sess-B-blocked.example")
}

func TestScopedBashRunner_ConcurrentRunsDoNotSharePermittedAuditRecords(t *testing.T) {
	resA, resB := runConcurrentScopedAttributionRuns(t)

	assertOnlyAuditHost(t, resA, "sess-A-allowed.example")
	assertOnlyAuditHost(t, resB, "sess-B-allowed.example")
}

func runConcurrentScopedAttributionRuns(t *testing.T) (BashRunResult, BashRunResult) {
	t.Helper()

	var factoryCalls atomic.Int64
	var ready sync.WaitGroup
	ready.Add(2)
	release := make(chan struct{})

	runner, err := NewScopedBashRunner(func(context.Context) (BashRunner, error) {
		factoryCalls.Add(1)
		return &scopedAttributionFakeRunner{
			ready:       &ready,
			release:     release,
			decisionBuf: newDecisionBuffer(),
		}, nil
	})
	if err != nil {
		t.Fatalf("NewScopedBashRunner: %v", err)
	}
	defer runner.Close(context.Background())

	type runResult struct {
		res BashRunResult
		err error
	}
	outA := make(chan runResult, 1)
	outB := make(chan runResult, 1)

	go func() {
		res, err := runner.Run(WithSessionID(context.Background(), "sess-A"), BashRunRequest{DisplayCWD: "."})
		outA <- runResult{res: res, err: err}
	}()
	go func() {
		res, err := runner.Run(WithSessionID(context.Background(), "sess-B"), BashRunRequest{DisplayCWD: "."})
		outB <- runResult{res: res, err: err}
	}()

	ready.Wait()
	close(release)

	gotA := <-outA
	gotB := <-outB
	if gotA.err != nil {
		t.Fatalf("run A: %v", gotA.err)
	}
	if gotB.err != nil {
		t.Fatalf("run B: %v", gotB.err)
	}
	if got := factoryCalls.Load(); got < 3 {
		t.Fatalf("factory calls = %d, want at least 3 (probe + one runner per concurrent run)", got)
	}
	return gotA.res, gotB.res
}

func assertOnlyViolationHost(t *testing.T, res BashRunResult, want string) {
	t.Helper()
	if len(res.Violations) != 1 {
		t.Fatalf("len(Violations) = %d, want 1: %+v", len(res.Violations), res.Violations)
	}
	if got := res.Violations[0].Host; got != want {
		t.Fatalf("violation host = %q, want %q", got, want)
	}
}

func assertOnlyAuditHost(t *testing.T, res BashRunResult, want string) {
	t.Helper()
	if len(res.AuditRecords) != 1 {
		t.Fatalf("len(AuditRecords) = %d, want 1: %+v", len(res.AuditRecords), res.AuditRecords)
	}
	if got := res.AuditRecords[0].Host; got != want {
		t.Fatalf("audit host = %q, want %q", got, want)
	}
}

func TestScopedBashRunner_RunSetupFailureIsToolResultError(t *testing.T) {
	runner, err := NewScopedBashRunner(func(ctx context.Context) (BashRunner, error) {
		if SessionIDFrom(ctx) == "setup-fails" {
			return nil, fmt.Errorf("proxy setup failed")
		}
		return &scopedAttributionFakeRunner{
			ready:       &sync.WaitGroup{},
			release:     closedChannelForTest(),
			decisionBuf: newDecisionBuffer(),
		}, nil
	})
	if err != nil {
		t.Fatalf("NewScopedBashRunner: %v", err)
	}
	defer runner.Close(context.Background())

	res, err := runner.Run(WithSessionID(context.Background(), "setup-fails"), BashRunRequest{DisplayCWD: "."})
	if err != nil {
		t.Fatalf("Run returned Go error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("setup failure must surface as Result error")
	}
	if res.Classification != "sandbox_setup_failed" {
		t.Fatalf("Classification = %q, want sandbox_setup_failed", res.Classification)
	}
}

func closedChannelForTest() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}
