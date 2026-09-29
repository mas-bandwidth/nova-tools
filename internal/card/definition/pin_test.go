package definition

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var bg = context.Background()

func TestPinReadsCommittedBlobs(t *testing.T) {
	t.Parallel()
	f := sharedRepo(t)
	pins, refs := Pin(bg, f.dir, f.c1, []string{"dir/b.md", "a.md"}) // the identity is the origin's
	if len(refs) > 0 {
		t.Fatalf("%v", Lines(refs))
	}
	if len(pins) != 2 || pins[0].Path != "dir/b.md" || pins[1].Path != "a.md" {
		t.Fatalf("order: %+v", pins)
	}
	for _, p := range pins {
		if p.Repository != "example.com/Owner/Repo" {
			t.Errorf("identity %q: the scheme, user, credentials and .git are dropped, the host is lower case", p.Repository)
		}
		if p.Commit != f.c1 || len(p.Commit) != 40 {
			t.Errorf("commit %q", p.Commit)
		}
		if want := testGit(t, f.dir, "rev-parse", f.c1+":"+p.Path); p.ObjectID != want {
			t.Errorf("%s object %s, git says %s", p.Path, p.ObjectID, want)
		}
		if p.SHA256 != sum(p.Data) || p.Size != len(p.Data) || p.Mode != "100644" {
			t.Errorf("%+v", p)
		}
	}
	if string(pins[0].Data) != "committed b\n" || string(pins[1].Data) != "committed a\n" {
		t.Errorf("bytes %q %q", pins[0].Data, pins[1].Data)
	}
	if sources := Sources(pins); sources[1].Name != "a.md" || string(sources[1].Data) != "committed a\n" {
		t.Errorf("sources %+v", sources)
	}
}

func TestPinReadsCommittedBytesNotWorkingFiles(t *testing.T) {
	t.Parallel()
	dir, commit := newRepo(t)
	write(t, dir, "a.md", "modified but not committed\n")
	write(t, dir, "dir/b.md", "staged but not committed\n")
	testGit(t, dir, "add", "dir/b.md")
	pins, refs := Pin(bg, dir, commit, []string{"a.md", "dir/b.md"})
	if len(refs) > 0 {
		t.Fatalf("%v", Lines(refs))
	}
	if string(pins[0].Data) != "committed a\n" || string(pins[1].Data) != "committed b\n" {
		t.Fatalf("read working or staged bytes: %q %q", pins[0].Data, pins[1].Data)
	}
	// A file deleted in the working tree is still at the commit.
	if err := os.Remove(filepath.Join(dir, "a.md")); err != nil {
		t.Fatal(err)
	}
	if pins, refs := Pin(bg, dir, commit, []string{"a.md"}); len(refs) > 0 || string(pins[0].Data) != "committed a\n" {
		t.Fatalf("uncommitted delete: %v", Lines(refs))
	}
	// And a file that exists only in the working tree is not at the commit.
	write(t, dir, "new.md", "untracked\n")
	if _, refs := Pin(bg, dir, commit, []string{"new.md"}); len(refs) != 1 || refs[0].Cause != CauseMissingPath {
		t.Fatalf("%v", Lines(refs))
	}
}

func TestPinRefusesPerPathAndNamesEachOne(t *testing.T) {
	t.Parallel()
	f := sharedRepo(t)
	pins, refs := f.pin([]string{"a.md", "link.md", "dirlink/b.md", "dir", "sub", "absent.md"}, f.c2)
	if pins != nil {
		t.Fatal("pins beside refusals")
	}
	want := map[string]Cause{
		"link.md":      CauseSymlink,
		"dirlink/b.md": CauseMissingPath, // git does not walk a symlinked directory
		"dir":          CauseNotBlob,
		"sub":          CauseNotBlob, // a submodule entry
		"absent.md":    CauseMissingPath,
	}
	if len(refs) != len(want) {
		t.Fatalf("%v", Lines(refs))
	}
	for p, c := range want {
		r, ok := hasRefusal(refs, p, 0, "", c)
		if !ok {
			t.Errorf("%s: no %s in %v", p, c, Lines(refs))
			continue
		}
		wellFormed(t, r)
	}
}

