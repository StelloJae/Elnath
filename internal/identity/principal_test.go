package identity

import (
	"crypto/sha1"
	"encoding/hex"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/config"
)

func TestResolveCLIPrincipalPrefersFlagOverConfigAndEnv(t *testing.T) {
	remote := "git@github.com:stello/elnath.git"
	dir := initGitRepoWithRemote(t, remote)
	t.Setenv("USER", "env-user")

	got := ResolveCLIPrincipal(&config.Config{
		Principal: config.PrincipalConfig{UserID: "config-user"},
	}, "flag-user", dir)

	if got.UserID != "flag-user" {
		t.Fatalf("UserID = %q, want flag-user", got.UserID)
	}
	if got.ProjectID != shortHash(remote) {
		t.Fatalf("ProjectID = %q, want %q", got.ProjectID, shortHash(remote))
	}
	if got.Surface != "cli" {
		t.Fatalf("Surface = %q, want cli", got.Surface)
	}
}

func TestResolveCLIPrincipalUsesConfigWhenFlagMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("USER", "env-user")

	got := ResolveCLIPrincipal(&config.Config{
		Principal: config.PrincipalConfig{UserID: "config-user"},
	}, "", dir)

	if got.UserID != "config-user" {
		t.Fatalf("UserID = %q, want config-user", got.UserID)
	}
	if got.ProjectID != shortHash(filepath.Clean(dir)) {
		t.Fatalf("ProjectID = %q, want cwd hash", got.ProjectID)
	}
}

func TestResolveCLIPrincipalFallsBackToUserAtHostname(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("USER", "stello")

	got := ResolveCLIPrincipal(nil, "", dir)

	if !strings.HasPrefix(got.UserID, "stello@") {
		t.Fatalf("UserID = %q, want stello@<hostname>", got.UserID)
	}
	if got.ProjectID != shortHash(filepath.Clean(dir)) {
		t.Fatalf("ProjectID = %q, want cwd hash", got.ProjectID)
	}
	if got.Surface != "cli" {
		t.Fatalf("Surface = %q, want cli", got.Surface)
	}
}

func TestResolveProjectIDPrefersOverride(t *testing.T) {
	got := ResolveProjectID(t.TempDir(), "project-override")
	if got != "project-override" {
		t.Fatalf("ResolveProjectID override = %q, want project-override", got)
	}
}

func TestResolveProjectIDUsesGitRemoteHash(t *testing.T) {
	remote := "https://github.com/stello/elnath.git"
	dir := initGitRepoWithRemote(t, remote)

	got := ResolveProjectID(dir, "")
	if got != shortHash(remote) {
		t.Fatalf("ResolveProjectID = %q, want %q", got, shortHash(remote))
	}
}

func TestResolveTelegramPrincipalUsesTelegramUserID(t *testing.T) {
	dir := t.TempDir()
	got := ResolveTelegramPrincipal(42, dir)

	if got.UserID != "42" {
		t.Fatalf("UserID = %q, want 42", got.UserID)
	}
	if got.ProjectID != shortHash(filepath.Clean(dir)) {
		t.Fatalf("ProjectID = %q, want cwd hash", got.ProjectID)
	}
	if got.Surface != "telegram" {
		t.Fatalf("Surface = %q, want telegram", got.Surface)
	}
}

func TestLegacyPrincipal(t *testing.T) {
	got := LegacyPrincipal()
	want := Principal{UserID: "legacy", ProjectID: "unknown", Surface: "unknown"}
	if got != want {
		t.Fatalf("LegacyPrincipal = %+v, want %+v", got, want)
	}
}

func initGitRepoWithRemote(t *testing.T, remote string) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "remote", "add", "origin", remote)
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func shortHash(value string) string {
	sum := sha1.Sum([]byte(value))
	return hex.EncodeToString(sum[:])[:8]
}

func TestPrincipalSurfaceIdentity(t *testing.T) {
	tests := []struct {
		name    string
		surface string
		userID  string
		want    string
	}{
		{"both_set", "telegram", "12345", "telegram:12345"},
		{"surface_only", "cli", "", "cli"},
		{"user_only", "", "stello", "stello"},
		{"both_empty", "", "", ""},
		{"whitespace_only", "  ", "  ", ""},
		{"surface_with_whitespace_user", "telegram", "  ", "telegram"},
		{"whitespace_surface_with_user", "  ", "12345", "12345"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Principal{Surface: tt.surface, UserID: tt.userID}
			if got := p.SurfaceIdentity(); got != tt.want {
				t.Errorf("SurfaceIdentity() = %q, want %q", got, tt.want)
			}
		})
	}
}
