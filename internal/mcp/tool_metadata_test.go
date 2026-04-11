package mcp

import (
	"encoding/json"
	"reflect"
	"testing"

	toolspkg "github.com/stello/elnath/internal/tools"
)

func TestMCPToolMetadata(t *testing.T) {
	tool := NewTool(nil, ToolInfo{Name: "search", InputSchema: json.RawMessage(`{}`)})

	cases := []struct {
		name        string
		params      json.RawMessage
		wantSafe    bool
		wantCancel  bool
		wantReverse bool
		wantScope   toolspkg.ToolScope
	}{
		{
			name:        "valid params stay conservative",
			params:      json.RawMessage(`{"query":"elnath"}`),
			wantSafe:    false,
			wantCancel:  false,
			wantReverse: false,
			wantScope:   toolspkg.ConservativeScope(),
		},
		{
			name:        "nil params stay conservative",
			params:      nil,
			wantSafe:    false,
			wantCancel:  false,
			wantReverse: false,
			wantScope:   toolspkg.ConservativeScope(),
		},
		{
			name:        "malformed json stays conservative",
			params:      json.RawMessage("{not valid"),
			wantSafe:    false,
			wantCancel:  false,
			wantReverse: false,
			wantScope:   toolspkg.ConservativeScope(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tool.IsConcurrencySafe(tc.params); got != tc.wantSafe {
				t.Errorf("IsConcurrencySafe() = %v, want %v", got, tc.wantSafe)
			}
			if got := tool.ShouldCancelSiblingsOnError(); got != tc.wantCancel {
				t.Errorf("ShouldCancelSiblingsOnError() = %v, want %v", got, tc.wantCancel)
			}
			if got := tool.Reversible(); got != tc.wantReverse {
				t.Errorf("Reversible() = %v, want %v", got, tc.wantReverse)
			}
			if got := tool.Scope(tc.params); !reflect.DeepEqual(got, tc.wantScope) {
				t.Errorf("Scope() = %+v, want %+v", got, tc.wantScope)
			}
		})
	}
}
