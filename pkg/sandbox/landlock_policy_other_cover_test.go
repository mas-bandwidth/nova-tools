// Unit coverage for pkg/sandbox/landlock_policy_other.go: LandlockPolicyText
// (the per-function coverage table of the unit tier held it at 0.0%: no unit
// test reached it). landlock_policy_other.go is built only under GOOS != linux,
// so the assertions over its answer run on the darwin leg; on every linux build
// the same test skips with that reason written, the shape TestWrapDarwinCoverABI
// uses for the darwin body. Everything is in process: no sleeps, no real time,
// no network, no subprocess, no Redis or Postgres.
package sandbox

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLandlockPolicyOtherCoverText covers LandlockPolicyText's one path: the
// refusal (docs/SPEC-SANDBOX.md, "Linux — Landlock, no root": the ruleset text
// is linux's alone). The `policy` verb prints this platform's own generated
// policy here, and this body exists so the verb's backend branch compiles on
// every build — so the rows pin the refusal whatever the policy holds: an empty
// text, a non-nil error naming linux's body and this platform's backend. A
// ruleset text or a nil error on this platform would hand a run a wall the
// platform cannot build.
func TestLandlockPolicyOtherCoverText(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "linux" {
		t.Skipf("skipped on linux: landlock_policy_other.go is the other platforms' body; linux builds its own")
	}
	rows := []struct {
		name string
		p    *Policy
	}{
		{name: "nil policy", p: nil},
		{name: "policy with writes", p: &Policy{Writes: []string{"/cover-write"}}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			text, err := LandlockPolicyText(row.p)
			require.Error(t, err, "err = nil: the landlock ruleset text is linux's; %s must refuse it", runtime.GOOS)
			assert.Empty(t, text, "text = %q, want empty: there is no ruleset text to print on %s", text, runtime.GOOS)
			assert.Contains(t, err.Error(), "the landlock ruleset is linux's", "the refusal must name linux's body: %q", err.Error())
			assert.Contains(t, err.Error(), Backend, "the refusal must name this platform's backend %q: %q", Backend, err.Error())
		})
	}
}
