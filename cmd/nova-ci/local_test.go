package main

import (
	"bytes"
	"strings"
	"testing"
)

// local_test.go: the argv contract of `nova-ci local` (nova-tools#4293).
// Nothing here runs go or changes this process's priority: every case below
// refuses before the yield, which is the verb's first act after parsing.

func TestLocalRefusesWithoutPackages(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := cmdLocal(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "nova-ci local [-p N] <pkg>...") || !strings.Contains(stderr.String(), "run: nova-ci help") {
		t.Fatalf("stdout %q stderr %q", stdout.String(), stderr.String())
	}
}

func TestLocalRefusesTheWholeTree(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := cmdLocal([]string{"./..."}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "never the whole tree") {
		t.Fatalf("stderr %q", stderr.String())
	}
}

func TestLocalRefusesABadP(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"-p", "0", "./internal/yield"}, {"-p", "two", "./internal/yield"}, {"-x", "./internal/yield"}} {
		var stdout, stderr bytes.Buffer
		if code := cmdLocal(args, &stdout, &stderr); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
		if n := strings.Count(stderr.String(), "\n"); n != 1 {
			t.Errorf("%v: stderr %q, want one line", args, stderr.String())
		}
	}
}

// The verb is on the door.
func TestLocalIsInTheBanner(t *testing.T) {
	t.Parallel()
	_, stdout, _ := runCI(t, []string{"help"}, "")
	if !strings.Contains(stdout, "nova-ci local [-p N] <pkg>...") {
		t.Fatalf("help does not name local:\n%s", stdout)
	}
}
