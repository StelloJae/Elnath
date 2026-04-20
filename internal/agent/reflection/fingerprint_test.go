package reflection

import (
	"strings"
	"testing"
)

func TestComputeFingerprint_Stable(t *testing.T) {
	a := ComputeFingerprint("make the tests pass", []string{"bash", "read_file"})
	b := ComputeFingerprint("make the tests pass", []string{"bash", "read_file"})
	if a != b {
		t.Fatalf("expected stable fingerprint, got %q != %q", a, b)
	}
	if a == "" {
		t.Fatal("fingerprint must not be empty")
	}
}

func TestComputeFingerprint_ToolOrderInvariant(t *testing.T) {
	a := ComputeFingerprint("same subject", []string{"bash", "read_file", "grep"})
	b := ComputeFingerprint("same subject", []string{"grep", "bash", "read_file"})
	if a != b {
		t.Fatalf("tool order should not affect fingerprint: %q vs %q", a, b)
	}
}

func TestComputeFingerprint_SubjectNormalization(t *testing.T) {
	a := ComputeFingerprint("  Hello World  ", []string{"bash"})
	b := ComputeFingerprint("hello world", []string{"bash"})
	if a != b {
		t.Fatalf("case/whitespace normalization broken: %q vs %q", a, b)
	}
}

func TestComputeFingerprint_EmptyTools(t *testing.T) {
	fp := ComputeFingerprint("subject only", nil)
	if fp == "" {
		t.Fatal("empty tool slice must still yield non-empty fingerprint")
	}
	fp2 := ComputeFingerprint("subject only", []string{})
	if fp != fp2 {
		t.Fatalf("nil vs empty slice must be equivalent, %q vs %q", fp, fp2)
	}
}

func TestComputeFingerprint_Distinct(t *testing.T) {
	a := ComputeFingerprint("alpha", []string{"bash"})
	b := ComputeFingerprint("beta", []string{"bash"})
	if a == b {
		t.Fatalf("different subjects must produce different fingerprints")
	}
	c := ComputeFingerprint("alpha", []string{"grep"})
	if a == c {
		t.Fatalf("different tool sets must produce different fingerprints")
	}
}

func TestComputeFingerprint_Length12Base32(t *testing.T) {
	fp := ComputeFingerprint("len check", []string{"bash"})
	if len(string(fp)) != 12 {
		t.Fatalf("fingerprint length expected 12, got %d (%q)", len(string(fp)), fp)
	}
	const base32Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	for _, r := range string(fp) {
		if !strings.ContainsRune(base32Alphabet, r) {
			t.Fatalf("fingerprint char %q outside base32 alphabet", r)
		}
	}
}
