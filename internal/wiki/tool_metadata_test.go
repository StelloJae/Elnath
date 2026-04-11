package wiki

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stello/elnath/internal/tools"
)

func TestWikiToolMetadata(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	pagePath := filepath.Join(store.WikiDir(), "concepts", "foo.md")

	cases := []struct {
		name        string
		tool        tools.Tool
		params      json.RawMessage
		wantSafe    bool
		wantCancel  bool
		wantReverse bool
		wantScope   tools.ToolScope
	}{
		{
			name:        "wiki_search happy",
			tool:        NewWikiSearchTool(nil),
			params:      rawJSON(`{"query":"elnath"}`),
			wantSafe:    true,
			wantCancel:  false,
			wantReverse: true,
			wantScope:   tools.ToolScope{},
		},
		{
			name:        "wiki_search nil params falls back",
			tool:        NewWikiSearchTool(nil),
			params:      nil,
			wantSafe:    true,
			wantCancel:  false,
			wantReverse: true,
			wantScope:   tools.ConservativeScope(),
		},
		{
			name:        "wiki_search malformed json falls back",
			tool:        NewWikiSearchTool(nil),
			params:      json.RawMessage("{not valid"),
			wantSafe:    true,
			wantCancel:  false,
			wantReverse: true,
			wantScope:   tools.ConservativeScope(),
		},
		{
			name:        "wiki_read happy",
			tool:        NewWikiReadTool(store),
			params:      rawJSON(`{"path":"concepts/foo.md"}`),
			wantSafe:    true,
			wantCancel:  false,
			wantReverse: true,
			wantScope:   tools.ToolScope{ReadPaths: []string{pagePath}},
		},
		{
			name:        "wiki_read nil params falls back",
			tool:        NewWikiReadTool(nil),
			params:      nil,
			wantSafe:    true,
			wantCancel:  false,
			wantReverse: true,
			wantScope:   tools.ConservativeScope(),
		},
		{
			name:        "wiki_read malformed json falls back",
			tool:        NewWikiReadTool(nil),
			params:      json.RawMessage("{not valid"),
			wantSafe:    true,
			wantCancel:  false,
			wantReverse: true,
			wantScope:   tools.ConservativeScope(),
		},
		{
			name:        "wiki_write happy",
			tool:        NewWikiWriteTool(store),
			params:      rawJSON(`{"path":"concepts/foo.md","title":"Foo","content":"bar","type":"concept"}`),
			wantSafe:    false,
			wantCancel:  false,
			wantReverse: false,
			wantScope:   tools.ToolScope{WritePaths: []string{pagePath}, Persistent: true},
		},
		{
			name:        "wiki_write nil params falls back",
			tool:        NewWikiWriteTool(store),
			params:      nil,
			wantSafe:    false,
			wantCancel:  false,
			wantReverse: false,
			wantScope:   tools.ConservativeScope(),
		},
		{
			name:        "wiki_write malformed json falls back",
			tool:        NewWikiWriteTool(store),
			params:      json.RawMessage("{not valid"),
			wantSafe:    false,
			wantCancel:  false,
			wantReverse: false,
			wantScope:   tools.ConservativeScope(),
		},
		{
			name:        "cross_project_search happy",
			tool:        NewCrossProjectSearchTool(nil),
			params:      rawJSON(`{"query":"elnath"}`),
			wantSafe:    true,
			wantCancel:  false,
			wantReverse: true,
			wantScope:   tools.ToolScope{},
		},
		{
			name:        "cross_project_search nil params falls back",
			tool:        NewCrossProjectSearchTool(nil),
			params:      nil,
			wantSafe:    true,
			wantCancel:  false,
			wantReverse: true,
			wantScope:   tools.ConservativeScope(),
		},
		{
			name:        "cross_project_search malformed json falls back",
			tool:        NewCrossProjectSearchTool(nil),
			params:      json.RawMessage("{not valid"),
			wantSafe:    true,
			wantCancel:  false,
			wantReverse: true,
			wantScope:   tools.ConservativeScope(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.tool.IsConcurrencySafe(tc.params); got != tc.wantSafe {
				t.Errorf("IsConcurrencySafe() = %v, want %v", got, tc.wantSafe)
			}
			if got := tc.tool.ShouldCancelSiblingsOnError(); got != tc.wantCancel {
				t.Errorf("ShouldCancelSiblingsOnError() = %v, want %v", got, tc.wantCancel)
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
