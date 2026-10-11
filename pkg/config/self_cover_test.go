package config

import (
	"context"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSelfCoverTailscaleStatusBoundedByItsCallerContext covers TailscaleStatus's
// main path (pkg/config/self.go:118): the status read runs under the
// caller's context, bounded to it. The context here is ended before the call,
// and exec.Cmd.Start returns the context's error before os.StartProcess (go1.26
// os/exec) -- and subproc.Context is exec.CommandContext with WaitDelay -- so
// the bound is pinned with no child ever forked and no daemon asked: the run's
// error surfaces and carries no status bytes. Where the host has no tailscale
// program the run cannot start at all, so the bound is pinned where it can be
// reached and the host stands down honestly.
func TestSelfCoverTailscaleStatusBoundedByItsCallerContext(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("tailscale"); err != nil {
		t.Skipf("tailscale is not installed on this host (%v); the bound is pinned where the program is there to find", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	raw, err := TailscaleStatus(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled, "a run under the caller's ended context surfaces the context's error")
	assert.Nil(t, raw, "a run stopped at its context carries no status bytes")
}

// TestSelfCoverTailscaleStatusRefusesWithoutTheProgram covers TailscaleStatus's
// refusal (pkg/config/self.go:118): with no tailscale program on PATH the
// status is refused with ErrNoTailnet, never a lookup error, and no child is
// started. Where the host has the program the refusal cannot be produced
// without rewriting the process PATH, which no test in this package may do
// (the package's env-seam ledger is at its ceiling), so the case stands down
// there and pins the refusal wherever tailscale is absent.
func TestSelfCoverTailscaleStatusRefusesWithoutTheProgram(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("tailscale"); err == nil {
		t.Skip("tailscale is installed on this host; the no-program refusal cannot be produced without rewriting PATH, which no test in this package may do")
	}
	raw, err := TailscaleStatus(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoTailnet, "no program is refused as no tailnet, not as a lookup failure")
	assert.Nil(t, raw, "a refused status read carries no status bytes")
}
