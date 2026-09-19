package swarm

// THE JOB LEASE: the launcher's own word that a card is alive (issue #1499).
//
// The bench's hygiene pass has to decide, hourly, which job directories are finished work
// and which are a card still doing its job. It used to decide by SILENCE -- no new bytes in
// harness-output.log for fifteen minutes -- and a card in one long model call or one long
// compile is silent and working. On 2026-09-19 that heuristic deleted <slot>/data and
// <slot>/tmp, a running card's HOME and TMPDIR, out from under two certify passes.
//
// A launcher knows what a heuristic can only guess, so it says so on disk: `nova-swarm
// native` writes <job>/.lease before the child starts, carrying the launcher's pid, and
// heartbeats the file's mtime while the child runs. The reaper (scripts/bench-hygiene.sh)
// reads exactly this: a job whose lease names a live pid, or whose heartbeat is younger
// than its stale window, is LIVE and is never touched. The file is removed when the run
// ends, so a finished job leaves nothing behind that pretends to be alive.
//
// The lease is opened O_NOFOLLOW, like the capture beside it: the job directory is the
// card's own writable place, and a symlink planted there by an earlier run of the same
// card would carry this process's write out of the wall.

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// JobLeaseName is the lease file's name inside the job directory. The reaper knows it by
// this name and by nothing else.
const JobLeaseName = ".lease"

// JobLeaseHeartbeat is how often a held lease's mtime is bumped. It is far under the ten
// minutes the reaper allows a heartbeat to age, so a bench under load that misses a tick
// or two still reads as live.
const JobLeaseHeartbeat = 30 * time.Second

// StartJobLease takes the lease on jobDir for the running child and returns the release.
// The release is safe to call more than once, and calling it removes the file: a lease
// nobody holds is not a lease. A lease that cannot be written is not an error the run
// fails on -- the reaper's other rules (an age, a shape) still protect the job -- so the
// caller gets a release that does nothing and the run goes on.
func StartJobLease(jobDir, label string) (release func()) {
	return startJobLeaseEvery(jobDir, label, JobLeaseHeartbeat)
}

func startJobLeaseEvery(jobDir, label string, every time.Duration) func() {
	path := filepath.Join(jobDir, JobLeaseName)
	host, _ := os.Hostname()
	body := fmt.Sprintf("pid=%d\nhost=%s\nlabel=%s\nstarted=%s\n",
		os.Getpid(), host, label, time.Now().UTC().Format(time.RFC3339))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|ONoFollow, 0o644)
	if err != nil {
		return func() {}
	}
	_, werr := f.WriteString(body)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		return func() {}
	}
	done := make(chan struct{})
	var once sync.Once
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case now := <-t.C:
				_ = os.Chtimes(path, now, now)
			}
		}
	}()
	return func() { once.Do(func() { close(done); _ = os.Remove(path) }) }
}