func TestPinDeletedAndRenamedFiles(t *testing.T) {
	t.Parallel()
	f := sharedRepo(t)
	// c3 deleted dir/b.md and renamed a.md to renamed.md.
	_, refs := f.pin([]string{"dir/b.md", "a.md", "renamed.md"}, f.c3)
	if len(refs) != 2 {
		t.Fatalf("%v", Lines(refs))
	}
	for _, p := range []string{"dir/b.md", "a.md"} {
		r, ok := hasRefusal(refs, p, 0, "", CauseMissingPath)
		if !ok || !strings.Contains(r.Found, f.c3) {
			t.Errorf("%s: %v", p, Lines(refs))
		}
	}
	// The earlier commit still has them, and the rename kept the object.
	before, refs := f.pin([]string{"dir/b.md", "a.md"}, f.c2)
	if len(refs) > 0 || string(before[0].Data) != "committed b\n" {
		t.Fatalf("%v", Lines(refs))
	}
	after, refs := f.pin([]string{"renamed.md"}, f.c3)
	if len(refs) > 0 {
		t.Fatalf("%v", Lines(refs))
	}
	if after[0].ObjectID != before[1].ObjectID || after[0].SHA256 != before[1].SHA256 || after[0].Commit == before[1].Commit {
		t.Fatalf("same bytes, same object, different commit: %+v %+v", before[1], after[0])
	}
}

func TestPinRefusesBadInputBeforeAnyGit(t *testing.T) {
	t.Parallel()
	f := sharedRepo(t)
	commit := f.c1

	commits := []struct {
		name, commit string
		cause        Cause
	}{
		{"abbreviated", commit[:12], CauseInvalidCommit},
		{"a ref name", "HEAD", CauseInvalidCommit},
		{"a branch", "main", CauseInvalidCommit},
		{"upper case", strings.ToUpper(commit), CauseInvalidCommit},
		{"empty", "", CauseInvalidCommit},
		{"41 digits", commit + "0", CauseInvalidCommit},
		{"a flag", "--all", CauseInvalidCommit},
		{"unknown", strings.Repeat("1", 40), CauseUnknownCommit},
		{"a tree", f.tree, CauseNotCommit},
		{"an annotated tag", f.tag, CauseNotCommit},
	}
	for _, c := range commits {
		t.Run("commit "+c.name, func(t *testing.T) {
			t.Parallel()
			pins, refs := f.pin([]string{"a.md"}, c.commit)
			if pins != nil || len(refs) != 1 || refs[0].Cause != c.cause {
				t.Fatalf("%v", Lines(refs))
			}
			wellFormed(t, refs[0])
			if refs[0].Operation != OpPin {
				t.Errorf("operation %q", refs[0].Operation)
			}
		})
	}

	paths := []struct {
		name  string
		paths []string
		cause Cause
		on    string
	}{
		{"parent segment", []string{"../a.md"}, CausePathEscapes, "../a.md"},
		{"climbing inside", []string{"dir/../../a.md"}, CausePathEscapes, "dir/../../a.md"},
		{"absolute", []string{"/etc/passwd"}, CausePathEscapes, "/etc/passwd"},
		{"drive letter", []string{"C:/x"}, CausePathEscapes, "C:/x"},
		{"empty segment", []string{"dir//b.md"}, CauseInvalidPath, "dir//b.md"},
		{"dot segment", []string{"./a.md"}, CauseInvalidPath, "./a.md"},
		{"trailing slash", []string{"dir/"}, CauseInvalidPath, "dir/"},
		{"git directory", []string{".git/config"}, CauseInvalidPath, ".git/config"},
		{"backslash", []string{`dir\b.md`}, CauseInvalidPath, `dir\b.md`},
		{"control character", []string{"a\n.md"}, CauseInvalidPath, "a\n.md"},
		{"empty path", []string{""}, CauseInvalidPath, ""},
		{"path over the bound", []string{strings.Repeat("d/", MaxPathBytes)}, CauseInvalidPath, strings.Repeat("d/", MaxPathBytes)},
		{"a duplicate", []string{"a.md", "dir/b.md", "a.md"}, CauseDuplicatePath, "a.md"},
	}
	for _, c := range paths {
		t.Run("path "+c.name, func(t *testing.T) {
			t.Parallel()
			g := &gitRun{dir: f.dir}
			pins, refs := pin(bg, g, f.dir, commit, c.paths)
			if pins != nil {
				t.Fatal("pins beside a refusal")
			}
			if g.calls != 0 {
				t.Errorf("%d git calls before the input was refused", g.calls)
			}
			if _, ok := hasRefusal(refs, c.on, 0, "", c.cause); !ok {
				t.Fatalf("%v", Lines(refs))
			}
			for _, r := range refs {
				wellFormed(t, r)
			}
		})
	}
}

