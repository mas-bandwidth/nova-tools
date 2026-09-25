package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
)

// TestExpireSSHProberThroughBenchsh (#3350): the shipped prober runs
// probeScript through internal/benchsh with the batch as the script's stdin.
// The fake ssh plays the bench locally (its login shell is `bash -c` on the
// remote command word), and the bench answers each batch line: a job dir and
// a pushed branch are evidence, an empty bench is proven absence.
func TestExpireSSHProberThroughBenchsh(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	dir := t.TempDir()
	ssh := filepath.Join(dir, "ssh")
	fake := "#!/bin/bash\nwhile [ \"$1\" = \"-o\" ]; do shift 2; done\nshift\nexec bash -c \"$1\"\n"
	if err := os.WriteFile(ssh, []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	work := filepath.Join(dir, "work")
	bare := filepath.Join(dir, "remote.git")
	git("init", "-q", work)
	git("-C", work, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "card")
	sha := git("-C", work, "rev-parse", "HEAD")
	git("init", "-q", "--bare", bare)
	git("-C", work, "push", "-q", bare, "HEAD:refs/heads/rowan/card-1")
	job := filepath.Join(dir, "job-1")
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	cards := []reconcile.Suspect{
		{Sprint: "S", Label: "l1", JobDir: job, Identity: "nova-3350-no-such-process-a", Repo: "o/r", Branch: "rowan/card-1"},
		{Sprint: "S", Label: "l2", JobDir: filepath.Join(dir, "gone"), Identity: "nova-3350-no-such-process-b", Repo: "o/r", Branch: "rowan/card-2"},
	}
	p := sshProber{Program: ssh, Remote: func(string) string { return bare }}
	ev, err := p.Probe(context.Background(), deal.Bench{Name: "b", Host: "bench.example"}, cards)
	if err != nil {
		t.Fatal(err)
	}
	if e := ev["S/l1"]; e.PushedSHA != sha || e.Branch != "rowan/card-1" {
		t.Fatalf("S/l1 evidence %+v, want the pushed branch at %s", e, sha)
	}
	if e := ev["S/l2"]; !e.Absent || e.Effect() {
		t.Fatalf("S/l2 evidence %+v, want proven absence", e)
	}
}
