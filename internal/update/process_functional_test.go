//go:build functional

package update

import (
	"context"
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// process_functional_test.go holds the tests of this package that stage a real
// child process against a real deadline, so they are the functional tier
// (a real process is functional), not a unit test paying the wall clock.
// TestHeldPipePastGraceIsNamedAndEchoesNoContent waits out killGrace (2 s);
// the join tests each stage a kill inside a window they do not own, and run
// 1.3 s to 4.4 s under load, over the unit tier's 1 s budget.

// Past the grace the refusal must name the pipe rather than blame the version
// command, and it must say so without echoing a byte the child wrote.
func TestHeldPipePastGraceIsNamedAndEchoesNoContent(t *testing.T) {
	secret := "x 9.9.9-secret"
	e := Entry{Name: "x", Kind: "tool", Installed: mustArgv(t, command(t, "linger", base64.StdEncoding.EncodeToString([]byte(secret+"\n")), (killGrace+time.Second).String()))}
	r := Installed(context.Background(), e, killGrace+5*time.Second, false)
	if r.Known() || r.Reason != "output_not_closed" {
		require.Failf(t, "", "reason=%q remedy=%q", r.Reason, r.Remedy)
	}
	require.EqualValuesf(t, leakRemedy, r.Remedy, "remedy=%q", r.Remedy)
	if strings.Contains(r.Reason, "9.9.9") || strings.Contains(r.Remedy, "9.9.9") {
		require.Failf(t, "", "diagnostic echoed child content: %q %q", r.Reason, r.Remedy)
	}
}

// Killed once the prepared artifact is on disk and nothing is confirmed: the
// next reporter must finish THAT report rather than prepare a new one.
//
// Staged up to stagingAttempts times for killReporterWhen's reason: the kill has
// to land inside a window the observer does not own. Every assertion below is the
// one it always was -- only an attempt in which the window was MISSED is retried,
// and a run that never catches it says so rather than passing.
func TestJoinReporterDeathWithPendingSavedFinishesTheSameReport(t *testing.T) {
	for attempt := 1; attempt <= stagingAttempts; attempt++ {
		if reporterDeathWithPendingSaved(t, attempt) {
			return
		}
	}
	t.Skipf("no reporter death landed with a pending artifact saved and nothing confirmed in %d staged attempts, so this case is UNPROVEN in this run rather than green", stagingAttempts)
}

// TestJoinTwoPhaseInterruptionPreservesIndexPrefixAndRecovers establishes an
// existing INDEX prefix, interrupts a real prepared send, interrupts
// its production recovery append before confirmation, then retries to prove byte-identical
// prior entries and exactly one new contribution.
func TestJoinTwoPhaseInterruptionPreservesIndexPrefixAndRecovers(t *testing.T) {
	for attempt := 1; attempt <= stagingAttempts; attempt++ {
		if twoPhaseAttempt(t, attempt) {
			return
		}
		t.Logf("two-phase interruption missed live window on attempt %d of %d; retrying", attempt, stagingAttempts)
	}
	require.Failf(t, "", "two-phase interruption failed to observe both live kill boundaries in %d attempts", stagingAttempts)
}

// Killed after the note is ON the remote but before the confirmation is
// recorded: the retry must find that same note and must not publish a second.
//
// This is the narrowest window in the package -- between the push landing and
// the reporter writing the confirmation down -- and it is watched by spawning a
// `git ls-tree` against the bare repo, so it is also the one the observer loses
// most often. Staged up to stagingAttempts times for that reason; the
// assertions are untouched.
func TestJoinReporterDeathAfterRemoteConfirmationDoesNotPublishTwice(t *testing.T) {
	for attempt := 1; attempt <= stagingAttempts; attempt++ {
		if reporterDeathAfterRemoteConfirmation(t, attempt) {
			return
		}
	}
	t.Skipf("no reporter death landed between the note reaching the remote and the confirmation being recorded in %d staged attempts, so this case is UNPROVEN in this run rather than green", stagingAttempts)
}

// TestJoinInterruptionNegativeControlWithoutKillFails proves that the witness
// strictly refuses an uninterrupted child completion: an observed boundary
// without an actual live kill (killed-alive=false) fails the witness, proving
// that an uninterrupted run cannot be reported as an interrupted recovery.
func TestJoinInterruptionNegativeControlWithoutKillFails(t *testing.T) {
	r := newReporter(t, "v1.2.3")
	wrap, record := r.wrapperOnPath(t, killLostResult)
	code, out, errs := r.send(t, wrap)
	if code != 1 || !strings.Contains(errs+out, "sent=uncertain") {
		require.Failf(t, "", "negative control wrapper was not reported as failure: %d\n%s\n%s", code, out, errs)
	}
	b, err := os.ReadFile(record)
	require.NoErrorf(t, err, "wrapper left no record: %v", err)
	receipt := parseStageRecord(string(b))
	if !receipt.observed {
		require.Failf(t, "", "expected boundary observed=true, got %s", string(b))
	}
	if receipt.killedAlive {
		require.Failf(t, "", "negative control must not report killed-alive=true: %s", string(b))
	}
}
