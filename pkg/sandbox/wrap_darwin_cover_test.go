// Unit coverage for the four answers pkg/sandbox/wrap_darwin.go gives the
// check verb and the SANDBOX OK line: ABI, ClampedABI, NetEnforceable and Note
// (the per-function coverage table of the unit tier held all four at 0.0%: no
// unit test reached them). wrap_darwin.go is built only under GOOS=darwin, so
// the assertions over its answers run on the darwin leg; on every other
// platform the same tests skip with that reason written, the shape
// TestAnOptionalRootsAncestorsAreGranted uses for the darwin profile's table.
// The one refusal of the darwin body that is reachable in process -- the
// backend unavailable -- needs no darwin host: it is asserted everywhere
// through the p.Available seam, the seam the body itself reads for this
// decision. Everything is in process: no sleeps, no real time, no network, no
// subprocess, no Redis or Postgres.
package sandbox

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWrapDarwinCoverABI covers ABI's one path. abi= is the kernel's Landlock
// number, and only linux has a numbered table to report (docs/SPEC-SANDBOX.md,
// the linux body): darwin's field is the dash. A darwin build that printed a
// number here would describe a wall this platform does not have.
func TestWrapDarwinCoverABI(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "darwin" {
		t.Skipf("skipped on %s: wrap_darwin.go's ABI is the darwin body's; %s builds its own", runtime.GOOS, runtime.GOOS)
	}
	t.Run("main path", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "-", ABI(), "abi=%q, want the dash: only linux fills abi=", ABI())
	})
}

// TestWrapDarwinCoverClampedABI covers ClampedABI's one path. Darwin has no
// numbered table this build can be newer or older than, so there is nothing to
// clamp to and no used= field: zero and false. A build that answered true here
// would hang a clamp note on every darwin run for a clamp that never happened.
func TestWrapDarwinCoverClampedABI(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "darwin" {
		t.Skipf("skipped on %s: wrap_darwin.go's ClampedABI is the darwin body's; %s builds its own", runtime.GOOS, runtime.GOOS)
	}
	t.Run("main path", func(t *testing.T) {
		t.Parallel()
		n, clamped := ClampedABI()
		assert.Equal(t, 0, n, "used=%d; darwin has no used= field: there is no table to clamp to", n)
		assert.False(t, clamped, "clamped=true on darwin would print a clamp the platform never made")
	})
}

// TestWrapDarwinCoverNetEnforceable covers NetEnforceable's one path. The
// darwin wall enforces a network denial by withholding the grant from the
// generated profile, so --net-deny is enforceable on this platform at every
// abi: the check verb composes its net=enforceable from this answer. A false
// would print net=unenforceable on the one platform that always denies.
func TestWrapDarwinCoverNetEnforceable(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "darwin" {
		t.Skipf("skipped on %s: wrap_darwin.go's NetEnforceable is the darwin body's; %s builds its own", runtime.GOOS, runtime.GOOS)
	}
	t.Run("main path", func(t *testing.T) {
		t.Parallel()
		assert.True(t, NetEnforceable(), "net=denied is enforced by withholding the grant from the profile; the answer must not depend on the machine")
	})
}

// TestWrapDarwinCoverNote covers Note's one path: the one clause the check
// verb prints about this backend. The rows are the clause's load bearers: the
// backend's name, that Apple deprecated it, and that the wall is the profile
// it applies -- the sentence a cold reader acts on.
func TestWrapDarwinCoverNote(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "darwin" {
		t.Skipf("skipped on %s: wrap_darwin.go's Note is the darwin body's; %s builds its own", runtime.GOOS, runtime.GOOS)
	}
	t.Run("main path", func(t *testing.T) {
		t.Parallel()
		note := Note()
		for _, want := range []string{"sandbox-exec", "deprecated", "profile"} {
			assert.Contains(t, note, want, "the note does not say %q: %q", want, note)
		}
	})
}

// TestWrapDarwinCoverNoSandboxRefusal covers the one refusal of the darwin
// body that is reachable in process: with the backend on no PATH entry and not
// at its path, Run refuses no_sandbox at exit 125 before the command runs. The
// seam is p.Available, the body's own test seam; each body refuses no_sandbox
// for an unavailable backend, so the row runs wherever the suite builds.
func TestWrapDarwinCoverNoSandboxRefusal(t *testing.T) {
	t.Parallel()

	p := &Policy{Available: func() (string, bool) { return "", false }}
	var out, errb bytes.Buffer
	code, err := Run(p, nil, strings.NewReader(""), &out, &errb, nil)
	assert.Equal(t, ExitRefused, code, "exit %d, want %d", code, ExitRefused)
	r, ok := err.(Refusal)
	require.True(t, ok, "err = %v, want a no_sandbox Refusal", err)
	assert.Equal(t, "no_sandbox", r.Reason, "reason = %q, want no_sandbox: %s", r.Reason, r.Text)
	assert.Empty(t, out.String(), "the command produced output; it must not have run: %q", out.String())
}
