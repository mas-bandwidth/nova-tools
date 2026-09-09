package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The union, both ways: it keeps every line of both sides in a stable order, and it does
// not double a line both sides already have. An INDEX and a RECEIPTS are sets of lines
// whose order is history, so "ours then whatever of theirs is new" is the whole of it.
func TestUnionLinesKeepsBothSidesAndDoublesNothing(t *testing.T) {
	ours := "a\nb\n"
	theirs := "a\nc\n"
	if got, want := UnionLines(ours, theirs), "a\nb\nc\n"; got != want {
		t.Fatalf("UnionLines = %q, want %q", got, want)
	}
	// Identical sides are one side.
	if got, want := UnionLines(ours, ours), "a\nb\n"; got != want {
		t.Fatalf("UnionLines over one content twice = %q, want %q", got, want)
	}
	// A side that is missing entirely contributes nothing and loses nothing.
	if got, want := UnionLines("", theirs), "a\nc\n"; got != want {
		t.Fatalf("UnionLines with an empty side = %q, want %q", got, want)
	}
	if got := UnionLines("", ""); got != "" {
		t.Fatalf("UnionLines over nothing = %q, want empty", got)
	}
	// Blank lines and CRLF are folded, because every reader of these files already skips
	// the first and this tool never writes the second.
	if got, want := UnionLines("a\n\nb\n", "a\r\nc\r\n"), "a\nb\nc\n"; got != want {
		t.Fatalf("UnionLines = %q, want %q", got, want)
	}
}

// The attribute file, both ways: it is written when the rules are not there, and writing it
// again changes nothing. It is APPENDED to a file the bus already had rather than
// replacing it, because that file belongs to the bus and this tool owns two lines of it.
func TestEnsureMergeAttributes(t *testing.T) {
	root := t.TempDir()
	wrote, err := EnsureMergeAttributes(root)
	if err != nil {
		t.Fatal(err)
	}
	if !wrote {
		t.Fatal("the first call wrote nothing; a bus with no .gitattributes has no union rule")
	}
	first, err := os.ReadFile(filepath.Join(root, AttributesName))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"from-*/INDEX merge=union", "from-*/RECEIPTS merge=union"} {
		if !strings.Contains(string(first), want) {
			t.Fatalf("%s does not carry %q:\n%s", AttributesName, want, first)
		}
	}
	// The second call is a no-op: no write, and not a second copy of the rules.
	wrote, err = EnsureMergeAttributes(root)
	if err != nil {
		t.Fatal(err)
	}
	if wrote {
		t.Fatal("the rules were written twice; every send after the first would touch a shared file for nothing")
	}
	again, err := os.ReadFile(filepath.Join(root, AttributesName))
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(first) {
		t.Fatalf("the second call changed the file:\n%s\n---\n%s", first, again)
	}

	// A bus that already has a .gitattributes of its own keeps it, and gains the rules.
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, AttributesName), []byte("*.md text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if wrote, err := EnsureMergeAttributes(other); err != nil || !wrote {
		t.Fatalf("EnsureMergeAttributes over an existing file = %v %v", wrote, err)
	}
	got, err := os.ReadFile(filepath.Join(other, AttributesName))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"*.md text", "from-*/INDEX merge=union", "from-*/RECEIPTS merge=union"} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("the bus's own rule or the tool's is missing:\n%s", got)
		}
	}
}

// THE ABORT THAT FAILS, which used to return as though it had worked. `git rebase --abort`
// can fail -- here because the rebase state directory cannot be removed -- and the run then
// returned with the checkout STILL in a rebase, so every later verb refused for a reason
// that was true and unhelpful and nothing said the real one. The state is checked after the
// attempt, and the refusal carries the recovery that works on the state it found.
func TestAnAbortThatFailsIsRefusedWithTheRecovery(t *testing.T) {
	hermetic(t)
	dir := t.TempDir()
	if out, err := git(dir, "init", "--quiet", "-b", "main"); err != nil {
		t.Fatalf("init: %v %s", err, out)
	}
	commit := func(content, message string) {
		t.Helper()
		write(t, dir, "x.txt", content)
		if out, err := git(dir, "add", "-A"); err != nil {
			t.Fatalf("add: %v %s", err, out)
		}
		if out, err := git(dir, append(identityArgs(testIdentity["Ada"]), "commit", "-q", "-m", message)...); err != nil {
			t.Fatalf("commit: %v %s", err, out)
		}
	}
	commit("base\n", "base")
	if out, err := git(dir, "checkout", "-q", "-b", "other"); err != nil {
		t.Fatalf("checkout: %v %s", err, out)
	}
	commit("theirs\n", "theirs")
	if out, err := git(dir, "checkout", "-q", "main"); err != nil {
		t.Fatalf("checkout: %v %s", err, out)
	}
	commit("mine\n", "mine")
	// A rebase that conflicts, so the checkout is genuinely mid-rebase.
	if _, err := git(dir, append(identityArgs(testIdentity["Ada"]), "rebase", "other")...); err == nil {
		t.Fatal("the fixture did not conflict")
	}
	where, still := inRebase(dir)
	if !still {
		t.Fatal("the fixture is not in a rebase")
	}

	// Make the abort fail: git cannot remove the entries of a directory it cannot write.
	if err := os.Chmod(where, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(where, 0o700) })

	err := abortRebase(dir)
	if err == nil {
		t.Fatal("an abort that failed reported success; the checkout is still in a rebase and every later verb will refuse for the wrong reason")
	}
	for _, want := range []string{"could not be aborted", "STILL in a rebase", where, "git rebase --abort", "git reset --hard ORIG_HEAD"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not say %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "\n") {
		t.Fatalf("the refusal is more than one line: %q", err.Error())
	}

	// The other way: with the directory writable again, the abort works and says nothing.
	if err := os.Chmod(where, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := abortRebase(dir); err != nil {
		t.Fatalf("an abort that worked was reported as a failure: %v", err)
	}
	if _, still := inRebase(dir); still {
		t.Fatal("the checkout is still in a rebase after a successful abort")
	}
	if _, err := CurrentBranch(dir); err != nil {
		t.Fatalf("the checkout is not on a branch after the abort: %v", err)
	}
}

// The recovery a refusal offers is a sentence a person types, so the sentence is read back
// out of the code rather than trusted: `git push` alone cannot land a branch that is ahead
// of a remote which has moved, and that is exactly the state these refusals are about.
func TestNoRefusalRecommendsABarePush(t *testing.T) {
	if !strings.Contains(pullRebaseAdvice, "git pull --rebase && git push") {
		t.Fatalf("the shared advice is %q, which is not the two commands that land a branch that is behind", pullRebaseAdvice)
	}
	for _, bad := range []string{"push or drop them first", "run git push"} {
		if strings.Contains(pullRebaseAdvice, bad) {
			t.Fatalf("the advice still says %q, which does not work against a remote that has moved", bad)
		}
	}
}
