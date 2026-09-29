package definition

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// objectPresent asks git, with lazy fetching off, whether the object is in the
// repository.
func objectPresent(t *testing.T, dir, oid string) bool {
	t.Helper()
	cmd := exec.Command("git", "cat-file", "-e", oid)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_NO_LAZY_FETCH=1", "GIT_TERMINAL_PROMPT=0")
	return cmd.Run() == nil
}

// B4: pin never reaches the network. In a partial clone a blob that was not fetched
// is a named refusal with a next action, and it is still not fetched afterwards.
func TestPinNeverFetchesIntoAPartialClone(t *testing.T) {
	t.Parallel()
	src, commit := repoWith(t, map[string]string{"a.md": cardFor("a"), "b.md": cardFor("b")})
	testGit(t, src, "config", "uploadpack.allowFilter", "true")
	testGit(t, src, "config", "uploadpack.allowAnySHA1InWant", "true")
	clone := filepath.Join(t.TempDir(), "clone")
	testGit(t, src, "clone", "-q", "--no-checkout", "--filter=blob:none", "file://"+src, clone)
	oidA := testGit(t, clone, "rev-parse", commit+":a.md")
	if objectPresent(t, clone, oidA) {
		t.Fatal("the partial clone already has the blob: the fixture does not test anything")
	}
	// the source is up: a fetch would succeed
	pins, refs := pinL(context.Background(), clone, commit, []string{"a.md", "b.md"}, WithIdentity("example.com/o/r"))
	if pins != nil || len(refs) != 2 {
		t.Fatalf("pins %v refusals %v", pins, Lines(refs))
	}
	for i, p := range []string{"a.md", "b.md"} {
		r, ok := hasRefusal(refs, p, 0, "", CauseMissingObject)
		if !ok || !strings.Contains(r.Next, "fetch") || !strings.Contains(r.Found, "not in this clone") || r.Operation != OpPin {
			t.Errorf("%s: %v", p, Lines(refs))
		}
		wellFormed(t, refs[i])
	}
	if objectPresent(t, clone, oidA) {
		t.Fatal("pin fetched the blob: it is present after the refusal")
	}
	// The same through the one public function.
	as, arefs := Admissions(context.Background(), clone, commit, []string{"a.md"}, WithIdentity("example.com/o/r"))
	if as != nil || arefs == nil || arefs.List[0].Cause != CauseMissingObject {
		t.Fatalf("%v", arefs)
	}
	if objectPresent(t, clone, oidA) {
		t.Fatal("Admissions fetched the blob")
	}
	// A blob that IS in the clone reads fine: fetch it by hand, then pin.
	testGit(t, clone, "fetch", "-q", "origin", oidA)
	if !objectPresent(t, clone, oidA) {
		t.Skip("this git cannot fetch a blob by id from a file remote")
	}
	pins, refs = pinL(context.Background(), clone, commit, []string{"a.md"}, WithIdentity("example.com/o/r"))
	if len(refs) > 0 || string(pins[0].Data) != cardFor("a") {
		t.Fatalf("a present blob: %v", Lines(refs))
	}
}

// The BAD size ls-tree prints for a blob it cannot size is a missing object.
func TestLsTreeBadSizeIsAMissingObject(t *testing.T) {
	t.Parallel()
	e, ok := parseLsEntry("100644 blob " + strings.Repeat("a", 40) + "     BAD\tdir/a.md")
	if !ok || !e.bad || e.path != "dir/a.md" || e.size != 0 {
		t.Fatalf("%+v %v", e, ok)
	}
	if _, ok := parseLsEntry("100644 blob " + strings.Repeat("a", 40) + "     12x\ta.md"); ok {
		t.Fatal("a size that is not a number was read")
	}
	if e, ok := parseLsEntry("040000 tree " + strings.Repeat("a", 40) + "       -\tdir"); !ok || e.bad || e.typ != "tree" {
		t.Fatalf("%+v", e)
	}
}

