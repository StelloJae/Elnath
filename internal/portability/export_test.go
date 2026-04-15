package portability

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestExportHappyPath(t *testing.T) {
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

	info, err := os.Stat(bundlePath)
	if err != nil {
		t.Fatalf("Stat(bundle): %v", err)
	}
	if info.Size() <= 0 {
		t.Fatalf("bundle size = %d, want > 0", info.Size())
	}

	manifest, files := readBundleManifest(t, bundlePath, "strong-passphrase")
	if !manifest.HasSecrets {
		t.Fatal("manifest.HasSecrets = false, want true")
	}
	if len(manifest.Files) < 3 {
		t.Fatalf("manifest file count = %d, want >= 3", len(manifest.Files))
	}
	if _, ok := files["config.yaml"]; !ok {
		t.Fatal("bundle missing config.yaml")
	}
	if _, ok := files[filepath.ToSlash(filepath.Join("data", "elnath.db"))]; !ok {
		t.Fatal("bundle missing data/elnath.db")
	}
}

func TestExportScopeFilter(t *testing.T) {
	root := writePortabilityFixture(t)
	bundlePath := filepath.Join(t.TempDir(), "config-only.eln")

	if err := Export(context.Background(), ExportOptions{
		DataDir:    root,
		WikiDir:    filepath.Join(root, "wiki"),
		OutPath:    bundlePath,
		Passphrase: []byte("strong-passphrase"),
		Scope:      BundleScope{Config: true},
	}); err != nil {
		t.Fatalf("Export: %v", err)
	}

	manifest, files := readBundleManifest(t, bundlePath, "strong-passphrase")
	if len(manifest.Files) != 1 {
		t.Fatalf("manifest file count = %d, want 1", len(manifest.Files))
	}
	if _, ok := files["config.yaml"]; !ok {
		t.Fatal("config-only bundle missing config.yaml")
	}
	for relPath := range files {
		if relPath != "config.yaml" {
			t.Fatalf("unexpected file in config-only bundle: %s", relPath)
		}
	}
}

func TestExportEmptyDataDir(t *testing.T) {
	root := t.TempDir()
	configData := "wiki_dir: " + filepath.Join(root, "wiki") + "\npermission:\n  mode: default\n"
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(configData), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	bundlePath := filepath.Join(t.TempDir(), "bundle.eln")

	if err := Export(context.Background(), ExportOptions{
		DataDir:    root,
		WikiDir:    filepath.Join(root, "wiki"),
		OutPath:    bundlePath,
		Passphrase: []byte("strong-passphrase"),
	}); err != nil {
		t.Fatalf("Export: %v", err)
	}

	manifest, _ := readBundleManifest(t, bundlePath, "strong-passphrase")
	if len(manifest.Files) < 1 {
		t.Fatalf("manifest file count = %d, want >= 1", len(manifest.Files))
	}
}

func TestExportResolvesRelativeWikiDirFromConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "wiki"), 0o755); err != nil {
		t.Fatalf("mkdir wiki: %v", err)
	}
	configData := "wiki_dir: wiki\npermission:\n  mode: default\n"
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(configData), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "wiki", "page.md"), []byte("# Relative Wiki\n"), 0o644); err != nil {
		t.Fatalf("write wiki page: %v", err)
	}
	bundlePath := filepath.Join(t.TempDir(), "bundle.eln")

	if err := Export(context.Background(), ExportOptions{
		DataDir:    root,
		OutPath:    bundlePath,
		Passphrase: []byte("strong-passphrase"),
	}); err != nil {
		t.Fatalf("Export: %v", err)
	}

	_, files := readBundleManifest(t, bundlePath, "strong-passphrase")
	if _, ok := files[filepath.ToSlash(filepath.Join("wiki", "page.md"))]; !ok {
		t.Fatal("bundle missing wiki/page.md for relative wiki_dir")
	}
}

