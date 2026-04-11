package tools

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBuiltinToolMetadata(t *testing.T) {
	guard := NewPathGuard(t.TempDir(), nil)

	cases := []struct {
		name        string
		tool        Tool
		params      json.RawMessage
		wantSafe    bool
		wantReverse bool
		wantScope   ToolScope
	}{
		{
			name:        "read_file happy",
			tool:        NewReadTool(guard),
			params:      rawJSON(`{"file_path":"foo.txt"}`),
			wantSafe:    true,
			wantReverse: true,
			wantScope:   ToolScope{ReadPaths: []string{filepath.Join(guard.WorkDir(), "foo.txt")}},
		},
		{
			name:        "read_file nil params falls back",
			tool:        NewReadTool(guard),
			params:      nil,
			wantSafe:    true,
			wantReverse: true,
			wantScope:   ConservativeScope(),
		},
		{
			name:        "read_file malformed json falls back",
			tool:        NewReadTool(guard),
			params:      json.RawMessage("{not valid"),
			wantSafe:    true,
			wantReverse: true,
			wantScope:   ConservativeScope(),
		},
		{
			name:        "write_file happy",
			tool:        NewWriteTool(guard),
			params:      rawJSON(`{"file_path":"foo.txt","content":"x"}`),
			wantSafe:    false,
			wantReverse: false,
			wantScope:   ToolScope{WritePaths: []string{filepath.Join(guard.WorkDir(), "foo.txt")}, Persistent: true},
		},
		{
			name:        "write_file nil params falls back",
			tool:        NewWriteTool(guard),
			params:      nil,
			wantSafe:    false,
			wantReverse: false,
			wantScope:   ConservativeScope(),
		},
		{
			name:        "write_file malformed json falls back",
			tool:        NewWriteTool(guard),
			params:      json.RawMessage("{not valid"),
			wantSafe:    false,
			wantReverse: false,
			wantScope:   ConservativeScope(),
		},
		{
			name:        "edit_file happy",
			tool:        NewEditTool(guard),
			params:      rawJSON(`{"file_path":"foo.txt","old_string":"a","new_string":"b"}`),
			wantSafe:    false,
			wantReverse: false,
			wantScope:   ToolScope{WritePaths: []string{filepath.Join(guard.WorkDir(), "foo.txt")}, Persistent: true},
		},
		{
			name:        "edit_file nil params falls back",
			tool:        NewEditTool(guard),
			params:      nil,
			wantSafe:    false,
			wantReverse: false,
			wantScope:   ConservativeScope(),
		},
		{
			name:        "edit_file malformed json falls back",
			tool:        NewEditTool(guard),
			params:      json.RawMessage("{not valid"),
			wantSafe:    false,
			wantReverse: false,
			wantScope:   ConservativeScope(),
		},
		{
			name:        "glob explicit base path",
			tool:        NewGlobTool(guard),
			params:      rawJSON(`{"pattern":"*.txt","path":"subdir"}`),
			wantSafe:    true,
			wantReverse: true,
			wantScope:   ToolScope{ReadPaths: []string{filepath.Join(guard.WorkDir(), "subdir")}},
		},
		{
			name:        "glob nil params falls back",
			tool:        NewGlobTool(guard),
			params:      nil,
			wantSafe:    true,
			wantReverse: true,
			wantScope:   ConservativeScope(),
		},
		{
			name:        "glob malformed json falls back",
			tool:        NewGlobTool(guard),
			params:      json.RawMessage("{not valid"),
			wantSafe:    true,
			wantReverse: true,
			wantScope:   ConservativeScope(),
		},
		{
			name:        "grep default base path",
			tool:        NewGrepTool(guard),
			params:      rawJSON(`{"pattern":"needle"}`),
			wantSafe:    true,
			wantReverse: true,
			wantScope:   ToolScope{ReadPaths: []string{guard.WorkDir()}},
		},
		{
			name:        "grep nil params falls back",
			tool:        NewGrepTool(guard),
			params:      nil,
			wantSafe:    true,
			wantReverse: true,
			wantScope:   ConservativeScope(),
		},
		{
			name:        "grep malformed json falls back",
			tool:        NewGrepTool(guard),
			params:      json.RawMessage("{not valid"),
			wantSafe:    true,
			wantReverse: true,
			wantScope:   ConservativeScope(),
		},
		{
			name:        "bash conservative scope",
			tool:        NewBashTool(guard),
			params:      rawJSON(`{"command":"ls"}`),
			wantSafe:    false,
			wantReverse: false,
			wantScope: ToolScope{
				Network:    true,
				Persistent: true,
				WritePaths: []string{guard.WorkDir()},
			},
		},
		{
			name:        "bash nil params falls back",
			tool:        NewBashTool(guard),
			params:      nil,
			wantSafe:    false,
			wantReverse: false,
			wantScope:   ConservativeScope(),
		},
		{
			name:        "bash malformed json falls back",
			tool:        NewBashTool(guard),
			params:      json.RawMessage("{not valid"),
			wantSafe:    false,
			wantReverse: false,
			wantScope:   ConservativeScope(),
		},
		{
			name:        "git status is read only",
			tool:        NewGitTool(guard),
			params:      rawJSON(`{"subcommand":"status"}`),
			wantSafe:    true,
			wantReverse: false,
			wantScope:   ToolScope{ReadPaths: []string{guard.WorkDir()}},
		},
		{
			name:        "git commit is mutating",
			tool:        NewGitTool(guard),
			params:      rawJSON(`{"subcommand":"commit","message":"x"}`),
			wantSafe:    false,
			wantReverse: false,
			wantScope:   ToolScope{WritePaths: []string{guard.WorkDir()}, Persistent: true},
		},
		{
			name:        "git branch is write",
			tool:        NewGitTool(guard),
			params:      rawJSON(`{"subcommand":"branch"}`),
			wantSafe:    false,
			wantReverse: false,
			wantScope:   ToolScope{WritePaths: []string{guard.WorkDir()}, Persistent: true},
		},
		{
			name:        "git diff is read",
			tool:        NewGitTool(guard),
			params:      rawJSON(`{"subcommand":"diff"}`),
			wantSafe:    true,
			wantReverse: false,
			wantScope:   ToolScope{ReadPaths: []string{guard.WorkDir()}},
		},
		{
			name:        "git log is read",
			tool:        NewGitTool(guard),
			params:      rawJSON(`{"subcommand":"log"}`),
			wantSafe:    true,
			wantReverse: false,
			wantScope:   ToolScope{ReadPaths: []string{guard.WorkDir()}},
		},
		{
			name:        "git nil params falls back",
			tool:        NewGitTool(guard),
			params:      nil,
			wantSafe:    false,
			wantReverse: false,
			wantScope:   ConservativeScope(),
		},
		{
			name:        "git malformed json falls back",
			tool:        NewGitTool(guard),
			params:      json.RawMessage("{not valid"),
			wantSafe:    false,
			wantReverse: false,
			wantScope:   ConservativeScope(),
		},
		{
			name:        "web_fetch network read",
			tool:        NewWebFetchTool(),
			params:      rawJSON(`{"url":"https://example.com"}`),
			wantSafe:    true,
			wantReverse: true,
			wantScope:   ToolScope{Network: true},
		},
		{
			name:        "web_fetch nil params falls back",
			tool:        NewWebFetchTool(),
			params:      nil,
			wantSafe:    true,
			wantReverse: true,
			wantScope:   ConservativeScope(),
		},
		{
			name:        "web_fetch malformed json falls back",
			tool:        NewWebFetchTool(),
			params:      json.RawMessage("{not valid"),
			wantSafe:    true,
			wantReverse: true,
			wantScope:   ConservativeScope(),
		},
		{
			name:        "web_search network read",
			tool:        NewWebSearchTool(),
			params:      rawJSON(`{"query":"elnath"}`),
			wantSafe:    true,
			wantReverse: true,
			wantScope:   ToolScope{Network: true},
		},
		{
			name:        "web_search nil params falls back",
			tool:        NewWebSearchTool(),
			params:      nil,
			wantSafe:    true,
			wantReverse: true,
			wantScope:   ConservativeScope(),
		},
		{
			name:        "web_search malformed json falls back",
			tool:        NewWebSearchTool(),
			params:      json.RawMessage("{not valid"),
			wantSafe:    true,
			wantReverse: true,
			wantScope:   ConservativeScope(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.tool.IsConcurrencySafe(tc.params); got != tc.wantSafe {
				t.Errorf("IsConcurrencySafe() = %v, want %v", got, tc.wantSafe)
			}
			if got := tc.tool.Reversible(); got != tc.wantReverse {
				t.Errorf("Reversible() = %v, want %v", got, tc.wantReverse)
			}
			if got := tc.tool.Scope(tc.params); !reflect.DeepEqual(got, tc.wantScope) {
				t.Errorf("Scope() = %+v, want %+v", got, tc.wantScope)
			}
		})
	}
}

func rawJSON(s string) json.RawMessage {
	return json.RawMessage(s)
}
