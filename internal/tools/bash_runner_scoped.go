package tools

import (
	"context"
	"fmt"
)

// BashRunnerFactory constructs a fresh runner for one execution context.
// Stateful sandbox runners (Seatbelt/Bwrap proxy mode) own per-instance
// decision buffers and proxy drain goroutines, so production callers should
// avoid sharing those runner instances across concurrent work.
type BashRunnerFactory func(context.Context) (BashRunner, error)

type scopedBashRunner struct {
	factory BashRunnerFactory
	name    string
	probe   BashRunnerProbe
}

// NewScopedBashRunner returns a BashRunner facade that creates and closes a
// fresh underlying runner for every Run. The facade itself is safe to share
// from a global tool registry because it holds only immutable probe metadata;
// per-run proxy/audit/violation state lives in the short-lived underlying
// runner.
func NewScopedBashRunner(factory BashRunnerFactory) (BashRunner, error) {
	if factory == nil {
		return nil, fmt.Errorf("bash runner factory is required")
	}
	probeRunner, err := factory(context.Background())
	if err != nil {
		return nil, err
	}
	if probeRunner == nil {
		return nil, fmt.Errorf("bash runner factory returned nil runner")
	}
	name := probeRunner.Name()
	probe := probeRunner.Probe(context.Background())
	if err := probeRunner.Close(context.Background()); err != nil {
		return nil, fmt.Errorf("close probe runner: %w", err)
	}
	return &scopedBashRunner{
		factory: factory,
		name:    name,
		probe:   probe,
	}, nil
}

// NewScopedBashRunnerForConfig validates the configured runner eagerly, then
// returns a shareable facade that constructs isolated runner state per Run.
func NewScopedBashRunnerForConfig(cfg SandboxConfig) (BashRunner, error) {
	captured := SandboxConfig{
		Mode:                       cfg.Mode,
		NetworkAllowlist:           append([]string(nil), cfg.NetworkAllowlist...),
		NetworkDenylist:            append([]string(nil), cfg.NetworkDenylist...),
		NetworkProxyConnectTimeout: cfg.NetworkProxyConnectTimeout,
	}
	return NewScopedBashRunner(func(context.Context) (BashRunner, error) {
		return NewBashRunnerForConfig(captured)
	})
}

func (r *scopedBashRunner) Name() string { return r.name }

func (r *scopedBashRunner) Probe(context.Context) BashRunnerProbe { return r.probe }

func (r *scopedBashRunner) Run(ctx context.Context, req BashRunRequest) (BashRunResult, error) {
	runner, err := r.factory(ctx)
	if err != nil {
		return BashRunResult{
			Output:         fmt.Sprintf("sandbox runner setup failed: %v", err),
			IsError:        true,
			CWD:            req.DisplayCWD,
			Classification: "sandbox_setup_failed",
		}, nil
	}
	if runner == nil {
		return BashRunResult{
			Output:         "sandbox runner setup failed: factory returned nil runner",
			IsError:        true,
			CWD:            req.DisplayCWD,
			Classification: "sandbox_setup_failed",
		}, nil
	}

	res, runErr := runner.Run(ctx, req)
	closeErr := runner.Close(context.Background())
	if runErr != nil {
		return res, runErr
	}
	if closeErr != nil {
		return BashRunResult{
			Output:         fmt.Sprintf("sandbox runner cleanup failed: %v", closeErr),
			IsError:        true,
			CWD:            req.DisplayCWD,
			Classification: "sandbox_setup_failed",
		}, nil
	}
	return res, nil
}

func (r *scopedBashRunner) Close(context.Context) error { return nil }
