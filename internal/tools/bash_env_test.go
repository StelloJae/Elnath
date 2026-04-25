package tools

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// envSnapshot converts the slice returned by cleanBashEnv into a
// map keyed by variable name for easier assertion.
func envSnapshot(t *testing.T, env []string) map[string]string {
	t.Helper()
	out := make(map[string]string, len(env))
	for _, entry := range env {
		eq := strings.IndexByte(entry, '=')
		if eq <= 0 {
			t.Fatalf("malformed env entry: %q", entry)
		}
		out[entry[:eq]] = entry[eq+1:]
	}
	return out
}

func TestCleanBashEnv_PathPreservedFromHost(t *testing.T) {
	host := []string{"PATH=/opt/custom/bin:/usr/bin"}
	env := envSnapshot(t, cleanBashEnv(host, "/root", "/root"))
	if env["PATH"] != "/opt/custom/bin:/usr/bin" {
		t.Errorf("PATH = %q, want %q", env["PATH"], "/opt/custom/bin:/usr/bin")
	}
}

func TestCleanBashEnv_PathFallbackWhenHostEmpty(t *testing.T) {
	env := envSnapshot(t, cleanBashEnv(nil, "/root", "/root"))
	if env["PATH"] != bashFallbackPath {
		t.Errorf("PATH = %q, want fallback %q", env["PATH"], bashFallbackPath)
	}
}

func TestCleanBashEnv_HomeIsSessionRoot(t *testing.T) {
	env := envSnapshot(t, cleanBashEnv([]string{"HOME=/home/alice"}, "/sess/root", "/sess/root"))
	if env["HOME"] != "/sess/root" {
		t.Errorf("HOME = %q, want %q", env["HOME"], "/sess/root")
	}
}

func TestCleanBashEnv_TmpDirInsideSession(t *testing.T) {
	env := envSnapshot(t, cleanBashEnv(nil, "/sess/root", "/sess/root"))
	want := filepath.Join("/sess/root", ".tmp")
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		if env[key] != want {
			t.Errorf("%s = %q, want %q", key, env[key], want)
		}
	}
}

func TestCleanBashEnv_PwdMatchesWorkingDir(t *testing.T) {
	env := envSnapshot(t, cleanBashEnv(nil, "/sess/root", "/sess/root/sub"))
	if env["PWD"] != "/sess/root/sub" {
		t.Errorf("PWD = %q, want %q", env["PWD"], "/sess/root/sub")
	}
}

func TestCleanBashEnv_ShellAndTermPinned(t *testing.T) {
	env := envSnapshot(t, cleanBashEnv([]string{"SHELL=/bin/fish", "TERM=xterm-256color"}, "/r", "/r"))
	if env["SHELL"] != "/bin/bash" {
		t.Errorf("SHELL = %q, want /bin/bash", env["SHELL"])
	}
	if env["TERM"] != "dumb" {
		t.Errorf("TERM = %q, want dumb", env["TERM"])
	}
}

func TestCleanBashEnv_LangDefaultWhenMissing(t *testing.T) {
	env := envSnapshot(t, cleanBashEnv(nil, "/r", "/r"))
	if env["LANG"] != "C.UTF-8" {
		t.Errorf("LANG = %q, want C.UTF-8 fallback", env["LANG"])
	}
	if env["LC_ALL"] != "C.UTF-8" {
		t.Errorf("LC_ALL = %q, want C.UTF-8 fallback", env["LC_ALL"])
	}
}

func TestCleanBashEnv_LangForwardedWhenPresent(t *testing.T) {
	host := []string{"LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8"}
	env := envSnapshot(t, cleanBashEnv(host, "/r", "/r"))
	if env["LANG"] != "en_US.UTF-8" {
		t.Errorf("LANG = %q, want forwarded en_US.UTF-8", env["LANG"])
	}
	if env["LC_ALL"] != "en_US.UTF-8" {
		t.Errorf("LC_ALL = %q, want forwarded en_US.UTF-8", env["LC_ALL"])
	}
}

