package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestIssue2378(t *testing.T) {
	t.Parallel()
	bin := buildNovaSecrets(t)

	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v, out: %s", err, out)
	}

	stdout, stderr, code := runNovaSecrets(bin, "gate", "--store", dir, "--base", "HEAD", "--head", "HEAD")

	if code != 2 {
		t.Fatalf("unborn store: expected exit code 2 (REFUSE), got %d (stdout=%q, stderr=%q)", code, stdout, stderr)
	}

	if stdout != "" {
		t.Fatalf("unborn store: gate wrote to stdout: %q", stdout)
	}

	if !strings.Contains(stderr, "GATE REFUSE") {
		t.Fatalf("unborn store: gate stderr does not contain GATE REFUSE: %q", stderr)
	}
}
