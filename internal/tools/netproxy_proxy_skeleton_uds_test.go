package tools

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// B3b-4-3 additive contract: listenForFlag accepts both `host:port`
// (TCP, legacy) and `unix:<path>` (UDS, new). The UDS form is required
// by the Linux bwrap bridge so the netproxy child can bind UDS
// endpoints OUTSIDE the bwrap netns and the in-bwrap bridge can dial
// them via a bind-mounted directory.

func TestListenForFlag_TCPLegacyShape(t *testing.T) {
	l, err := listenForFlag("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listenForFlag tcp: %v", err)
	}
	defer l.Close()
	if _, ok := l.Addr().(*net.TCPAddr); !ok {
		t.Errorf("expected *net.TCPAddr; got %T", l.Addr())
	}
}

// shortUDSPath returns a tempdir-relative socket path short enough to
// satisfy macOS's 104-byte sockaddr_un limit. t.TempDir on darwin can
// return paths >100 bytes which then push any sub-path over the limit;
// we work around by using os.MkdirTemp("", ...) which respects $TMPDIR
// (typically /tmp on linux, /var/folders/... on darwin) and registering
// our own cleanup.
func shortUDSPath(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "udst-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, name)
}

func TestListenForFlag_UnixPrefixBindsUDS(t *testing.T) {
	path := shortUDSPath(t, "ln.sock")
	l, err := listenForFlag("unix:" + path)
	if err != nil {
		// macOS sockaddr_un limit is 104 bytes; if the test's
		// tempdir prefix already exceeds the limit we cannot bind
		// at all. Skip rather than assert in that environment.
		if strings.Contains(err.Error(), "invalid argument") && len(path) > 100 {
			t.Skipf("UDS path too long for this OS (%d bytes): %s", len(path), path)
		}
		t.Fatalf("listenForFlag unix: %v", err)
	}
	defer l.Close()
	if _, ok := l.Addr().(*net.UnixAddr); !ok {
		t.Errorf("expected *net.UnixAddr; got %T", l.Addr())
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("UDS path %s not created: %v", path, statErr)
	}
}

func TestListenForFlag_UnixOverwritesStaleSocket(t *testing.T) {
	path := shortUDSPath(t, "stale.sock")
	// Pre-create a stale entry at the path so the bind would
	// otherwise fail with EADDRINUSE.
	if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
		t.Fatalf("seed stale: %v", err)
	}
	l, err := listenForFlag("unix:" + path)
	if err != nil {
		if strings.Contains(err.Error(), "invalid argument") && len(path) > 100 {
			t.Skipf("UDS path too long for this OS (%d bytes): %s", len(path), path)
		}
		t.Fatalf("listenForFlag unix (with stale): %v", err)
	}
	defer l.Close()
}

func TestStringsCutPrefix_LocalCopy(t *testing.T) {
	cases := []struct {
		s, prefix string
		want      string
		ok        bool
	}{
		{"unix:/tmp/x", "unix:", "/tmp/x", true},
		{"127.0.0.1:0", "unix:", "", false},
		{"unix:", "unix:", "", true},
		{"", "unix:", "", false},
	}
	for _, tc := range cases {
		got, ok := stringsCutPrefix(tc.s, tc.prefix)
		if got != tc.want || ok != tc.ok {
			t.Errorf("stringsCutPrefix(%q, %q) = (%q, %v), want (%q, %v)",
				tc.s, tc.prefix, got, ok, tc.want, tc.ok)
		}
	}
}

func TestRemoveIfExists_NoError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing")
	if err := removeIfExists(path); err != nil {
		t.Errorf("missing path must not error: %v", err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := removeIfExists(path); err != nil {
		t.Errorf("existing file must not error: %v", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("path should be gone; got stat: %v", statErr)
	}
}

func TestListenForFlag_TCPParseError(t *testing.T) {
	_, err := listenForFlag("not a valid address")
	if err == nil {
		t.Fatal("expected error on invalid tcp spec")
	}
	if strings.Contains(err.Error(), "unix") {
		t.Errorf("tcp parse error should not mention unix; got: %v", err)
	}
}
