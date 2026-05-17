package daemon

import "testing"

func TestParseDeliveryTarget(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want DeliveryTarget
	}{
		{
			name: "origin",
			raw:  " origin ",
			want: DeliveryTarget{Kind: DeliveryTargetOrigin},
		},
		{
			name: "local",
			raw:  "local",
			want: DeliveryTarget{Kind: DeliveryTargetLocal},
		},
		{
			name: "platform home channel",
			raw:  "Telegram",
			want: DeliveryTarget{Kind: DeliveryTargetPlatform, Platform: "telegram"},
		},
		{
			name: "explicit platform address",
			raw:  "telegram:123456",
			want: DeliveryTarget{Kind: DeliveryTargetPlatform, Platform: "telegram", Address: "123456", Explicit: true},
		},
		{
			name: "explicit thread address preserves case",
			raw:  "teams:RoomA:ThreadB",
			want: DeliveryTarget{Kind: DeliveryTargetPlatform, Platform: "teams", Address: "RoomA", ThreadID: "ThreadB", Explicit: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDeliveryTarget(tt.raw)
			if err != nil {
				t.Fatalf("ParseDeliveryTarget(%q): %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("ParseDeliveryTarget(%q) = %+v, want %+v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParseDeliveryTargetRejectsEmpty(t *testing.T) {
	if _, err := ParseDeliveryTarget("   "); err == nil {
		t.Fatal("expected empty target error")
	}
}

func TestParseDeliveryTargetRejectsEmptyExplicitAddress(t *testing.T) {
	if _, err := ParseDeliveryTarget("telegram:"); err == nil {
		t.Fatal("expected empty explicit address error")
	}
	if _, err := ParseDeliveryTarget("telegram::thread"); err == nil {
		t.Fatal("expected empty explicit address with thread error")
	}
}

func TestParseDeliveryTargets(t *testing.T) {
	got, err := ParseDeliveryTargets([]string{"origin", "local", "telegram:123"})
	if err != nil {
		t.Fatalf("ParseDeliveryTargets: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("targets = %d, want 3", len(got))
	}
	if got[0].Kind != DeliveryTargetOrigin || got[1].Kind != DeliveryTargetLocal || got[2].String() != "telegram:123" {
		t.Fatalf("targets = %+v, want origin/local/telegram:123", got)
	}
}

func TestDeliveryTargetString(t *testing.T) {
	tests := []struct {
		target DeliveryTarget
		want   string
	}{
		{DeliveryTarget{Kind: DeliveryTargetOrigin}, "origin"},
		{DeliveryTarget{Kind: DeliveryTargetLocal}, "local"},
		{DeliveryTarget{Kind: DeliveryTargetPlatform, Platform: "telegram"}, "telegram"},
		{DeliveryTarget{Kind: DeliveryTargetPlatform, Platform: "telegram", Address: "123456", Explicit: true}, "telegram:123456"},
		{DeliveryTarget{Kind: DeliveryTargetPlatform, Platform: "teams", Address: "RoomA", ThreadID: "ThreadB", Explicit: true}, "teams:RoomA:ThreadB"},
	}

	for _, tt := range tests {
		if got := tt.target.String(); got != tt.want {
			t.Fatalf("DeliveryTarget.String() = %q, want %q", got, tt.want)
		}
	}
}

func TestDeliveryTargetHomeChannel(t *testing.T) {
	if !(DeliveryTarget{Kind: DeliveryTargetPlatform, Platform: "telegram"}).IsHomeChannel() {
		t.Fatal("platform without explicit address should be home channel")
	}
	if (DeliveryTarget{Kind: DeliveryTargetPlatform, Platform: "telegram", Address: "123456", Explicit: true}).IsHomeChannel() {
		t.Fatal("explicit platform address should not be home channel")
	}
}
