#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/elnath-go-bug002-guidance.XXXXXX")"

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

python3 - <<'PY' "$REPO_ROOT/scripts/run_current_benchmark_wrapper.sh" "$REPO_ROOT/scripts/run_baseline_benchmark_wrapper.sh"
from pathlib import Path
import sys

current = Path(sys.argv[1]).read_text().replace("\\`", "`")
baseline = Path(sys.argv[2]).read_text()

required = [
    "verify observable reload behavior through `v.GetString(\"foo\")`",
    "Use existing `TestWatchFile` helper patterns",
    "`SetConfigFile(configFile)`",
    "Do not assert `v.configFile` is empty after `ReadInConfig()`",
    "avoid unrelated logger/test-helper machinery such as `slog`",
    "If verification first fails on a test compile error, recovery must still fix semantic regression assertions",
]
missing = [snippet for snippet in required if snippet not in current]
if missing:
    raise SystemExit("current wrapper missing GO-BUG-002 guidance: " + ", ".join(missing))

for forbidden in required:
    if forbidden in baseline:
        raise SystemExit("baseline wrapper should not receive current-side GO-BUG-002 guidance: " + forbidden)
PY

create_source_repo() {
  local repo_dir="$1"
  mkdir -p "$repo_dir"
  cat >"$repo_dir/go.mod" <<'EOF'
module example.com/benchmark

go 1.22
EOF
  cat >"$repo_dir/main.go" <<'EOF'
package benchmark

func Answer() int { return 42 }
EOF
  cat >"$repo_dir/main_test.go" <<'EOF'
package benchmark

import "testing"

func TestAnswer(t *testing.T) {
	if got := Answer(); got != 42 {
		t.Fatalf("Answer() = %d, want 42", got)
	}
}
EOF
  cat >"$repo_dir/viper.go" <<'EOF'
package benchmark

type Viper struct {
	configFile string
	value      string
}

func (v *Viper) ReadInConfig() error { return nil }
func (v *Viper) WatchConfig()        {}
func (v *Viper) SetConfigFile(path string) {
	v.configFile = path
}
func (v *Viper) GetString(string) string {
	if v.value == "" {
		return "bar"
	}
	return v.value
}
EOF
  cat >"$repo_dir/viper_test.go" <<'EOF'
package benchmark

import "testing"

var require = struct {
	NoError func(*testing.T, error)
	Empty   func(*testing.T, string)
	Equal   func(*testing.T, string, string)
}{
	NoError: func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	},
	Empty: func(t *testing.T, got string) {
		t.Helper()
		if got != "" {
			t.Fatalf("not empty: %s", got)
		}
	},
	Equal: func(t *testing.T, want string, got string) {
		t.Helper()
		if got != want {
			t.Fatalf("got %s, want %s", got, want)
		}
	},
}

func TestLegacyConfigState(t *testing.T) {
	v := &Viper{}
	require.NoError(t, v.ReadInConfig())
	require.Empty(t, v.configFile)
}
EOF
  git -C "$repo_dir" init -q
  git -C "$repo_dir" add .
  git -C "$repo_dir" -c user.name='Test User' -c user.email='test@example.com' commit -qm "init"
}

create_fake_elnath() {
  local bin_path="$1"
  cat >"$bin_path" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

case "${FAKE_SCENARIO:?}" in
  brittle_empty_config_assertion)
    cat > viper.go <<'GO'
package benchmark

type Viper struct{ configFile string }

func (v *Viper) ReadInConfig() error { return nil }
func (v *Viper) WatchConfig() {}
func (v *Viper) GetString(string) string { return "baz" }
GO
    cat > viper_test.go <<'GO'
package benchmark

import "testing"

var require = struct {
	NoError func(*testing.T, error)
	Empty   func(*testing.T, string)
	Equal   func(*testing.T, string, string)
}{
	NoError: func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	},
	Empty: func(t *testing.T, got string) {
		t.Helper()
		if got != "" {
			t.Fatalf("not empty: %s", got)
		}
	},
	Equal: func(t *testing.T, want string, got string) {
		t.Helper()
		if got != want {
			t.Fatalf("got %s, want %s", got, want)
		}
	},
}

func TestWatchFile(t *testing.T) {
	v := &Viper{}
	require.NoError(t, v.ReadInConfig())
	require.Empty(t, v.configFile) // generated brittle assertion
	require.Equal(t, "baz", v.GetString("foo"))
}
GO
    echo "Modified files: viper.go, viper_test.go"
    echo "Verification: go test ./... passed."
    ;;
  brittle_equal_config_assertion)
    cat > viper.go <<'GO'
package benchmark

type Viper struct{ configFile string }

func (v *Viper) ReadInConfig() error { return nil }
func (v *Viper) WatchConfig() {}
func (v *Viper) GetString(string) string { return "baz" }
GO
    cat > viper_test.go <<'GO'
package benchmark

import "testing"

var assert = struct {
	NoError func(*testing.T, error)
	Equal   func(*testing.T, string, string)
}{
	NoError: func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	},
	Equal: func(t *testing.T, want string, got string) {
		t.Helper()
		if got != want {
			t.Fatalf("got %s, want %s", got, want)
		}
	},
}

func TestWatchFile(t *testing.T) {
	v := &Viper{}
	assert.NoError(t, v.ReadInConfig())
	assert.Equal(t, "", v.configFile)
	assert.Equal(t, "baz", v.GetString("foo"))
}
GO
    echo "Modified files: viper.go, viper_test.go"
    echo "Verification: go test ./... passed."
    ;;
  legacy_empty_assertion_elsewhere)
    cat > viper.go <<'GO'
