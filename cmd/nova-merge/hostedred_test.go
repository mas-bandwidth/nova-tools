package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// The coordinator's DECISION (Stella, 5648500966), point 4: rule 15's arm is derived from
// the remote's OWN default branch, not the literal "main", and the derivation is
// conservative -- a failed discovery never downgrades a lane. These drive the ACTUAL run
// path against local bare repositories.

// setDefault points the fixture remote's HEAD at name, the way a repository whose default
// branch is trunk or master answers `git ls-remote --symref`.
func (l *lab) setDefault(name string) {
	l.t.Helper()
	l.git(l.work, "push", "-q", "origin", "HEAD:refs/heads/"+name)
	l.git(l.remote, "symbolic-ref", "HEAD", "refs/heads/"+name)
}

// initArgs is init on the given base with any extra flags (--default-branch, --hosted-red).
func (l *lab) initArgs(base string, extra ...string) {
	l.t.Helper()
	args := []string{"init", "--lane", l.lane, "--repo", "o/n", "--base", base, "--lane-branch", "nova-merge/lane"}
	args = append(args, extra...)
	if exit, _, errb := l.run(args...); exit != 0 {
		l.t.Fatalf("init: exit %d\n%s", exit, errb)
	}
}

// failLSRemote is a runner that answers an error to `git ls-remote` and passes every other
// command through, so a test can make discovery fail while the fold, the fetch and the
// push all still work.
type failLSRemote struct{ inner merge.Runner }

func (r failLSRemote) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	if name == "git" && len(args) > 0 && args[0] == "ls-remote" {
		return "", fmt.Errorf("the remote did not answer")
	}
	return r.inner.Run(ctx, dir, name, args...)
}

// greenGateAndRedChecks drives an entry to a green integration gate and then turns its
// hosted checks red, the shared tail of every policy test: the entry now has the one
// candidate evidence (a green gate) plus a hosted red, and the policy decides the arm.
func (l *lab) greenGateAndRedChecks(n int, oid, base, redName string) {
	l.t.Helper()
	_, stdout, _ := l.run("run", "--lane", l.lane, "--once")
	m := mergeSHAOf(l.t, stdout, itoa(n))
	if exit, _, errb := l.run("gate", "--lane", l.lane, "--pr", itoa(n), "--head", oid,
		"--base-sha", base, "--merge", m, "--verdict", "green", "--summary", l.summary("g")); exit != 0 {
		l.t.Fatalf("gate: %s", errb)
	}
	l.host.SetChecks(oid, 3, 0, redName)
}

