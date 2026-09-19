package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// The landing gate's own tests. They drive cmdBatch through run(), against a REAL git
// and a bare fixture repository in t.TempDir(), and they reach no network: the clone
// URL comes from Deps.RepoURL, which the lab points at its own bare repository, and the
// module the gate builds has no dependencies.

// batchRepo builds the fixture: a bare remote holding a dev branch with a tiny go
// module, and three pull request heads under refs/pull/<n>/head.
//
//	#1 adds pkg/a and changes base/shared.go -- clean on top of dev
//	#2 changes the same line of base/shared.go -- CONFLICTS once #1 is ahead of it
//	#3 adds pkg/c with a test that fails -- green to build and vet, RED at go test
//
// That is the shape the shell script found on hulk: a member that will not merge, and a
// member that merges and then poisons the batch.
func batchRepo(t *testing.T) *lab {
	t.Helper()
	l := newLab(t)
	// THE GATE CLONES OVER A TRANSPORT, the way it does everywhere but here (#1578).
	// Deps.RepoURL is `https://github.com/<owner>/<repo>.git` in the binary, so the gate
	// asks upload-pack for a packfile and reads no other directory's files. Handed a bare
	// PATH, which is all this fixture used to hand it, git takes clone_local instead and
	// hardlinks the fixture's object store into the clone entry by entry -- a code path the
	// tool never runs in production, whose failure is `fatal: failed to copy file to
	// '<dest>': No such file or directory` and whose message names the destination while
	// the errno may be the source's. A `file://` URL is git's own way of asking for the
	// ordinary transport. TestTheGatesCloneSharesNoObjectFileWithTheFixtureRepository
	// holds it.
	l.urlFor = func(string) string { return fileURL(l.remote) }
	l.git(l.work, "checkout", "-q", "-B", "dev", "origin/main")
	l.write("go.mod", "module example.com/batch\n\ngo 1.21\n")
	l.write("base/base.go", "package base\n\nfunc Base() int { return 1 }\n")
	l.write("base/shared.go", "package base\n\nvar Shared = \"base\"\n")
	dev := l.commit("dev base")
	l.git(l.work, "push", "-q", "origin", "HEAD:refs/heads/dev")

	l.git(l.work, "checkout", "-q", "-B", "pr1", dev)
	l.write("pkg/a/a.go", "package a\n\nfunc A() int { return 1 }\n")
	l.write("base/shared.go", "package base\n\nvar Shared = \"pr1\"\n")
	one := l.commit("pr 1")

	l.git(l.work, "checkout", "-q", "-B", "pr2", dev)
	l.write("base/shared.go", "package base\n\nvar Shared = \"pr2\"\n")
	two := l.commit("pr 2")

	l.git(l.work, "checkout", "-q", "-B", "pr3", dev)
	l.write("pkg/c/c.go", "package c\n\nfunc C() int { return 3 }\n")
	l.write("pkg/c/c_test.go", "package c\n\nimport \"testing\"\n\nfunc TestBroken(t *testing.T) { t.Fatal(\"the poison\") }\n")
	three := l.commit("pr 3")

	for n, sha := range map[int]string{1: one, 2: two, 3: three} {
		l.git(l.work, "push", "-q", "origin", sha+":refs/pull/"+strconv.Itoa(n)+"/head")
	}
	l.git(l.work, "checkout", "-q", "main")
	// EDGE 25: every member's own head is green on ci-ok, which is what the gate now
	// requires before it merges one. The forge is the FAKE host: no test here opens a
	// socket, and a test that wants a member that has never been green says so by
	// changing this one commit's checks.
	l.heads = map[int]string{1: one, 2: two, 3: three}
	for n, sha := range l.heads {
		l.host.PRs[n] = merge.PR{Number: n, HeadOID: sha, Base: "dev"}
		l.host.SetCheckRuns(sha, merge.CheckDetail{Name: "ci-ok", Conclusion: "success", SHA: sha})
	}
	return l
}

// remoteRefs is every ref the bare fixture repository holds, so a test can say that the
// batch's branch never reached it.
func remoteRefs(l *lab) string {
	return l.git(l.remote, "for-each-ref", "--format=%(refname)")
}

