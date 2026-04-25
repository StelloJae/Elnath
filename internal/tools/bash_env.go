package tools

import (
	"os"
	"path/filepath"
	"strings"
)

// bashEnvInjectionVectors enumerates environment variables that bash
// itself interprets before any user command runs. Passing host values
// here would let the caller of elnath inject code into every command.
var bashEnvInjectionVectors = map[string]struct{}{
	"BASH_ENV":              {},
	"ENV":                   {},
	"LD_PRELOAD":            {},
	"LD_LIBRARY_PATH":       {},
	"DYLD_INSERT_LIBRARIES": {},
	"DYLD_LIBRARY_PATH":     {},
	"DYLD_FRAMEWORK_PATH":   {},
	"SSH_AUTH_SOCK":         {},
	"GPG_AGENT_INFO":        {},
}

// bashEnvSecretNames lists credential variables known to carry
// provider API keys by exact name.
var bashEnvSecretNames = map[string]struct{}{
	"OPENAI_API_KEY":                 {},
	"ANTHROPIC_API_KEY":              {},
	"GITHUB_TOKEN":                   {},
	"GH_TOKEN":                       {},
	"GOOGLE_APPLICATION_CREDENTIALS": {},
	"HUGGINGFACE_TOKEN":              {},
	"HF_TOKEN":                       {},
}

// bashEnvSecretPrefixes lists namespace prefixes whose full set of
// variables should be treated as secret.
var bashEnvSecretPrefixes = []string{
	"AWS_",
}

// bashEnvSecretSuffixes lists suffix patterns that mark secrets by
// convention (e.g. FOO_API_KEY, BAR_TOKEN).
var bashEnvSecretSuffixes = []string{
	"_API_KEY",
	"_TOKEN",
	"_SECRET",
	"_PASSWORD",
}

// bashFallbackPath is used when the host PATH is empty or unusable.
const bashFallbackPath = "/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"

// bashShellCandidates lists absolute paths searched in priority order
// for an executable bash binary. Lookup deliberately bypasses
// exec.LookPath: that consults the caller's PATH, which is exactly the
// surface this hardening pass closes off — a fake "bash" placed in the
// session workspace or any user-controlled PATH entry could otherwise
// intercept command execution.
var bashShellCandidates = []string{
	"/bin/bash",
	"/usr/bin/bash",
	"/usr/local/bin/bash",
	"/opt/homebrew/bin/bash",
}

// resolveBashShell returns the absolute path to the bash executable
// used for command invocations. It picks the first candidate in
// bashShellCandidates that resolves to an executable regular file. If
// no candidate resolves, /bin/bash is returned as a deterministic
// fallback so any failure surfaces as an exec error (caught by
// configureProcessCleanup) rather than a silent host-PATH lookup.
func resolveBashShell() string {
	for _, candidate := range bashShellCandidates {
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Mode()&0o111 == 0 {
			continue
		}
		return candidate
	}
	return "/bin/bash"
}

// sanitizeBashPath returns a colon-separated PATH with unsafe entries
// removed: empty entries, the implicit "." (current directory), and
// any non-absolute path. Duplicates are collapsed while preserving the
// first occurrence's position. Returns "" when no entries survive;
// callers must apply bashFallbackPath in that case.
func sanitizeBashPath(raw string) string {
	if raw == "" {
		return ""
	}
	seen := make(map[string]struct{})
	keep := make([]string, 0)
	for _, entry := range strings.Split(raw, ":") {
		if entry == "" || entry == "." {
			continue
		}
		if !strings.HasPrefix(entry, "/") {
			continue
		}
		if _, dup := seen[entry]; dup {
			continue
		}
		seen[entry] = struct{}{}
		keep = append(keep, entry)
	}
	return strings.Join(keep, ":")
}

// cleanBashEnv returns the environment passed to bash invocations. It
// forwards PATH/LANG/LC_ALL from the host so common tools keep
// resolving, strips injection and credential variables, and pins
// HOME/TMPDIR/PWD inside the session workspace so commands cannot
// read or write through the caller's real home directory.
//
// hostEnv is the "KEY=VALUE" slice typically returned by os.Environ().
// sessionRoot and workingDir must be absolute, cleaned directory paths.
func cleanBashEnv(hostEnv []string, sessionRoot, workingDir string) []string {
	var pathValue, langValue, lcValue string

	for _, entry := range hostEnv {
		eq := strings.IndexByte(entry, '=')
		if eq <= 0 {
			continue
		}
		key := entry[:eq]
		value := entry[eq+1:]
		if isBlockedBashEnv(key) {
			continue
		}
		switch key {
		case "PATH":
			pathValue = value
		case "LANG":
			langValue = value
		case "LC_ALL":
			lcValue = value
		}
	}

	pathValue = sanitizeBashPath(pathValue)
	if pathValue == "" {
		pathValue = bashFallbackPath
	}
	if langValue == "" {
		langValue = "C.UTF-8"
	}
	if lcValue == "" {
		lcValue = "C.UTF-8"
	}

	tmpDir := filepath.Join(sessionRoot, ".tmp")

	return []string{
		"PATH=" + pathValue,
		"HOME=" + sessionRoot,
		"TMPDIR=" + tmpDir,
		"TMP=" + tmpDir,
		"TEMP=" + tmpDir,
		"PWD=" + workingDir,
		"SHELL=" + resolveBashShell(),
		"TERM=dumb",
		"LANG=" + langValue,
		"LC_ALL=" + lcValue,
	}
}

// isBlockedBashEnv decides whether a single host env key should be
// dropped from bash invocations.
func isBlockedBashEnv(key string) bool {
	if _, ok := bashEnvInjectionVectors[key]; ok {
		return true
	}
	if _, ok := bashEnvSecretNames[key]; ok {
		return true
	}
	for _, p := range bashEnvSecretPrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	for _, s := range bashEnvSecretSuffixes {
		if strings.HasSuffix(key, s) {
			return true
		}
	}
	// Apple ships more than the three DYLD_ names above
	// (e.g. DYLD_FALLBACK_LIBRARY_PATH) so we block the whole
	// namespace defensively.
	if strings.HasPrefix(key, "DYLD_") {
		return true
	}
	return false
}
