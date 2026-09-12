//go:build unix

package swarm

import (
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

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
	in := SuperviseInput{Nonce: "public-nonce", Now: time.Now, Stderr: io.Discard}
	rc := endWith(in, job, time.Now(), ExitRecord{RC: 0, End: EndDone}, "synthetic-secret", pgid, stamp)
	var rec ExitRecord
	err := ReadJSON(ExitPath(job), &rec)
	if rc == 0 && err == nil && rec.Attest == "synthetic-secret" && GroupAlive(pgid, stamp) {
		t.Fatal("supervisor published reusable attestation while the worker group remained alive")
	}
}
