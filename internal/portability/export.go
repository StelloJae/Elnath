package portability

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type ExportOptions struct {
	DataDir    string
	WikiDir    string
	OutPath    string
	Passphrase []byte
	Scope      BundleScope
	Logger     *slog.Logger
}

type exportFile struct {
	RelPath string
	AbsPath string
	Size    int64
	SHA256  string
}

func Export(ctx context.Context, opts ExportOptions) error {
	if opts.OutPath == "" {
		return fmt.Errorf("portability: output path is required")
	}
	if opts.DataDir == "" {
		return fmt.Errorf("portability: data dir is required")
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	scope := normalizeScope(opts.Scope)
	files, hasSecrets, err := collectExportFiles(opts.DataDir, resolveWikiDir(opts.DataDir, opts.WikiDir), scope)
	if err != nil {
		return err
	}

	manifest := Manifest{
		Version:    BundleVersion,
		CreatedAt:  time.Now().UTC(),
		SourceHost: currentHostname(),
		ElnathVer:  currentElnathVersion(),
		HasSecrets: hasSecrets,
		Scope:      scope,
		Files:      make([]ManifestFile, 0, len(files)),
	}
	for _, file := range files {
		manifest.Files = append(manifest.Files, ManifestFile{
			RelPath: file.RelPath,
			Size:    file.Size,
			SHA256:  file.SHA256,
		})
	}

	if err := os.MkdirAll(filepath.Dir(opts.OutPath), 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	out, err := os.OpenFile(opts.OutPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open output: %w", err)
	}
	cleanup := func() {
		out.Close()
		_ = os.Remove(opts.OutPath)
	}
	if err := buildSealedBundle(ctx, files, manifest, opts.Passphrase, out); err != nil {
		cleanup()
		return err
	}
	if err := out.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync bundle: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(opts.OutPath)
		return fmt.Errorf("close bundle: %w", err)
	}
	stat, err := os.Stat(opts.OutPath)
	var size int64
	if err == nil {
		size = stat.Size()
	}
	if err := writeHistory(opts.DataDir, manifest, opts.OutPath, size); err != nil {
		logger.Warn("portability history write failed", "error", err)
	}
	return nil
}

func collectExportFiles(dataDir, wikiDir string, scope BundleScope) ([]exportFile, bool, error) {
	configPath := filepath.Join(dataDir, "config.yaml")
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return nil, false, fmt.Errorf("read config: %w", err)
	}
	hasSecrets := HasSecretAPIKeys(configData)

	var files []exportFile
	if scope.Config {
		file, err := describeExportFile("config.yaml", configPath)
		if err != nil {
			return nil, false, err
		}
		files = append(files, file)
	}
	if scope.DB {
		files = appendExistingExportFiles(files, dataDir,
			filepath.Join("data", "elnath.db"),
			filepath.Join("data", "wiki.db"),
		)
	}
	if scope.Lessons {
		files = appendExistingExportFiles(files, dataDir,
			filepath.Join("data", "lessons.jsonl"),
			filepath.Join("data", "lesson_cursors.jsonl"),
			filepath.Join("data", "audit.jsonl"),
			filepath.Join("data", "breaker.json"),
			filepath.Join("data", "llm_extraction_state.json"),
		)
	}
	if scope.Sessions {
		sessionFiles, err := collectMatchingFiles(dataDir, filepath.Join("data", "sessions"), ".jsonl")
		if err != nil {
			return nil, false, err
		}
		files = append(files, sessionFiles...)
	}
	if scope.Wiki {
		wikiFiles, err := collectWikiFiles(wikiDir)
		if err != nil {
			return nil, false, err
		}
		files = append(files, wikiFiles...)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].RelPath < files[j].RelPath })
	return files, hasSecrets, nil
}

func buildSealedBundle(ctx context.Context, files []exportFile, manifest Manifest, passphrase []byte, out io.Writer) error {
	sw, err := SealWriter(out, passphrase)
	if err != nil {
		return err
	}
	gzw := gzip.NewWriter(sw)
	tw := tar.NewWriter(gzw)

	manifestData, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := writeTarFile(tw, ManifestName, manifestData); err != nil {
		return err
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := streamTarFile(tw, file); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("close tar writer: %w", err)
	}
	if err := gzw.Close(); err != nil {
		return fmt.Errorf("close gzip writer: %w", err)
	}
	if err := sw.Close(); err != nil {
		return fmt.Errorf("close seal writer: %w", err)
	}
	return nil
}

