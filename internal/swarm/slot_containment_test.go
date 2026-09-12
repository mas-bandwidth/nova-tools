package swarm

import (
	"encoding/json"
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