// stripUserinfo is the redaction behind the DECISION's "never print a credential-bearing
// remote URL": the token travels in the userinfo and is dropped before anything is printed.
func TestStripUserinfo(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://user:token@host/repo.git", "https://host/repo.git"},
		{"https://host/repo.git", "https://host/repo.git"},
		{"https://user:pass@host:8080/owner/repo.git", "https://host:8080/owner/repo.git"},
		{"/abs/path/remote.git", "/abs/path/remote.git"},
	} {
		if got := stripUserinfo(tc.in); got != tc.want {
			t.Errorf("stripUserinfo(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAHostedRedBlocksOnANonMainDefaultBranch(t *testing.T) {
	for _, def := range []string{"trunk", "master"} {
		t.Run(def, func(t *testing.T) {
			t.Parallel()
			l := newLab(t)
			l.setDefault(def)
			l.initArgs(def)
			oid := l.branch("feature-a", "a.txt", "a\n", "a change")
			l.host.PRs[951] = merge.PR{Number: 951, Author: "pat", Base: def, HeadRef: "feature-a",
				HeadOID: oid, Mergeable: "MERGEABLE", URL: "https://example.invalid/feature-a"}
			l.host.SetChecks(oid, 3, 0)
			base := l.baseSHA()
			l.host.SetChecks(base, 3, 0)
			if exit, _, errb := l.run("add", "--lane", l.lane, "--pr", "951"); exit != 0 {
				t.Fatalf("add: %s", errb)
			}
			l.greenGateAndRedChecks(951, oid, base, "tables-java-versioning")
			exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once")
			if exit != 1 {
				t.Fatalf("a hosted red on the default branch %s blocks: exit %d\n%s\n%s", def, exit, stdout, stderr)
			}
			contains(t, stdout, "hosted_red_policy=blocks")
			contains(t, stdout, "state=RED")
			absent(t, stdout, "MERGE OK")
		})
	}
}

func TestAStackedBaseNamesTheHostedRed(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.git(l.work, "checkout", "-q", "-B", "rowan/step-2", "origin/main")
	l.git(l.work, "push", "-q", "origin", "HEAD:refs/heads/rowan/step-2")
	l.git(l.work, "checkout", "-q", "main")
	l.initArgs("rowan/step-2")
	oid := l.branch("feature-b", "b.txt", "b\n", "b")
	l.host.PRs[942] = merge.PR{Number: 942, Author: "pat", Base: "rowan/step-2", HeadRef: "feature-b",
		HeadOID: oid, Mergeable: "MERGEABLE"}
	l.host.SetChecks(oid, 2, 0, "tables-java-fixedform")
	base := l.git(l.work, "rev-parse", "refs/remotes/origin/rowan/step-2")
	l.host.SetChecks(base, 0, 0)
	if exit, _, errb := l.run("add", "--lane", l.lane, "--pr", "942"); exit != 0 {
		t.Fatalf("add: %s", errb)
	}
	if exit, _, errb := l.run("gate", "--lane", l.lane, "--branch", "rowan/step-2", "--head", base,
		"--base-sha", base, "--merge", base, "--verdict", "green", "--summary", l.summary("base")); exit != 0 {
		t.Fatalf("base gate: %s", errb)
	}
	exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once")
	if exit != 0 {
		t.Fatalf("exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "hosted_red_policy=names")
	contains(t, stdout, "state=NEEDS-GATE")
	m := mergeSHAOf(t, stdout, "942")
	if exit, _, errb := l.run("gate", "--lane", l.lane, "--pr", "942", "--head", oid,
		"--base-sha", base, "--merge", m, "--verdict", "green", "--summary", l.summary("g942")); exit != 0 {
		t.Fatalf("gate: %s", errb)
	}
	exit, stdout, stderr = l.run("run", "--lane", l.lane, "--once")
	if exit != 0 {
		t.Fatalf("below main a hosted red is printed by name, never blocking: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "MERGE OK entry=942")
	contains(t, stdout, "admitted=gate")
	contains(t, stdout, "hosted_red=tables-java-fixedform")
	contains(t, stdout, "hosted_red_policy=names")
}

func TestExplicitHostedRedIsTheArmItStates(t *testing.T) {
	t.Run("names on the default base", func(t *testing.T) {
		t.Parallel()
		l := newLab(t)
		l.initArgs("main", "--hosted-red", "names")
		oid := l.branch("feature-a", "a.txt", "a\n", "a change")
		l.host.PRs[951] = merge.PR{Number: 951, Author: "pat", Base: "main", HeadRef: "feature-a",
			HeadOID: oid, Mergeable: "MERGEABLE", URL: "https://example.invalid/feature-a"}
		l.host.SetChecks(oid, 3, 0)
		base := l.baseSHA()
		l.host.SetChecks(base, 3, 0)
		if exit, _, errb := l.run("add", "--lane", l.lane, "--pr", "951"); exit != 0 {
			t.Fatalf("add: %s", errb)
		}
		l.greenGateAndRedChecks(951, oid, base, "tables-java-versioning")
		exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once")
		if exit != 0 {
			t.Fatalf("an explicit names is the team's choice: a hosted red is named, not blocking: exit %d\n%s\n%s", exit, stdout, stderr)
		}
		contains(t, stdout, "hosted_red_policy=names")
		contains(t, stdout, "MERGE OK entry=951")
		contains(t, stdout, "hosted_red=tables-java-versioning")
	})

	t.Run("blocks on a stacked base", func(t *testing.T) {
		t.Parallel()
		l := newLab(t)
		l.git(l.work, "checkout", "-q", "-B", "rowan/step-2", "origin/main")
		l.git(l.work, "push", "-q", "origin", "HEAD:refs/heads/rowan/step-2")
		l.git(l.work, "checkout", "-q", "main")
		l.initArgs("rowan/step-2", "--hosted-red", "blocks")
		oid := l.branch("feature-b", "b.txt", "b\n", "b")
		l.host.PRs[942] = merge.PR{Number: 942, Author: "pat", Base: "rowan/step-2", HeadRef: "feature-b",
			HeadOID: oid, Mergeable: "MERGEABLE"}
		l.host.SetChecks(oid, 2, 0)
		base := l.git(l.work, "rev-parse", "refs/remotes/origin/rowan/step-2")
		l.host.SetChecks(base, 0, 0)
		if exit, _, errb := l.run("add", "--lane", l.lane, "--pr", "942"); exit != 0 {
			t.Fatalf("add: %s", errb)
		}
		l.greenGateAndRedChecks(942, oid, base, "tables-java-fixedform")
		exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once")
		if exit != 1 {
			t.Fatalf("an explicit blocks stops a hosted red even below main: exit %d\n%s\n%s", exit, stdout, stderr)
		}
		contains(t, stdout, "hosted_red_policy=blocks")
		contains(t, stdout, "state=RED")
		absent(t, stdout, "MERGE OK")
	})
}

func TestAnInvalidHostedRedIsRefusedAtLoad(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	bogus := []byte(`{"version":1,"repo":"o/n","base":"main","lane_branch":"nova-merge/lane","hosted_red":"bogus","prs":[],"branches":[],"gates":[]}`)
	if err := os.WriteFile(filepath.Join(l.lane, merge.StateName), bogus, 0o644); err != nil {
		t.Fatal(err)
	}
	exit, stdout, stderr := l.run("status", "--lane", l.lane)
	if exit != 2 {
		t.Fatalf("an unknown hosted_red is refused at load: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "hosted_red")
	contains(t, stderr, "bogus")
}

func TestADefaultBranchRenameKeepsBlocks(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	// trunk already exists at main's commit, but the default is still main: init records
	// "main" from the remote's HEAD, and the lane's base is trunk.
	l.git(l.work, "push", "-q", "origin", "HEAD:refs/heads/trunk")
	l.initArgs("trunk")
	// The team renames the default branch main -> trunk.
	l.git(l.remote, "symbolic-ref", "HEAD", "refs/heads/trunk")
	oid := l.branch("feature-a", "a.txt", "a\n", "a change")
	l.host.PRs[951] = merge.PR{Number: 951, Author: "pat", Base: "trunk", HeadRef: "feature-a",
		HeadOID: oid, Mergeable: "MERGEABLE", URL: "https://example.invalid/feature-a"}
	l.host.SetChecks(oid, 3, 0)
	base := l.baseSHA()
	l.host.SetChecks(base, 3, 0)
	if exit, _, errb := l.run("add", "--lane", l.lane, "--pr", "951"); exit != 0 {
		t.Fatalf("add: %s", errb)
	}
	l.greenGateAndRedChecks(951, oid, base, "tables-java-versioning")
	exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once")
	if exit != 1 {
		t.Fatalf("a rename keeps blocks: the freshly discovered default trunk matches the base: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "hosted_red_policy=blocks")
	contains(t, stdout, "state=RED")
}

func TestAFailedDiscoveryAfterASuccessfulOneStillBlocks(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	// base is trunk and the recorded fact is the stale name "main" (seeded with
	// --default-branch, which skips the initial lookup and cannot disable later discovery).
	l.git(l.work, "push", "-q", "origin", "HEAD:refs/heads/trunk")
	l.git(l.remote, "symbolic-ref", "HEAD", "refs/heads/trunk")
	l.initArgs("trunk", "--default-branch", "main")
	oid := l.branch("feature-a", "a.txt", "a\n", "a change")
	l.host.PRs[951] = merge.PR{Number: 951, Author: "pat", Base: "trunk", HeadRef: "feature-a",
		HeadOID: oid, Mergeable: "MERGEABLE", URL: "https://example.invalid/feature-a"}
	l.host.SetChecks(oid, 3, 0)
	base := l.baseSHA()
	l.host.SetChecks(base, 3, 0)
	if exit, _, errb := l.run("add", "--lane", l.lane, "--pr", "951"); exit != 0 {
		t.Fatalf("add: %s", errb)
	}
	l.greenGateAndRedChecks(951, oid, base, "tables-java-versioning")
	// A successful discovery (trunk) blocks.
	exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once")
	if exit != 1 {
		t.Fatalf("a successful discovery of trunk blocks: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "hosted_red_policy=blocks")
	// A failed discovery, with only the stale recorded name "main", still blocks: the hole.
	l.runner = failLSRemote{inner: merge.Exec{}}
	exit, stdout, stderr = l.run("run", "--lane", l.lane, "--once")
	if exit != 1 {
		t.Fatalf("a failed discovery still blocks even with a stale recorded name: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "hosted_red_policy=blocks")
	contains(t, stdout, "state=RED")
	absent(t, stdout, "MERGE OK")
}