// B4: only the repository root is the repository directory.
func TestPinAcceptsOnlyTheRepositoryRoot(t *testing.T) {
	t.Parallel()
	dir, commit := repoWith(t, map[string]string{"a.md": cardFor("a"), "sub/b.md": cardFor("b")})
	id := WithIdentity("example.com/o/r")
	refused := func(name, d string) {
		t.Helper()
		pins, refs := pinL(context.Background(), d, commit, []string{"a.md"}, id)
		if pins != nil || len(refs) != 1 || refs[0].Cause != CauseNotRepository {
			t.Errorf("%s: %v", name, Lines(refs))
		}
	}
	refused("the .git directory", filepath.Join(dir, ".git"))
	refused("a subdirectory", filepath.Join(dir, "sub"))
	refused("a directory inside .git", filepath.Join(dir, ".git", "objects"))
	// the root, by any path that names it
	for name, d := range map[string]string{"the root": dir, "with a trailing slash": dir + "/", "with a dot": filepath.Join(dir, "sub", "..")} {
		if pins, refs := pinL(context.Background(), d, commit, []string{"a.md"}, id); len(refs) > 0 || len(pins) != 1 {
			t.Errorf("%s: %v", name, Lines(refs))
		}
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if pins, refs := pinL(context.Background(), link, commit, []string{"a.md"}, id); len(refs) > 0 || len(pins) != 1 {
		t.Errorf("a symlink to the root: %v", Lines(refs))
	}
	// a bare repository is its own root; a directory inside it is not
	bare := filepath.Join(t.TempDir(), "bare.git")
	testGit(t, dir, "clone", "-q", "--bare", dir, bare)
	if pins, refs := pinL(context.Background(), bare, commit, []string{"a.md"}, id); len(refs) > 0 || len(pins) != 1 {
		t.Errorf("a bare repository: %v", Lines(refs))
	}
	if pins, refs := pinL(context.Background(), filepath.Join(bare, "objects"), commit, []string{"a.md"}, id); pins != nil || len(refs) != 1 || refs[0].Cause != CauseNotRepository {
		t.Errorf("inside a bare repository: %v", Lines(refs))
	}
	// a linked worktree is a root
	wt := filepath.Join(t.TempDir(), "wt")
	testGit(t, dir, "worktree", "add", "-q", "-b", "other", wt)
	if pins, refs := pinL(context.Background(), wt, commit, []string{"a.md"}, id); len(refs) > 0 || len(pins) != 1 {
		t.Errorf("a linked worktree: %v", Lines(refs))
	}
}

// B4: an unreachable commit is accepted, and the doc says so: a commit object that
// exists is what is pinned, whatever refs do or do not name it.
func TestPinAcceptsAnUnreachableCommit(t *testing.T) {
	t.Parallel()
	dir, commit := repoWith(t, map[string]string{"a.md": cardFor("a")})
	tree := testGit(t, dir, "rev-parse", commit+"^{tree}")
	orphan := testGit(t, dir, "commit-tree", tree, "-m", "no ref names me")
	if got := testGit(t, dir, "branch", "--contains", orphan); got != "" {
		t.Fatalf("the commit is reachable from %q", got)
	}
	pins, refs := pinL(context.Background(), dir, orphan, []string{"a.md"}, WithIdentity("example.com/o/r"))
	if len(refs) > 0 || len(pins) != 1 || pins[0].Commit != orphan {
		t.Fatalf("%v", Lines(refs))
	}
}

// B4: a path that is not valid UTF-8 refuses, by name, with no git call.
func TestPinRefusesInvalidUTF8Paths(t *testing.T) {
	t.Parallel()
	dir, commit := repoWith(t, map[string]string{"a.md": cardFor("a")})
	g := &gitRun{dir: dir}
	pins, refs := pinG(context.Background(), g, dir, commit, []string{"a\xffb.md"}, WithIdentity("example.com/o/r"))
	if pins != nil || len(refs) != 1 || refs[0].Cause != CauseInvalidUTF8 || g.calls != 0 {
		t.Fatalf("%v (%d git calls)", Lines(refs), g.calls)
	}
	wellFormed(t, refs[0])
}

// B4: git's own error text is never copied into a refusal: a known failure maps to
// a cause, any other says git failed with its exit status.
func TestGitsOwnWordsAreNeverCopied(t *testing.T) {
	t.Parallel()
	dir, commit := repoWith(t, map[string]string{"a.md": cardFor("a")})
	// a broken configuration makes git fail with words of its own
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("[core\n\tGARBLED-WORD-FROM-GIT = = =\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pins, refs := pinL(context.Background(), dir, commit, []string{"a.md"}, WithIdentity("example.com/o/r"))
	if pins != nil || len(refs) != 1 || refs[0].Cause != CauseGitFailed {
		t.Fatalf("%v", Lines(refs))
	}
	line := refs[0].String()
	if strings.Contains(line, "GARBLED") || strings.Contains(line, "config") || !strings.Contains(refs[0].Found, "exit status") {
		t.Fatalf("git's words were copied or the exit status is missing: %s", line)
	}
	// a directory that is not a repository is a named cause, not git's sentence
	_, refs = pinL(context.Background(), t.TempDir(), commit, []string{"a.md"}, WithIdentity("example.com/o/r"))
	if len(refs) != 1 || refs[0].Cause != CauseNotRepository || strings.Contains(refs[0].String(), "fatal") {
		t.Fatalf("%v", Lines(refs))
	}
}

// B4 and A2: a credential in an origin is never echoed by a refusal or a record.
func TestNoCredentialFromAnOriginIsEverEchoed(t *testing.T) {
	t.Parallel()
	const secret = "ghp_SECRETTOKEN"
	for name, origin := range map[string]string{
		"https with userinfo": strings.ReplaceAll("https://x-access-token:TOKEN@example.com/o/r.git", "TOKEN", secret),
		"a query":             "https://example.com/o/r?token=" + secret,
		"an unusual scheme":   "ftp://" + secret + "@example.com/o/r",
		"a local path":        "/srv/" + secret + "/r.git",
		"a file url":          "file:///srv/" + secret,
		"a path segment":      "https://example.com/" + secret + "/../r",
		"a fragment":          "https://example.com/o/r#" + secret,
		"a space in the path": "https://example.com/o/r " + secret,
	} {
		dir, commit := repoWith(t, map[string]string{"a.md": cardFor("a")})
		testGit(t, dir, "remote", "add", "origin", origin)
		as, refs := Admissions(context.Background(), dir, commit, []string{"a.md"})
		var out string
		if refs != nil {
			out = strings.Join(refs.Lines(), "\n")
		}
		for _, a := range as {
			out += string(EncodeAdmissions([]Admission{a})[0])
		}
		if strings.Contains(out, secret) || strings.Contains(out, "SECRET") {
			t.Errorf("%s: a credential is echoed:\n%s", name, out)
		}
		if refs == nil && (name == "an unusual scheme" || name == "a local path" || name == "a file url") {
			t.Errorf("%s: an origin with no identity was accepted", name)
		}
	}
	// a supplied identity that is a URL with a credential is refused without quoting it
	dir, commit := repoWith(t, map[string]string{"a.md": cardFor("a")})
	_, refs := Admissions(context.Background(), dir, commit, []string{"a.md"}, WithIdentity(strings.ReplaceAll("https://u:TOKEN@example.com/o/r", "TOKEN", secret)))
	if refs == nil || refs.List[0].Cause != CauseInvalidRepository || strings.Contains(strings.Join(refs.Lines(), ""), "SECRET") {
		t.Fatalf("%v", refs)
	}
}
