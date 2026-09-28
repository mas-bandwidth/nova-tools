package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// EVERY REFUSAL SAYS WHAT THE INPUT WANTS, AND ONE RUN NAMES EVERY INDEPENDENT PROBLEM
// (ONBOARDING point 2). A refusal naming only the fault has moved the guessing onto the
// reader, and sending a first run back three times for three independent flags is three
// refusals the first one already knew about.

// `supervise` is run's child and nobody's verb.
func TestSuperviseTypedByHandIsRefused(t *testing.T) {
	t.Parallel()

	pool := filepath.Join(t.TempDir(), "pool")
	runSwarm(t, "quickstart", "--pool", pool)
	exit, _, stderr := runSwarm(t, "supervise", "--pool", pool, "--task", "whatever", "--slot", "1", "--nonce", "abc123abc123", "--worker", "w.json")
	if exit != 2 {
		t.Errorf("a hand-typed supervise exits %d, want 2", exit)
	}
	if !strings.Contains(stderr, "run: nova-swarm help") {
		t.Errorf("the refusal names no door:\n%s", stderr)
	}
}

// An unknown verb and a flag typo cost ONE line each, never the banner.
func TestAnUnusableInvocationCostsOneLine(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"tirage"}, {"status", "--pooll", "x"}} {
		exit, stdout, stderr := runSwarm(t, args...)
		if exit != 2 {
			t.Errorf("`%s` exits %d, want 2", strings.Join(args, " "), exit)
		}
		if stdout != "" {
			t.Errorf("`%s` wrote to stdout: %q", strings.Join(args, " "), stdout)
		}
		if lines := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n"); len(lines) != 1 {
			t.Errorf("`%s` printed %d lines, want 1:\n%s", strings.Join(args, " "), len(lines), stderr)
		}
	}
}

// An empty task directory is a BATCH REFUSED at exit 1, and one unreadable file queues
// nothing at all: a batch is all of its tasks or none.
func TestABatchIsAllOfItsTasksOrNone(t *testing.T) {
	t.Parallel()

	b := newBench(t)
	empty := filepath.Join(b.dir, "empty")
	write(b.t, filepath.Join(empty, "keep"), "")
	if err := removeFile(filepath.Join(empty, "keep")); err != nil {
		t.Fatal(err)
	}
	exit, _, stderr := b.swarm("batch", "--pool", b.pool, "--tasks", empty, "--files", "3", "--tokens", "1000")
	if exit != 1 {
		t.Errorf("a batch over a directory with no task file exits %d, want 1; stderr: %s", exit, stderr)
	}
	if !strings.Contains(stderr, "BATCH REFUSED") {
		t.Errorf("stderr wants a BATCH REFUSED line:\n%s", stderr)
	}
}

func removeFile(path string) error { return os.Remove(path) }
