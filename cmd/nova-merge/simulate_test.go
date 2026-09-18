package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeQueue is the fake gh of these tests: the live merge queue, as numbers, with no
// network anywhere. It is the QueueReader a simulate run with no --entries reads.
type fakeQueue struct {
	entries []int
	err     error
}

func (f fakeQueue) Entries(string) ([]int, error) { return f.entries, f.err }

// simulateRepo builds the fixture the red tests run against: a bare repository, a dev
// branch holding a tiny go module, and four pull request heads. Entry 1 adds a package
// and changes base/shared.go; entry 2 adds package b with symbol B; entry 3 REDECLARES
// B, so it builds alone and breaks once entry 2 is ahead of it; entry 4 changes the
// same line of base/shared.go that entry 1 changed, so it conflicts. The go build is
// real and reaches no network: the module has no dependencies.
func simulateRepo(t *testing.T) *lab {
	t.Helper()
	l := newLab(t)
	l.git(l.work, "checkout", "-q", "-B", "dev", "origin/main")
	l.write("go.mod", "module example.com/sim\n\ngo 1.21\n")
	l.write("base/base.go", "package base\n\nfunc Base() int { return 1 }\n")
	l.write("base/shared.go", "package base\n\nvar Shared = \"base\"\n")
	dev := l.commit("dev base")
	l.git(l.work, "push", "-q", "origin", "HEAD:refs/heads/dev")

	l.git(l.work, "checkout", "-q", "-B", "entry1", dev)
	l.write("pkg/a/a.go", "package a\n\nfunc A() int { return 1 }\n")
	l.write("base/shared.go", "package base\n\nvar Shared = \"entry1\"\n")
	one := l.commit("entry 1")

	l.git(l.work, "checkout", "-q", "-B", "entry2", dev)
	l.write("pkg/b/b.go", "package b\n\nfunc B() int { return 2 }\n")
	two := l.commit("entry 2")

	l.git(l.work, "checkout", "-q", "-B", "entry3", dev)
	l.write("pkg/b/dup.go", "package b\n\nfunc B() int { return 3 }\n")
	three := l.commit("entry 3")

	l.git(l.work, "checkout", "-q", "-B", "entry4", dev)
	l.write("base/shared.go", "package base\n\nvar Shared = \"entry4\"\n")
	four := l.commit("entry 4")

	for n, sha := range map[int]string{1: one, 2: two, 3: three, 4: four} {
		l.git(l.work, "push", "-q", "origin", sha+":refs/pull/"+strconv.Itoa(n)+"/head")
	}
	l.git(l.work, "checkout", "-q", "main")
	return l
}