package benchmark

type Viper struct {
	configFile string
	value      string
}

func (v *Viper) ReadInConfig() error { return nil }
func (v *Viper) WatchConfig() {}
func (v *Viper) SetConfigFile(path string) {
	v.configFile = path
}
func (v *Viper) GetString(string) string {
	if v.value == "" {
		return "bar"
	}
	return v.value
}
GO
    cat >> viper_test.go <<'GO'

func TestWatchFile(t *testing.T) {
	v := &Viper{value: "baz"}
	v.SetConfigFile("config.yaml")
	v.WatchConfig()
	require.Equal(t, "baz", v.GetString("foo"))
}
GO
    echo "Modified files: viper.go, viper_test.go"
    echo "Verification: go test ./... passed."
    ;;
  observable_reload_missing)
    cat > viper.go <<'GO'
package benchmark

func WatchConfig() {}
GO
    cat > viper_test.go <<'GO'
package benchmark

import "testing"

func TestWatchFile(t *testing.T) {
	t.Fatal("observable reload behavior missing")
}
GO
    echo "Modified files: viper.go, viper_test.go"
    ;;
  *)
    echo "unknown FAKE_SCENARIO=${FAKE_SCENARIO}" >&2
    exit 2
    ;;
esac
EOF
  chmod +x "$bin_path"
}

assert_json_case() {
  local output_path="$1"
  local python_assert="$2"
  python3 - <<'PY' "$output_path" "$python_assert"
import json
import sys

path = sys.argv[1]
code = sys.argv[2]
data = json.load(open(path))
ns = {"data": data, "path": path}
exec(code, ns, ns)
PY
}

run_wrapper_case() {
  local scenario="$1"
  local output_path="$2"
  FAKE_SCENARIO="$scenario" \
  ELNATH_BIN="$TMP_DIR/fake-elnath.sh" \
  ELNATH_TIMEOUT=30 \
  ELNATH_BENCHMARK_PERMISSION_MODE=bypass \
  HOME="$TMP_DIR/host-home" \
  "$REPO_ROOT/scripts/run_current_benchmark_wrapper.sh" \
    "$output_path" \
    "GO-BUG-002" \
    "bugfix" \
    "go" \
    "Identify and fix a configuration reload regression in a Go service and verify the fix with targeted regression coverage." \
    "file://$SOURCE_REPO" \
    "" \
    "service_backend" \
    "brownfield_holdout"
}

SOURCE_REPO="$TMP_DIR/source-repo"
create_source_repo "$SOURCE_REPO"
create_fake_elnath "$TMP_DIR/fake-elnath.sh"
mkdir -p "$TMP_DIR/host-home"

before_corpus_hash="$(find "$REPO_ROOT/benchmarks" -maxdepth 1 -name '*.json' -type f -print0 | sort -z | xargs -0 shasum -a 256)"
before_baseline_hash="$(shasum -a 256 "$REPO_ROOT/scripts/run_baseline_benchmark_wrapper.sh")"

run_wrapper_case brittle_empty_config_assertion "$TMP_DIR/brittle-empty-config-assertion.json"
assert_json_case "$TMP_DIR/brittle-empty-config-assertion.json" '
assert data["success"] is False, data
assert data["verification_passed"] is True, data
assert data["failure_family"] == "incomplete_patch", data
assert "viper_test.go" in data["changed_files"], data
'

run_wrapper_case brittle_equal_config_assertion "$TMP_DIR/brittle-equal-config-assertion.json"
assert_json_case "$TMP_DIR/brittle-equal-config-assertion.json" '
assert data["success"] is False, data
assert data["verification_passed"] is True, data
assert data["failure_family"] == "incomplete_patch", data
assert "viper_test.go" in data["changed_files"], data
'

run_wrapper_case legacy_empty_assertion_elsewhere "$TMP_DIR/legacy-empty-assertion-elsewhere.json"
assert_json_case "$TMP_DIR/legacy-empty-assertion-elsewhere.json" '
assert data["success"] is True, data
assert data["verification_passed"] is True, data
assert data["failure_family"] == "", data
assert "viper_test.go" in data["changed_files"], data
'

run_wrapper_case observable_reload_missing "$TMP_DIR/observable-reload-missing.json"
assert_json_case "$TMP_DIR/observable-reload-missing.json" '
assert data["success"] is False, data
assert data["verification_passed"] is False, data
assert data["failure_family"] != "", data
'

after_corpus_hash="$(find "$REPO_ROOT/benchmarks" -maxdepth 1 -name '*.json' -type f -print0 | sort -z | xargs -0 shasum -a 256)"
after_baseline_hash="$(shasum -a 256 "$REPO_ROOT/scripts/run_baseline_benchmark_wrapper.sh")"

if [[ "$before_corpus_hash" != "$after_corpus_hash" ]]; then
  echo "FAIL: benchmark corpus JSON files changed during GO-BUG-002 guidance tests" >&2
  exit 1
fi
if [[ "$before_baseline_hash" != "$after_baseline_hash" ]]; then
  echo "FAIL: baseline wrapper changed during GO-BUG-002 guidance tests" >&2
  exit 1
fi

echo "PASS: GO-BUG-002 benchmark wrapper guidance/guards are bounded"
