//go:build functional

package swarm

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests exec whole programs -- the fake runner this package builds
// (testdata/fakerunner), git, sqlite3 or sh -- so they are the functional tier's,
// not unit tests (Glenn 2026-09-26, nova-tools#4328: unit tests under 2 s and
// frugal with the machine's cores). The rest of the file's tests stay in the
// unit tier.

// A lease whose launcher is gone is not a live lease: the next run takes the directory
// over rather than refusing forever. A crashed launcher must not cost a card ten minutes.
// This is the stale-owner recovery the exclusion must never grow over.
func TestADeadHoldersLeaseIsTakenOver(t *testing.T) {
	t.Parallel()

	job := t.TempDir()
	path := filepath.Join(job, JobLeaseName)
	host, _ := os.Hostname()
	// A REAPED CHILD's pid: a process that really ran and really ended, so the kernel's
	// answer is the test's fact and not an assumption about which numbers are never alive.
	body := fmt.Sprintf("pid=%d\nhost=%s\nlabel=dead-card\nnonce=deadbeef\nstarted=%s\n",
		deadPID(t), host, time.Now().UTC().Format(time.RFC3339))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	release, err := StartJobLease(job, "card-1")
	require.NoError(t, err, "a lease whose launcher is dead was treated as live: %v", err)
	defer release()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "label=card-1\n", "the dead holder's lease is still on disk:\n%s", raw)
}

// COMPETING STALE TAKERS (Stella, #1585, after the atomic publication landed). Publication
// being atomic does not make RECLAMATION atomic. Two runs can both read one abandoned
// record and both judge it stale; the first clears it and links its own live lease into
// place; and the second -- deciding from a record that is no longer there, and removing a
// PATH rather than that record -- unlinks the winner's live lease and takes the directory
// from underneath it. That is the same class as the defect this whole PR is about, arriving
// one layer down.
//
// THE BARRIER IS THE RECLAMATION ITSELF, and there is no clock anywhere in here: B is held
// at the instant it has read and judged the abandoned record and is about to clear it, A
// runs to completion inside that window, and only then is B let go.
//
//	B reads and judges -> [held] -> A reclaims, publishes, is live -> B released -> B clears
//
// Both ways a record can be abandoned are driven, because the two recoveries are kept
// deliberately apart: a pid this kernel asked about and was answered for, and a record it
// cannot read at all which only age can retire.
func TestACompetingStaleTakerNeverRemovesTheWinnersLiveLease(t *testing.T) {
	t.Parallel()

	for _, how := range []string{"dead-pid", "unknown-and-stale"} {
		t.Run(how, func(t *testing.T) {
			job := t.TempDir()
			path := filepath.Join(job, JobLeaseName)
			staleRecord(t, job, how)

			atReclaim, holdReclaim := make(chan struct{}), make(chan struct{})
			type taken struct {
				release func()
				err     error
			}
			bDone := make(chan taken, 1)
			go func() {
				release, err := startJobLeaseTicking(job, "B", make(chan time.Time), func() {},
					jobLeaseHooks{atReclaim: atReclaim, holdReclaim: holdReclaim})
				bDone <- taken{release, err}
			}()

			// B has read the abandoned record and judged it: it is about to clear it, and
			// it is held exactly there.
			<-atReclaim

			// A wins the path, from the same abandoned record, and is now the live holder.
			releaseA, err := StartJobLease(job, "A")
			require.NoError(t, err, "A could not reclaim the abandoned record: %v", err)
			defer releaseA()
			mine, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Contains(t, string(mine), "label=A\n", "A did not win the path:\n%s", mine)

			// B is let go, and clears the record it judged -- which is not there any more.
			close(holdReclaim)
			// Any FURTHER reclamation B makes runs free: the barrier was for the one
			// window this test is about, and a drained channel cannot deadlock a run that
			// goes round its loop again.
			go func() {
				for range atReclaim {
				}
			}()
			got := <-bDone
			require.Error(t, got.err, "the losing reclaimer took the job directory from the run that won it: two launchers, one job directory (#1585, competing stale takers)")
			_, ok := HeldJobLease(got.err)
			assert.True(t, ok, "the loser's refusal is %v; the winner is a live holder and has to be named as one", got.err)

			// AND A'S LEASE IS STILL ON DISK, BYTE FOR BYTE. This is the assertion the
			// whole test exists for: a reclaimer removed a live lease it had never read.
			after, err := os.ReadFile(path)
			require.NoError(t, err, "the losing reclaimer removed the winner's live lease: %v (#1585)", err)
			assert.Equal(t, string(mine), string(after), "the losing reclaimer rewrote the winner's lease:\nA published:\n%s\nnow:\n%s", mine, after)
		})
	}
}

// A reclamation that puts a record back leaves no tombstone behind either: the job
// directory holds the lease and nothing else when the dust settles.
func TestACompetingReclamationLeavesNoTombstoneBehind(t *testing.T) {
	t.Parallel()

	job := t.TempDir()
	staleRecord(t, job, "dead-pid")

	atReclaim, holdReclaim := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := startJobLeaseTicking(job, "B", make(chan time.Time), func() {},
			jobLeaseHooks{atReclaim: atReclaim, holdReclaim: holdReclaim})
		done <- err
	}()
	<-atReclaim
	releaseA, err := StartJobLease(job, "A")
	require.NoError(t, err, "A could not reclaim the abandoned record: %v", err)
	defer releaseA()
	close(holdReclaim)
	go func() {
		for range atReclaim {
		}
	}()
	err = <-done
	require.Error(t, err, "the loser was told it holds the directory")

	entries, err := os.ReadDir(job)
	require.NoError(t, err)
	for _, e := range entries {
		assert.Equal(t, JobLeaseName, e.Name(), "the job directory holds %q beside the lease", e.Name())
	}
}

// staleRecord writes a lease nobody holds any more, in whichever of the two ways a record
// can be abandoned, and backdates the heartbeat where the rule needs it. It is the fixture
// the competing-takers tests start from.
func staleRecord(t *testing.T, job, how string) {
	t.Helper()
	path := filepath.Join(job, JobLeaseName)
	switch how {
	case "dead-pid":
		host, _ := os.Hostname()
		body := fmt.Sprintf("pid=%d\nhost=%s\nlabel=gone\nnonce=abandoned\nstarted=%s\n",
			deadPID(t), host, time.Now().UTC().Format(time.RFC3339))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	case "unknown-and-stale":
		require.NoError(t, os.WriteFile(path, []byte("half a li"), 0o644))
		old := time.Now().Add(-2 * JobLeaseStale)
		require.NoError(t, os.Chtimes(path, old, old))
	default:
		t.Fatalf("no such abandoned record: %s", how)
	}
}

// deadPID returns a pid no process holds: a child that has already been reaped. The child is
// the package's own fake runner doing nothing -- `/bin/sh` is not a program windows has, and
// a test that only needs a process to start and stop has no business naming one shell.
func deadPID(t *testing.T) int {
	t.Helper()
	bin := runnerDoing(t, t.TempDir(), "throwaway", runnerStep{Op: "exit", N: 0})
	cmd := exec.Command(bin, "throwaway", "1", "model", filepath.Join(t.TempDir(), "no.card"), t.TempDir(), "unmetered")
	err := cmd.Run()
	require.NoError(t, err, "starting a throwaway child: %v", err)
	return cmd.Process.Pid
}