func TestPinRefusesWhatIsNotARepository(t *testing.T) {
	t.Parallel()
	f := sharedRepo(t)
	file := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"an empty directory":       t.TempDir(),
		"a subdirectory of a repo": filepath.Join(f.dir, "dir"),
		"a missing directory":      filepath.Join(t.TempDir(), "absent"),
		"a file":                   file,
	}
	for name, d := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			pins, refs := Pin(bg, d, f.c1, []string{"a.md"}, WithIdentity("example.com/o/r"))
			if pins != nil || len(refs) != 1 || refs[0].Cause != CauseNotRepository {
				t.Fatalf("%v", Lines(refs))
			}
			wellFormed(t, refs[0])
		})
	}
}

func TestPinRepositoryIdentity(t *testing.T) {
	t.Parallel()
	dir, commit := newRepo(t)
	// The caller's identity wins over the origin, and is validated.
	pins, refs := Pin(bg, dir, commit, []string{"a.md"}, WithIdentity("example.com/x/y"))
	if len(refs) > 0 || pins[0].Repository != "example.com/x/y" {
		t.Fatalf("%v", Lines(refs))
	}
	for _, bad := range []string{"https://example.com/x/y", "user@example.com/x/y", "example.com", "example.com/../y", "a b/c", strings.Repeat("a", 300) + "/x"} {
		if _, refs := Pin(bg, dir, commit, []string{"a.md"}, WithIdentity(bad)); len(refs) != 1 || refs[0].Cause != CauseIdentityInvalid {
			t.Errorf("%q: %v", bad, Lines(refs))
		}
	}
	// A local-path origin gives no identity.
	testGit(t, dir, "remote", "set-url", "origin", "/srv/git/repo.git")
	if _, refs := Pin(bg, dir, commit, []string{"a.md"}); len(refs) != 1 || refs[0].Cause != CauseIdentityMissing {
		t.Fatalf("%v", Lines(refs))
	}
	// No origin is refused unless the caller supplies an identity.
	testGit(t, dir, "remote", "remove", "origin")
	_, refs = Pin(bg, dir, commit, []string{"a.md"})
	if len(refs) != 1 || refs[0].Cause != CauseIdentityMissing || !strings.Contains(refs[0].Next, "WithIdentity") {
		t.Fatalf("%v", Lines(refs))
	}
	wellFormed(t, refs[0])
	pins, refs = Pin(bg, dir, commit, []string{"a.md"}, WithIdentity("example.com/owner/name"))
	if len(refs) > 0 || pins[0].Repository != "example.com/owner/name" {
		t.Fatalf("%v", Lines(refs))
	}
}

