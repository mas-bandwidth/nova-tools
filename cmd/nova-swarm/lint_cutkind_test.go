package main

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// The #1852 reproducer end to end: a card `nova-pulse cut --kind fix` writes must
// be `nova-swarm lint --card` clean, not eight drifts and exit 2.
func TestCutKindFixCardLintsClean(t *testing.T) {
	dir := t.TempDir()
	out, queue := filepath.Join(dir, "out-kind"), filepath.Join(dir, "queue-kind")
	var stdout, stderr bytes.Buffer
	code := pulse.CutKind(pulse.CutKindInput{
		Kind: "fix", Repo: "mas-bandwidth/nova-tools", Issue: 123, Title: "fix",
		Out: out, Queue: queue, Stdout: &stdout, Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("cut --kind exit = %d, stderr=%s", code, stderr.String())
	}
	exit, outstr, errstr := runSwarm(t, "lint", "--card", filepath.Join(out, "card-1.md"))
	if exit != 0 {
		t.Fatalf("nova-swarm lint --card on a cut --kind fix card: exit %d\nstdout: %s\nstderr: %s", exit, outstr, errstr)
	}
}
