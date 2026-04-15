package portability

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type ExportRecord struct {
	Timestamp time.Time
	OutPath   string
	Scope     BundleScope
	ByteSize  int64
}

type VerifyOptions struct {
	BundlePath string
	Passphrase []byte
	Logger     *slog.Logger
}

type VerifyReport struct {
	BundleVersion int
	FileCount     int
	TotalBytes    int64
	SourceHost    string
	ElnathVer     string
	IntegrityOK   bool
	HostWarnings  []string
}

func List(_ context.Context, dataDir string) ([]ExportRecord, error) {
	historyDir := filepath.Join(dataDir, "portability", "history")
	entries, err := os.ReadDir(historyDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read history dir: %w", err)
	}
	records := make([]ExportRecord, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(historyDir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read history record: %w", err)
		}
		var record historyRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return nil, fmt.Errorf("parse history record: %w", err)
		}
		records = append(records, ExportRecord{
			Timestamp: record.Timestamp,
			OutPath:   record.OutPath,
			Scope:     record.Scope,
			ByteSize:  record.ByteSize,
		})
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Timestamp.After(records[j].Timestamp) })
	return records, nil
}

func Verify(_ context.Context, opts VerifyOptions) (*VerifyReport, error) {
	contents, err := readBundle(opts.BundlePath, opts.Passphrase)
	if err != nil {
		return nil, err
	}
	totalBytes, err := verifyBundleFiles(contents.Manifest, contents.Files)
	if err != nil {
		return nil, err
	}
	report := &VerifyReport{
		BundleVersion: contents.Manifest.Version,
		FileCount:     len(contents.Manifest.Files),
		TotalBytes:    totalBytes,
		SourceHost:    contents.Manifest.SourceHost,
		ElnathVer:     contents.Manifest.ElnathVer,
		IntegrityOK:   true,
	}
	if contents.Manifest.Version != BundleVersion {
		report.HostWarnings = append(report.HostWarnings, fmt.Sprintf("bundle version %d is unsupported on this host", contents.Manifest.Version))
	}
	currentVersion := currentElnathVersion()
	if contents.Manifest.ElnathVer != "" && currentVersion != "" && !strings.EqualFold(contents.Manifest.ElnathVer, currentVersion) {
		report.HostWarnings = append(report.HostWarnings, fmt.Sprintf("bundle built with %s, current host is %s", contents.Manifest.ElnathVer, currentVersion))
	}
	return report, nil
}