func TestNormalizeOrigin(t *testing.T) {
	t.Parallel()
	good := map[string]string{
		"https://example.com/owner/repo.git":           "example.com/owner/repo",
		"https://example.com/owner/repo":               "example.com/owner/repo",
		"https://example.com/owner/repo/":              "example.com/owner/repo",
		"https://user:pass@EXAMPLE.com/Owner/Repo.git": "example.com/Owner/Repo",
		"git@example.com:owner/repo.git":               "example.com/owner/repo",
		"example.com:owner/repo":                       "example.com/owner/repo",
		"ssh://git@example.com/owner/repo.git":         "example.com/owner/repo",
		"ssh://git@git.example.com:2222/team/sub/repo": "git.example.com:2222/team/sub/repo",
		"git@host.example:/owner/repo.git":             "host.example/owner/repo",
		"http://example.com/a/b":                       "example.com/a/b",
		"  https://example.com/a/b.git \n":             "example.com/a/b",
	}
	for in, want := range good {
		if got, why := normalizeOrigin(in); got != want || why != "" {
			t.Errorf("%q: got %q (%s), want %q", in, got, why, want)
		}
	}
	for _, in := range []string{"", "/srv/repo.git", "./repo", "../repo", "~/repo", "file:///srv/repo.git", "https://example.com/a/b?x=1", "https://example.com/a/b#f",
		"https://example.com", "https://example.com/", "ftp://example.com/a/b", "C:/repo", "nonsense", "https:///a/b", "https://example.com/a b/c"} {
		if got, why := normalizeOrigin(in); got != "" || why == "" {
			t.Errorf("%q: accepted as %q", in, got)
		}
	}
}

func TestPinBounds(t *testing.T) {
	t.Parallel()
	f := sharedRepo(t)
	t.Run("no paths", func(t *testing.T) {
		t.Parallel()
		if _, refs := f.pin(nil, f.c1); len(refs) != 1 || refs[0].Cause != CauseEmptyArray {
			t.Fatalf("%v", Lines(refs))
		}
	})
	t.Run("too many paths names the limit", func(t *testing.T) {
		t.Parallel()
		var paths []string
		for i := 0; i <= MaxFiles; i++ {
			paths = append(paths, "p"+strings.Repeat("x", i))
		}
		_, refs := f.pin(paths, f.c1)
		if len(refs) != 1 || refs[0].Cause != CauseTooManyFiles || !strings.Contains(refs[0].Found, "128") {
			t.Fatalf("%v", Lines(refs))
		}
	})
	t.Run("a blob over the bound names the limit", func(t *testing.T) {
		t.Parallel()
		_, refs := f.pin([]string{"exact.md", "big.md"}, f.c1)
		r, ok := hasRefusal(refs, "big.md", 0, "", CauseBlobTooLarge)
		if !ok || len(refs) != 1 || !strings.Contains(r.Found, "262144") {
			t.Fatalf("%v", Lines(refs))
		}
		if pins, refs := f.pin([]string{"exact.md"}, f.c1); len(refs) > 0 || pins[0].Size != MaxCardBytes {
			t.Fatalf("a blob at the bound is fine: %v", Lines(refs))
		}
	})
	t.Run("the array over the byte bound", func(t *testing.T) {
		t.Parallel()
		_, refs := f.pin(f.huge, f.c1)
		if len(refs) != 1 || refs[0].Cause != CauseTotalTooLarge || !strings.Contains(refs[0].Found, "8388608") {
			t.Fatalf("%v", Lines(refs))
		}
	})
}

func TestPinUsesAFixedNumberOfGitCalls(t *testing.T) {
	t.Parallel()
	f := sharedRepo(t)
	counts := map[int]int{}
	for _, n := range []int{1, 10, 100} {
		g := &gitRun{dir: f.dir}
		pins, refs := pin(bg, g, f.dir, f.c1, f.many[:n]) // no identity: the origin is read
		if len(refs) > 0 || len(pins) != n {
			t.Fatalf("n=%d: %v", n, Lines(refs))
		}
		counts[n] = g.calls
	}
	if counts[1] != counts[10] || counts[10] != counts[100] || counts[100] > GitCalls {
		t.Fatalf("git invocations grow with the array or pass the constant %d: %v", GitCalls, counts)
	}
	g := &gitRun{dir: f.dir}
	if _, refs := pin(bg, g, f.dir, f.c1, f.many[:3], WithIdentity("example.com/o/r")); len(refs) > 0 || g.calls != counts[1]-1 {
		t.Fatalf("with an identity the origin lookup is skipped: %d calls, %v", g.calls, Lines(refs))
	}
}

