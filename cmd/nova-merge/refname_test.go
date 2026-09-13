package main

import (
	"strings"
	"testing"
)

// security#30 finding 5, the typed half: `add-branch --branch` is only REQUIRED, never
// checked by ValidRefName, though init's --base and --lane-branch beside it are. The
// branch an add stores is handed to git on every pass afterwards -- `fetch origin
// <branch>` -- so a branch named `--upload-pack=<cmd>` is a flag to git, exactly as
// lesson 48 says. The value is never executed here: add is refused before the lane is
// opened, so nothing reaches a pass at all.
func TestAddBranchChecksTheBranchNameWhereItIsTyped(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	for _, name := range []string{"--upload-pack=touch /tmp/nova-merge-finding-5", "-x", "a branch", "a/../b"} {
		exit, stdout, stderr := l.run("add-branch", "--lane", l.lane, "--branch", name)
		if exit != 2 {
			t.Errorf("add-branch --branch %q: exit %d, want 2; stdout %q stderr %q", name, exit, stdout, stderr)
		}
		if !strings.HasPrefix(stderr, "nova-merge add-branch: ") {
			t.Errorf("add-branch --branch %q: the refusal must ride the existing grammar, got %q", name, stderr)
		}
		if strings.Contains(stdout, "ADD OK") {
			t.Errorf("add-branch --branch %q was QUEUED: %q", name, stdout)
		}
	}
}
