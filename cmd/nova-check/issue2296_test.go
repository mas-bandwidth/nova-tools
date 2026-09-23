package main

// nova-tools#2296: the nocode --staged verb -- required --dir, the root test,
// the base detector, and the exit codes. SPEC.md:805 classifies what is about
// to be committed, not what is on disk; SPEC.md:831 makes --dir required on
// the no-guessing law; SPEC.md:1021 makes the root test
// `git rev-parse --show-toplevel` compared with --dir after resolving
// symlinks, never a `.git`-is-a-directory test; SPEC.md:1028 makes the base
// HEAD, except on an unborn HEAD, detected by `git rev-parse -q --verify
// HEAD`'s exit code, where the empty tree comes from `git hash-object -t tree
// /dev/null` run inside the repository rather than a hard-coded constant;
// SPEC.md:1005,1010 give the exit codes: 0 with a count of zero when nothing
// classifiable is staged, 1 with `NOCODE FAIL <path>: <reason>` per path on
// stderr, 2 for every refusal.
//
// Every case drives the command line end to end through run(), over real
// temporary git repositories, so the file is red on a tree without the verb
// and stays red if the wiring moves off the verb the spec names. The one
// exception is the two records real git cannot emit through this command --
// an unrecognised status letter and an unclassifiable destination mode --
// where a fake git stands in front of the real one and crafts the record,
// because the refusal branches exist precisely for a git that one day will.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stGitOut runs one git command in dir under the hermetic test environment
// and returns its combined output and error. Real git, real repositories:
// the whole point of #2296 is what the plumbing actually prints.
func stGitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Rowan", "GIT_AUTHOR_EMAIL=rowan@example.com",
		"GIT_COMMITTER_NAME=Rowan", "GIT_COMMITTER_EMAIL=rowan@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
	)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// stGit is stGitOut with the test failed on any error.
func stGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := stGitOut(dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// stLab is a committed prose repository: one tracked f.md and a clean index,
// the smallest tree every case below can stage into.
func stLab(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	stGit(t, dir, "init", "-q", "-b", "main")
	mustWrite(t, dir, "f.md", "prose base\n")
	stGit(t, dir, "add", "f.md")
	stGit(t, dir, "commit", "-q", "-m", "base")
	return dir
}

// stLine returns the one line of stream that begins with prefix, so an OK or
// FAIL line is asserted whole rather than as a substring that another line
// could satisfy.
func stLine(t *testing.T, stream, prefix string) string {
	t.Helper()
	for _, line := range strings.Split(strings.TrimRight(stream, "\n"), "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	t.Fatalf("no %q line in:\n%s", prefix, stream)
	return ""
}

// TestIssue2296 is the anchor: the whole #2296 surface in one test, so the
// card's check (`go test ./cmd/nova-check/ -run TestIssue2296`) exercises
// every behaviour the issue names. The six bodies also stand as the six
// top-level tests below, under the issue's own names.
func TestIssue2296(t *testing.T) {
	t.Run("TestNoCodeStagedClassifiesTheIndex", noCodeStagedClassifiesTheIndex)
	t.Run("TestNoCodeStagedRequiresDir", noCodeStagedRequiresDir)
	t.Run("TestNoCodeStagedNothingToSay", noCodeStagedNothingToSay)
	t.Run("TestNoCodeStagedSaysNo", noCodeStagedSaysNo)
	t.Run("TestNoCodeStagedRefusals", noCodeStagedRefusals)
	t.Run("TestNoCodeStagedRootAndBase", noCodeStagedRootAndBase)
}

func TestNoCodeStagedClassifiesTheIndex(t *testing.T) { noCodeStagedClassifiesTheIndex(t) }

// A commit commits an index, not a tree (SPEC.md:805). Staging a shebang and
// then replacing the file with prose on disk leaves the shebang in what will
// be committed while every working-tree reader sees prose -- measured in the
// issue, and the whole reason this mode exists. The staged blob is what is
// classified; the audit over the same tree must stay clean, and the two
// answers together are the point.
func noCodeStagedClassifiesTheIndex(t *testing.T) {
	dir := stLab(t)
	mustWrite(t, dir, "runner", "#!/bin/sh\necho hi\n")
	stGit(t, dir, "add", "runner")
	// ... and then the working tree is made innocent.
	mustWrite(t, dir, "runner", "harmless prose now\n")

	exit, stdout, stderr := runCheck(t, "nocode", "--staged", "--dir", dir)
	if exit != 1 {
		t.Fatalf("exit = %d, want 1 (the staged blob is the shebang); stdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}
	if want := "NOCODE FAIL runner: executable script (shebang)"; !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q,\nwant the staged shebang named: %q", stderr, want)
	}
	if got, want := stLine(t, stderr, "NOCODE FAIL staged="), "NOCODE FAIL staged=1 findings=1 shown=1 deny-list=floor\\x20list"; got != want {
		t.Errorf("FAIL summary = %q, want %q", got, want)
	}
	if strings.Contains(stdout, "NOCODE OK") {
		t.Errorf("a failing run printed an OK line: %q", stdout)
	}

	// The audit over the SAME tree walks the disk and sees prose only.
	aexit, astdout, _ := runCheck(t, "nocode", "--dir", dir)
	if aexit != 0 || !strings.Contains(astdout, "NOCODE OK") {
		t.Errorf("the audit over prose on disk exited %d:\n%s", aexit, astdout)
	}
}

func TestNoCodeStagedRequiresDir(t *testing.T) { noCodeStagedRequiresDir(t) }

// --dir is required on the no-guessing law, never inferred from the working
// directory (SPEC.md:831). The test binary runs inside this repository, so
// the working directory IS inside a git repository: a tool that inferred
// --dir from it would pass this case, and the refusal is what the law wants.
func noCodeStagedRequiresDir(t *testing.T) {
	exit, stdout, stderr := runCheck(t, "nocode", "--staged")
	if exit != 2 {
		t.Fatalf("exit = %d, want 2 (a missing --dir is a refusal, never a guess); stderr:\n%s", exit, stderr)
	}
	if !strings.Contains(stderr, "--dir is required; refusing to guess") {
		t.Errorf("stderr = %q,\nwant the required-flag refusal", stderr)
	}
	if stdout != "" {
		t.Errorf("a refusal printed to stdout: %q", stdout)
	}
}

func TestNoCodeStagedNothingToSay(t *testing.T) { noCodeStagedNothingToSay(t) }

// Nothing staged, or deletions only, is exit 0 with a count of zero -- an
// empty change set is a fact about the commit, not a broken check
// (SPEC.md:1005). An unborn HEAD with an empty index reaches the same answer
// through the base detector, which is the only route to it.
func noCodeStagedNothingToSay(t *testing.T) {
	ok0 := func(t *testing.T, dir, why string) {
		t.Helper()
		exit, stdout, stderr := runCheck(t, "nocode", "--staged", "--dir", dir)
		if exit != 0 {
			t.Fatalf("%s: exit = %d, want 0; stderr:\n%s", why, exit, stderr)
		}
		if got, want := stLine(t, stdout, "NOCODE OK"), "NOCODE OK staged=0 clean deny-list=floor\\x20list"; got != want {
			t.Errorf("%s: OK line = %q, want %q", why, got, want)
		}
	}

	// A committed repository with nothing staged.
	ok0(t, stLab(t), "nothing staged")

	// Deletions only: a D record is the one status skip, and the count of what
	// was classified is still zero.
	del := stLab(t)
	stGit(t, del, "rm", "-q", "f.md")
	ok0(t, del, "deletions only")

	// An unborn HEAD with an empty index: the empty tree is a valid base, the
	// diff against it is empty, and that is an answer, not a refusal.
	fresh := t.TempDir()
	stGit(t, fresh, "init", "-q", "-b", "main")
	ok0(t, fresh, "an unborn HEAD with an empty index")
}

func TestNoCodeStagedSaysNo(t *testing.T) { noCodeStagedSaysNo(t) }

// Any staged path classified as machinery is one `NOCODE FAIL <path>:
// <reason>` line per path on stderr, exit 1, and no OK line anywhere; a clean
// index prints the OK line on stdout (SPEC.md:1010,1013). The three staged
// tells: the extension floor, a shebang with no extension and no executable
// bit, and prose whose INDEX mode alone is executable.
func noCodeStagedSaysNo(t *testing.T) {
	dir := stLab(t)
	mustWrite(t, dir, "run.sh", "echo hi\n")
	stGit(t, dir, "add", "run.sh")
	mustWrite(t, dir, "runner", "#!/bin/sh\necho hi\n")
	stGit(t, dir, "add", "runner")
	mustWrite(t, dir, "notes.txt", "just prose\n")
	// --chmod=+x sets the index mode even where the filesystem has no
	// executable bit to read, so this tell is platform-independent.
	stGit(t, dir, "add", "--chmod=+x", "notes.txt")

	exit, stdout, stderr := runCheck(t, "nocode", "--staged", "--dir", dir)
	if exit != 1 {
		t.Fatalf("exit = %d, want 1; stdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}
	for _, want := range []string{
		"NOCODE FAIL run.sh: code extension .sh (floor list)",
		"NOCODE FAIL runner: executable script (shebang)",
		"NOCODE FAIL notes.txt: executable (index mode 100755)",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q,\nwant the per-path line: %q", stderr, want)
		}
	}
	if got, want := stLine(t, stderr, "NOCODE FAIL staged="), "NOCODE FAIL staged=3 findings=3 shown=3 deny-list=floor\\x20list"; got != want {
		t.Errorf("FAIL summary = %q, want %q", got, want)
	}
	if stdout != "" {
		t.Errorf("a failing run printed to stdout: %q", stdout)
	}

	// A clean index prints the OK line on stdout, with the count of the one
	// record classified.
	clean := stLab(t)
	mustWrite(t, clean, "g.md", "more prose\n")
	stGit(t, clean, "add", "g.md")
	cexit, cstdout, cstderr := runCheck(t, "nocode", "--staged", "--dir", clean)
	if cexit != 0 {
		t.Fatalf("a clean index exited %d, want 0; stderr:\n%s", cexit, cstderr)
	}
	if got, want := stLine(t, cstdout, "NOCODE OK"), "NOCODE OK staged=1 clean deny-list=floor\\x20list"; got != want {
		t.Errorf("OK line = %q, want %q", got, want)
	}
}

func TestNoCodeStagedRefusals(t *testing.T) { noCodeStagedRefusals(t) }

// The refusals are exit 2 (SPEC.md:1015-1021): a --dir that is not the root
// of a git repository, a diff-index that itself fails, unmerged entries, an
// unrecognised status letter, an unclassifiable destination mode, and the
// deny-list refusals the audit already makes.
func noCodeStagedRefusals(t *testing.T) {
	refused := func(t *testing.T, why string, exit int, stderr, want string) {
		t.Helper()
		if exit != 2 {
			t.Fatalf("%s: exit = %d, want 2; stderr:\n%s", why, exit, stderr)
		}
		if !strings.Contains(stderr, want) {
			t.Errorf("%s: stderr = %q,\nwant it to name %q", why, stderr, want)
		}
	}

	// A --dir that is not a repository at all.
	notrepo := t.TempDir()
	exit, _, stderr := runCheck(t, "nocode", "--staged", "--dir", notrepo)
	refused(t, "a directory that is not a repository", exit, stderr, "is not the root of a git repository")

	// A --dir inside a repository but not its root: the root test compares
	// --dir with the toplevel git reports, and a subdirectory is not it.
	repo := stLab(t)
	sub := filepath.Join(repo, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	exit, _, stderr = runCheck(t, "nocode", "--staged", "--dir", sub)
	refused(t, "a subdirectory of a repository", exit, stderr, "is not the root of a git repository")

	// A diff-index that itself fails is never a clean tree: a corrupt index
	// makes the plumbing exit non-zero, and the refusal is what that costs.
	corrupt := stLab(t)
	mustWrite(t, corrupt, ".git/index", "not an index at all, far too short\n")
	exit, _, stderr = runCheck(t, "nocode", "--staged", "--dir", corrupt)
	refused(t, "a failed diff-index", exit, stderr, "diff-index")

	// Unmerged entries: a real conflict leaves a U record, which has no staged
	// content to classify and is a refusal, never a silent skip.
	conflict := stLab(t)
	stGit(t, conflict, "checkout", "-q", "-b", "side")
	mustWrite(t, conflict, "f.md", "side's line\n")
	stGit(t, conflict, "commit", "-q", "-am", "side")
	stGit(t, conflict, "checkout", "-q", "main")
	mustWrite(t, conflict, "f.md", "main's line\n")
	stGit(t, conflict, "commit", "-q", "-am", "main")
	if _, err := stGitOut(conflict, "merge", "side"); err == nil {
		t.Fatal("the merge was expected to conflict")
	}
	exit, _, stderr = runCheck(t, "nocode", "--staged", "--dir", conflict)
	refused(t, "unmerged entries", exit, stderr, "unmerged")

	// The deny-list refusals the audit already makes, kept by this mode: an
	// empty list, a malformed entry, and both deny-list flags at once.
	bad := stLab(t)
	empty := filepath.Join(t.TempDir(), "empty-exts.txt")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		why  string
		args []string
		want string
	}{
		{"an empty deny-list file", []string{"nocode", "--staged", "--dir", bad, "--deny-ext", "@" + empty}, "contains no extensions"},
		{"a malformed deny-list entry", []string{"nocode", "--staged", "--dir", bad, "--deny-ext", "mylist.txt"}, "more than one dot"},
		{"both deny-list flags at once", []string{"nocode", "--staged", "--dir", bad, "--deny-ext", ".sh", "--deny-ext-add", ".py"}, "mutually exclusive"},
	} {
		cexit, cstdout, cstderr := runCheck(t, tc.args...)
		refused(t, tc.why, cexit, cstderr, tc.want)
		if cstdout != "" {
			t.Errorf("%s printed to stdout: %q", tc.why, cstdout)
		}
	}

	// An unrecognised status letter and an unclassifiable destination mode.
	// Real git emits neither through this command -- the branches exist for a
	// git that one day will, and a switch with no default must refuse rather
	// than skip -- so a fake git stands in front of the real one and crafts
	// the one record. Everything else the tool asks git reaches the real
	// binary behind it, over a real repository with a real staged record, so
	// a fake that failed to take would classify prose and the case would go
	// green over the refusal it came to pin.
	for _, tc := range []struct{ name, record, want string }{
		{
			"an unrecognised status letter",
			":000000 100644 0000000000000000000000000000000000000000 4b825dc642cb6eb9a060e54bf8d69288fbee4904 X\\0evil\\0",
			"unrecognised status",
		},
		{
			"an unclassifiable destination mode",
			":000000 040000 0000000000000000000000000000000000000000 4b825dc642cb6eb9a060e54bf8d69288fbee4904 A\\0evil\\0",
			"destination mode",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			real, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			dir := stLab(t)
			mustWrite(t, dir, "g.md", "staged prose\n")
			stGit(t, dir, "add", "g.md")
			// The record reaches the tool as printf's own escapes, so the
			// NUL separators are interpreted at run time, not embedded in
			// the script. Everything else the tool asks git -- the root
			// test, the base detector -- is exec'd through to the real
			// binary behind the fake.
			bin := t.TempDir()
			fakeBin(t, bin, "git", fmt.Sprintf(`case "$3" in
  diff-index)
    printf '%s'
    exit 0
    ;;
  *)
    exec '%s' "$@"
    ;;
esac
`, tc.record, real))
			orig := os.Getenv("PATH")
			os.Setenv("PATH", bin+string(os.PathListSeparator)+orig)
			t.Cleanup(func() { os.Setenv("PATH", orig) })
			exit, stdout, stderr := runCheck(t, "nocode", "--staged", "--dir", dir)
			refused(t, tc.name, exit, stderr, tc.want)
			if stdout != "" {
				t.Errorf("%s printed to stdout: %q", tc.name, stdout)
			}
		})
	}
}

func TestNoCodeStagedRootAndBase(t *testing.T) { noCodeStagedRootAndBase(t) }

// The root test and the base detector. A linked worktree and a submodule pass
// the root test -- their .git is a file, so any test for .git being a
// directory refuses exactly the legitimate places to commit from -- and an
// unborn HEAD is compared against the empty tree obtained from the repository
// itself, which is the only spelling that is right in a sha256 repository
// too. A repository's first commit is gated like every later one.
func noCodeStagedRootAndBase(t *testing.T) {
	// A linked worktree: .git is a file there, and the root test passes.
	repo := stLab(t)
	wt := filepath.Join(t.TempDir(), "wt")
	stGit(t, repo, "worktree", "add", "-q", "-b", "wtbranch", wt)
	mustWrite(t, wt, "g.md", "prose in a linked worktree\n")
	stGit(t, wt, "add", "g.md")
	exit, stdout, stderr := runCheck(t, "nocode", "--staged", "--dir", wt)
	if exit != 0 {
		t.Fatalf("a linked worktree was refused at the root test:\n%s", stderr)
	}
	if got, want := stLine(t, stdout, "NOCODE OK"), "NOCODE OK staged=1 clean deny-list=floor\\x20list"; got != want {
		t.Errorf("worktree OK line = %q, want %q", got, want)
	}

	// A submodule: its .git is a file too, and it is its own repository root
	// with its own clean index.
	outer := stLab(t)
	src := t.TempDir()
	stGit(t, src, "init", "-q", "-b", "main")
	mustWrite(t, src, "s.md", "submodule prose\n")
	stGit(t, src, "add", "s.md")
	stGit(t, src, "commit", "-q", "-m", "sub")
	stGit(t, outer, "-c", "protocol.file.allow=always", "submodule", "add", "-q", src, "sub")
	exit, stdout, stderr = runCheck(t, "nocode", "--staged", "--dir", filepath.Join(outer, "sub"))
	if exit != 0 {
		t.Fatalf("a submodule was refused at the root test:\n%s", stderr)
	}
	if got, want := stLine(t, stdout, "NOCODE OK"), "NOCODE OK staged=0 clean deny-list=floor\\x20list"; got != want {
		t.Errorf("submodule OK line = %q, want %q", got, want)
	}
	// And the gitlink the add staged in the outer repository is machinery
	// arriving by reference: classified from its mode alone, its OID never
	// read -- the one matching rule this mode adds to the audit's.
	oexit, _, ostderr := runCheck(t, "nocode", "--staged", "--dir", outer)
	if oexit != 1 || !strings.Contains(ostderr, "NOCODE FAIL sub: submodule gitlink (machinery arriving by reference)") {
		t.Errorf("the staged gitlink exited %d, want 1 with the gitlink finding:\n%s", oexit, ostderr)
	}

	// An unborn HEAD is gated like every later commit: the base detector
	// reaches for the empty tree, and the shebang staged for the FIRST commit
	// is classified against it.
	first := t.TempDir()
	stGit(t, first, "init", "-q", "-b", "main")
	mustWrite(t, first, "runner", "#!/bin/sh\necho hi\n")
	stGit(t, first, "add", "runner")
	exit, _, stderr = runCheck(t, "nocode", "--staged", "--dir", first)
	if exit != 1 || !strings.Contains(stderr, "NOCODE FAIL runner: executable script (shebang)") {
		t.Fatalf("the unborn repository exited %d, want 1 with the staged shebang named:\n%s", exit, stderr)
	}

	// The sha256 form: the empty tree is obtained inside the repository, never
	// hard-coded. The sha1 constant 4b825dc6... names no object a sha256
	// repository knows, so a hard-coded base turns this case into a refusal
	// where the detector classifies.
	s256 := t.TempDir()
	if _, err := stGitOut(s256, "init", "-q", "-b", "main", "--object-format=sha256"); err != nil {
		t.Skipf("this git cannot open a sha256 repository: %v", err)
	}
	mustWrite(t, s256, "runner", "#!/bin/sh\necho hi\n")
	stGit(t, s256, "add", "runner")
	exit, _, stderr = runCheck(t, "nocode", "--staged", "--dir", s256)
	if exit == 2 {
		t.Fatalf("the unborn sha256 repository was refused; the empty tree was not obtained from inside it (a hard-coded sha1 constant does not exist there):\n%s", stderr)
	}
	if exit != 1 || !strings.Contains(stderr, "NOCODE FAIL runner: executable script (shebang)") {
		t.Fatalf("the unborn sha256 repository exited %d, want 1 with the staged shebang named:\n%s", exit, stderr)
	}
	if et := strings.TrimSpace(stGit(t, s256, "hash-object", "-t", "tree", os.DevNull)); et == "4b825dc642cb6eb9a060e54bf8d69288fbee4904" {
		t.Fatalf("this sha256 repository names the sha1 empty tree %q", et)
	}
}
