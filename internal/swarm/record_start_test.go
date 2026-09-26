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

// Finding 5 of Stella's audit (stella-e6353bf80360): the supervisor's writes
// after the harness has started, and its abort before, name what they could
// not write. These are targeted failure injections at the write sites.

func recordStartPool(t *testing.T, nonce string) (*Pool, string) {
	t.Helper()
	p, err := OpenPool(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	jobDir := filepath.Join(t.TempDir(), "job")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := p.Reserve(1, "audit-card", jobDir, nonce, os.Getpid(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := p.Identify(1, nonce, SlotFile{State: SlotLaunched, Pid: os.Getpid(), LaunchedAt: Stamp(time.Now())}); err != nil {
		t.Fatal(err)
	}
	return p, jobDir
}

// TestRecordStartNamesAnUnwrittenPidRecord: a pid record that cannot be
// written after the start (a directory sits at its path) is a WARN line
// naming the path, the cause, that the harness runs under its group, and
// where to look; the slot file still takes the group.
func TestRecordStartNamesAnUnwrittenPidRecord(t *testing.T) {
	t.Parallel()
	p, jobDir := recordStartPool(t, "nonce-a")
	if err := os.Mkdir(PidPath(jobDir), 0o755); err != nil {
		t.Fatal(err)
	}
	var errOut bytes.Buffer
	recordStart(SuperviseInput{Pool: p, Task: "audit-card", Slot: 1, Nonce: "nonce-a", Stderr: &errOut}, jobDir, os.Getpid(), "-", 4242, "started", time.Now())
	for _, want := range []string{"SUPERVISE WARN slot=1 id=audit-card: the pid record " + PidPath(jobDir) + " could not take the job's group", "the harness runs (job pgid 4242)", "inspect:"} {
		if !strings.Contains(errOut.String(), want) {
			t.Fatalf("stderr %q lacks %q", errOut.String(), want)
		}
	}
	if sf, err := p.ReadSlot(1); err != nil || sf.JobPgid != 4242 || sf.JobStarted != "started" {
		t.Fatalf("slot after the pid write failed: %+v %v", sf, err)
	}
}

// TestRecordStartNamesASlotThatIsNoLongerThisLaunchs: a slot file another
// writer took between identify and the post-start update is a WARN line
// naming the slot file and the cause; the pid record still carries the group.
func TestRecordStartNamesASlotThatIsNoLongerThisLaunchs(t *testing.T) {
	t.Parallel()
	p, jobDir := recordStartPool(t, "nonce-a")
	if err := writeSlot(p.slotPath(1), SlotFile{Job: "audit-card", JobDir: jobDir, State: SlotReserved, Nonce: "nonce-b"}); err != nil {
		t.Fatal(err)
	}
	var errOut bytes.Buffer
	recordStart(SuperviseInput{Pool: p, Task: "audit-card", Slot: 1, Nonce: "nonce-a", Stderr: &errOut}, jobDir, os.Getpid(), "-", 4242, "started", time.Now())
	for _, want := range []string{"SUPERVISE WARN slot=1 id=audit-card: the slot file " + p.slotPath(1) + " could not take the job's group", "slot 1 is no longer this launch's", "the harness runs (job pgid 4242)", "do not free the slot while pgid 4242 is alive"} {
		if !strings.Contains(errOut.String(), want) {
			t.Fatalf("stderr %q lacks %q", errOut.String(), want)
		}
	}
	var rec PidRecord
	if err := ReadJSON(PidPath(jobDir), &rec); err != nil || rec.JobPgid != 4242 {
		t.Fatalf("pid record after the slot update failed: %+v %v", rec, err)
	}
	if strings.Contains(errOut.String(), "pid record") {
		t.Fatalf("the pid write was named as failed: %q", errOut.String())
	}
}

// TestRecordStartIsSilentWhenBothWritesLand: no WARN when both records took
// the group.
func TestRecordStartIsSilentWhenBothWritesLand(t *testing.T) {
	t.Parallel()
	p, jobDir := recordStartPool(t, "nonce-a")
	var errOut bytes.Buffer
	recordStart(SuperviseInput{Pool: p, Task: "audit-card", Slot: 1, Nonce: "nonce-a", Stderr: &errOut}, jobDir, os.Getpid(), "-", 4242, "started", time.Now())
	if errOut.Len() != 0 {
		t.Fatalf("stderr %q", errOut.String())
	}
}

// TestAbortKeepsTheCauseAndNamesTheEvidence: the abort line carries the
// cause it was handed and the aborted.json it wrote; the record holds the
// cause too. With no write failure the line says nothing of one.
func TestAbortKeepsTheCauseAndNamesTheEvidence(t *testing.T) {
	t.Parallel()
	jobDir := filepath.Join(t.TempDir(), "job")
	var errOut bytes.Buffer
	code := abort(SuperviseInput{Task: "audit-card", Slot: 1, Nonce: "fixture", Stderr: &errOut, Now: time.Now}, jobDir, errors.New("reservation changed: slot 1 reads state=orphaned nonce=other"))
	line := errOut.String()
	for _, want := range []string{"SUPERVISE ABORTED slot=1 id=audit-card: reservation changed: slot 1 reads state=orphaned nonce=other", "no harness was started", "evidence=" + AbortedPath(jobDir)} {
		if code != 2 || !strings.Contains(line, want) {
			t.Fatalf("exit=%d stderr %q lacks %q", code, line, want)
		}
	}
	if strings.Contains(line, "NOT WRITTEN") {
		t.Fatalf("a written acknowledgement reported as unwritten: %q", line)
	}
	var ab AbortedRecord
	if err := ReadJSON(AbortedPath(jobDir), &ab); err != nil || ab.Nonce != "fixture" || !strings.HasPrefix(ab.Reason, "reservation changed") {
		t.Fatalf("aborted.json %+v %v", ab, err)
	}
}

// TestAbortNamesTheUnwrittenEvidence: when aborted.json cannot be written
// (a file sits where the job directory would be) the one line keeps the
// cause, names the evidence path, says NOT WRITTEN with the write's cause,
// and tells recovery not to infer the acknowledgement; forcing the write
// error to nil turns this red.
func TestAbortNamesTheUnwrittenEvidence(t *testing.T) {
	t.Parallel()
	job := filepath.Join(t.TempDir(), "job")
	if err := os.WriteFile(job, []byte("a file where the job directory would be"), 0o600); err != nil {
		t.Fatal(err)
	}
	var errOut bytes.Buffer
	code := abort(SuperviseInput{Task: "audit-card", Slot: 1, Nonce: "fixture", Stderr: &errOut, Now: time.Now}, job, errors.New("reservation changed: slot 1 reads state=free nonce=other"))
	line := errOut.String()
	for _, want := range []string{"SUPERVISE ABORTED slot=1 id=audit-card: reservation changed: slot 1 reads state=free nonce=other", "no harness was started", "evidence=" + AbortedPath(job) + " NOT WRITTEN: the job directory could not be made: not a directory", "the durable acknowledgement does not exist, so recovery must not infer it", "rule 17 decides slot 1 from the slot file alone", "inspect: " + job} {
		if code != 2 || !strings.Contains(line, want) {
			t.Fatalf("exit=%d stderr %q lacks %q", code, line, want)
		}
	}
	if _, err := os.Stat(AbortedPath(job)); err == nil {
		t.Fatal("aborted.json exists")
	}
}
