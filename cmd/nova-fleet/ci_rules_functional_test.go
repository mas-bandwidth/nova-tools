//go:build functional

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The branches a push may run CI on come from push-branches beside the hooks, which apply writes.

func TestCIRulesPushBranchesComeFromTheHookDirectory(t *testing.T) {
	t.Parallel()
	for _, sh := range ciShells(t) {
		dir := ciCopy(t, "")
		if err := os.WriteFile(filepath.Join(dir, "push-branches"), []byte("main release/* hotfix\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for ref, want := range map[string]int{
			"refs/heads/main":         0,
			"refs/heads/release/v2":   0,
			"refs/heads/hotfix":       0,
			"refs/tags/v1.2.3":        0,
			"refs/heads/sprint/v2":    1,
			"refs/heads/release/v2/x": 1,
			"refs/heads/release/":     1,
			"refs/heads/hotfix-2":     1,
		} {
			if got, _ := ciRun(t, sh, dir, "", nil, "push-ok", ref); got != want {
				t.Errorf("%s push-ok %s with push-branches = %d, want %d", sh.name, ref, got, want)
			}
		}
		code, out := ciRun(t, sh, dir, "", []string{"GITHUB_EVENT_NAME=push", "GITHUB_REF=refs/heads/sprint/v2", "NOVA_CI_CHAIN_JOB_STARTED="}, "started")
		if code == 0 || !strings.Contains(out, "push CI runs only on main, release/*, hotfix and tags, not refs/heads/sprint/v2") ||
			!strings.Contains(out, "on.push.branches to ['main', 'release/*', 'hotfix']") {
			t.Errorf("%s started on a push outside push-branches = %d %q, want a refusal naming the configured branches", sh.name, code, out)
		}
	}
}

func TestCIRulesApplyWritesThePushBranches(t *testing.T) {
	t.Parallel()
	hooks := ciCopy(t, "")
	runner := t.TempDir()
	sh := ciShells(t)[0]
	code, out := ciRun(t, sh, hooks, "", nil, "apply", "90", "main release/*", runner)
	if code != 0 || !strings.Contains(out, "hooks=set") {
		t.Fatalf("apply = %d %q, want hooks=set", code, out)
	}
	if b, err := os.ReadFile(filepath.Join(runner, ".nova-ci-rules", "push-branches")); err != nil || string(b) != "main release/*\n" {
		t.Errorf("push-branches = %q, %v", b, err)
	}
	if code, out = ciRun(t, sh, hooks, "", nil, "apply", "90", "main release/*", runner); code != 0 || !strings.Contains(out, "hooks=ok") {
		t.Errorf("apply again = %d %q, want hooks=ok", code, out)
	}
	if code, _ = ciRun(t, sh, hooks, "", nil, "apply", "90"); code != 2 {
		t.Errorf("apply without BRANCHES = %d, want 2", code)
	}
}

// apply writes nothing through a link in the runner's directory (a runner login could plant one for root's apply to
// follow): a .nova-ci-rules link is replaced by a directory, a .env link by a file, and what they named is untouched.
func TestCIRulesApplyFollowsNoLinkInTheRunnerDirectory(t *testing.T) {
	t.Parallel()
	hooks := ciCopy(t, "")
	runner := t.TempDir()
	elsewhere := t.TempDir()
	victim := filepath.Join(elsewhere, "victim")
	if err := os.WriteFile(victim, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(runner, ".nova-ci-rules")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(runner, ".env")); err != nil {
		t.Fatal(err)
	}
	sh := ciShells(t)[0]
	if code, out := ciRun(t, sh, hooks, "", nil, "apply", "90", "main", runner); code != 0 || !strings.Contains(out, "hooks=set") {
		t.Fatalf("apply = %d %q, want hooks=set", code, out)
	}
	for _, name := range []string{".nova-ci-rules", ".env"} {
		if fi, err := os.Lstat(filepath.Join(runner, name)); err != nil || fi.Mode()&os.ModeSymlink != 0 {
			t.Errorf("%s after apply: %v, %v; want no link", name, fi, err)
		}
	}
	if b, err := os.ReadFile(victim); err != nil || string(b) != "keep\n" {
		t.Errorf("the .env link's target = %q, %v; want it untouched", b, err)
	}
	if entries, err := os.ReadDir(elsewhere); err != nil || len(entries) != 1 {
		t.Errorf("the .nova-ci-rules link's target holds %d entries, %v; want only its own file", len(entries), err)
	}
}
