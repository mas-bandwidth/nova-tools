//go:build functional

package ci_test

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// TestFleetCIUnhealthyNonUp verifies that ci_healthy treats a bench with beat present
// but non-UP state (DOWN, PROBING, HELD) as unhealthy, blocking rerun, and treats UP as healthy.
func TestFleetCIUnhealthyNonUp(t *testing.T) {
	t.Setenv(testutil.CIEnv, "1")
	f := newFixture(t, "ctl-a", "ctl-b")

	f.client.HSet(f.ctx, "bench:ctl-a:state", "state", "UP", "at", "1000")
	label := f.cut()
	token, identity := f.deal(label, "ctl-a")
	f.end(label, token, identity, "DONE", "done", ci.Fail, "internal/x", "TestX")

	for _, nonUp := range []string{"DOWN", "PROBING", "HELD"} {
		f.client.HSet(f.ctx, "bench:ctl-b:state", "state", nonUp, "at", "1000")
		reply, err := f.client.FCall(f.ctx, "ns_ci_rerun", nil, f.sprint, label, "rowan", "rerun test").StringSlice()
		if err != nil {
			t.Fatalf("ci rerun on %s: %v", nonUp, err)
		}
		if len(reply) < 1 || reply[0] != "BLOCKED" {
			t.Fatalf("ci rerun on %s got %v; want [BLOCKED ...]", nonUp, reply)
		}
	}

	// Control: state UP accepts and schedules rerun on ctl-b
	f.client.HSet(f.ctx, "bench:ctl-b:state", "state", "UP", "at", "1000")
	reply, err := f.client.FCall(f.ctx, "ns_ci_rerun", nil, f.sprint, label, "rowan", "rerun test").StringSlice()
	if err != nil {
		t.Fatalf("ci rerun on UP: %v", err)
	}
	if len(reply) < 3 || reply[0] != "RERUN" || reply[2] != "ctl-b" {
		t.Fatalf("ci rerun on UP got %v; want [RERUN 2 ctl-b]", reply)
	}
}
