package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// caseFoldingDir is a directory plus the filesystem's own answer about whether two spellings of
// one name are one name there. THE FILESYSTEM DECIDES WHETHER A FOLD TEST CAN RUN, NOT
// `runtime.GOOS`: APFS can be formatted case-sensitive and a linux mount can fold, so the test
// WRITES a file and asks for it back in another case. Where the answer is no, the
// misconfiguration these tests are about cannot exist on this machine and they skip by name.
func caseFoldingDir(t *testing.T) (string, bool) {
	t.Helper()
	dir := t.TempDir()
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	if err := os.WriteFile(filepath.Join(dir, "CaseProbe"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(filepath.Join(dir, "caseprobe"))
	return dir, err == nil
}

// DEMANDED (SPEC-SANDBOX.md rule 6, "the secret is never inside either list"), and the second
// site #145 named: `Inside` answered with `strings.HasPrefix`, a case-SENSITIVE comparison,
// while APFS is case-INsensitive by default. A `--secret` spelled in another case than the
// `--read` it actually sits inside passed the very check that exists to catch that
// misconfiguration, and the probe reported a pass.
//
// `filepath.EvalSymlinks` does not fold case on darwin, so resolving the path does not close
// it; the answer has to come from the filesystem, `os.SameFile` over the ancestors.
func TestInsideAsksTheFilesystemNotAStringPrefix(t *testing.T) {
	dir, folds := caseFoldingDir(t)
	if !folds {
		t.Skipf("the filesystem under %s is case-SENSITIVE: caseprobe is not CaseProbe, so %s/Read and %s/read are two directories here and the fold this test is about cannot happen", dir, dir, dir)
	}
	read := filepath.Join(dir, "read")
	if err := os.MkdirAll(filepath.Join(read, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The write itself goes through the fold: there is no `<dir>/Read`, only `<dir>/read`,
	// so the file this path names IS the file inside the read set.
	secret := filepath.Join(dir, "Read", "env")
	if err := os.WriteFile(secret, []byte("not-a-real-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !Inside(secret, read) {
		t.Errorf("a secret at %s is reported outside %s, which is the same directory on this filesystem", secret, read)
	}
	if !Inside(filepath.Join(dir, "READ", "sub", "deeper", "env"), read) {
		t.Error("a path several levels inside a folded spelling is reported outside")
	}
	// `Inside` deliberately answers true for the directory ITSELF, and that holds for a
	// spelling of it the filesystem folds: rule 9 asks this of HOME, and a HOME that IS a
	// --write spelled another way is inside the write set, not `home_outside`.
	if !Inside(filepath.Join(dir, "Read"), read) {
		t.Error("the directory itself, spelled in another case, is reported outside itself")
	}
	if !Inside(read, read) {
		t.Error("the directory itself is reported outside itself")
	}
	// AND THE FOLD DOES NOT MAKE A NEIGHBOUR INSIDE. A sibling whose name merely starts the
	// same way is outside, in every spelling: the repair is `os.SameFile`, not a lowercased
	// prefix, and a lowercased prefix would answer this one wrong.
	for _, outside := range []string{
		filepath.Join(dir, "readme", "env"),
		filepath.Join(dir, "Readme", "env"),
		filepath.Join(dir, "read2", "env"),
		filepath.Join(dir, "env"),
	} {
		if err := os.MkdirAll(filepath.Dir(outside), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(outside, []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if Inside(outside, read) {
			t.Errorf("%s is reported inside %s", outside, read)
		}
	}
}

// The prefix is the ONLY answer available for a directory that is not there -- nothing has an
// inode -- and there it is asked case-insensitively only where the filesystem is measured to
// fold. Every caller path of this package exists by rule 5, so this is defence in depth, and it
// is asserted here rather than assumed.
func TestInsideFallsBackToTheFoldedPrefixForADirectoryThatIsNotThere(t *testing.T) {
	dir, folds := caseFoldingDir(t)
	absent := filepath.Join(dir, "gone")
	if got := Inside(filepath.Join(dir, "Gone", "env"), absent); got != folds {
		t.Errorf("a path inside an absent directory spelled in another case = %v, the filesystem folds = %v", got, folds)
	}
	if !Inside(filepath.Join(absent, "env"), absent) {
		t.Error("a path inside an absent directory, spelled the same way, is reported outside it")
	}
	if Inside(filepath.Join(dir, "gonebeyond", "env"), absent) {
		t.Error("a neighbour of an absent directory is reported inside it")
	}
	if got := dirFoldsCase(dir); got != folds {
		t.Errorf("dirFoldsCase = %v, the filesystem folds = %v", got, folds)
	}
	// A name with no letter to re-case is answered by the written probe rather than by a
	// read-only look at its own spelling.
	numeric := filepath.Join(dir, "123")
	if err := os.MkdirAll(numeric, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := dirFoldsCase(numeric); got != folds {
		t.Errorf("dirFoldsCase of a numeric name = %v, the filesystem folds = %v", got, folds)
	}
}

// insideAny is what rules 9 and 13 ask of HOME and --cwd, so the fold reaches those two
// answers -- and there a fold read as "outside" is a refusal of a configuration that is SOUND:
// `HOME` inside the write set under a spelling the filesystem folds was `home_outside`, and a
// wall that refuses the run a person configured correctly is the other half of rule 1.
func TestRulesNineAndThirteenAskTheFilesystemToo(t *testing.T) {
	dir, folds := caseFoldingDir(t)
	if !folds {
		t.Skipf("the filesystem under %s is case-SENSITIVE: a HOME spelled in another case is a different directory here", dir)
	}
	write := filepath.Join(dir, "w")
	home := filepath.Join(write, "home")
	read := filepath.Join(dir, "r")
	for _, d := range []string{home, read} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if !insideAny(filepath.Join(dir, "W", "home"), []string{read, write}) {
		t.Error("insideAny says a folded spelling of a path inside the write set is inside none of it")
	}
	// Rule 9, through Build: HOME is the job's data home inside a --write, spelled in
	// another case. It is the same directory, so there is nothing to refuse.
	folded := filepath.Join(dir, "W", "home")
	p, bad := Build(Input{Reads: []string{read}, Writes: []string{write}, Home: folded, Argv: []string{anExecutable(t)}})
	if len(bad) > 0 {
		t.Fatalf("a HOME inside the write set spelled in another case was refused: %v", bad)
	}
	if p.Home == "" {
		t.Error("the policy carries no HOME")
	}
	// Rule 13, the same question about --cwd.
	cwd := filepath.Join(dir, "W", "cwd")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	p, bad = Build(Input{Reads: []string{read}, Writes: []string{write}, Home: home, Cwd: cwd, Argv: []string{anExecutable(t)}})
	if len(bad) > 0 {
		t.Fatalf("a --cwd inside the write set spelled in another case was refused: %v", bad)
	}
	if p.Cwd == "" {
		t.Error("the policy carries no cwd")
	}
	// And a HOME that is genuinely outside every --write is still `home_outside`: the
	// repair widens no list.
	outside := filepath.Join(dir, "elsewhere")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	_, bad = Build(Input{Reads: []string{read}, Writes: []string{write}, Home: outside, Argv: []string{anExecutable(t)}})
	if len(bad) == 0 {
		t.Fatal("a HOME outside every --write was built")
	}
	var said bool
	for _, r := range bad {
		if r.Reason == "home_outside" && strings.Contains(r.Text, "outside every --write") {
			said = true
		}
	}
	if !said {
		t.Fatalf("a HOME outside every --write is not home_outside: %v", bad)
	}
}

// measuredFold is the GROUND TRUTH about one directory: a file written INSIDE it and asked for
// again in another case. ok is false where the directory would not take the write, which is the
// one case no measurement can answer.
func measuredFold(dir string) (folds, ok bool) {
	f, err := os.CreateTemp(dir, "GroundTruth")
	if err != nil {
		return false, false
	}
	name := f.Name()
	f.Close()
	defer os.Remove(name)
	orig, err := os.Lstat(name)
	if err != nil {
		return false, false
	}
	other, err := os.Lstat(filepath.Join(dir, strings.ToLower(filepath.Base(name))))
	return err == nil && os.SameFile(orig, other), true
}

// nameFoldsInParent is the answer the REMOVED shortcut gave: the spelling of the directory's own
// name, looked up in its PARENT. It is kept here as the wrong answer this test is about.
func nameFoldsInParent(dir string) (folds, ok bool) {
	base := filepath.Base(dir)
	lower := strings.ToLower(base)
	if lower == base {
		return false, false
	}
	orig, err := os.Lstat(dir)
	if err != nil {
		return false, false
	}
	other, err := os.Lstat(filepath.Join(filepath.Dir(dir), lower))
	return err == nil && os.SameFile(orig, other), true
}

// caseBoundaryDir finds a directory whose OWN NAME folds one way in its parent while lookups
// INSIDE it fold the other way -- a MOUNT BOUNDARY, the one place the two can differ. It looks
// along the ancestors of t.TempDir() up to one level above os.TempDir(), which is the
// case-sensitive-image setup this PR already uses: `TMPDIR=<volume>/tmp` puts the volume root on
// that path, and the bound keeps every probe write inside temp directories and their volume root.
func caseBoundaryDir(t *testing.T) (dir string, folds bool, found bool) {
	t.Helper()
	stop := filepath.Dir(filepath.Clean(os.TempDir()))
	for d := t.TempDir(); ; {
		inside, okInside := measuredFold(d)
		byName, okName := nameFoldsInParent(d)
		if okInside && okName && inside != byName {
			return d, inside, true
		}
		if d == stop {
			return "", false, false
		}
		up := filepath.Dir(d)
		if up == d {
			return "", false, false
		}
		d = up
	}
}

// DEMANDED by both eyes of #159 (Stella, comment 5648066751; the Fable read, comment
// 5648102050): `dirFoldsCase` measured the spelling of the directory's own name IN ITS PARENT
// and treated that as the lookup behaviour INSIDE the directory. At a mount boundary those are
// two filesystems. The shortcut was never reachable from `Inside` -- it asks the fold only about
// a directory that does NOT exist, and the shortcut needed it to exist -- so this test is the
// guard that keeps the mechanism honest here, and it asserts the reachable path as well.
//
// It needs a boundary on the path it can reach, so run it with `TMPDIR` inside a case-sensitive
// image mounted under a folding parent; where none is reachable it skips by name.
func TestTheFoldIsMeasuredInsideTheDirectoryNotInItsParent(t *testing.T) {
	boundary, folds, found := caseBoundaryDir(t)
	if !found {
		t.Skip("no case-sensitivity boundary is reachable from this machine's temp directory: every directory on that path answers the same inside as its own name does in its parent, so the inference this test is about cannot be observed here. Run with TMPDIR inside a case-sensitive image mounted under a folding parent")
	}
	if got := dirFoldsCase(boundary); got != folds {
		t.Errorf("dirFoldsCase(%s) = %v, but a file written INSIDE it says %v: the answer is being inferred from the directory's own name in its parent, which is a different filesystem here", boundary, got, folds)
	}
	// The reachable path: a directory that is not there, judged at that boundary. The climb
	// answers from the boundary itself, so the folded prefix must follow what the boundary
	// measured and not what its mountpoint name does in /Volumes.
	absent := filepath.Join(boundary, "novagone")
	if got := Inside(filepath.Join(boundary, "NovaGone", "env"), absent); got != folds {
		t.Errorf("Inside(%s/NovaGone/env, %s) = %v, the boundary folds = %v", boundary, absent, got, folds)
	}
}
