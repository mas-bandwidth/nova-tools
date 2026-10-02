package swarm

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// collideUntilRead makes the first few reads of `path` fail and then ENDS the collision, so
// a test can assert the rule with no Windows and no race. `path` becomes a DIRECTORY -- this
// package's portable stand-in for the microseconds a Windows replace is pending -- and the
// reader meets the two things a real collision has: a read that fails, and a path that holds
// still again a few polls later, far inside SteadyWindow.
//
// THE SEAM IS ALSO WHAT LIFTS THE COLLISION, and that is the whole of its correctness. A
// fixture that replaced the path from a goroutine and then flipped a flag judges the read
// ALREADY IN FLIGHT by the platform's rule -- that read failed while the stand-in was still
// there and asks this seam afterwards -- and hands the caller a stale error it was never
// given the chance to re-read past. Here the replace happens INSIDE the call that answers
// "yes, a collision", so the reader that paid for it is by construction the one whose retry
// finds the record. No goroutine, no clock, nothing to lose a race to.
//
// What a directory read fails WITH is the platform's business (EISDIR here, something else
// on Windows) and the rule under test is not, so while the stand-in is in place EVERY failed
// read is ANSWERED as a collision -- the shape
// `TestTheLaunchHandshakeEndsAtItsOwnTimeoutWhenEveryReadCollides` already uses on that
// runner. Two things keep that from arming on somebody else's read: a record that is GONE
// answers ErrNotExist and is an ANSWER, never a collision (the package's own `missing` rule,
// and true on every platform), and each fixture leaves every OTHER record of its job present
// and readable. Once the record is back the platform's rule decides again, so nothing here
// loops twice.
//
// The COUNT this returns is narrower than that answer: only a failed read AT `path` is
// counted, and only such a read lifts the collision, so the `hits.Load() == 0` guard every
// caller ends with names the read it means rather than any failure that happened to land
// while the seam was armed (#132). The narrowing stops at the count: what is transient stays
// the seam's unnarrowed answer, for the bd6f3d7 reason above.
func collideUntilRead(t *testing.T, path string, body string) *atomic.Int64 {
	t.Helper()
	// A path that is already a record is REPLACED by the stand-in, so a collision can be
	// armed over a file a reader has read once already -- which is what a rehash meets.
	_ = os.Remove(path)
	require.NoError(t, os.MkdirAll(path, 0o755))
	var hits atomic.Int64
	var restored atomic.Bool
	forceTransientIO = func(err error) bool {
		if err == nil || restored.Load() || errors.Is(err, fs.ErrNotExist) {
			return transientIO(err)
		}
		// THE COUNT IS NARROWED TO THIS PATH, THE ANSWER IS NOT (#132). `hits` is what every
		// caller asserts on last -- a zero means no read ever went through the collision
		// wait -- so it must count reads of THIS record and not any failed read that happens
		// while the seam is armed. The match is on the count (and on the lift, which belongs
		// to the read that paid for it) and never on the transient ANSWER above: a
		// PathError.Path match in that position is what broke on the Windows runner at
		// bd6f3d7, where a pending replace does not always hand its error back at the path
		// the reader named. A failure at somebody else's path is still waited out, and is
		// nobody's collision here.
		var pe *fs.PathError
		if !errors.As(err, &pe) || filepath.Clean(pe.Path) != filepath.Clean(path) {
			return true
		}
		// A few polls of collision -- enough that a reader which does not wait one out is
		// caught, and orders of magnitude less than SteadyWindow -- and then the record is
		// put back. The replace is retried on the next turn if it does not land, because a
		// directory with a reader in it does not come away on the first ask on Windows.
		if hits.Add(1) >= 3 {
			_ = os.RemoveAll(path)
			if err := os.WriteFile(path, []byte(body), 0o644); err == nil {
				restored.Store(true)
			}
		}
		return true
	}
	t.Cleanup(func() { forceTransientIO = nil })
	return &hits
}

// THE FIXTURE'S OWN GUARD IS ONLY AS GOOD AS WHAT IT COUNTS (#132, a LOW of the #126 read).
//
// `hits` is what every test above asserts on LAST -- `hits.Load() == 0` means no read ever
// went through the collision wait, which is the bug itself rather than a pass. So what the
// counter counts is load-bearing: an armed seam that counted ANY failed read would let a
// guard be satisfied by somebody else's failure at somebody else's path, and the test would
// report a collision it never produced.
//
// The narrowing is of the COUNT ONLY. Whether a failure is transient must stay the seam's
// unnarrowed answer: a `PathError.Path` match in that position is what broke on the Windows
// runner at bd6f3d7, where the error a pending replace hands back is not always carried at
// the path the reader named. This test holds both halves at once -- a wrong-path read is
// still waited out, and is not counted.
func TestTheCollisionSeamCountsOnlyReadsOfThePathItArmed(t *testing.T) {
	const body = "# a published report\n\n## Head\nfindings: 1\n"
	dir := t.TempDir()
	armed := filepath.Join(dir, CopiedResult)
	// Somebody else's record, unreadable for a reason that is NOT ErrNotExist: a directory,
	// the same portable stand-in the seam itself uses for a pending replace.
	other := filepath.Join(dir, "somebody-elses-record")
	require.NoError(t, os.MkdirAll(other, 0o755))

	hits := collideUntilRead(t, armed, body)

	// Half one: the transient ANSWER is not narrowed. A failed read of another path through
	// the armed seam is still waited out, to its caller's own bound and not this package's,
	// so the test costs a tenth of a second rather than SteadyWindow.
	started := time.Now()
	_, err := readFileSteadyBy(other, time.Now().Add(20*steadyPoll))
	require.Error(t, err, "reading a directory answered no error at all; this fixture has nothing to arm on")
	waited := time.Since(started)
	assert.GreaterOrEqual(t, waited, steadyPoll, "an armed seam called a wrong-path failure final after %s: the transient answer must not be narrowed by path (bd6f3d7)", waited)

	// Half two: the COUNT is narrowed. Nothing above was a read of the armed path.
	n := hits.Load()
	assert.Zero(t, n, "hits counted %d read(s) of a path this seam never armed: the vacuity guard of every test here would be satisfied by somebody else's failure", n)
	// And the collision is still standing: it is lifted by the read that PAID for it, never
	// by somebody else's failures.
	_, err = os.ReadFile(armed)
	assert.Error(t, err, "the stand-in over the armed path was lifted by reads of another path")

	// And the seam still does its own job: a read of the armed path is counted, waited out,
	// and answered with the record.
	raw, err := readFileSteady(armed)
	require.NoError(t, err, "the armed path never came back: %v", err)
	assert.Equal(t, body, string(raw), "the record the retry found is not the one the seam restored:\n%s", raw)
	assert.NotZero(t, hits.Load(), "no read of the armed path went through the collision wait")
}
