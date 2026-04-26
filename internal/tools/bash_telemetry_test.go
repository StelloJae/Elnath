package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// B3b-4-1 Phase D: slog telemetry extension.
//
// emitBashTelemetry MUST surface:
//   - violation_count (existing)
//   - violation_drop_count (N4: events dropped by ChannelEventSink)
//   - structured violations list with {source, host, port, protocol,
//     reason} per entry
//
// N6 retention policy MUST be respected: telemetry MUST NOT log full
// URL paths, query strings, HTTP headers, or request bodies. Tests
// pin the negative invariant.

// telemetryFakeRunnerWithViolations returns a runner that emits the
// canned violations list and Output so the slog handler captures the
// full telemetry record.
func telemetryFakeRunnerWithViolations(violations []SandboxViolation, dropCount int) *fakeBashRunner {
	r := &fakeBashRunner{
		runResult: BashRunResult{
			Output:             "BASH RESULT\nstatus: success\n",
			IsError:            false,
			Classification:     "success",
			Violations:         violations,
			ViolationDropCount: dropCount,
		},
	}
	return r
}

// telemetryFakeRunnerWithAuditRecords returns a runner that emits the
// canned audit records list so the slog handler captures both the
// summary fields on the primary record and the per-record detail
// entries the v42-1b loop emits.
func telemetryFakeRunnerWithAuditRecords(records []SandboxAuditRecord, dropCount int) *fakeBashRunner {
	return &fakeBashRunner{
		runResult: BashRunResult{
			Output:               "BASH RESULT\nstatus: success\n",
			IsError:              false,
			Classification:       "success",
			AuditRecords:         records,
			AuditRecordDropCount: dropCount,
		},
	}
}

