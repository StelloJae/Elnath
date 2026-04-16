package scorecard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// AppendJSON appends one Report as a newline-delimited JSON line to filePath.
// Parent directories are created if missing. File permission is 0o600.
func AppendJSON(r Report, filePath string) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return fmt.Errorf("scorecard: mkdir: %w", err)
	}
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("scorecard: open %s: %w", filePath, err)
	}
	defer f.Close()
	line, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("scorecard: marshal: %w", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("scorecard: write: %w", err)
	}
	return nil
}
