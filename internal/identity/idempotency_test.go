package identity

import "testing"

func TestKeyFor(t *testing.T) {
	tests := []struct {
		name      string
		principal Principal
		prompt    string
		other     Principal
		otherText string
		wantSame  bool
		wantEmpty bool
	}{
		{
			name:      "same principal and prompt yields same key",
			principal: Principal{UserID: "user-1", ProjectID: "project-1", Surface: "cli"},
			prompt:    "deploy now",
			other:     Principal{UserID: "user-1", ProjectID: "project-1", Surface: "cli"},
			otherText: "deploy now",
			wantSame:  true,
		},
		{
			name:      "surface is excluded from key",
			principal: Principal{UserID: "user-1", ProjectID: "project-1", Surface: "cli"},
			prompt:    "deploy now",
			other:     Principal{UserID: "user-1", ProjectID: "project-1", Surface: "telegram"},
			otherText: "deploy now",
			wantSame:  true,
		},
		{
			name:      "prompt whitespace is ignored",
			principal: Principal{UserID: "user-1", ProjectID: "project-1", Surface: "cli"},
			prompt:    "deploy now",
			other:     Principal{UserID: "user-1", ProjectID: "project-1", Surface: "cli"},
			otherText: "  deploy now  ",
			wantSame:  true,
		},
		{
			name:      "empty prompt yields empty key",
			principal: Principal{UserID: "user-1", ProjectID: "project-1", Surface: "cli"},
			prompt:    "   ",
			wantEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := KeyFor(tt.principal, tt.prompt)
			if tt.wantEmpty {
				if got != "" {
					t.Fatalf("KeyFor() = %q, want empty", got)
				}
				return
			}
			if got == "" {
				t.Fatal("KeyFor() returned empty key")
			}

			other := KeyFor(tt.other, tt.otherText)
			if tt.wantSame && got != other {
				t.Fatalf("KeyFor() = %q, other = %q, want same key", got, other)
			}
		})
	}
}
