package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// `queue audit` is the hand sweep of 2026-09-18 as a verb: twenty-seven open pull requests
// were carrying GitHub's auto-merge, four had already landed themselves on dev, and a
// person took the rest off one at a time. Everything here drives a fake forge.

type fakeAudit struct {
	open     []merge.AutoMergePR
	listErr  error
	failOn   map[int]error
	disabled []int
	repo     string
}

func (f *fakeAudit) AutoMergePRs(ctx context.Context) ([]merge.AutoMergePR, error) {
	return f.open, f.listErr
}

func (f *fakeAudit) DisableAutoMerge(ctx context.Context, pr int) error {
	if err := f.failOn[pr]; err != nil {
		return err
	}
	f.disabled = append(f.disabled, pr)
	return nil
}

func (f *fakeAudit) run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	deps := Deps{NewAuditHost: func(repo string, timeout time.Duration) merge.AuditHost {
		f.repo = repo
		return f
	}}
	exit := run(args, &out, &errb, deps)
	return exit, out.String(), errb.String()
}

// Every auto-merge found is taken off, each one named, and the counts are one line. The
// taking off is --apply's: since dogfood round 5 a bare `queue audit` READS (edge 1).
func TestQueueAuditDisablesEveryAutoMergeAndNamesThem(t *testing.T) {
	f := &fakeAudit{open: []merge.AutoMergePR{
		{Number: 1301, HeadRef: "rowan/impl-a", Title: "a card"},
		{Number: 1307, HeadRef: "rowan/impl-b", Title: "another card"},
	}}
	exit, stdout, stderr := f.run(t, "queue", "audit", "--repo", "mas-bandwidth/nova-tools", "--apply")
	if exit != 0 {
		t.Fatalf("queue audit: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	if len(f.disabled) != 2 {
		t.Fatalf("want both cleared, got %v", f.disabled)
	}
	contains(t, stdout, "QUEUE AUDIT entry=1301 branch=rowan/impl-a")
	contains(t, stdout, "QUEUE AUDIT entry=1307 branch=rowan/impl-b")
	contains(t, stdout, "QUEUE AUDIT repo=mas-bandwidth/nova-tools found=2 disabled=2 failed=0 mode=apply\n")
	if f.repo != "mas-bandwidth/nova-tools" {
		t.Fatalf("the fake forge was handed repo=%q", f.repo)
	}
}

// A clean repository is one line saying so, and nothing else: the verb a person runs every
// session must be readable when there is nothing to do.
func TestQueueAuditOnACleanRepositorySaysSoInOneLine(t *testing.T) {
	f := &fakeAudit{}
	exit, stdout, stderr := f.run(t, "queue", "audit", "--repo", "o/n")
	if exit != 0 {
		t.Fatalf("exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "found=0 disabled=0 failed=0 mode=dry-run\n")
	if lines := strings.Count(stdout, "\n"); lines != 1 {
		t.Fatalf("a clean audit is one line; got %d:\n%s", lines, stdout)
	}
}

// --dry-run lists and writes nothing.
func TestQueueAuditDryRunWritesNothing(t *testing.T) {
	f := &fakeAudit{open: []merge.AutoMergePR{{Number: 1301, HeadRef: "rowan/impl-a"}}}
	exit, stdout, stderr := f.run(t, "queue", "audit", "--repo", "o/n", "--dry-run")
	if exit != 0 {
		t.Fatalf("exit %d\n%s\n%s", exit, stdout, stderr)
	}
	if len(f.disabled) != 0 {
		t.Fatalf("a dry run wrote to the forge: %v", f.disabled)
	}
	contains(t, stdout, "found=1 disabled=0 failed=0 mode=dry-run\n")
}

// One pull request the forge will not clear does not end the pass: the others are cleared,
// the exit code says the work is not finished, and the ones still armed are named.
func TestQueueAuditNamesWhatItCouldNotClear(t *testing.T) {
	f := &fakeAudit{
		open:   []merge.AutoMergePR{{Number: 1}, {Number: 2}, {Number: 3}},
		failOn: map[int]error{2: errors.New("gh: not authorized")},
	}
	exit, stdout, stderr := f.run(t, "queue", "audit", "--repo", "o/n", "--apply")
	if exit != 1 {
		t.Fatalf("exit %d, want 1 (the verb ran and some work did not get done)\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "found=3 disabled=2 failed=1")
	contains(t, stderr, "QUEUE AUDIT REFUSED")
	contains(t, stderr, "2")
}

// The no-guessing law: no repository, no audit.
func TestQueueAuditRefusesWithoutARepository(t *testing.T) {
	f := &fakeAudit{}
	exit, stdout, stderr := f.run(t, "queue", "audit")
	if exit != 2 {
		t.Fatalf("exit %d, want 2\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "--repo")
	if len(f.disabled) != 0 {
		t.Fatalf("the forge was reached: %v", f.disabled)
	}
}

// An audit is NOT a lane verb: it takes no --lane, and it never opens one.
func TestQueueAuditNeedsNoLane(t *testing.T) {
	f := &fakeAudit{}
	if exit, stdout, stderr := f.run(t, "queue", "audit", "--repo", "o/n"); exit != 0 {
		t.Fatalf("an audit asked for a lane: exit %d\n%s\n%s", exit, stdout, stderr)
	}
}
