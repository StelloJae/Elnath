package portability

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportRoundTrip(t *testing.T) {
	source := writePortabilityFixture(t)
	bundlePath := filepath.Join(t.TempDir(), "bundle.eln")
	if err := Export(context.Background(), ExportOptions{
		DataDir:    source,
		WikiDir:    filepath.Join(source, "wiki"),
		OutPath:    bundlePath,
		Passphrase: []byte("strong-passphrase"),
	}); err != nil {
		t.Fatalf("Export: %v", err)
	}

	target := t.TempDir()
	report, err := Import(context.Background(), ImportOptions{
		BundlePath: bundlePath,
		TargetDir:  target,
		Passphrase: []byte("strong-passphrase"),
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(report.FilesApplied) == 0 {
		t.Fatal("FilesApplied = 0, want imported files")
	}

	sourceConfig, err := os.ReadFile(filepath.Join(source, "config.yaml"))
	if err != nil {
		t.Fatalf("ReadFile(source config): %v", err)
	}
	targetConfig, err := os.ReadFile(filepath.Join(target, "config.yaml"))
	if err != nil {
		t.Fatalf("ReadFile(target config): %v", err)
	}
	if string(targetConfig) != string(sourceConfig) {
		t.Fatalf("imported config mismatch\nsource=%q\ntarget=%q", sourceConfig, targetConfig)
	}
}

func TestImportConflictNoForce(t *testing.T) {
	source := writePortabilityFixture(t)
	bundlePath := filepath.Join(t.TempDir(), "bundle.eln")
	if err := Export(context.Background(), ExportOptions{
		DataDir:    source,
		WikiDir:    filepath.Join(source, "wiki"),
		OutPath:    bundlePath,
		Passphrase: []byte("strong-passphrase"),
	}); err != nil {
		t.Fatalf("Export: %v", err)
	}

	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "config.yaml"), []byte("existing"), 0o644); err != nil {
		t.Fatalf("write existing config: %v", err)
	}

	report, err := Import(context.Background(), ImportOptions{
		BundlePath: bundlePath,
		TargetDir:  target,
		Passphrase: []byte("strong-passphrase"),
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Import error = %v, want ErrConflict", err)
	}
	if len(report.FilesApplied) != 0 {
		t.Fatalf("FilesApplied = %v, want empty", report.FilesApplied)
	}
	if len(report.Conflicts) == 0 {
		t.Fatal("Conflicts = 0, want at least one conflict")
	}
}

func TestImportConflictWithForce(t *testing.T) {
	source := writePortabilityFixture(t)
	bundlePath := filepath.Join(t.TempDir(), "bundle.eln")
	if err := Export(context.Background(), ExportOptions{
		DataDir:    source,
		WikiDir:    filepath.Join(source, "wiki"),
		OutPath:    bundlePath,
		Passphrase: []byte("strong-passphrase"),
	}); err != nil {
		t.Fatalf("Export: %v", err)
	}

	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "config.yaml"), []byte("existing"), 0o644); err != nil {
		t.Fatalf("write existing config: %v", err)
	}

	if _, err := Import(context.Background(), ImportOptions{
		BundlePath: bundlePath,
		TargetDir:  target,
		Passphrase: []byte("strong-passphrase"),
		Force:      true,
	}); err != nil {
		t.Fatalf("Import(force): %v", err)
	}

	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatalf("ReadDir(target): %v", err)
	}
	var foundBackup bool
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "config.yaml.preimport.") {
			foundBackup = true
			break
		}
	}
	if !foundBackup {
		t.Fatal("backup file not found after force import")
	}
}

func TestImportDryRun(t *testing.T) {
	source := writePortabilityFixture(t)
	bundlePath := filepath.Join(t.TempDir(), "bundle.eln")
	if err := Export(context.Background(), ExportOptions{
		DataDir:    source,
		WikiDir:    filepath.Join(source, "wiki"),
		OutPath:    bundlePath,
		Passphrase: []byte("strong-passphrase"),
	}); err != nil {
		t.Fatalf("Export: %v", err)
	}

	target := t.TempDir()
	report, err := Import(context.Background(), ImportOptions{
		BundlePath: bundlePath,
		TargetDir:  target,
		Passphrase: []byte("strong-passphrase"),
		DryRun:     true,
	})
	if err != nil {
		t.Fatalf("Import(dry-run): %v", err)
	}
	if len(report.FilesSkipped) == 0 {
		t.Fatal("FilesSkipped = 0, want would-apply entries")
	}
	if _, err := os.Stat(filepath.Join(target, "config.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target config stat error = %v, want not exist", err)
	}
}

func TestImportIntegrityFail(t *testing.T) {
	source := writePortabilityFixture(t)
	bundlePath := filepath.Join(t.TempDir(), "bundle.eln")
	if err := Export(context.Background(), ExportOptions{
		DataDir:    source,
		WikiDir:    filepath.Join(source, "wiki"),
		OutPath:    bundlePath,
		Passphrase: []byte("strong-passphrase"),
	}); err != nil {
		t.Fatalf("Export: %v", err)
	}

	rewriteBundleManifest(t, bundlePath, "strong-passphrase", func(manifest *Manifest) {
		manifest.Files[0].SHA256 = strings.Repeat("0", 64)
	})

	_, err := Import(context.Background(), ImportOptions{
		BundlePath: bundlePath,
		TargetDir:  t.TempDir(),
		Passphrase: []byte("strong-passphrase"),
	})
	if !errors.Is(err, ErrIntegrity) {
		t.Fatalf("Import error = %v, want ErrIntegrity", err)
	}
}

func TestImportPathTraversal(t *testing.T) {
	bundlePath := filepath.Join(t.TempDir(), "malicious.eln")
	files := map[string][]byte{"../etc/passwd": []byte("owned")}
	manifest := Manifest{
		Version:   BundleVersion,
		CreatedAt: timeNowForTest(),
		ElnathVer: "dev",
		Scope:     BundleScope{Config: true},
		Files:     []ManifestFile{{RelPath: "../etc/passwd", Size: 5, SHA256: hashHex(files["../etc/passwd"])}},
	}
	writeSealedBundle(t, bundlePath, "strong-passphrase", manifest, files)

	_, err := Import(context.Background(), ImportOptions{
		BundlePath: bundlePath,
		TargetDir:  t.TempDir(),
		Passphrase: []byte("strong-passphrase"),
	})
	if err == nil || !strings.Contains(err.Error(), "path traversal") {
		t.Fatalf("Import error = %v, want path traversal error", err)
	}
}