func writePortabilityFixture(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "data", "sessions"), 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "wiki"), 0o755); err != nil {
		t.Fatalf("mkdir wiki: %v", err)
	}

	configData := strings.Join([]string{
		"data_dir: " + filepath.Join(root, "data"),
		"wiki_dir: " + filepath.Join(root, "wiki"),
		"permission:",
		"  mode: default",
		"anthropic:",
		"  api_key: secret-value",
		"",
	}, "\n")
	files := map[string][]byte{
		filepath.Join(root, "config.yaml"):                   []byte(configData),
		filepath.Join(root, "data", "elnath.db"):             []byte("main-db"),
		filepath.Join(root, "data", "wiki.db"):               []byte("wiki-db"),
		filepath.Join(root, "data", "lessons.jsonl"):         []byte(`{"id":"1"}` + "\n"),
		filepath.Join(root, "data", "lesson_cursors.jsonl"):  []byte(`{"cursor":1}` + "\n"),
		filepath.Join(root, "data", "audit.jsonl"):           []byte(`{"action":"export"}` + "\n"),
		filepath.Join(root, "data", "sessions", "abc.jsonl"): []byte(`{"id":"abc"}` + "\n"),
		filepath.Join(root, "wiki", "page.md"):               []byte("# Page\n"),
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return root
}

func readBundleManifest(t *testing.T, bundlePath string, passphrase string) (Manifest, map[string][]byte) {
	t.Helper()

	raw, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatalf("ReadFile(bundle): %v", err)
	}
	plaintext, err := Open(raw, []byte(passphrase))
	if err != nil {
		t.Fatalf("Open(bundle): %v", err)
	}

	gzr, err := gzip.NewReader(bytes.NewReader(plaintext))
	if err != nil {
		t.Fatalf("NewReader(gzip): %v", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	files := make(map[string][]byte)
	var manifest Manifest
	for {
		hdr, err := tr.Next()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("tar.Next: %v", err)
		}
		data := mustReadAll(t, tr)
		if hdr.Name == ManifestName {
			if err := json.Unmarshal(data, &manifest); err != nil {
				t.Fatalf("Unmarshal(manifest): %v", err)
			}
			continue
		}
		files[filepath.ToSlash(hdr.Name)] = data
	}
	return manifest, files
}

func rewriteBundleManifest(t *testing.T, bundlePath string, passphrase string, mutate func(*Manifest)) {
	t.Helper()

	manifest, files := readBundleManifest(t, bundlePath, passphrase)
	mutate(&manifest)
	writeSealedBundle(t, bundlePath, passphrase, manifest, files)
}

func writeSealedBundle(t *testing.T, bundlePath string, passphrase string, manifest Manifest, files map[string][]byte) {
	t.Helper()

	var gzBuf bytes.Buffer
	gzw := gzip.NewWriter(&gzBuf)
	tw := tar.NewWriter(gzw)
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal(manifest): %v", err)
	}
	writeTarEntry(t, tw, ManifestName, manifestData)

	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		writeTarEntry(t, tw, path, files[path])
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("Close(tar): %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("Close(gzip): %v", err)
	}

	sealed, err := Seal(gzBuf.Bytes(), []byte(passphrase))
	if err != nil {
		t.Fatalf("Seal(bundle): %v", err)
	}
	if err := os.WriteFile(bundlePath, sealed, 0o600); err != nil {
		t.Fatalf("WriteFile(bundle): %v", err)
	}
}

func writeTarEntry(t *testing.T, tw *tar.Writer, name string, data []byte) {
	t.Helper()
	if err := tw.WriteHeader(&tar.Header{
		Name:    filepath.ToSlash(name),
		Mode:    0o644,
		Size:    int64(len(data)),
		ModTime: time.Unix(0, 0),
	}); err != nil {
		t.Fatalf("WriteHeader(%s): %v", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatalf("Write(%s): %v", name, err)
	}
}

func mustReadAll(t *testing.T, r *tar.Reader) []byte {
	t.Helper()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("ReadFrom(tar): %v", err)
	}
	return buf.Bytes()
}

func hashHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
