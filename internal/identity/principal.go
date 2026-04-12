package identity

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/stello/elnath/internal/config"
)

type Principal struct {
	UserID    string `json:"user_id"`
	ProjectID string `json:"project_id"`
	Surface   string `json:"surface"`
}

type PrincipalSource struct {
	UserID    string
	ProjectID string
	Surface   string
}

func NewPrincipal(source PrincipalSource) Principal {
	return Principal{
		UserID:    strings.TrimSpace(source.UserID),
		ProjectID: strings.TrimSpace(source.ProjectID),
		Surface:   strings.TrimSpace(source.Surface),
	}
}

func (p Principal) IsZero() bool {
	return strings.TrimSpace(p.UserID) == "" && strings.TrimSpace(p.ProjectID) == "" && strings.TrimSpace(p.Surface) == ""
}

func (p Principal) SurfaceIdentity() string {
	surface := strings.TrimSpace(p.Surface)
	userID := strings.TrimSpace(p.UserID)
	switch {
	case surface != "" && userID != "":
		return surface + ":" + userID
	case surface != "":
		return surface
	default:
		return userID
	}
}

func LegacyPrincipal() Principal {
	return Principal{UserID: "legacy", ProjectID: "unknown", Surface: "unknown"}
}

func fromCLIFlag(flagValue string) string {
	return strings.TrimSpace(flagValue)
}

func fromConfig(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.Principal.UserID)
}

func fromEnv() string {
	user := strings.TrimSpace(os.Getenv("USER"))
	host, _ := os.Hostname()
	host = strings.TrimSpace(host)
	if user == "" && host == "" {
		return ""
	}
	if user == "" {
		user = "unknown"
	}
	if host == "" {
		host = "unknown"
	}
	return user + "@" + host
}

func fromTelegram(fromID int64) string {
	if fromID <= 0 {
		return ""
	}
	return strconv.FormatInt(fromID, 10)
}

func fromGitRemote(cwd string) (string, bool) {
	cmd := exec.Command("git", "config", "--get", "remote.origin.url")
	cmd.Dir = cleanPath(cwd)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	remote := strings.TrimSpace(string(out))
	return remote, remote != ""
}

func hashValue(value string) string {
	sum := sha1.Sum([]byte(value))
	return hex.EncodeToString(sum[:])[:8]
}

func cleanPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "."
	}
	return filepath.Clean(path)
}