// THE RED RUN. #2 conflicts and is dropped by name, #1 and #3 merge, and #3's failing
// test turns the batch red: one line naming the step, the failing package and the
// failing test, exit 1, and nothing pushed.
func TestBatchDropsTheConflictAndGoesRedOnTheFailingMember(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "batch")
	before := len(l.pushes())

	exit, stdout, stderr := l.run("batch", "--name", "integration-1", "--pr", "1,2,3",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m")

	if exit != 1 {
		t.Fatalf("a red batch is exit 1, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stdout, "BATCH FAIL name=integration-1")
	contains(t, stdout, "members=1,3")
	contains(t, stdout, "dropped=2")
	contains(t, stdout, "step=test")
	contains(t, stdout, "packages=example.com/batch/pkg/c")
	contains(t, stdout, "tests=TestBroken")
	absent(t, stdout, "BATCH OK")

	// THE PROGRESS REACHES THE STDERR WRITER, with elapsed time, while it runs: a gate
	// that says nothing for three minutes and a gate that has hung read the same.
	contains(t, stderr, "BATCH DROP #2 reason=\"the merge conflicts with the members ahead\" t=")
	contains(t, stderr, "BATCH MERGED #1 t=")
	contains(t, stderr, "BATCH MERGED #3 t=")
	contains(t, stderr, "BATCH STEP build ")
	// AND THE TEST STEP RAN THE WAY CI RUNS IT: the failing test is still named on the
	// FAIL line above, out of a -json stream rather than out of go test's text output.
	// The command is quoted from ciTestArgs rather than spelled again here, so that this
	// assertion follows CI the day TestTheGateTestsTheWayCIDoes says the command moved.
	contains(t, stderr, "BATCH STEP test command=\""+strings.Join(ciTestArgs(), " ")+"\"")

	// NOTHING IS PUSHED. The caller pushes the branch and opens the pull request; the
	// verb has no path to a push at all, and the fixture's update hook recorded none.
	if got := len(l.pushes()); got != before {
		t.Errorf("the remote received %d new pushes; the batch pushes nothing", got-before)
	}
	absent(t, remoteRefs(l), "rowan/integration-1")
}

// THE GREEN RUN, and the exact shape of the line a caller parses. #2 still conflicts and
// is still dropped, out loud, and the head named is the branch the caller then pushes.
func TestBatchOKNamesTheBaseTheHeadAndTheDroppedMember(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "batch")
	before := len(l.pushes())

	exit, stdout, stderr := l.run("batch", "--name", "integration-2", "--pr", "1 2",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m")

	if exit != 0 {
		t.Fatalf("a green batch is exit 0, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	clone := filepath.Join(root, "integration-2", "repo")
	base := l.git(l.work, "rev-parse", "dev")
	head := l.git(clone, "rev-parse", "refs/heads/rowan/integration-2")
	want := fmt.Sprintf("BATCH OK name=integration-2 base=%s head=%s members=1 dropped=2 skipped=lisp checks=required", base, head)
	if !strings.Contains(stdout, want) {
		t.Errorf("want the one-line shape\n\t%s\ngot:\n%s", want, stdout)
	}
	if head == base {
		t.Error("the head is the base; #1 was supposed to be merged onto it")
	}
	// The lisp leg is SKIPPED OUT LOUD here: this fixture holds no tools/ci/lisp-test.sh.
	// A gate that quietly ran three steps instead of four is a gate nobody can read --
	// and EDGE 2 is that the skip is on the VERDICT line too, which the shape above
	// pins: `skipped=lisp`.
	contains(t, stderr, "BATCH SKIP lisp ")
	// EDGE 25's cross vet is NOT run here: its cost is a build of the windows standard
	// library, which inside `go test` comes out of this package's own -timeout (see
	// labBatchGate). That the product's gate carries it, after vet and with GOOS=windows,
	// is TestTheGateCrossVetsForWindows below -- which is a read of the list and starts no
	// subprocess at all.
	absent(t, stderr, "BATCH STEP "+crossVetStep+" ")
	if got := len(l.pushes()); got != before {
		t.Errorf("the remote received %d new pushes; the batch pushes nothing", got-before)
	}
	absent(t, remoteRefs(l), "rowan/integration-2")
}

// THE STEP THE TESTS DO NOT RUN IS STILL THE STEP THE GATE RUNS, and this is what says so.
//
// It reads the two lists rather than starting anything: the product's gate must carry the
// cross vet, immediately after `vet`, under GOOS=windows; and the list the tests run must
// differ from it by THAT ONE NAME AND NO OTHER, so a second step can never be quietly
// added to the exemption and go untested everywhere.
func TestTheGateCrossVetsForWindows(t *testing.T) {
	t.Parallel()
	at := -1
	for i, step := range batchGate {
		if step.name == crossVetStep {
			at = i
		}
	}
	if at < 0 {
		t.Fatalf("the gate no longer carries a %s step; the gate runs on one operating system and CI runs on three, and three members green here were red on CI's windows legs the day it did not", crossVetStep)
	}
	if at == 0 || batchGate[at-1].name != "vet" {
		t.Errorf("%s does not follow vet in the gate; it is the same read on another platform and belongs beside it", crossVetStep)
	}
	var goos string
	for _, kv := range batchGate[at].env {
		if name, value, _ := strings.Cut(kv, "="); name == "GOOS" {
			goos = value
		}
	}
	if goos != "windows" {
		t.Errorf("the %s step's GOOS is %q, want windows; a cross vet that does not cross checks the platform it already checked", crossVetStep, goos)
	}

	real, lab := map[string]bool{}, map[string]bool{}
	for _, step := range batchGate {
		real[step.name] = true
	}
	for _, step := range labBatchGate() {
		lab[step.name] = true
	}
	var missing []string
	for name := range real {
		if !lab[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) != 1 || missing[0] != crossVetStep {
		t.Errorf("the tests' gate omits %v; it may omit %s and nothing else, or a step goes untested on every bench and every runner", missing, crossVetStep)
	}
	for name := range lab {
		if !real[name] {
			t.Errorf("the tests' gate runs a step %q the real gate does not; a test of a suite nobody runs is not a test", name)
		}
	}
}

// The four flags nothing may guess, refused together in one go rather than one per run.
func TestBatchRefusesTheFlagsItWillNotGuess(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	exit, stdout, stderr := l.run("batch")
	if exit != 2 {
		t.Fatalf("a batch with no flags is exit 2, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	for _, flag := range []string{"--name is required", "--pr is required", "--root is required", "--repo is required"} {
		contains(t, stderr, flag)
	}
	absent(t, stdout, "BATCH")
}

// --name is the directory under --root and half the branch name, so it is ONE path
// element and nothing that could climb out of the root the caller named.
func TestBatchRefusesANameThatIsNotOnePathElement(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	for _, bad := range []string{"../escape", "a/b", "-x"} {
		exit, stdout, stderr := l.run("batch", "--name", bad, "--pr", "1",
			"--repo", "o/n", "--root", filepath.Join(l.dir, "batch"))
		if exit != 2 {
			t.Errorf("--name %q is exit 2, got %d\nstderr: %s", bad, exit, stderr)
		}
		contains(t, stderr, "--name is one path element")
		absent(t, stdout, "BATCH")
	}
}

// A --pr list holding something that is not a pull request number is refused before any
// directory is removed or any clone is made.
func TestBatchRefusesAPRListThatIsNotNumbers(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	exit, stdout, stderr := l.run("batch", "--name", "integration-3", "--pr", "1,two",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "batch"))
	if exit != 2 {
		t.Fatalf("a bad --pr list is exit 2, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	contains(t, stderr, "not a pull request number")
	absent(t, stdout, "BATCH")
}

// Rule 13, carried into the CHILDREN the gate starts: every check runs with its temp
// directory inside the batch's own working directory. An sbcl suite and a go build that
// key off the ambient one write into /tmp instead, and two gates on one host wrecked
// each other's state that way (tools/ci/lisp-test.sh, 2026-09-18).
func TestPrivateTempEnvPointsEveryTempVariableAtTheBatchsOwnDirectory(t *testing.T) {
	t.Parallel()
	got := privateTempEnv([]string{"PATH=/bin", "TMPDIR=/tmp/ambient", "HOME=/home/x", "GOTMPDIR=/tmp/ambient"}, "/w/tmp")
	seen := map[string]int{}
	for _, kv := range got {
		key, value, _ := strings.Cut(kv, "=")
		seen[key]++
		if strings.Contains(key, "TMP") || strings.Contains(key, "TEMP") {
			if value != "/w/tmp" {
				t.Errorf("%s is %q, want the batch's own temp directory", key, value)
			}
		}
	}
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "GOTMPDIR", "LISP_TEST_TMPROOT"} {
		if seen[key] != 1 {
			t.Errorf("the child environment holds %s %d times, want exactly one", key, seen[key])
		}
	}
}

// makeRecipe returns the tab-indented recipe lines of one Makefile target, joined by
// newlines, or "" when the target has none. A target may be written more than once -- the
// CL test is a `test: PKGS := ...` line and then a `test:` with the recipe -- so every
// recipe line under any occurrence of the name belongs to it.
func makeRecipe(makefile, target string) string {
	var out []string
	in := false
	for _, line := range strings.Split(makefile, "\n") {
		if strings.HasPrefix(line, "\t") {
			if in {
				out = append(out, strings.TrimPrefix(line, "\t"))
			}
			continue
		}
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		in = strings.HasPrefix(line, target+":")
	}
	return strings.Join(out, "\n")
}

// THE GATE TESTS THE WAY CI TESTS, and this reads every side so it goes red the day any
// of them moves. integration-4 ran green on hulk under a plain `go test ./...` and three
// CI legs then failed, because CI does not run a plain `go test ./...`; a gate that tests
// differently from CI is a gate that passes what CI fails.
//
// integration-4 also moved the command itself: ci.yml's `test` step is now `make test
// PKGS=...` and the Makefile's `test` target holds the flags. So this reads BOTH -- that
// ci.yml still delegates to `make test`, and what that target actually runs -- and
// ciTestArgs must mirror the target.
func TestTheGateTestsTheWayCIDoes(t *testing.T) {
	t.Parallel()
	yml, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(yml), "make test PKGS=") {
		t.Fatal("ci.yml's test step no longer runs `make test PKGS=...`; the gate mirrors whatever CI runs, so find the command CI runs now and update ciTestArgs in batch.go with it")
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	recipe := makeRecipe(string(raw), "test")
	if !strings.Contains(recipe, "test") {
		t.Fatalf("the Makefile's `test` target has no recipe, but ci.yml runs `make test`; update ciTestArgs in batch.go with whatever CI runs now\nrecipe: %q", recipe)
	}
	got := strings.Join(ciTestArgs(), " ")
	// -json travels as GOFLAGS=-json on CI's outer command and as an argv flag on the
	// gate's, which is the same thing for that command; both spellings contain "-json".
	for _, flag := range []string{"-json", "-count=1"} {
		if !strings.Contains(recipe, flag) {
			t.Errorf("the Makefile's `test` target no longer carries %q; whatever it carries now is what ciTestArgs must mirror\nrecipe: %s", flag, recipe)
		}
		if !strings.Contains(got, flag) {
			t.Errorf("the gate's test command is %q and does not carry %q, which the Makefile's `test` target does", got, flag)
		}
	}
	// A -timeout the gate sets and CI does not is a gate that can go red on a tree CI
	// passes, which is the same divergence from the other side.
	if strings.Contains(got, "-timeout") && !strings.Contains(recipe, "-timeout") {
		t.Errorf("the gate's test command is %q and sets a per-package -timeout the Makefile's `test` target does not:\n%s", got, recipe)
	}
	if !strings.HasPrefix(got, "go test ") || !strings.HasSuffix(got, " ./...") {
		t.Errorf("the gate's test command is %q; it is `go test <the Makefile's flags> ./...`, the whole merged tree in one run where CI splits it across the matrix", got)
	}
}

// The -json stream is read with the SAME decoder cmd/nova-ci slowtests reads it with, so
// the gate and the budget check cannot disagree about what the stream said.
func TestTestFailuresReadsTheJSONStream(t *testing.T) {
	t.Parallel()
	stream := `{"Action":"run","Package":"example.com/batch/pkg/c","Test":"TestBroken"}
{"Action":"output","Package":"example.com/batch/pkg/c","Test":"TestBroken","Output":"    c_test.go:5: the poison\n"}
{"Action":"fail","Package":"example.com/batch/pkg/c","Test":"TestBroken","Elapsed":0}
{"Action":"pass","Package":"example.com/batch/pkg/a","Elapsed":0.01}
{"Action":"fail","Package":"example.com/batch/pkg/c","Elapsed":0.123}
`
	pkgs, tests, ok := testFailures(stream)
	if !ok {
		t.Fatal("a stream that is all JSON must read as JSON")
	}
	if len(pkgs) != 1 || pkgs[0] != "example.com/batch/pkg/c" {
		t.Errorf("packages = %v, want [example.com/batch/pkg/c]", pkgs)
	}
	if len(tests) != 1 || tests[0] != "TestBroken" {
		t.Errorf("tests = %v, want [TestBroken]", tests)
	}
	// A build failure writes plain text on stderr and runCheck captures both streams, so
	// a stream that is not all JSON is NOT read as an empty one -- it falls back to the
	// text reader, and a gate that read it as empty would print no failing package at all.
	if _, _, ok := testFailures("# example.com/batch/pkg/c\nc.go:3: undefined: X\nFAIL\texample.com/batch/pkg/c [build failed]\n"); ok {
		t.Error("a build failure's plain text read as a JSON stream; it must fall back to the text reader")
	}
}

// The failing packages and tests on the FAIL line are read out of the go test run's own
// output, so a caller sees what to look at without opening the log.
func TestFailuresInReadsThePackagesAndTests(t *testing.T) {
	t.Parallel()
	out := "--- FAIL: TestBroken (0.00s)\n    c_test.go:5: the poison\nFAIL\nFAIL\texample.com/batch/pkg/c\t0.123s\nok  \texample.com/batch/pkg/a\t0.010s\nFAIL\n"
	pkgs, tests := failuresIn(out)
	if len(pkgs) != 1 || pkgs[0] != "example.com/batch/pkg/c" {
		t.Errorf("packages = %v, want [example.com/batch/pkg/c]", pkgs)
	}
	if len(tests) != 1 || tests[0] != "TestBroken" {
		t.Errorf("tests = %v, want [TestBroken]", tests)
	}
}

// #1578, THE GATE CLONES OVER A TRANSPORT. Handed a filesystem path, git does not clone:
// it takes `clone_local`, walking the source repository's objects directory entry by
// entry and HARDLINKING each loose object into the destination -- and the first time one
// `link()` fails it latches a byte-copy fallback for the rest of the clone and dies with
//
//	fatal: failed to copy file to '<dest>/.git/objects/xx/yyy…': No such file or directory
//
// That line printed on three studio legs on three unrelated branches in one night, twice
// on documentation-only changes, and held a pull request that had every read it needed
// (#1578). It is unattributable by construction: the path it names is the DESTINATION,
// while the errno can come from the SOURCE open inside git's copy_file, so the message
// cannot even say which side went missing, and nothing in the row under review is near
// the merge tool.
//
// The gate NEVER clones a path in production: Deps.RepoURL is
// `https://github.com/<owner>/<repo>.git`, a transport clone that asks upload-pack for a
// packfile and touches no other directory's files. Only the fixture handed it a path, so
// only the fixture ran that code. It hands it a `file://` URL instead -- git's own way of
// saying "the ordinary transport, please" -- and the gate's clone then shares no file
// with the fixture's object store at all.
//
// This test is the property, not the flake: it is red against a local clone on every
// machine and every run, which the flake never was.
func TestTheGatesCloneSharesNoObjectFileWithTheFixtureRepository(t *testing.T) {
	t.Parallel()
	l := batchRepo(t)
	root := filepath.Join(l.dir, "batch")
	exit, stdout, stderr := l.run("batch", "--name", "integration-transport", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m")
	if exit != 0 {
		t.Fatalf("the gate did not run: exit %d\n%s\n%s", exit, stdout, stderr)
	}

	// Identity, not bytes: os.SameFile answers the question a hardlink poses -- is this
	// the same file on disk as that one -- on every platform this tool builds for.
	fixture := objectFiles(t, filepath.Join(l.remote, "objects"))
	if len(fixture) == 0 {
		t.Fatal("the fixture's bare repository holds no loose object, so this proves nothing")
	}
	cloned := objectFiles(t, filepath.Join(root, "integration-transport", "repo", ".git", "objects"))
	for clonePath, cloneInfo := range cloned {
		for fixturePath, fixtureInfo := range fixture {
			if os.SameFile(cloneInfo, fixtureInfo) {
				t.Fatalf("the gate's clone is the same file on disk as the fixture's:\n  %s\n  %s\nthat is git's clone_local, which hardlinks the source object store entry by entry; the gate must clone over a transport (#1578)",
					clonePath, fixturePath)
			}
		}
	}
}

// objectFiles is every file under an object store, by path, with the FileInfo os.SameFile
// compares. It skips nothing: a pack, an idx and a loose object are all files the local
// clone would have hardlinked.
func objectFiles(t *testing.T, dir string) map[string]os.FileInfo {
	t.Helper()
	out := map[string]os.FileInfo{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out[path] = info
		return nil
	})
	if err != nil {
		t.Fatalf("reading the object store %s: %v", dir, err)
	}
	return out
}

// fileURL is an absolute path as the `file://` URL git reads it, on every platform this
// tool builds for: forward slashes, and a leading one so a windows path keeps its drive
// letter inside the URL (`C:\r.git` is `file:///C:/r.git`).
func fileURL(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "file://" + p
}

// #1607, AT THE FIXTURE. The two refusal tests above passed their assertions and then
// failed in `t.TempDir RemoveAll cleanup: unlinkat .../work/.git/objects: directory not
// empty`, because the fixture's own `git commit` and `git push` each fork
// `git maintenance run --auto --quiet --detach` and return without it: that child is still
// inside `work/.git` -- it creates objects/maintenance.lock there -- while t.TempDir
// removes the directory under it. A refusal test is where it shows, because a refusal
// returns in milliseconds and the longer tests outlive their own strays by accident.
//
// So: building the fixture starts no git that the fixture does not wait for. This reads
// git's own trace2 stream, which records every child a git starts.
func TestTheFixtureStartsNoGitItDoesNotWaitFor(t *testing.T) {
	// No t.Parallel: it sets GIT_TRACE2_EVENT for the process, and Go runs the
	// sequential tests with every parallel one paused.
	trace := t.TempDir()
	t.Setenv("GIT_TRACE2_EVENT", trace)
	batchRepo(t)

	entries, err := os.ReadDir(trace)
	if err != nil {
		t.Fatalf("reading the trace directory: %v", err)
	}
	if len(entries) == 0 {
		t.Skip("this git wrote no trace2 events, so this bench cannot see the children git starts")
	}
	var background []string
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(trace, e.Name()))
		if err != nil {
			t.Fatalf("reading a trace file: %v", err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if !strings.Contains(line, `"child_start"`) {
				continue
			}
			var ev struct {
				Event string   `json:"event"`
				Argv  []string `json:"argv"`
			}
			if err := json.Unmarshal([]byte(line), &ev); err != nil || ev.Event != "child_start" {
				continue
			}
			for _, word := range ev.Argv {
				if word == "maintenance" || word == "gc" || word == "fsmonitor--daemon" {
					background = append(background, strings.Join(ev.Argv, " "))
					break
				}
			}
		}
	}
	if len(background) > 0 {
		t.Errorf("the fixture started %d background git(s) nothing waits for:\n\t%s\nthey are still writing into this test's own t.TempDir when it is removed (#1607); the fixture runs git through merge.NoBackgroundGit",
			len(background), strings.Join(background, "\n\t"))
	}
}