func TestPinGitCallsRunUnderADeadline(t *testing.T) {
	t.Parallel()
	f := sharedRepo(t)
	ctx, cancel := context.WithCancel(bg)
	cancel()
	pins, refs := Pin(ctx, f.dir, f.c1, []string{"a.md"})
	if pins != nil || len(refs) != 1 || refs[0].Cause != CauseTimeout {
		t.Fatalf("%v", Lines(refs))
	}
	wellFormed(t, refs[0])
	if DefaultGitTimeout <= 0 {
		t.Fatal("no default deadline")
	}
}

func TestGitEnvironmentIsScrubbed(t *testing.T) {
	t.Parallel()
	env := gitEnv()
	has := func(kv string) bool {
		for _, e := range env {
			if e == kv {
				return true
			}
		}
		return false
	}
	for _, want := range []string{"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_LITERAL_PATHSPECS=1", "GIT_NO_REPLACE_OBJECTS=1"} {
		if !has(want) {
			t.Errorf("environment lacks %s", want)
		}
	}
	for _, e := range env {
		for _, banned := range []string{"GIT_DIR=", "GIT_WORK_TREE=", "GIT_INDEX_FILE=", "GIT_OBJECT_DIRECTORY=", "GIT_ALTERNATE_OBJECT_DIRECTORIES="} {
			if strings.HasPrefix(e, banned) {
				t.Errorf("environment carries %s", e)
			}
		}
	}
}

func TestPinTwoPathsWithTheSameBytes(t *testing.T) {
	t.Parallel()
	f := sharedRepo(t)
	pins, refs := f.pin([]string{"y.md", "x.md"}, f.c1)
	if len(refs) > 0 {
		t.Fatalf("%v", Lines(refs))
	}
	if pins[0].ObjectID != pins[1].ObjectID || pins[0].Path == pins[1].Path || string(pins[0].Data) != "same bytes\n" || string(pins[1].Data) != "same bytes\n" {
		t.Fatalf("%+v", pins)
	}
	pins[0].Data[0] = 'X'
	if pins[1].Data[0] != 's' {
		t.Fatalf("the two pins share their bytes")
	}
}

func TestPinThenParseThenAdmitEndToEnd(t *testing.T) {
	t.Parallel()
	f := sharedRepo(t)
	pins, refs := Pin(bg, f.dir, f.c1, f.cards) // the identity is the origin's
	if len(refs) > 0 {
		t.Fatalf("%v", Lines(refs))
	}
	defs := mustParse(t, Sources(pins))
	if _, refs := Validate(defs); len(refs) > 0 {
		t.Fatalf("%v", Lines(refs))
	}
	as, refs := Admissions(defs, pins)
	if len(refs) > 0 {
		t.Fatalf("%v", Lines(refs))
	}
	for i, a := range as {
		if a.Repository != "example.com/Owner/Repo" || a.Commit != f.c1 || a.ObjectID != pins[i].ObjectID || a.DefinitionDigest != pins[i].SHA256 {
			t.Errorf("%+v", a)
		}
	}
	// The same record as the golden one, except the commit and repository, which are this fixture's.
	enc, _ := EncodeAdmissions(as)
	want := strings.TrimSuffix(string(readTestdata(t, "admissions/card-beta.json")), "\n")
	want = strings.ReplaceAll(strings.ReplaceAll(want, fixtureCommit, f.c1), fixtureRepo, "example.com/Owner/Repo")
	if string(enc[1]) != want {
		t.Errorf("record differs from the golden one:\n got %s\nwant %s", enc[1], want)
	}
}