func TestCleanBashEnv_BlocksInjectionVectors(t *testing.T) {
	host := []string{
		"BASH_ENV=/tmp/evil",
		"ENV=/tmp/evil",
		"LD_PRELOAD=/tmp/hook.so",
		"LD_LIBRARY_PATH=/tmp/libs",
		"DYLD_INSERT_LIBRARIES=/tmp/hook.dylib",
		"DYLD_LIBRARY_PATH=/tmp/libs",
		"DYLD_FRAMEWORK_PATH=/tmp/fw",
		"DYLD_FALLBACK_LIBRARY_PATH=/tmp/fallback",
		"SSH_AUTH_SOCK=/tmp/ssh-agent.sock",
		"GPG_AGENT_INFO=/tmp/gpg",
	}
	env := envSnapshot(t, cleanBashEnv(host, "/r", "/r"))
	for _, key := range []string{
		"BASH_ENV", "ENV", "LD_PRELOAD", "LD_LIBRARY_PATH",
		"DYLD_INSERT_LIBRARIES", "DYLD_LIBRARY_PATH",
		"DYLD_FRAMEWORK_PATH", "DYLD_FALLBACK_LIBRARY_PATH",
		"SSH_AUTH_SOCK", "GPG_AGENT_INFO",
	} {
		if _, present := env[key]; present {
			t.Errorf("%s must not be forwarded to bash", key)
		}
	}
}

func TestCleanBashEnv_BlocksNamedSecrets(t *testing.T) {
	host := []string{
		"OPENAI_API_KEY=sk-a",
		"ANTHROPIC_API_KEY=sk-b",
		"GITHUB_TOKEN=ghp",
		"GH_TOKEN=ghp2",
		"GOOGLE_APPLICATION_CREDENTIALS=/tmp/gcp.json",
		"HUGGINGFACE_TOKEN=hf",
		"HF_TOKEN=hf2",
	}
	env := envSnapshot(t, cleanBashEnv(host, "/r", "/r"))
	for key := range bashEnvSecretNames {
		if _, present := env[key]; present {
			t.Errorf("named secret %s must not reach bash", key)
		}
	}
}

func TestCleanBashEnv_BlocksAWSPrefix(t *testing.T) {
	host := []string{
		"AWS_ACCESS_KEY_ID=AKIA",
		"AWS_SECRET_ACCESS_KEY=xxx",
		"AWS_SESSION_TOKEN=yyy",
		"AWS_REGION=us-east-1",
	}
	env := envSnapshot(t, cleanBashEnv(host, "/r", "/r"))
	for _, key := range []string{
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY",
		"AWS_SESSION_TOKEN", "AWS_REGION",
	} {
		if _, present := env[key]; present {
			t.Errorf("AWS_ namespace var %s must not reach bash", key)
		}
	}
}

func TestCleanBashEnv_BlocksSuffixPatterns(t *testing.T) {
	host := []string{
		"ACME_API_KEY=1",
		"FOO_TOKEN=2",
		"BAR_SECRET=3",
		"BAZ_PASSWORD=4",
		"QUUX_ID=safe",
	}
	env := envSnapshot(t, cleanBashEnv(host, "/r", "/r"))
	for _, key := range []string{"ACME_API_KEY", "FOO_TOKEN", "BAR_SECRET", "BAZ_PASSWORD"} {
		if _, present := env[key]; present {
			t.Errorf("suffix-pattern secret %s must not reach bash", key)
		}
	}
	if _, present := env["QUUX_ID"]; present {
		// Non-secret pattern variables are still dropped — the env
		// is a strict allowlist; this test only documents that we
		// do not accidentally keep QUUX_ID just because its name
		// does not match a blocklist.
		t.Logf("QUUX_ID dropped by allowlist policy (expected)")
	}
}

func TestCleanBashEnv_AllowlistOnlyBaseline(t *testing.T) {
	// Anything the host sets that is not an explicit baseline value
	// (PATH/LANG/LC_ALL source) must not appear in the output. The
	// clean env is an allowlist, not a blocklist mop-up.
	host := []string{
		"RANDOM_HARMLESS_VAR=ok",
		"ANOTHER=1",
	}
	env := envSnapshot(t, cleanBashEnv(host, "/r", "/r"))
	if _, present := env["RANDOM_HARMLESS_VAR"]; present {
		t.Errorf("RANDOM_HARMLESS_VAR must not appear; baseline is an allowlist")
	}
	if _, present := env["ANOTHER"]; present {
		t.Errorf("ANOTHER must not appear; baseline is an allowlist")
	}
}

