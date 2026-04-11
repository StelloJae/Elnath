package conversation

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stello/elnath/internal/tools"
)

func TestConversationToolMetadata(t *testing.T) {
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
			name:        "conversation_search happy",
			tool:        NewConversationSearchTool(nil),
			params:      rawJSON(`{"query":"elnath"}`),
			wantSafe:    true,
			wantCancel:  false,
			wantReverse: true,
			wantScope:   tools.ToolScope{},
		},
		{
			name:        "conversation_search nil params falls back",
			tool:        NewConversationSearchTool(nil),
			params:      nil,
			wantSafe:    true,
			wantCancel:  false,
			wantReverse: true,
			wantScope:   tools.ConservativeScope(),
		},
		{
			name:        "conversation_search malformed json falls back",
			tool:        NewConversationSearchTool(nil),
			params:      json.RawMessage("{not valid"),
			wantSafe:    true,
			wantCancel:  false,
			wantReverse: true,
			wantScope:   tools.ConservativeScope(),
		},
		{
			name:        "cross_project_conversation_search happy",
			tool:        NewCrossProjectConversationSearchTool(nil),
			params:      rawJSON(`{"query":"elnath"}`),
			wantSafe:    true,
			wantCancel:  false,
			wantReverse: true,
			wantScope:   tools.ToolScope{},
		},
		{
			name:        "cross_project_conversation_search nil params falls back",
			tool:        NewCrossProjectConversationSearchTool(nil),
			params:      nil,
			wantSafe:    true,
			wantCancel:  false,
			wantReverse: true,
			wantScope:   tools.ConservativeScope(),
		},
		{
			name:        "cross_project_conversation_search malformed json falls back",
			tool:        NewCrossProjectConversationSearchTool(nil),
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