func streamTarFile(tw *tar.Writer, file exportFile) error {
	f, err := os.Open(file.AbsPath)
	if err != nil {
		return fmt.Errorf("open export file %s: %w", file.RelPath, err)
	}
	defer f.Close()
	hdr := &tar.Header{
		Name:    file.RelPath,
		Mode:    0o644,
		Size:    file.Size,
		ModTime: time.Now().UTC(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("tar header %s: %w", file.RelPath, err)
	}
	if _, err := io.Copy(tw, f); err != nil {
		return fmt.Errorf("tar copy %s: %w", file.RelPath, err)
	}
	return nil
}

func appendExistingExportFiles(files []exportFile, dataDir string, relPaths ...string) []exportFile {
	for _, relPath := range relPaths {
		file, err := describeExportFile(relPath, filepath.Join(dataDir, relPath))
		if err == nil {
			files = append(files, file)
		}
	}
	return files
}

func collectMatchingFiles(dataDir, relDir, suffix string) ([]exportFile, error) {
	root := filepath.Join(dataDir, relDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read export dir %s: %w", relDir, err)
	}
	files := make([]exportFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
			continue
		}
		file, err := describeExportFile(filepath.Join(relDir, entry.Name()), filepath.Join(root, entry.Name()))
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

func collectWikiFiles(wikiDir string) ([]exportFile, error) {
	if wikiDir == "" {
		return nil, nil
	}
	entries := []exportFile{}
	err := filepath.WalkDir(wikiDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		rel, err := filepath.Rel(wikiDir, path)
		if err != nil {
			return err
		}
		file, err := describeExportFile(filepath.Join("wiki", rel), path)
		if err != nil {
			return err
		}
		entries = append(entries, file)
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("walk wiki dir: %w", err)
	}
	return entries, nil
}

func describeExportFile(relPath, absPath string) (exportFile, error) {
	info, err := os.Stat(absPath)
	if err != nil {
		return exportFile{}, err
	}
	f, err := os.Open(absPath)
	if err != nil {
		return exportFile{}, fmt.Errorf("open %s: %w", relPath, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return exportFile{}, fmt.Errorf("hash %s: %w", relPath, err)
	}
	return exportFile{
		RelPath: filepath.ToSlash(relPath),
		AbsPath: absPath,
		Size:    info.Size(),
		SHA256:  hex.EncodeToString(h.Sum(nil)),
	}, nil
}

func writeTarFile(tw *tar.Writer, name string, data []byte) error {
	if err := tw.WriteHeader(&tar.Header{
		Name:    filepath.ToSlash(name),
		Mode:    0o644,
		Size:    int64(len(data)),
		ModTime: time.Unix(0, 0),
	}); err != nil {
		return fmt.Errorf("write tar header %s: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("write tar body %s: %w", name, err)
	}
	return nil
}

func writeHistory(dataDir string, manifest Manifest, outPath string, byteSize int64) error {
	historyDir := filepath.Join(dataDir, "portability", "history")
	if err := os.MkdirAll(historyDir, 0o755); err != nil {
		return fmt.Errorf("create history dir: %w", err)
	}
	record := historyRecord{
		Timestamp: manifest.CreatedAt,
		OutPath:   outPath,
		ByteSize:  byteSize,
		Scope:     manifest.Scope,
		Manifest:  manifest,
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal history: %w", err)
	}
	name := manifest.CreatedAt.UTC().Format("20060102T150405.000000000Z0700") + ".json"
	if err := os.WriteFile(filepath.Join(historyDir, name), data, 0o644); err != nil {
		return fmt.Errorf("write history: %w", err)
	}
	return nil
}

func resolveWikiDir(dataDir, wikiDir string) string {
	if strings.TrimSpace(wikiDir) != "" {
		return wikiDir
	}
	configPath := filepath.Join(dataDir, "config.yaml")
	raw, err := os.ReadFile(configPath)
	if err == nil {
		var cfg struct {
			WikiDir string `yaml:"wiki_dir"`
		}
		if yaml.Unmarshal(raw, &cfg) == nil && strings.TrimSpace(cfg.WikiDir) != "" {
			if filepath.IsAbs(cfg.WikiDir) {
				return cfg.WikiDir
			}
			return filepath.Join(dataDir, cfg.WikiDir)
		}
	}
	return filepath.Join(dataDir, "wiki")
}

func currentHostname() string {
	host, err := os.Hostname()
	if err != nil {
		return ""
	}
	return host
}

func currentElnathVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
