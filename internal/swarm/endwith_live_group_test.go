//go:build unix

package swarm

import (
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

// graceClock is the supervisor test's clock: Sleep moves it, so the group's bounded drain
// and the terminate grace reach their ends with no wall time. The fixture's SIGKILLed child
// stays an unreaped zombie, which kill(pid, 0) still answers for, so the group genuinely
// cannot be confirmed dead and the paths under test are the real ones.
type graceClock struct{ now time.Time }

func newGraceClock() *graceClock { return &graceClock{now: time.Unix(1_700_000_000, 0)} }

func (c *graceClock) Now() time.Time { return c.now }

func (c *graceClock) Sleep(d time.Duration) { c.now = c.now.Add(d) }

// A disposable sleep process stands in for a surviving worker in its own group.
func TestATamperedPidCannotPublishAttestationToALiveGroup(t *testing.T) {
	job := t.TempDir()
	cmd := exec.Command("sleep", "30")
	ownGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pgid := cmd.Process.Pid
	stamp := StartStamp(pgid)
	t.Cleanup(func() { KillGroup(pgid, stamp); _ = cmd.Wait() })
	if !GroupAlive(pgid, stamp) {
		t.Fatal("fixture group never became alive")
	}
	// The worker can rewrite this file in its own job directory.
	if err := os.WriteFile(PidPath(job), []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	clk := newGraceClock()
	in := SuperviseInput{Nonce: "public-nonce", Now: clk.Now, Sleep: clk.Sleep, Stderr: io.Discard}
	rc := endWith(in, job, time.Now(), ExitRecord{RC: 0, End: EndDone}, "synthetic-secret", pgid, stamp)
	var rec ExitRecord
	err := ReadJSON(ExitPath(job), &rec)
	if rc == 0 && err == nil && rec.Attest == "synthetic-secret" && GroupAlive(pgid, stamp) {
		t.Fatal("supervisor published reusable attestation while the worker group remained alive")
	}
}