// entriesFile writes the --entries file a run reads, one pull request number per line.
func entriesFile(t *testing.T, l *lab, numbers ...int) string {
	t.Helper()
	var b strings.Builder
	for _, n := range numbers {
		b.WriteString(strconv.Itoa(n))
		b.WriteString("\n")
	}
	path := filepath.Join(l.dir, "entries.txt")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The poison: entry 3 builds alone but redeclares the symbol entry 2 introduced, so it
// is the first red on top of the entries ahead of it. One line per entry, and the
// summary names it.
func TestSimulateNamesThePoisonEntry(t *testing.T) {
	l := simulateRepo(t)
	entries := entriesFile(t, l, 1, 2, 3)
	exit, stdout, stderr := l.run("simulate", "--repo", l.work, "--base", "dev",
		"--entries", entries, "--checks", "go build ./...", "--timeout", "2m")
	if exit != 2 {
		t.Fatalf("a poison is exit 2, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "SIMULATE OK #1")
	contains(t, stdout, "SIMULATE OK #2")
	contains(t, stdout, "SIMULATE POISON #3 check=\"go build ./...\"")
	contains(t, stdout, "example.com/sim/pkg/b")
	contains(t, stdout, "SIMULATE DONE entries=3 ok=2 conflicts=0 poison=#3")
	// ONE LINE PER ENTRY: an OK line for each that passed and a poison line for the one
	// that did not, never a line for an entry that was not reached.
	absent(t, stdout, "SIMULATE OK #3")
	absent(t, stdout, "SIMULATE REFUSED")
}

// A conflicting entry is named and SKIPPED, and the entries after it are still judged:
// the conflict does not poison the ones behind it.
func TestSimulateSkipsAConflictAndContinues(t *testing.T) {
	l := simulateRepo(t)
	entries := entriesFile(t, l, 1, 4, 2)
	exit, stdout, stderr := l.run("simulate", "--repo", l.work, "--base", "dev",
		"--entries", entries, "--checks", "go build ./...", "--timeout", "2m")
	if exit != 0 {
		t.Fatalf("a run with a conflict and no poison is exit 0, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "SIMULATE OK #1")
	contains(t, stdout, "SIMULATE CONFLICT #4 with the entries ahead")
	contains(t, stdout, "SIMULATE OK #2")
	contains(t, stdout, "SIMULATE DONE entries=3 ok=2 conflicts=1 poison=none")
}

// With no --entries the queue comes from the injected reader -- the fake gh -- so the
// live-queue path is exercised without a network or a subprocess.
func TestSimulateReadsTheLiveQueueThroughTheFakeReader(t *testing.T) {
	l := simulateRepo(t)
	l.queue = fakeQueue{entries: []int{1, 2}}
	exit, stdout, stderr := l.run("simulate", "--repo", l.work, "--base", "dev",
		"--checks", "go build ./...", "--timeout", "2m")
	if exit != 0 {
		t.Fatalf("simulate: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "SIMULATE OK #1")
	contains(t, stdout, "SIMULATE OK #2")
	contains(t, stdout, "SIMULATE DONE entries=2 ok=2 conflicts=0 poison=none")
}

// A check that outruns --timeout is the poison, and the deadline ends it rather than
// letting the run hang. The process group is killed, so this test finishes; if the
// deadline did not fire, `go test`'s own timeout is what would catch it, not an
// assertion on the clock.
func TestSimulateDeadlineMakesASlowCheckPoison(t *testing.T) {
	l := simulateRepo(t)
	entries := entriesFile(t, l, 1)
	exit, stdout, stderr := l.run("simulate", "--repo", l.work, "--base", "dev",
		"--entries", entries, "--checks", "sleep 30", "--timeout", "1s")
	if exit != 2 {
		t.Fatalf("a timed-out check is a poison at exit 2, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "SIMULATE POISON #1 check=\"sleep 30\"")
	contains(t, stdout, "no answer within")
}

// THE EXIT TABLE docs/CLI.md documents, which the code contradicted on every invalid
// invocation: "Exit 2 means either a configured check failed or the invocation was
// invalid... Exit 1 is a preparation or runtime refusal." Every case below names something
// the CALLER asked for and could not have, and every one of them exited 1.
//
// One fixture and one `true` check for all five: none of these reaches a check, and a real
// `go build` per case would be four minutes of CI for an exit code (docs/TEST-DURATIONS.md).
func TestSimulateInvalidInvocationsAreExitTwo(t *testing.T) {
	l := simulateRepo(t)
	notARepo := filepath.Join(l.dir, "not-a-repo")
	if err := os.MkdirAll(notARepo, 0o755); err != nil {
		t.Fatal(err)
	}
	badEntries := filepath.Join(l.dir, "bad-entries.txt")
	if err := os.WriteFile(badEntries, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, repo, base, entries, want string
	}{
		{"an --entries file that is not there", l.work, "dev", filepath.Join(l.dir, "no-such-entries"), "could not be read"},
		{"an --entries line that is not a number", l.work, "dev", badEntries, "not a pull request number"},
		{"a --repo that is not a git repository", notARepo, "dev", entriesFile(t, l, 1), "is not a git repository"},
		{"a --base the origin does not have", l.work, "no-such-branch", entriesFile(t, l, 1), "could not fetch origin/no-such-branch"},
		{"an entry the origin does not have", l.work, "dev", entriesFile(t, l, 9999), "could not fetch pull/9999/head"},
	} {
		t.Run(c.name, func(t *testing.T) {
			exit, stdout, stderr := l.run("simulate", "--repo", c.repo, "--base", c.base,
				"--entries", c.entries, "--checks", "true")
			if exit != 2 {
				t.Fatalf("%s is an invalid invocation at exit 2 (docs/CLI.md), got %d\nstdout: %s\nstderr: %s", c.name, exit, stdout, stderr)
			}
			contains(t, stderr, "SIMULATE REFUSED")
			contains(t, stderr, c.want)
			absent(t, stdout, "SIMULATE DONE")
		})
	}
}

// THE LEFTOVER. Four dogfood runs on 2026-09-18 left four entries under .git/worktrees and
// printed no SIMULATE NOTE: the removal was `git worktree remove --force`, and the lane's
// git seam refuses --force in any argument, so the command never ran and its error went
// into a discarded `_`. A pass leaves the repository as it found it — no scratch directory
// and nothing for `git worktree prune` to find — and a removal that cannot happen is one
// NOTE, which is what CLI.md promises.
//
// The checks are `true`: what is under test is the worktree's life, not a compiler.
func TestSimulateLeavesNoWorktreeEntryBehind(t *testing.T) {
	l := simulateRepo(t)
	gitDir := l.git(l.work, "rev-parse", "--absolute-git-dir")
	before := l.git(l.work, "worktree", "list", "--porcelain")

	exit, stdout, stderr := l.run("simulate", "--repo", l.work, "--base", "dev",
		"--entries", entriesFile(t, l, 1, 2), "--checks", "true")
	if exit != 0 {
		t.Fatalf("simulate: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	absent(t, stderr, "SIMULATE NOTE")

	// Nothing of the scratch worktree is left: not the directory, not git's own record
	// of it, and `worktree list` says exactly what it said before the run.
	ents, err := os.ReadDir(gitDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "nova-merge-simulate-") {
			t.Errorf("the scratch directory %s is still under %s", e.Name(), gitDir)
		}
	}
	if wt, err := os.ReadDir(filepath.Join(gitDir, "worktrees")); err == nil {
		for _, e := range wt {
			if strings.HasPrefix(e.Name(), "nova-merge-simulate-") {
				t.Errorf(".git/worktrees/%s is a prunable entry the pass left behind", e.Name())
			}
		}
	}
	if after := l.git(l.work, "worktree", "list", "--porcelain"); after != before {
		t.Errorf("worktree list changed across the pass:\nbefore %q\n after %q", before, after)
	}

	// Both halves of the removal are driven directly for the NOTE, because neither
	// failure is a state a whole pass can be made to reach reliably. safepath refusing a
	// path that is not below the root is the shape of every removal this function must
	// never make: one NOTE, and the directory untouched.
	outside := t.TempDir()
	var buf bytes.Buffer
	removeScratchWorktree(&buf, l.work, gitDir, outside, time.Minute, l.deps())
	contains(t, buf.String(), "SIMULATE NOTE")
	contains(t, buf.String(), "could not be removed")
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("the refusal removed the directory anyway: %v", err)
	}

	// And a prune that cannot run is the second NOTE, naming the entry still on disk
	// rather than leaving it for somebody's `git worktree prune` weeks later.
	scratch, err := os.MkdirTemp(gitDir, "nova-merge-simulate-")
	if err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	removeScratchWorktree(&buf, filepath.Join(l.dir, "not-a-repo-at-all"), gitDir, scratch, time.Minute, l.deps())
	contains(t, buf.String(), "SIMULATE NOTE")
	contains(t, buf.String(), "was not")
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Fatalf("the scratch directory survived a run that reported only the prune: %v", err)
	}
}

// The GraphQL answer is decoded here, where no gh and no network are needed.
func TestDecodeMergeQueueReadsTheOrderedNumbers(t *testing.T) {
	out := `{"data":{"repository":{"mergeQueue":{"entries":{"nodes":[` +
		`{"position":1,"pullRequest":{"number":12}},` +
		`{"position":2,"pullRequest":{"number":7}},` +
		`{"position":3,"pullRequest":null}]}}}}}`
	got, err := decodeMergeQueue(out)
	if err != nil {
		t.Fatalf("decodeMergeQueue: %v", err)
	}
	if len(got) != 2 || got[0] != 12 || got[1] != 7 {
		t.Fatalf("decodeMergeQueue = %v, want [12 7]", got)
	}
}

// The live reader derives <owner>/<name> from a github origin in either spelling.
func TestParseRepoSlugFromBothOriginForms(t *testing.T) {
	for _, c := range []struct{ raw, want string }{
		{"https://example.com/mas-bandwidth/nova-tools.git", "mas-bandwidth/nova-tools"},
		{"git@example.com:mas-bandwidth/nova-tools.git", "mas-bandwidth/nova-tools"},
		{"git@example.com:owner/name", "owner/name"},
	} {
		got, err := parseRepoSlug(c.raw)
		if err != nil || got != c.want {
			t.Errorf("parseRepoSlug(%q) = %q, %v; want %q", c.raw, got, err, c.want)
		}
	}
}

// A missing --repo is a refusal at 2 (the invocation could not run), and it says what
// the flag wants rather than only that it is missing.
func TestSimulateRequiresRepoAndBase(t *testing.T) {
	l := simulateRepo(t)
	exit, stdout, stderr := l.run("simulate", "--base", "dev")
	if exit != 2 {
		t.Fatalf("a missing --repo is exit 2, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stderr, "--repo is required")
	absent(t, stdout, "SIMULATE")
}
