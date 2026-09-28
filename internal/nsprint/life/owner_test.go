package life_test

import (
	"errors"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"testing"
)

func TestOwnerStateNeedsIndependentProcessIdentity(t *testing.T) {
	t.Parallel()
	owner := taskcard.ProcessOwner{Host: "host", PID: 42, Start: "creation-1"}
	cases := []struct {
		name   string
		owner  taskcard.ProcessOwner
		sample life.ProcessSample
		want   string
		called bool
	}{
		{"live", owner, life.ProcessSample{Start: "creation-1"}, "live", true},
		{"reused pid", owner, life.ProcessSample{Start: "creation-2"}, "dead", true},
		{"exited", owner, life.ProcessSample{Absent: true}, "dead", true},
		{"permission denied", owner, life.ProcessSample{Err: errors.New("permission denied")}, "unknown", true},
		{"no stamp", owner, life.ProcessSample{Start: "-"}, "unknown", true},
		{"unbound", taskcard.ProcessOwner{}, life.ProcessSample{}, "unknown", false},
		{"remote", taskcard.ProcessOwner{Host: "elsewhere", PID: 42, Start: "creation-1"}, life.ProcessSample{}, "unknown", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			called := false
			got := life.OwnerState(tc.owner, "host", func(pid int) life.ProcessSample {
				called = true
				if pid != 42 {
					t.Fatalf("pid=%d", pid)
				}
				return tc.sample
			})
			if got != tc.want || called != tc.called {
				t.Fatalf("state=%s probe=%v, want %s %v", got, called, tc.want, tc.called)
			}
		})
	}
}
