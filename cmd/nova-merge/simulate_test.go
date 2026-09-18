package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
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

// A tool error -- an entries file that is not there -- is exit 1, and never a poison.
func TestSimulateToolErrorIsExitOne(t *testing.T) {
	l := simulateRepo(t)
	exit, stdout, stderr := l.run("simulate", "--repo", l.work, "--base", "dev",
		"--entries", filepath.Join(l.dir, "no-such-entries"), "--checks", "go build ./...")
	if exit != 1 {
		t.Fatalf("a tool error is exit 1, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stderr, "SIMULATE REFUSED")
	absent(t, stdout, "SIMULATE DONE")
}

// An entries file holding something that is not a pull request number is refused before
// any worktree is made.
func TestSimulateRefusesAnEntriesFileThatIsNotNumbers(t *testing.T) {
	l := simulateRepo(t)
	path := filepath.Join(l.dir, "bad-entries.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, _, stderr := l.run("simulate", "--repo", l.work, "--base", "dev", "--entries", path)
	if exit != 1 {
		t.Fatalf("a bad entries file is a tool error at exit 1, got %d\n%s", exit, stderr)
	}
	contains(t, stderr, "not a pull request number")
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