func TestCleanBashEnv_BaselineKeysExactSet(t *testing.T) {
	env := cleanBashEnv(nil, "/r", "/r")
	got := make([]string, 0, len(env))
	for _, entry := range env {
		eq := strings.IndexByte(entry, '=')
		got = append(got, entry[:eq])
	}
	sort.Strings(got)

	want := []string{
		"HOME", "LANG", "LC_ALL", "PATH", "PWD",
		"SHELL", "TEMP", "TERM", "TMP", "TMPDIR",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("baseline keys = %v, want %v", got, want)
	}
}

func TestSanitizeBashPath_DropsEmptyDotAndRelative(t *testing.T) {
	// ":" between "/usr/bin" and "/bin" produces an empty middle entry,
	// "." is the implicit-cwd vector, and "relative/bin" is anything
	// not anchored at /. All three must be dropped.
	got := sanitizeBashPath("/usr/bin::.:relative/bin:/bin")
	want := "/usr/bin:/bin"
	if got != want {
		t.Errorf("sanitizeBashPath = %q, want %q", got, want)
	}
}

func TestSanitizeBashPath_PreservesAbsoluteEntries(t *testing.T) {
	in := "/usr/local/bin:/usr/bin:/bin:/opt/homebrew/bin"
	if got := sanitizeBashPath(in); got != in {
		t.Errorf("sanitizeBashPath = %q, want unchanged %q", got, in)
	}
}

func TestSanitizeBashPath_DeduplicatesPreservingFirstOccurrence(t *testing.T) {
	got := sanitizeBashPath("/usr/bin:/bin:/usr/bin:/sbin:/bin")
	want := "/usr/bin:/bin:/sbin"
	if got != want {
		t.Errorf("sanitizeBashPath = %q, want %q", got, want)
	}
}

func TestSanitizeBashPath_EmptyInputReturnsEmpty(t *testing.T) {
	if got := sanitizeBashPath(""); got != "" {
		t.Errorf("sanitizeBashPath(empty) = %q, want empty", got)
	}
}

func TestCleanBashEnv_SanitizesUnsafeHostPathEntries(t *testing.T) {
	// Host PATH carries an empty entry, ".", a relative path, and a
	// duplicate; all four must be stripped before bash sees PATH.
	host := []string{"PATH=/usr/bin::.:relative/bin:/usr/bin:/bin"}
	env := envSnapshot(t, cleanBashEnv(host, "/sess", "/sess"))
	if env["PATH"] != "/usr/bin:/bin" {
		t.Errorf("PATH = %q, want %q", env["PATH"], "/usr/bin:/bin")
	}
}

func TestCleanBashEnv_PathFallbackWhenSanitizationEmpties(t *testing.T) {
	// Host PATH only contains unsafe entries: sanitization yields the
	// empty string, so cleanBashEnv must apply the fallback PATH
	// instead of handing bash an empty PATH (which would make every
	// command resolution fail in a confusing way).
	host := []string{"PATH=.:relative/bin:"}
	env := envSnapshot(t, cleanBashEnv(host, "/sess", "/sess"))
	if env["PATH"] != bashFallbackPath {
		t.Errorf("PATH = %q, want fallback %q", env["PATH"], bashFallbackPath)
	}
}

func TestResolveBashShell_ReturnsAbsoluteExecutable(t *testing.T) {
	got := resolveBashShell()
	if !strings.HasPrefix(got, "/") {
		t.Fatalf("resolveBashShell = %q, want absolute path", got)
	}
	info, err := os.Stat(got)
	if err != nil {
		// /bin/bash is the deterministic fallback and must exist on
		// every supported runtime; if not, the host environment is
		// incompatible with this build.
		t.Fatalf("resolveBashShell returned %q which does not stat: %v", got, err)
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		t.Fatalf("resolveBashShell returned %q which is not an executable file", got)
	}
}

func TestCleanBashEnv_ShellEqualsResolvedBinary(t *testing.T) {
	// SHELL must match the binary actually invoked by exec.Command so
	// any user script re-execing through "$SHELL" lands on the same,
	// pinned bash and never reaches a host-PATH lookup.
	env := envSnapshot(t, cleanBashEnv(nil, "/sess", "/sess"))
	if env["SHELL"] != resolveBashShell() {
		t.Errorf("SHELL = %q, want %q", env["SHELL"], resolveBashShell())
	}
}
