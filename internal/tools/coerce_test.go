package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type coercionIntArgs struct {
	Offset int `json:"offset"`
}

type coercionBoolArgs struct {
	Recursive bool `json:"recursive"`
}

type coercionFloatArgs struct {
	Threshold float64 `json:"threshold"`
}

func TestCoerceToolArgs_StringToInt(t *testing.T) {
	got := CoerceToolArgs(json.RawMessage(`{"offset":"42"}`), &coercionIntArgs{})

	var decoded coercionIntArgs
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if decoded.Offset != 42 {
		t.Fatalf("Offset = %d, want 42", decoded.Offset)
	}
}

func TestCoerceToolArgs_StringToBool(t *testing.T) {
	got := CoerceToolArgs(json.RawMessage(`{"recursive":"true"}`), &coercionBoolArgs{})

	var decoded coercionBoolArgs
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if !decoded.Recursive {
		t.Fatal("Recursive = false, want true")
	}
}

func TestCoerceToolArgs_FloatToInt(t *testing.T) {
	t.Run("whole number", func(t *testing.T) {
		got := CoerceToolArgs(json.RawMessage(`{"offset":5.0}`), &coercionIntArgs{})

		var decoded coercionIntArgs
		if err := json.Unmarshal(got, &decoded); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}
		if decoded.Offset != 5 {
			t.Fatalf("Offset = %d, want 5", decoded.Offset)
		}
	})

	t.Run("fractional value stays unchanged", func(t *testing.T) {
		params := json.RawMessage(`{"offset":5.5}`)
		got := CoerceToolArgs(params, &coercionIntArgs{})
		if !bytes.Equal(got, params) {
			t.Fatalf("CoerceToolArgs() = %s, want unchanged %s", got, params)
		}
	})
}

func TestCoerceToolArgs_StringToFloat(t *testing.T) {
	got := CoerceToolArgs(json.RawMessage(`{"threshold":"1.5"}`), &coercionFloatArgs{})

	var decoded coercionFloatArgs
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if decoded.Threshold != 1.5 {
		t.Fatalf("Threshold = %v, want 1.5", decoded.Threshold)
	}
}

func TestCoerceToolArgs_AlreadyCorrect(t *testing.T) {
	params := json.RawMessage(`{"offset":42}`)
	got := CoerceToolArgs(params, &coercionIntArgs{})
	if !bytes.Equal(got, params) {
		t.Fatalf("CoerceToolArgs() = %s, want unchanged %s", got, params)
	}
}

func TestCoerceToolArgs_UnknownField(t *testing.T) {
	got := CoerceToolArgs(json.RawMessage(`{"offset":"42","extra":"keep-me"}`), &coercionIntArgs{})

	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if decoded["extra"] != "keep-me" {
		t.Fatalf("extra = %#v, want %q", decoded["extra"], "keep-me")
	}
	if decoded["offset"] != float64(42) {
		t.Fatalf("offset = %#v, want 42", decoded["offset"])
	}
}

func TestReadToolWithCoercion(t *testing.T) {
	dir := t.TempDir()
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = strings.Repeat("x", 1)
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	reg := NewRegistry()
	reg.Register(NewReadTool(NewPathGuard(dir, nil)))

	res, err := reg.Execute(context.Background(), "read_file", json.RawMessage(`{"file_path":"test.txt","offset":"10","limit":"5"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	for _, want := range []string{"    10\t", "    14\t"} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("output does not contain %q:\n%s", want, res.Output)
		}
	}
	if strings.Contains(res.Output, "    15\t") {
		t.Fatalf("output should be limited to 5 lines:\n%s", res.Output)
	}
}
