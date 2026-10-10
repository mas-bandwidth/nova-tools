//go:build linux

package sandbox

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test 7's linux half. The seam is the `available` variable, the mirror of darwin's: no
// kernel on the fleet reports any of the three numbers these tests need, so without the
// seam none of the ABI rules has a test on the platform whose body is built.
//
// THE RULE CHANGED WHEN ubuntu-latest MOVED TO ABI 7, AND THIS FILE IS WHERE IT CHANGED. The previous revision
// REFUSED an ABI above the table. Landlock's contract is that a newer kernel accepts a
// ruleset built for an older ABI -- the kernel documentation tells a program to use the
// highest ABI it knows that is at or below the kernel's -- so what that refusal turned
// away was a wall the kernel would have enforced exactly as asked, and every new kernel
// was a maintenance trap until the table grew. It took `main` red on ubuntu-latest the
// day that runner moved to a kernel reporting abi 7 (run 35045469738):
//
//	SANDBOX REFUSED reason=landlock_abi_unknown: this kernel reports landlock abi 7
//	and the highest this tool's table knows is 6
//
// So an ABI above the table is now a CLAMP that is SAID, and the refusal is left where
// there is nothing to clamp to: no landlock at all, and an ABI below the table's first row.

// The clamp: a kernel above the table gets the wall the table's top row describes, and
// both numbers reach the operator -- the kernel's on abi=, the wall's on used=, and the
// sentence in the note. A mutation that clamps SILENTLY (drop the used= field, or make
// ClampedABI's second return false) turns this red, which is the point: the hole the old
// refusal guarded is real -- the rights the newer ABI added are not handled -- and what
// replaced the refusal is the saying of it.
func TestNewerLandlockABIIsClampedToTheTableOnLinux(t *testing.T) {
	t.Parallel()

	forced := maxKnownABI + 1
	abiFn := func() (int, bool) { return forced, true }

	used, clamped := ClampedABIWith(abiFn)
	require.True(t, clamped, "abi %d is above the table's %d and was not reported as clamped", forced, maxKnownABI)
	assert.Equal(t, maxKnownABI, used, "the wall is built at abi %d, want the table's maximum %d", used, maxKnownABI)
	// abi= stays the KERNEL's number: a line that printed the clamped one would hide the
	// very fact it exists to publish.
	assert.Equal(t, strconv.Itoa(forced), ABIWith(abiFn), "abi=%s, want the kernel's %d", ABIWith(abiFn), forced)
	// Both numbers, because the note is what tells the reader which kernel it has and
	// which table it needs: a note naming neither is a note nobody can act on.
	note := NoteWith(abiFn)
	for _, n := range []int{forced, maxKnownABI} {
		assert.Contains(t, note, strconv.Itoa(n), "the note does not name %d: %q", n, note)
	}
	assert.Contains(t, note, "clamped", "the note does not say the wall was clamped: %q", note)
	// That the command then RUNS is the other half, and it cannot be asserted here: Run
	// would wall this test binary for the rest of the suite. It is asserted where a
	// caller of its own exists -- cmd/nova-sandbox's TestLandlockWallClampsAnABIAboveTheTable,
	// which runs the real tool as a child on whatever kernel it finds.
}

// Every row of the table is itself, and only above the top is a clamp. A mutation that
// clamps everything to the table's maximum -- or that clamps nothing -- turns this red.
func TestWallABIClampsOnlyAboveTheTable(t *testing.T) {
	t.Parallel()

	for abi := minKnownABI; abi <= maxKnownABI; abi++ {
		used, clamped := wallABI(abi)
		assert.Equal(t, abi, used, "wallABI(%d) = %d, %v; want %d, false", abi, used, clamped, abi)
		assert.False(t, clamped, "wallABI(%d) = %d, %v; want %d, false", abi, used, clamped, abi)
	}
	for _, abi := range []int{maxKnownABI + 1, maxKnownABI + 2, 99} {
		used, clamped := wallABI(abi)
		assert.Equal(t, maxKnownABI, used, "wallABI(%d) = %d, %v; want %d, true", abi, used, clamped, maxKnownABI)
		assert.True(t, clamped, "wallABI(%d) = %d, %v; want %d, true", abi, used, clamped, maxKnownABI)
	}
}

// The refusal that is LEFT, and the tripwire that makes it more than a word in the exit
// table: an ABI below the table's first row names both numbers at exit 125 and the
// command does not run. The tripwire is the command's own stdout -- `/bin/echo` writes
// through an INHERITED descriptor, which Landlock does not govern, so a byte in the
// buffer means the tool reached `cmd.Start` whether or not a wall went up.
//
// To see it red, disable the guard in Run -- rewrite `if abi < minKnownABI {` as
// `if false && abi < minKnownABI {` -- and run this test on a linux machine: with the
// guard gone the forced 0 reaches createRuleset and the refusal that arrives is
// `sandbox_failed`, not this one.
func TestLandlockABIBelowTheTableRefusesOnLinux(t *testing.T) {
	t.Parallel()

	forced := minKnownABI - 1
	r, out := runRefused(t, func() (int, bool) { return forced, true })
	require.Equal(t, "landlock_abi_unknown", r.Reason, "reason = %q, want landlock_abi_unknown: %s", r.Reason, r.Text)
	for _, n := range []int{forced, minKnownABI} {
		assert.Contains(t, r.Text, strconv.Itoa(n), "the refusal does not name %d: %q", n, r.Text)
	}
	assert.Empty(t, out, "the command produced output; it must not have run: %q", out)
}

// Rule 1 on this platform: no landlock is no run. It had no linux test of its own while
// the ABI refusal above stood in for it; the clamp took that stand-in away, so it gets one.
func TestNoLandlockRefusesOnLinux(t *testing.T) {
	t.Parallel()

	r, out := runRefused(t, func() (int, bool) { return 0, false })
	require.Equal(t, "no_sandbox", r.Reason, "reason = %q, want no_sandbox: %s", r.Reason, r.Text)
	assert.Empty(t, out, "the command produced output; it must not have run: %q", out)
}

// runRefused runs a normal job through Run and insists it was refused at exit 125 before
// the command ran. It is safe to call in the test binary ONLY because every caller has
// forced `abiFn` to a value Run refuses on: a Run that reached restrictSelf would wall
// this process for the rest of the suite, and a Landlock domain cannot be lifted.
func runRefused(t *testing.T, abiFn func() (int, bool)) (Refusal, string) {
	t.Helper()
	write := t.TempDir()
	home := filepath.Join(write, "home")
	require.NoError(t, os.MkdirAll(home, 0o755))
	p, bad := Build(in(t, write, t.TempDir(), home, "/bin/echo", "tripwire"))
	require.Empty(t, bad, "refused at build: %v", bad)
	p.LandlockABI = abiFn
	var out, errb bytes.Buffer
	code, err := Run(p, os.Environ(), strings.NewReader(""), &out, &errb, nil)
	assert.Equal(t, ExitRefused, code, "exit %d, want %d", code, ExitRefused)
	r, ok := err.(Refusal)
	require.True(t, ok, "err = %v, want a Refusal", err)
	assert.Equal(t, ExitRefused, r.Code(), "the refusal carries exit %d, want %d", r.Code(), ExitRefused)
	return r, out.String()
}
