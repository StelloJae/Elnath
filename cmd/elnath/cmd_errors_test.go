package main

import (
	"context"
	"strings"
	"testing"
)

func TestCmdErrors_ListAndLookup(t *testing.T) {
	stdout, stderr := captureOutput(t, func() {
		if err := cmdErrors(context.Background(), []string{"list"}); err != nil {
			t.Fatalf("cmdErrors(list) error = %v", err)
		}
	})
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "ELN-001") {
		t.Fatalf("stdout = %q, want ELN-001", stdout)
	}

	stdout, stderr = captureOutput(t, func() {
		if err := cmdErrors(context.Background(), []string{"ELN-001"}); err != nil {
			t.Fatalf("cmdErrors(ELN-001) error = %v", err)
		}
	})
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "Provider not configured") {
		t.Fatalf("stdout = %q, want provider title", stdout)
	}

	stdout, stderr = captureOutput(t, func() {
		if err := cmdErrors(context.Background(), []string{"001"}); err != nil {
			t.Fatalf("cmdErrors(001) error = %v", err)
		}
	})
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "ELN-001") {
		t.Fatalf("stdout = %q, want ELN-001", stdout)
	}
}

func TestCmdErrors_UnknownCode(t *testing.T) {
	if err := cmdErrors(context.Background(), []string{"ELN-999"}); err == nil {
		t.Fatal("cmdErrors(ELN-999) error = nil, want error")
	}
}
