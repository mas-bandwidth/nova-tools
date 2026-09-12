package swarm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// caseFoldingTempDir is a temp directory plus the filesystem's own answer about whether two
// spellings of one name are one name there. THE FILESYSTEM DECIDES WHETHER A FOLD TEST CAN
// RUN, NOT `runtime.GOOS`: APFS can be formatted case-sensitive and a linux mount can fold,
// so the test WRITES a file and asks for it back in another case. Where the answer is no, the
// placement the fold tests are about cannot exist on this machine.
//
// The directory is symlink-resolved, so that the spelling a description carries and the
// spelling `resolvePath` answers with are one and the refusal names the path the test typed
// (`/var/folders/...` resolves to `/private/var/folders/...` on darwin).
func caseFoldingTempDir(t *testing.T) (string, bool) {
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

// slotDesc writes a worker description naming workerDir and keyFile, everything else sound.
func slotDesc(t *testing.T, dir, workerDir, keyFile string) string {
	t.Helper()
	raw, err := json.MarshalIndent(map[string]any{
		"name": "w", "provider": "p", "model": "m", "env_var": "K", "key_file": keyFile,
		"usage": "none", "harness": "h", "worker_dir": workerDir, "deadline": "1m",
		"harness_args": []string{"run", "--model", "{model}", "--", "{prompt}"},
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "worker.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// DEMANDED (SPEC-SANDBOX.md rule 6, "the secret is never inside either list"), and the hole
// #145 named: `slotDirHolding` matched the slot's `<base>-<digits>` name as TEXT, through
// `filepath.Rel` and `strings.CutPrefix`, and that comparison is case-SENSITIVE while APFS is
// case-INsensitive by default. With `worker_dir` `<dir>/worker`, a `key_file` at
// `<dir>/Worker-1/.key` sits in the directory the tool builds and hands to `--read` --
// `CutPrefix("Worker-1", "worker-")` said no, the load passed, and the job read the key inside
// the wall under a green `SANDBOX OK` and a probe that passed. It is #100 one directory over.
//
// AND THE SLOT DIRECTORY NEED NOT EXIST AT LOAD, which is why the repair is a different shape
// from #143's: slots are created at run, so there may be no `<dir>/worker-1` inode to compare
// with `os.SameFile`. The candidate is therefore derived from the KEY's ancestors toward
// `filepath.Dir(worker_dir)`, and the `<base>-<digits>` name is judged under the filesystem's
// own equality.
func TestAKeyFileInASlotDirectorySpelledInAnotherCaseIsRefusedWhereTheFilesystemFolds(t *testing.T) {
	dir, folds := caseFoldingTempDir(t)
	if !folds {
		t.Skipf("the filesystem under %s is case-SENSITIVE: caseprobe is not CaseProbe, so <dir>/Worker-1 and <dir>/worker-1 are two directories here and the fold this test is about cannot happen", dir)
	}
	home := filepath.Join(dir, "worker")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	// The slot directory the tool would build is `<dir>/worker-1`. This one is spelled
	// `<dir>/Worker-1`, which on this filesystem the tool's own `MkdirAll` in `RefreshSlot`
	// would open as the SAME directory: the key placed here is the key the job reads.
	folded := filepath.Join(dir, "Worker-1", "jobs", "j1")
	if err := os.MkdirAll(folded, 0o755); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(dir, "Worker-1", ".key")
	if err := os.WriteFile(key, []byte("sk-not-a-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, problems := LoadWorker(slotDesc(t, dir, home, key))
	if len(problems) != 1 {
		t.Fatalf("a key file in a slot directory spelled in another case reported %d problems, want 1: %v", len(problems), problems)
	}
	said := problems[0].Error()
	for _, want := range []string{key, filepath.Join(dir, "Worker-1"), "--read", "~/.keys/<provider>"} {
		if !strings.Contains(said, want) {
			t.Errorf("the folded-slot refusal does not say %q:\n%s", want, said)
		}
	}
	// The same key one level deeper, under the folded slot's own jobs/, is the same hole.
	deeper := filepath.Join(folded, ".key")
	if err := os.WriteFile(deeper, []byte("sk-not-a-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, problems := LoadWorker(slotDesc(t, dir, home, deeper)); len(problems) != 1 {
		t.Fatalf("a key file under a folded slot's jobs/ is not refused: %v", problems)
	}
	// AND NEITHER THE SLOT NOR THE WORKER DIRECTORY HAS TO BE THERE. This is the case that
	// has no inode to compare -- `<future>/worker` does not exist, so the name is all there
	// is -- and the answer comes from the filesystem's MEASURED fold rather than from
	// `runtime.GOOS`: the probe climbs to the nearest directory that does exist and asks it.
	future := filepath.Join(dir, "future", "worker")
	absent := filepath.Join(dir, "future", "Worker-2", ".key")
	if _, problems := LoadWorker(slotDesc(t, dir, future, absent)); len(problems) != 1 {
		t.Fatalf("a key file in the slot of a worker_dir that does not exist yet is not refused: %v", problems)
	}
	// And the placements that are SOUND stay sound: a neighbour whose name merely starts
	// the same way is not a slot, and the key file the README teaches is outside both lists.
	for _, sound := range []string{
		filepath.Join(dir, "Worker-keys", "provider"),
		filepath.Join(dir, "keys", "provider"),
	} {
		if err := os.MkdirAll(filepath.Dir(sound), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(sound, []byte("sk-not-a-key\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, problems := LoadWorker(slotDesc(t, dir, home, sound)); len(problems) != 0 {
			t.Errorf("a sound key file at %s is refused: %v", sound, problems)
		}
	}
}

// The name arithmetic of the slot check, on every filesystem: what the rewrite must keep
// answering after it stopped being a `strings.CutPrefix`. A path that IS the slot directory is
// not held by it, a slot that does not exist yet is still a slot, and a sibling whose name
// merely starts the same way is not one.
func TestSlotDirHoldingJudgesTheSlotName(t *testing.T) {
	dir := t.TempDir()
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	worker := filepath.Join(dir, "worker")
	for _, tc := range []struct {
		name, path, want string
	}{
		{"in a slot", filepath.Join(dir, "worker-1", ".key"), filepath.Join(dir, "worker-1")},
		{"under a slot's jobs", filepath.Join(dir, "worker-12", "jobs", "j", ".key"), filepath.Join(dir, "worker-12")},
		{"a padded slot number", filepath.Join(dir, "worker-01", ".key"), filepath.Join(dir, "worker-01")},
		{"the slot directory itself", filepath.Join(dir, "worker-1"), ""},
		{"the worker directory itself", worker, ""},
		{"inside the worker directory", filepath.Join(worker, ".key"), ""},
		{"a neighbour that is not a slot", filepath.Join(dir, "worker-keys", ".key"), ""},
		{"a slot number that is not digits", filepath.Join(dir, "worker-1a", ".key"), ""},
		{"an empty slot number", filepath.Join(dir, "worker-", ".key"), ""},
		{"another worker's slot", filepath.Join(dir, "other-1", ".key"), ""},
		{"a name that only ends the same way", filepath.Join(dir, "myworker-1", ".key"), ""},
		{"above the parent", filepath.Join(dir, ".key"), ""},
		{"elsewhere entirely", filepath.Join(dir, "a", "b", "c", ".key"), ""},
	} {
		if got := slotDirHolding(tc.path, worker); got != tc.want {
			t.Errorf("%s: slotDirHolding(%s, %s) = %q, want %q", tc.name, tc.path, worker, got, tc.want)
		}
	}
}

// The probe itself, both halves: two names that ARE one name on this filesystem, and two that
// are not on any. It runs on every platform, because what it asserts is what the filesystem
// under it says -- and the answer it gives is the answer the slot check acts on.
func TestNamesOneFileAsksTheFilesystem(t *testing.T) {
	dir, folds := caseFoldingTempDir(t)
	if err := os.MkdirAll(filepath.Join(dir, "worker"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := namesOneFile(dir, "worker", "worker"); !got {
		t.Error("one name is not itself")
	}
	if got := namesOneFile(dir, "worker", "other"); got {
		t.Error("two different names are one file")
	}
	if got := namesOneFile(dir, "Worker", "worker"); got != folds {
		t.Errorf("namesOneFile(Worker, worker) = %v on a filesystem whose fold is %v", got, folds)
	}
	// The same question about a name NOTHING has created: the pair cannot be stat'd, so the
	// answer is the measured fold of the directory rather than a guess.
	if got := namesOneFile(dir, "Absent", "absent"); got != folds {
		t.Errorf("namesOneFile(Absent, absent) = %v on a filesystem whose fold is %v", got, folds)
	}
	if got := dirFoldsCase(dir); got != folds {
		t.Errorf("dirFoldsCase = %v, the filesystem folds = %v", got, folds)
	}
	// A directory that is not there is answered by the nearest one that is.
	if got := dirFoldsCase(filepath.Join(dir, "not", "yet", "123")); got != folds {
		t.Errorf("dirFoldsCase of an absent directory = %v, the filesystem folds = %v", got, folds)
	}
}

// measuredFold is the GROUND TRUTH about one directory: a file written INSIDE it and asked
// for again in another case. ok is false where the directory would not take the write, which
// is the one case no measurement can answer.
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

// nameFoldsInParent is the answer the REMOVED shortcut gave: the spelling of the directory's
// own name, looked up in its PARENT. It is kept here as the wrong answer this test is about.
func nameFoldsInParent(dir string) (folds, ok bool) {
	base := filepath.Base(dir)
	lower := strings.ToLower(base)
	if lower == base {
		return false, false // no letter to re-case, so the shortcut could not answer either
	}
	orig, err := os.Lstat(dir)
	if err != nil {
		return false, false
	}
	other, err := os.Lstat(filepath.Join(filepath.Dir(dir), lower))
	return err == nil && os.SameFile(orig, other), true
}

// caseBoundaryDir finds a directory whose OWN NAME folds one way in its parent while lookups
// INSIDE it fold the other way. A MOUNT BOUNDARY is where the two can differ -- a
// case-sensitive volume mounted under the folding `/Volumes`, or the reverse -- and it is the
// one place a fold inferred from the parent is wrong (Stella's read of #159, comment
// 5648066751).
//
// It looks along the ancestors of `t.TempDir()` up to one level above `os.TempDir()`, which is
// the builder's own case-sensitive-image setup: `TMPDIR=<volume>/tmp` puts the volume root on
// that path. The walk is bounded there so that the probe writes land in temp directories and
// their volume root, never anywhere else on the machine.
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

// DEMANDED by Stella's read of #159 (comment 5648066751): `dirFoldsCase` measured the spelling
// of the directory's own name IN ITS PARENT and treated that as the lookup behaviour INSIDE the
// directory. Those differ at a mount boundary, and the slot check asks the question about
// CHILDREN of the directory -- `<base>-<digits>` siblings of `worker_dir` -- so the parent's
// answer is the wrong one. A case-sensitive volume mounted under the folding `/Volumes` is the
// case: the mountpoint's name folds, everything inside it does not.
//
// This test needs such a boundary to exist on the path it can reach, so run it with `TMPDIR`
// inside a case-sensitive image, which is the setup this PR already uses to prove its skips.
// Where no boundary is reachable it skips by name rather than passing about nothing.
func TestTheFoldIsMeasuredInsideTheDirectoryNotInItsParent(t *testing.T) {
	boundary, folds, found := caseBoundaryDir(t)
	if !found {
		t.Skip("no case-sensitivity boundary is reachable from this machine's temp directory: every directory on that path answers the same inside as its own name does in its parent, so the inference this test is about cannot be observed here. Run with TMPDIR inside a case-sensitive image mounted under a folding parent")
	}
	if got := dirFoldsCase(boundary); got != folds {
		t.Errorf("dirFoldsCase(%s) = %v, but a file written INSIDE it says %v: the answer is being inferred from the directory's own name in its parent, which is a different filesystem here", boundary, got, folds)
	}
	// AND THE FULL SLOT-NAME COMPARISON AT THAT BOUNDARY, which is what the check actually
	// asks. `<boundary>/<base>-1` is the slot the tool would build; the key sits in a
	// sibling spelled in another case. Whether that is ONE directory is the boundary's own
	// answer, so the refusal must follow the measured fold and not the mountpoint's name.
	// EXCLUSIVELY CREATED, AND ONLY WHAT THIS TEST MADE IS REMOVED. A pid in a name is not
	// ownership (Stella's read of 6eb566c9, comment 5648145223): `os.MkdirAll` succeeds on a
	// directory that is already there, so a preexisting `nf<pid>worker` at this volume root
	// would have been used by the test and then recursively deleted by its cleanup. `os.Mkdir`
	// fails where the name is taken, the counter moves to a free pair, and each cleanup is
	// registered only after the directory it removes was created HERE.
	// And the exclusivity is PROVED, not asserted: a decoy stands at the first name the loop
	// will try, holding a file. The loop must step over it and leave both alone -- which is
	// what `os.MkdirAll` plus `os.RemoveAll` did not do.
	decoy := filepath.Join(boundary, fmt.Sprintf("novafold%d-0", os.Getpid()))
	if err := os.Mkdir(decoy, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(decoy) })
	sentinel := filepath.Join(decoy, "preexisting.txt")
	if err := os.WriteFile(sentinel, []byte("not this test's\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, home, candidate := "", "", ""
	for i := 0; i < 64 && candidate == ""; i++ {
		base = fmt.Sprintf("novafold%d-%d", os.Getpid(), i)
		h := filepath.Join(boundary, base)
		if err := os.Mkdir(h, 0o755); err != nil {
			continue // taken, or not ours to make: try the next name, delete nothing
		}
		t.Cleanup(func() { os.RemoveAll(h) })
		c := filepath.Join(boundary, strings.ToUpper(base)+"-1")
		if err := os.Mkdir(c, 0o755); err != nil {
			continue // the slot spelling is taken; h is this test's and its cleanup holds
		}
		t.Cleanup(func() { os.RemoveAll(c) })
		home, candidate = h, c
	}
	if candidate == "" {
		t.Fatalf("could not exclusively create a fresh worker_dir and slot pair in %s", boundary)
	}
	if home == decoy {
		t.Fatalf("the test adopted %s, a directory it did not create", decoy)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Errorf("a directory this test did not create lost its contents: %v", err)
	}
	key := filepath.Join(candidate, ".key")
	if err := os.WriteFile(key, []byte("sk-not-a-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := 0
	if folds {
		want = 1 // one directory under two spellings: the key is in the slot
	}
	_, problems := LoadWorker(slotDesc(t, t.TempDir(), home, key))
	if len(problems) != want {
		t.Fatalf("a key file at %s with worker_dir %s reported %d problems, want %d on a filesystem that folds=%v: %v", key, home, len(problems), want, folds, problems)
	}
}
