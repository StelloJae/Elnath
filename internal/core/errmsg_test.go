package core

import (
	"errors"
	"fmt"
	"testing"
)

func TestUserError(t *testing.T) {
	inner := errors.New("connection refused")
	ue := NewUserError("Cannot connect to database", inner)

	if ue.Error() != "Cannot connect to database: connection refused" {
		t.Errorf("Error(): got %q", ue.Error())
	}

	if !errors.Is(ue, inner) {
		t.Error("Unwrap should expose inner error")
	}

	var target *UserError
	if !errors.As(ue, &target) {
		t.Error("errors.As should match *UserError")
	}
}

func TestFormatUserError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "UserError extracts message",
			err:  NewUserError("No API key configured", errors.New("empty")),
			want: "No API key configured",
		},
		{
			name: "plain error returned as-is",
			err:  errors.New("something went wrong"),
			want: "something went wrong",
		},
		{
			name: "strips load config prefix",
			err:  errors.New("load config: file not found"),
			want: "file not found",
		},
		{
			name: "strips init app prefix",
			err:  errors.New("init app: permission denied"),
			want: "permission denied",
		},
		{
			name: "strips build provider prefix",
			err:  errors.New("build provider: no key"),
			want: "no key",
		},
		{
			name: "wrapped UserError",
			err:  fmt.Errorf("outer: %w", NewUserError("inner message", errors.New("root"))),
			want: "inner message",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatUserError(tc.err)
			if got != tc.want {
				t.Errorf("FormatUserError: got %q, want %q", got, tc.want)
			}
		})
	}
}
