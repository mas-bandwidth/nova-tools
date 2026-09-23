package main

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// #1661, SPEC-TOOLWORK §6 rule 7: a swarm's member carries its gate's line and passes
// hygiene again, and a member that breaks the merged build is named by bisecting once.
// Every test here drives cmdBatch against the same real-git fixture as batch_test.go.

// addBatchPR adds pull request n to batchRepo's fixture: one commit on top of dev
// writing files, pushed to refs/pull/<n>/head, green on ci-ok, with the given body.
func addBatchPR(t *testing.T, l *lab, n int, files map[string]string, body string) string {
	t.Helper()
	l.git(l.work, "checkout", "-q", "-B", "pr"+strconv.Itoa(n), "origin/dev")
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		l.write(name, files[name])
	}
	sha := l.commit("pr " + strconv.Itoa(n))
	l.git(l.work, "push", "-q", "origin", sha+":refs/pull/"+strconv.Itoa(n)+"/head")
	l.git(l.work, "checkout", "-q", "main")
	l.heads[n] = sha
	l.host.PRs[n] = merge.PR{Number: n, HeadOID: sha, Base: "dev", Body: body}
	l.host.SetCheckRuns(sha, merge.CheckDetail{Name: "ci-ok", Conclusion: "success", SHA: sha})
	return sha
}