func TestEmitBashTelemetry_ViolationCountReflectsLength(t *testing.T) {
	buf := captureSlogOutput(t)
	violations := []SandboxViolation{
		{Source: string(SourceNetworkProxy), Host: "github.com", Port: 443, Protocol: string(ProtocolHTTPSConnect), Reason: string(ReasonNotInAllowlist)},
		{Source: string(SourceNetworkProxy), Host: "1.2.3.4", Port: 80, Protocol: string(ProtocolTCP), Reason: string(ReasonDeniedByRule)},
	}
	bt := NewBashToolWithRunner(NewPathGuard(t.TempDir(), nil), telemetryFakeRunnerWithViolations(violations, 0))
	if _, err := bt.Execute(context.Background(), json.RawMessage(`{"command":"echo hi"}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "violation_count=2") {
		t.Errorf("expected violation_count=2; got: %s", out)
	}
}

func TestEmitBashTelemetry_ViolationDropCountSurfaced(t *testing.T) {
	buf := captureSlogOutput(t)
	bt := NewBashToolWithRunner(NewPathGuard(t.TempDir(), nil), telemetryFakeRunnerWithViolations(nil, 7))
	if _, err := bt.Execute(context.Background(), json.RawMessage(`{"command":"echo hi"}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "violation_drop_count=7") {
		t.Errorf("expected violation_drop_count=7 in telemetry; got: %s", out)
	}
}

func TestEmitBashTelemetry_StructuredViolationsListIncludesPerEntryFields(t *testing.T) {
	buf := captureSlogOutput(t)
	violations := []SandboxViolation{
		{Source: string(SourceNetworkProxy), Host: "github.com", Port: 443, Protocol: string(ProtocolHTTPSConnect), Reason: string(ReasonNotInAllowlist)},
	}
	bt := NewBashToolWithRunner(NewPathGuard(t.TempDir(), nil), telemetryFakeRunnerWithViolations(violations, 0))
	if _, err := bt.Execute(context.Background(), json.RawMessage(`{"command":"echo hi"}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"github.com",
		"network_proxy",
		"https_connect",
		"not_in_allowlist",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("structured violations list missing %q in telemetry; got: %s", want, out)
		}
	}
}

// TestEmitBashTelemetry_DoesNotLogURLPathOrQueryOrHeaders pins the
// N6 retention policy: telemetry MUST NOT include full URL paths,
// query strings, HTTP headers, or request bodies. Decision.Host on
// SOCKS5 DOMAINNAME ATYP=0x03 may carry an FQDN containing private
// destination info; structured slog wiring respects retention by
// surfacing only host/port/protocol/reason — never path, query, or
// headers.
func TestEmitBashTelemetry_DoesNotLogURLPathOrQueryOrHeaders(t *testing.T) {
	buf := captureSlogOutput(t)
	violations := []SandboxViolation{
		{
			Source:   string(SourceNetworkProxy),
			Host:     "internal.example",
			Port:     443,
			Protocol: string(ProtocolHTTPSConnect),
			Reason:   string(ReasonNotInAllowlist),
			// Message and Path are populated to verify they are NOT
			// auto-included in the structured violations field.
			Message: "Authorization: Bearer SECRET-TOKEN-X path=/private/api?key=v",
		},
	}
	bt := NewBashToolWithRunner(NewPathGuard(t.TempDir(), nil), telemetryFakeRunnerWithViolations(violations, 0))
	if _, err := bt.Execute(context.Background(), json.RawMessage(`{"command":"echo hi"}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	for _, banned := range []string{
		"Bearer",
		"SECRET-TOKEN-X",
		"path=/private/api",
		"?key=v",
		"Authorization",
	} {
		if strings.Contains(out, banned) {
			t.Errorf("N6 retention violation: telemetry leaked %q; got: %s", banned, out)
		}
	}
}

func TestEmitBashTelemetry_EmptyViolationsZeroCount(t *testing.T) {
	buf := captureSlogOutput(t)
	bt := NewBashToolWithRunner(NewPathGuard(t.TempDir(), nil), telemetryFakeRunnerWithViolations(nil, 0))
	if _, err := bt.Execute(context.Background(), json.RawMessage(`{"command":"echo hi"}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "violation_count=0") {
		t.Errorf("expected violation_count=0 baseline; got: %s", out)
	}
	if !strings.Contains(out, "violation_drop_count=0") {
		t.Errorf("expected violation_drop_count=0 baseline; got: %s", out)
	}
}

// v42-1b: emitBashTelemetry MUST surface AuditRecord summaries on the
// primary "bash command completed" record AND emit one
// "bash sandbox permitted connection" record per AuditRecord with the
// structured {host, port, protocol, source, decision} fields.

func TestEmitBashTelemetry_AuditRecordCountSurfaced(t *testing.T) {
	buf := captureSlogOutput(t)
	records := []SandboxAuditRecord{
		{Host: "api.example.com", Port: 443, Protocol: string(ProtocolHTTPSConnect), Source: string(SourceNetworkProxy), Decision: "allow"},
		{Host: "github.com", Port: 443, Protocol: string(ProtocolHTTPSConnect), Source: string(SourceNetworkProxy), Decision: "allow"},
	}
	bt := NewBashToolWithRunner(NewPathGuard(t.TempDir(), nil), telemetryFakeRunnerWithAuditRecords(records, 7))
	if _, err := bt.Execute(context.Background(), json.RawMessage(`{"command":"echo hi"}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "audit_record_count=2") {
		t.Errorf("expected audit_record_count=2; got: %s", out)
	}
	if !strings.Contains(out, "audit_record_drop_count=7") {
		t.Errorf("expected audit_record_drop_count=7; got: %s", out)
	}
}

func TestEmitBashTelemetry_PerRecordPermittedConnectionLineEmitted(t *testing.T) {
	buf := captureSlogOutput(t)
	records := []SandboxAuditRecord{
		{Host: "api.example.com", Port: 443, Protocol: string(ProtocolHTTPSConnect), Source: string(SourceNetworkProxy), Decision: "allow"},
	}
	bt := NewBashToolWithRunner(NewPathGuard(t.TempDir(), nil), telemetryFakeRunnerWithAuditRecords(records, 0))
	if _, err := bt.Execute(context.Background(), json.RawMessage(`{"command":"echo hi"}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "bash sandbox permitted connection") {
		t.Errorf("missing per-record telemetry header; got: %s", out)
	}
	for _, want := range []string{
		"host=api.example.com",
		"port=443",
		"protocol=https_connect",
		"source=network_proxy",
		"decision=allow",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("per-record telemetry missing %q; got: %s", want, out)
		}
	}
}

func TestEmitBashTelemetry_EmptyAuditRecordsZeroCount(t *testing.T) {
	buf := captureSlogOutput(t)
	bt := NewBashToolWithRunner(NewPathGuard(t.TempDir(), nil), telemetryFakeRunnerWithAuditRecords(nil, 0))
	if _, err := bt.Execute(context.Background(), json.RawMessage(`{"command":"echo hi"}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "audit_record_count=0") {
		t.Errorf("expected audit_record_count=0 baseline; got: %s", out)
	}
	if !strings.Contains(out, "audit_record_drop_count=0") {
		t.Errorf("expected audit_record_drop_count=0 baseline; got: %s", out)
	}
	if strings.Contains(out, "bash sandbox permitted connection") {
		t.Errorf("baseline must not emit per-record entries; got: %s", out)
	}
}
