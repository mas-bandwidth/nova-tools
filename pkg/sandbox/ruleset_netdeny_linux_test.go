//go:build linux

package sandbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// 2026-10-17: TestWallNetDenyRefusesATCPConnect made a real TCP connect to
// 127.0.0.1:45678 in the unit tier and timed out instead of being refused on
// captain, hetzner and amd-3. It moves to the functional tier; the unit tier
// keeps the policy it checks: under --net-deny at ABI >= 4 the ruleset handles
// TCP connect and bind (Landlock denies what it handles and no rule grants), and
// below ABI 4, or without --net-deny, it handles no network access.
func TestTheNetDenyRulesetHandlesTCPConnectAndBind(t *testing.T) {
	t.Parallel()
	for abi := minKnownABI; abi <= maxKnownABI+1; abi++ {
		on, off := rulesetFor(abi, true), rulesetFor(abi, false)
		assert.Equal(t, handledFS(abi), on.HandledAccessFS, "abi %d: the file set is the ABI's", abi)
		assert.Equal(t, scopedFor(abi), on.Scoped, "abi %d: the scopes are the ABI's", abi)
		assert.Zero(t, off.HandledAccessNet, "abi %d: without --net-deny no network access is handled", abi)
		if abi < 4 {
			assert.Zero(t, on.HandledAccessNet, "abi %d: below 4 the kernel has no network rights", abi)
			continue
		}
		assert.NotZero(t, on.HandledAccessNet&netConnectTCP, "abi %d: --net-deny handles TCP connect", abi)
		assert.NotZero(t, on.HandledAccessNet&netBindTCP, "abi %d: --net-deny handles TCP bind", abi)
	}
}
