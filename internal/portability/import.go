package portability

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type ImportOptions struct {
	BundlePath string
	TargetDir  string
	Passphrase []byte
	DryRun     bool
	Force      bool
	Logger     *slog.Logger
}

type ImportReport struct {
	BundleVersion int
	FilesApplied  []string
	FilesSkipped  []string
	Conflicts     []string
	Warnings      []string
}

type bundleContents struct {
	Manifest Manifest
	Files    map[string][]byte
}

func Import(ctx context.Context, opts ImportOptions) (*ImportReport, error) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if opts.TargetDir == "" {
		return nil, fmt.Errorf("portability: target dir is required")
	}
	contents, err := readBundle(opts.BundlePath, opts.Passphrase)
	if err != nil {
		return nil, err
	}
	report := &ImportReport{BundleVersion: contents.Manifest.Version}
	if err := validateManifestVersion(contents.Manifest.Version, opts.Force); err != nil {
		return report, err
	}
	if _, err := verifyBundleFiles(contents.Manifest, contents.Files); err != nil {
		return report, err
	}
	paths, conflicts, err := resolveImportPaths(opts.TargetDir, contents.Manifest.Files)
	if err != nil {
		return report, err
	}
	if len(conflicts) > 0 && !opts.Force {
		report.Conflicts = conflicts
		return report, ErrConflict
	}
	if opts.DryRun {
		for _, file := range contents.Manifest.Files {
			report.FilesSkipped = append(report.FilesSkipped, file.RelPath)
		}
		if contents.Manifest.HasSecrets {
			report.Warnings = append(report.Warnings, "Secret-bearing fields restored — review on new host")
		}
		return report, nil
	}
	tempDir, err := createImportTempDir(opts.TargetDir)
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(tempDir)

	for _, file := range contents.Manifest.Files {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		tempPath, err := safeJoin(tempDir, file.RelPath)
		if err != nil {
			return report, err
		}
		if err := os.MkdirAll(filepath.Dir(tempPath), 0o755); err != nil {
			return report, fmt.Errorf("create temp parent: %w", err)
		}
		if err := os.WriteFile(tempPath, contents.Files[file.RelPath], 0o600); err != nil {
			return report, fmt.Errorf("write temp file: %w", err)
		}
	}

	backupSuffix := fmt.Sprintf(".preimport.%d", time.Now().Unix())
	for _, file := range contents.Manifest.Files {
		finalPath := paths[file.RelPath]
		tempPath, _ := safeJoin(tempDir, file.RelPath)
		if opts.Force {
			if _, err := os.Stat(finalPath); err == nil {
				backupPath := finalPath + backupSuffix
				if err := os.MkdirAll(filepath.Dir(backupPath), 0o755); err != nil {
					return report, fmt.Errorf("create backup parent: %w", err)
				}
				if err := os.Rename(finalPath, backupPath); err != nil {
					return report, fmt.Errorf("backup existing file: %w", err)
				}
			}
		}
		if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
			return report, fmt.Errorf("create target parent: %w", err)
		}
		if err := moveFile(tempPath, finalPath); err != nil {
			logger.Warn("portability move failed", "source", tempPath, "target", finalPath, "error", err)
			return report, err
		}
		report.FilesApplied = append(report.FilesApplied, file.RelPath)
	}
	if contents.Manifest.HasSecrets {
		report.Warnings = append(report.Warnings, "Secret-bearing fields restored — review on new host")
	}
	return report, nil
}

func readBundle(bundlePath string, passphrase []byte) (bundleContents, error) {
	f, err := os.Open(bundlePath)
	if err != nil {
		return bundleContents{}, fmt.Errorf("open bundle: %w", err)
	}
	defer f.Close()
	sr, err := OpenReader(f, passphrase)
	if err != nil {
		return bundleContents{}, err
	}
	gzr, err := gzip.NewReader(sr)
	if err != nil {
		return bundleContents{}, fmt.Errorf("open gzip payload: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	contents := bundleContents{Files: make(map[string][]byte)}
	manifestSeen := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return bundleContents{}, fmt.Errorf("read tar entry: %w", err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return bundleContents{}, fmt.Errorf("read tar payload: %w", err)
		}
		if hdr.Name == ManifestName {
			if err := json.Unmarshal(data, &contents.Manifest); err != nil {
				return bundleContents{}, fmt.Errorf("parse manifest: %w", err)
			}
			manifestSeen = true
			continue
		}
		contents.Files[filepath.ToSlash(hdr.Name)] = data
	}
	if !manifestSeen {
		return bundleContents{}, ErrIntegrity
	}
	return contents, nil
}

func verifyBundleFiles(manifest Manifest, files map[string][]byte) (int64, error) {
	if len(manifest.Files) != len(files) {
		return 0, ErrIntegrity
	}
	var total int64
	for _, file := range manifest.Files {
		data, ok := files[file.RelPath]
		if !ok {
			return 0, ErrIntegrity
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != file.SHA256 || int64(len(data)) != file.Size {
			return 0, ErrIntegrity
		}
		total += file.Size
	}
	return total, nil
}

func resolveImportPaths(targetDir string, files []ManifestFile) (map[string]string, []string, error) {
	paths := make(map[string]string, len(files))
	conflicts := make([]string, 0)
	for _, file := range files {
		cleaned, err := safeJoin(targetDir, file.RelPath)
		if err != nil {
			return nil, nil, err
		}
		paths[file.RelPath] = cleaned
		if _, err := os.Stat(cleaned); err == nil {
			conflicts = append(conflicts, file.RelPath)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, nil, fmt.Errorf("stat target path: %w", err)
		}
	}
	return paths, conflicts, nil
}

func safeJoin(base, rel string) (string, error) {
	base = filepath.Clean(base)
	cleaned := filepath.Clean(filepath.Join(base, rel))
	prefix := base + string(os.PathSeparator)
	if cleaned != base && !strings.HasPrefix(cleaned, prefix) {
		return "", fmt.Errorf("path traversal detected: %s", rel)
	}
	if cleaned == base {
		return "", fmt.Errorf("path traversal detected: %s", rel)
	}
	return cleaned, nil
}

func createImportTempDir(targetDir string) (string, error) {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", fmt.Errorf("%w: target dir 쓰기 권한 없음 또는 read-only fs", ErrCrossDevice)
	}
	tempDir := filepath.Join(targetDir, fmt.Sprintf(".import-%d", time.Now().UnixNano()))
	if err := os.Mkdir(tempDir, 0o700); err != nil {
		return "", fmt.Errorf("%w: target dir 쓰기 권한 없음 또는 read-only fs", ErrCrossDevice)
	}
	return tempDir, nil
}

func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := copyFileWithSync(src, dst); err != nil {
		return fmt.Errorf("%w: %v", ErrCrossDevice, err)
	}
	if err := os.Remove(src); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cleanup temp file: %w", err)
	}
	return nil
}

func copyFileWithSync(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".portability-copy-*")
	if err != nil {
		return fmt.Errorf("create temp copy: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return fmt.Errorf("copy data: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp copy: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp copy: %w", err)
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return fmt.Errorf("rename temp copy: %w", err)
	}
	return nil
}
