package portability

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestListEmpty(t *testing.T) {
	records, err := List(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("records = %d, want 0", len(records))
	}
}

func TestListAfterTwoExports(t *testing.T) {
	root := writePortabilityFixture(t)
	bundleA := filepath.Join(t.TempDir(), "a.eln")
	bundleB := filepath.Join(t.TempDir(), "b.eln")

	if err := Export(context.Background(), ExportOptions{
		DataDir:    root,
		WikiDir:    filepath.Join(root, "wiki"),
		OutPath:    bundleA,
		Passphrase: []byte("strong-passphrase"),
	}); err != nil {
		t.Fatalf("Export(a): %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := Export(context.Background(), ExportOptions{
		DataDir:    root,
		WikiDir:    filepath.Join(root, "wiki"),
		OutPath:    bundleB,
		Passphrase: []byte("strong-passphrase"),
	}); err != nil {
		t.Fatalf("Export(b): %v", err)
	}

	records, err := List(context.Background(), root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	if records[0].Timestamp.Before(records[1].Timestamp) {
		t.Fatalf("records not sorted newest-first: %+v", records)
	}
}

func TestVerifyValidBundle(t *testing.T) {
	root := writePortabilityFixture(t)
	bundlePath := filepath.Join(t.TempDir(), "bundle.eln")
	if err := Export(context.Background(), ExportOptions{
		DataDir:    root,
		WikiDir:    filepath.Join(root, "wiki"),
		OutPath:    bundlePath,
		Passphrase: []byte("strong-passphrase"),
	}); err != nil {
		t.Fatalf("Export: %v", err)
	}

	report, err := Verify(context.Background(), VerifyOptions{
		BundlePath: bundlePath,
		Passphrase: []byte("strong-passphrase"),
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !report.IntegrityOK {
		t.Fatal("IntegrityOK = false, want true")
	}
	if report.FileCount == 0 {
		t.Fatal("FileCount = 0, want > 0")
	}
}

func TestVerifyVersionMismatch(t *testing.T) {
	root := writePortabilityFixture(t)
	bundlePath := filepath.Join(t.TempDir(), "bundle.eln")
	if err := Export(context.Background(), ExportOptions{
		DataDir:    root,
		WikiDir:    filepath.Join(root, "wiki"),
		OutPath:    bundlePath,
		Passphrase: []byte("strong-passphrase"),
	}); err != nil {
		t.Fatalf("Export: %v", err)
	}

	rewriteBundleManifest(t, bundlePath, "strong-passphrase", func(manifest *Manifest) {
		manifest.Version = BundleVersion + 1
	})

	report, err := Verify(context.Background(), VerifyOptions{
		BundlePath: bundlePath,
		Passphrase: []byte("strong-passphrase"),
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(report.HostWarnings) == 0 {
		t.Fatal("HostWarnings = 0, want version warning")
	}
}

func timeNowForTest() time.Time {
	return time.Unix(1713110400, 0).UTC()
}
