package swarm

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Cold-read probes for #4430 (who=rowan-opus): abort with an unmakeable job
// dir and with a read-only one; recordStart with an unwritable pid path.
func TestRead4430AbortNamesCauseAndNotWritten(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	file := filepath.Join(base, "job-file")
	os.WriteFile(file, []byte("x"), 0o600)
	ro := filepath.Join(base, "job-ro")
	os.Mkdir(ro, 0o555)
	t.Cleanup(func() { os.Chmod(ro, 0o755) })
	for name, job := range map[string]string{"mkdir": file, "write": ro} {
		var out, errOut bytes.Buffer
		code := abort(SuperviseInput{Task: "audit-card", Slot: 3, Nonce: "n", Stdout: &out, Stderr: &errOut, Now: time.Now}, job, errors.New("reservation changed: slot 3 reads state=free nonce=m"))
		s := errOut.String()
		for _, want := range []string{"SUPERVISE ABORTED slot=3 id=audit-card: reservation changed: slot 3 reads state=free nonce=m", "evidence=" + AbortedPath(job), "NOT WRITTEN: ", "rule 17 decides slot 3"} {
			if code != 2 || !strings.Contains(s, want) {
				t.Errorf("%s: exit=%d stderr=%q lacks %q", name, code, s, want)
			}
		}
		if strings.Count(s, "\n") != 1 {
			t.Errorf("%s: not one line: %q", name, s)
		}
	}
}

func TestRead4430RecordStartWarnsOnUnwritablePid(t *testing.T) {
	t.Parallel()
	job := filepath.Join(t.TempDir(), "job")
	os.MkdirAll(job, 0o755)
	os.Chmod(job, 0o555)
	t.Cleanup(func() { os.Chmod(job, 0o755) })
	var out, errOut bytes.Buffer
	recordStart(SuperviseInput{Task: "audit-card", Slot: 2, Stdout: &out, Stderr: &errOut, Now: time.Now}, job, os.Getpid(), "st", 777, "js", time.Now())
	s := errOut.String()
	for _, want := range []string{"SUPERVISE WARN slot=2 id=audit-card", PidPath(job), "permission denied", "job pgid 777"} {
		if !strings.Contains(s, want) {
			t.Errorf("stderr %q lacks %q", s, want)
		}
	}
}
