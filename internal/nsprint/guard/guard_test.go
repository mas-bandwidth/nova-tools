package guard_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/guard"
)

type fakeProcTable struct {
	procs []guard.Proc
	terms []int
}

func (f *fakeProcTable) List() ([]guard.Proc, error) {
	return f.procs, nil
}

func (f *fakeProcTable) Alive(pid int) bool { return true }
func (f *fakeProcTable) Kill(pid int) error  { return nil }
func (f *fakeProcTable) Term(pid int) error {
	f.terms = append(f.terms, pid)
	return nil
}

type fakeRedisClient struct {
	roundTrips int
	config     guard.Config
	kills      []guard.KillRecord
	last       guard.LastRecord
}

func (r *fakeRedisClient) GetConfig(bench string) (guard.Config, error) {
	r.roundTrips++
	if r.config.MaxAge == 0 {
		return guard.Default(), nil
	}
	return r.config, nil
}

func (r *fakeRedisClient) SetConfig(bench string, cfg guard.Config) error {
	r.config = cfg
	return nil
}

func (r *fakeRedisClient) ExecutePass(bench string, kills []guard.KillRecord, last guard.LastRecord) error {
	r.roundTrips++
	r.kills = kills
	r.last = last
	return nil
}

func TestGuardPassKillsOldSearchesSparesPipelines(t *testing.T) {
	procs := []guard.Proc{
		{PID: 101, Age: 301 * time.Second, Comm: "rg", Args: "rg foo"},
		{PID: 102, Age: 299 * time.Second, Comm: "find", Args: "find . -name foo"},
		{PID: 103, Age: 900 * time.Second, Comm: "grep", Args: "grep bar harvest"},
		{PID: 104, Age: 400 * time.Second, Comm: "ugrep", Args: "ugrep baz"},
		{PID: 105, Age: 900 * time.Second, Comm: "rgx", Args: "rgx test"},
	}

	fakePT := &fakeProcTable{procs: procs}
	fakeRC := &fakeRedisClient{}

	var stdout, stderr bytes.Buffer
	in := guard.GuardInput{
		Bench:  "t",
		Procs:  fakePT,
		Redis:  fakeRC,
		Stdout: &stdout,
		Stderr: &stderr,
	}

	code := guard.Pass(in)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr: %s)", code, stderr.String())
	}

	// Verify SIGTERM sent to exactly rg (101) and ugrep (104)
	if len(fakePT.terms) != 2 || fakePT.terms[0] != 101 || fakePT.terms[1] != 104 {
		t.Fatalf("expected terms [101, 104], got %v", fakePT.terms)
	}

	// Verify kills entries count
	if len(fakeRC.kills) != 2 {
		t.Fatalf("expected 2 kill records, got %d", len(fakeRC.kills))
	}

	// Verify last record: killed=2, scanned=5
	if fakeRC.last.Killed != 2 || fakeRC.last.Scanned != 5 {
		t.Fatalf("expected killed=2, scanned=5, got killed=%d, scanned=%d", fakeRC.last.Killed, fakeRC.last.Scanned)
	}

	// Verify exactly two Redis round trips
	if fakeRC.roundTrips != 2 {
		t.Fatalf("expected exactly 2 Redis round trips, got %d", fakeRC.roundTrips)
	}
}

func TestGuardCommMismatchSpared(t *testing.T) {
	// Stella's spec requirement: argv[0]=rg but comm=sleep receives no signal
	procs := []guard.Proc{
		{PID: 201, Age: 900 * time.Second, Comm: "sleep", Args: "rg sleep 600"},
	}

	fakePT := &fakeProcTable{procs: procs}
	fakeRC := &fakeRedisClient{}

	var stdout, stderr bytes.Buffer
	in := guard.GuardInput{
		Bench:  "t",
		Procs:  fakePT,
		Redis:  fakeRC,
		Stdout: &stdout,
		Stderr: &stderr,
	}

	code := guard.Pass(in)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	if len(fakePT.terms) != 0 {
		t.Fatalf("expected 0 terms due to comm mismatch, got %v", fakePT.terms)
	}
	if fakeRC.last.Killed != 0 || fakeRC.last.Scanned != 1 {
		t.Fatalf("expected killed=0, scanned=1, got killed=%d, scanned=%d", fakeRC.last.Killed, fakeRC.last.Scanned)
	}
}