// swarm-member-without-accept-ok-is-dropped
//
// Four swarm members (their body's first line is the harvest's RESULT or the gate's
// ACCEPT line) and one friend's member. With --accept-control, only the swarm member
// whose ACCEPT OK names its CURRENT head with a control ON FILE is admitted; the one
// with no ACCEPT line, the one whose head= is another commit and the one whose control
// is not on file are each dropped by name. The friend's member is not a swarm's and is
// untouched by the rule.
func TestSwarmMemberWithoutAcceptOKIsDropped(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	control := filepath.Join(l.dir, "accept-control")
	if err := os.MkdirAll(control, 0o755); err != nil {
		t.Fatal(err)
	}
	const onFile, notOnFile = "0123456789ab", "ba9876543210"
	if err := os.WriteFile(filepath.Join(control, onFile), []byte("ACCEPT SELFTEST PASS\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	noAccept := addBatchPR(t, l, 4, map[string]string{"pkg/d/d.go": "package d\n"},
		"RESULT: CARD-4 sha=aaaaaaaaaaaa did the thing\nPATHS pkg/d/**\n")
	good := addBatchPR(t, l, 5, map[string]string{"pkg/e/e.go": "package e\n"}, "")
	l.host.PRs[5] = merge.PR{Number: 5, HeadOID: good, Base: "dev",
		Body: "ACCEPT OK      label=c5 kind=fix head=" + good[:12] + " base=aaaaaaaaaaaa tests=1 red_without=1 edits=1 control=" + onFile + " bench=b cert=c took=1s\n"}
	stale := addBatchPR(t, l, 6, map[string]string{"pkg/f/f.go": "package f\n"}, "")
	l.host.PRs[6] = merge.PR{Number: 6, HeadOID: stale, Base: "dev",
		Body: "ACCEPT OK      label=c6 kind=fix head=" + good[:12] + " base=aaaaaaaaaaaa tests=1 red_without=1 edits=1 control=" + onFile + " bench=b cert=c took=1s\n"}
	noControl := addBatchPR(t, l, 7, map[string]string{"pkg/g/g.go": "package g\n"}, "")
	l.host.PRs[7] = merge.PR{Number: 7, HeadOID: noControl, Base: "dev",
		Body: "ACCEPT OK      label=c7 kind=fix head=" + noControl[:12] + " base=aaaaaaaaaaaa tests=1 red_without=1 edits=1 control=" + notOnFile + " bench=b cert=c took=1s\n"}
	root := filepath.Join(l.dir, "batch")

	exit, stdout, stderr := l.run("batch", "--name", "integration-1661a", "--pr", "1,4,5,6,7",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m", "--accept-control", control)

	if exit != 0 {
		t.Fatalf("want a green batch of #1 and #5, got exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "BATCH OK name=integration-1661a")
	contains(t, stdout, "members=1,5 dropped=4,6,7 ")
	contains(t, stderr, "BATCH DROP #4 reason=\"no ACCEPT OK for head "+noAccept[:12]+"\"")
	contains(t, stderr, "BATCH DROP #6 reason=\"no ACCEPT OK for head "+stale[:12]+"\"")
	contains(t, stderr, "BATCH DROP #7 reason=\"no ACCEPT OK for head "+noControl[:12]+"\"")
	absent(t, stderr, "BATCH DROP #1 ")
	absent(t, stderr, "BATCH DROP #5 ")
}

// Without --accept-control the swarm rule is not armed, and it says so per member: the
// admission a caller already had is unchanged (no new refusal), and the line tells a
// reader which member went in unchecked.
func TestSwarmMemberIsNotedWhenAcceptControlIsNotGiven(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	addBatchPR(t, l, 4, map[string]string{"pkg/d/d.go": "package d\n"}, "RESULT: CARD-4 sha=aaaaaaaaaaaa x\n")
	root := filepath.Join(l.dir, "batch")

	exit, stdout, stderr := l.run("batch", "--name", "integration-1661b", "--pr", "1,4",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m")

	if exit != 0 {
		t.Fatalf("want green, got exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "members=1,4 dropped=none ")
	contains(t, stderr, "BATCH NOTE #4 accept=unchecked ")
	contains(t, stderr, "BATCH NOTE hygiene=off ")
}

// batch-names-the-member-that-breaks-the-build
//
// #4 and #5 are each green on their own: #4 adds a package calling base.Base, #5
// renames base.Base. Merged together the tree does not build -- #1479's shape,
// `undefined: useFakeForge`. The batch bisects once, drops #5 BY NAME with the build's
// first line, and goes green on the rest instead of failing whole.
func TestBatchNamesTheMemberThatBreaksTheBuild(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	addBatchPR(t, l, 4, map[string]string{
		"pkg/d/d.go": "package d\n\nimport \"example.com/batch/base\"\n\nfunc D() int { return base.Base() }\n",
	}, "")
	addBatchPR(t, l, 5, map[string]string{
		"base/base.go": "package base\n\nfunc Base2() int { return 1 }\n",
	}, "")
	root := filepath.Join(l.dir, "batch")

	exit, stdout, stderr := l.run("batch", "--name", "integration-1661c", "--pr", "1,4,5",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m")

	if exit != 0 {
		t.Fatalf("want the breaking member dropped and the rest green, got exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "BATCH OK name=integration-1661c")
	contains(t, stdout, "members=1,4 dropped=5 ")
	contains(t, stderr, "BATCH DROP #5 reason=\"build red with this member merged: ")
	contains(t, stderr, "undefined: base.Base")
	// The head named is the tree WITHOUT #5, and it is what the branch holds.
	clone := filepath.Join(root, "integration-1661c", "repo")
	head := l.git(clone, "rev-parse", "refs/heads/rowan/integration-1661c")
	contains(t, stdout, "head="+head+" ")
	absent(t, l.git(clone, "log", "--format=%s", "-n", "10"), "#5")
}

// The bisect runs ONCE. When the tree without the named member still does not build,
// the batch fails whole, as it did before this rule.
func TestBatchBisectsTheBuildOnlyOnce(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	// #4 is red on its own build (its own ci-ok is faked green), and so is #5.
	addBatchPR(t, l, 4, map[string]string{"pkg/d/d.go": "package d\n\nfunc D() int { return nope }\n"}, "")
	addBatchPR(t, l, 5, map[string]string{"pkg/e/e.go": "package e\n\nfunc E() int { return nope }\n"}, "")
	root := filepath.Join(l.dir, "batch")

	exit, stdout, stderr := l.run("batch", "--name", "integration-1661d", "--pr", "1,4,5",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m")

	if exit != 1 {
		t.Fatalf("want a red batch after one bisect, got exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stderr, "BATCH DROP #4 reason=\"build red with this member merged: ")
	contains(t, stdout, "BATCH FAIL name=integration-1661d")
	contains(t, stdout, "members=1,5 dropped=4 ")
	contains(t, stdout, "step=build")
}

// hygiene per member: with the lane's identities.tsv on file, internal/hygiene.Check runs
// over every member. A friend's member with a stray RESULT.md is dropped; a swarm
// member whose diff leaves its PATHS is dropped with out-of-path; a clean friend's member
// is admitted and the line says out-of-path was skipped for it (paths=-).
func TestBatchRunsHygieneOnEveryMember(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	if err := os.MkdirAll(l.lane, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.lane, "identities.tsv"),
		[]byte("name\temail\nfixture\tfixture@localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	addBatchPR(t, l, 4, map[string]string{"pkg/d/d.go": "package d\n", "RESULT.md": "notes\n"}, "")
	addBatchPR(t, l, 5, map[string]string{"pkg/e/e.go": "package e\n", "pkg/x/x.go": "package x\n"},
		"RESULT: CARD-5 sha=aaaaaaaaaaaa x\nPATHS pkg/e/**\n")
	addBatchPR(t, l, 6, map[string]string{"pkg/f/f.go": "package f\n"},
		"RESULT: CARD-6 sha=aaaaaaaaaaaa x\nPATHS: pkg/f/**\n")
	root := filepath.Join(l.dir, "batch")

	exit, stdout, stderr := l.run("batch", "--name", "integration-1661e", "--pr", "1,4,5,6",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m")

	if exit != 0 {
		t.Fatalf("want green, got exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "members=1,6 dropped=4,5 ")
	contains(t, stderr, "BATCH DROP #4 reason=\"hygiene: stray-file at=RESULT.md")
	contains(t, stderr, "BATCH DROP #5 reason=\"hygiene: out-of-path at=pkg/x/x.go")
	contains(t, stderr, "BATCH HYGIENE #1 ok paths=- ")
	contains(t, stderr, "BATCH HYGIENE #6 ok paths=pkg/f/** ")
}
