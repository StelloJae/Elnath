package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

const plistLabel = "com.elnath.daemon"

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{.Label}}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{.BinaryPath}}</string>
		<string>daemon</string>
		<string>start</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>{{.LogPath}}</string>
	<key>StandardErrorPath</key>
	<string>{{.LogPath}}</string>
</dict>
</plist>
`

type plistData struct {
	Label      string
	BinaryPath string
	LogPath    string
}

// GeneratePlist creates a launchd plist XML string for the daemon.
func GeneratePlist(binaryPath, socketPath string) string {
	logPath := filepath.Join(filepath.Dir(socketPath), "daemon.log")

	data := plistData{
		Label:      plistLabel,
		BinaryPath: binaryPath,
		LogPath:    logPath,
	}

	tmpl := template.Must(template.New("plist").Parse(plistTemplate))
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return ""
	}
	return buf.String()
}

// InstallPlist writes the launchd plist to ~/Library/LaunchAgents/ and
// returns the path of the written file.
func InstallPlist(binaryPath, socketPath string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("launchd: home dir: %w", err)
	}

	agentsDir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		return "", fmt.Errorf("launchd: create agents dir: %w", err)
	}

	plistPath := filepath.Join(agentsDir, plistLabel+".plist")
	content := GeneratePlist(binaryPath, socketPath)
	if content == "" {
		return "", fmt.Errorf("launchd: generate plist failed")
	}

	if err := os.WriteFile(plistPath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("launchd: write plist: %w", err)
	}

	return plistPath, nil
}
