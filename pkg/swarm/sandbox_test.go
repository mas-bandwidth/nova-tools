package swarm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit/fsx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The LAUNCH SEAM's own unit: the argv the dispatcher builds for a worker, and the one
// field the worker description gained. The wall itself is asked about the operating system
// in cmd/nova-swarm's tests, on the platform whose body is built; what is here is the shape
// of the two lists, which is the same shape on every platform.

// DEMANDED (SPEC-SANDBOX.md rule 5, "there is no --root flag"). read_roots is the one field
// the wall added to the worker description, and a root that is relative, absent or a file
// is refused at LOAD -- once, where a person can fix it -- rather than by the wall at every
// launch. Every independent problem is reported in one run.
func TestReadRootsAreRefusedBeforeTheyReachTheWall(t *testing.T) {
	t.Parallel()

	dir := fsx.Tree(t, map[string]string{
		"a-file":      "x",
		"key":         "k",
		"worker.json": "", // will be overwritten below
	})
	good := filepath.Join(dir, "toolchains")
	require.NoError(t, os.MkdirAll(good, 0o755))
	file := filepath.Join(dir, "a-file")
	home := filepath.Join(dir, "worker")
	require.NoError(t, os.MkdirAll(home, 0o755))
	key := filepath.Join(dir, "key")
	path := filepath.Join(dir, "worker.json")

	desc := map[string]any{
		"name": "w", "provider": "p", "model": "m", "env_var": "K", "key_file": key,
		"usage": "none", "harness": "h", "worker_dir": home, "deadline": "1m",
		"harness_args": []string{"run", "--model", "{model}", "--", "{prompt}"},
		"read_roots":   []string{good, "relative/toolchain", filepath.Join(dir, "absent"), file},
	}
	raw, _ := json.MarshalIndent(desc, "", "  ")
	fsx.Write(t, path, string(raw))
	w, problems := LoadWorker(path)
	require.Len(t, problems, 3, "three read roots are wrong and the load reported %d problems: %v", len(problems), problems)
	all := ""
	for _, p := range problems {
		all += p.Error() + "\n"
	}
	for _, want := range []string{"is relative", "does not exist", "is not a directory"} {
		assert.Contains(t, all, want, "no problem says %q:\n%s", want, all)
	}
	if assert.Len(t, w.ReadRoots, 4, "the roots are read as written, in order: %v", w.ReadRoots) {
		assert.Equal(t, good, w.ReadRoots[0], "the roots are read as written, in order: %v", w.ReadRoots)
	}
	// A description that names none is sound: the system roots are the floor.
	delete(desc, "read_roots")
	raw, _ = json.MarshalIndent(desc, "", "  ")
	fsx.Write(t, path, string(raw))
	_, problems = LoadWorker(path)
	assert.Empty(t, problems, "a description naming no read root is refused: %v", problems)
}

// DEMANDED (SPEC-SANDBOX.md rule 6 and the dispatcher caller: "the key FILE is in neither
// list, so the job cannot read it even if it is told to"). The tool must not put it in one.
// `RefreshSlot` copies every regular file of `worker_dir` into the slot directory, and the
// slot directory is the job's `--read`, so a `key_file` under `worker_dir` is a copy of the
// key INSIDE the wall, under a green `SANDBOX OK` and a probe that passed -- the probe's own
// `secret_inside_allow` is checked against the probe's lists, never against a job's (Rowan's
// Fable read of #88 at d0c1841, M1). A key under a `read_roots` entry is the same hole
// without the copy. Both are refused at LOAD, where a person can still move the file.
func TestAKeyFileInsideTheReadSetIsRefusedAtLoad(t *testing.T) {
	t.Parallel()

	dir := fsx.Tree(t, map[string]string{
		"worker.json": "", // placeholder, will be overwritten
	})
	home := filepath.Join(dir, "worker")
	tools := filepath.Join(dir, "toolchains")
	require.NoError(t, os.MkdirAll(home, 0o755))
	require.NoError(t, os.MkdirAll(tools, 0o755))

	descFor := func(workerDir, key string) string {
		raw, _ := json.MarshalIndent(map[string]any{
			"name": "w", "provider": "p", "model": "m", "env_var": "K", "key_file": key,
			"usage": "none", "harness": "h", "worker_dir": workerDir, "deadline": "1m",
			"harness_args": []string{"run", "--model", "{model}", "--", "{prompt}"},
			"read_roots":   []string{tools},
		}, "", "  ")
		path := filepath.Join(dir, "worker.json")
		fsx.Write(t, path, string(raw))
		return path
	}
	desc := func(key string) string { return descFor(home, key) }
	// The key file a description may NOT name: one inside the worker directory, which is
	// copied into the slot before every job.
	inside := filepath.Join(home, ".key")
	fsx.Write(t, inside, "sk-not-a-key\n")
	_, problems := LoadWorker(desc(inside))
	require.Len(t, problems, 1, "a key file inside worker_dir reported %d problems, want 1: %v", len(problems), problems)
	said := problems[0].Error()
	for _, want := range []string{inside, home, "copied into the slot", "--read", "Keep the key file outside worker_dir"} {
		assert.Contains(t, said, want, "the refusal does not say %q:\n%s", want, said)
	}
	// And one inside a read root, which every job of this worker may read directly.
	inRoot := filepath.Join(tools, "key")
	fsx.Write(t, inRoot, "sk-not-a-key\n")
	_, problems = LoadWorker(desc(inRoot))
	require.Len(t, problems, 1, "a key file inside a read root is not refused: %v", problems)
	require.Contains(t, problems[0].Error(), "read_roots[0]", "a key file inside a read root is not refused: %v", problems)
	// And one inside a SLOT directory, which is a sibling of worker_dir and not under it:
	// the slot IS the job's `--read`, so the key is read inside the wall under a probe that
	// passed, and neither check above sees it (Rowan's Fable read 2 of #88 at fc400ce, L1).
	// The refusal is at LOAD, so `nova-swarm run` returns before Run prints any RUN line --
	// the task stays pending and no worker starts.
	slotJobs := filepath.Join(dir, "worker-1", "jobs", "t1")
	require.NoError(t, os.MkdirAll(slotJobs, 0o755))
	inSlot := filepath.Join(dir, "worker-1", ".key")
	fsx.Write(t, inSlot, "sk-not-a-key\n")
	_, problems = LoadWorker(desc(inSlot))
	require.Len(t, problems, 1, "a key file inside a slot directory reported %d problems, want 1: %v", len(problems), problems)
	said = problems[0].Error()
	for _, want := range []string{inSlot, filepath.Join(dir, "worker-1"), "--read", "~/.keys/<provider>"} {
		assert.Contains(t, said, want, "the slot refusal does not say %q:\n%s", want, said)
	}
	// The same key one level deeper, under the slot's own jobs/, is the same hole.
	underJobs := filepath.Join(slotJobs, ".key")
	fsx.Write(t, underJobs, "sk-not-a-key\n")
	_, problems = LoadWorker(desc(underJobs))
	require.Len(t, problems, 1, "a key file under a slot's jobs/ is not refused: %v", problems)
	// A sibling that merely starts the same way is NOT a slot and is sound: the tool
	// refuses the placements it creates, not every neighbour of worker_dir.
	neighbour := filepath.Join(dir, "worker-keys")
	require.NoError(t, os.MkdirAll(neighbour, 0o700))
	sound := filepath.Join(neighbour, "provider")
	fsx.Write(t, sound, "sk-not-a-key\n")
	_, problems = LoadWorker(desc(sound))
	assert.Empty(t, problems, "a key file in a sibling that is not a slot is refused: %v", problems)
	// The key file the README teaches -- outside both lists -- is sound, and a symlink
	// into the worker directory does not walk around the check (rule 5 resolves paths).
	outside := filepath.Join(dir, "keys", "provider")
	require.NoError(t, os.MkdirAll(filepath.Dir(outside), 0o700))
	fsx.Write(t, outside, "sk-not-a-key\n")
	_, problems = LoadWorker(desc(outside))
	assert.Empty(t, problems, "a key file outside both lists is refused: %v", problems)
	link := filepath.Join(dir, "linked-key")
	require.NoError(t, os.Symlink(inside, link))
	_, problems = LoadWorker(desc(link))
	assert.Len(t, problems, 1, "a symlink walks around the check: %v", problems)
	// AND A SYMLINKED worker_dir DOES NOT WALK AROUND THE SLOT CHECK. `SlotDir` builds the
	// slot from the TYPED spelling, so with `worker_dir` a link the slot the tool creates and
	// hands to `--read` is a sibling of the LINK, not of its target: asking only the resolved
	// spelling left a key inside the wall under a green line (Rowan's Fable read 3 of #88 at
	// 56c7dcc, L1). Exactly one refusal, naming the key and the slot the tool would build.
	realDir := filepath.Join(dir, "real", "worker")
	require.NoError(t, os.MkdirAll(realDir, 0o755))
	typedDir := filepath.Join(dir, "typed-worker")
	require.NoError(t, os.Symlink(realDir, typedDir))
	linkedSlot := Worker{WorkerDir: typedDir}.SlotDir(1)
	require.NoError(t, os.MkdirAll(linkedSlot, 0o755))
	inLinkedSlot := filepath.Join(linkedSlot, ".key")
	fsx.Write(t, inLinkedSlot, "sk-not-a-key\n")
	_, problems = LoadWorker(descFor(typedDir, inLinkedSlot))
	require.Len(t, problems, 1, "a key file inside the slot of a SYMLINKED worker_dir reported %d problems, want 1: %v", len(problems), problems)
	said = problems[0].Error()
	for _, want := range []string{inLinkedSlot, linkedSlot, "--read", "~/.keys/<provider>"} {
		assert.Contains(t, said, want, "the symlinked-slot refusal does not say %q:\n%s", want, said)
	}
}

// DEMANDED (SPEC-SANDBOX.md rule 6, "the secret is never inside either list"), and the hole
// both cold readers of #88 at fc400ce found: `insideDir` answered with `strings.HasPrefix`
// over cleaned, symlink-resolved paths, and that comparison is case-SENSITIVE while APFS is
// case-INsensitive by default. A `key_file` typed `<dir>/Worker/.key` under a `worker_dir` of
// `<dir>/worker` is THE SAME FILE on such a filesystem -- `RefreshSlot` copies it into the
// slot before every job and the slot IS the job's `--read` -- and the prefix said no, so the
// load passed and the key was read inside the wall under a green `SANDBOX OK`.
// `filepath.EvalSymlinks` does not fold case on darwin, so resolving the path did not close
// it either; the answer has to come from the filesystem, `os.SameFile` over the ancestors.
//
// THE FILESYSTEM DECIDES WHETHER THIS TEST CAN RUN, NOT `runtime.GOOS`: APFS can be formatted
// case-sensitive and a linux mount can fold, so the test WRITES a file and asks for it back
// in another case. Where the answer is no, the placement this test is about cannot exist on
// this machine and the test skips with that reason named.
func TestAKeyFileSpelledInAnotherCaseIsRefusedWhereTheFilesystemFolds(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fsx.Write(t, filepath.Join(dir, "CaseProbe"), "x\n")
	if _, err := os.Stat(filepath.Join(dir, "caseprobe")); err != nil {
		t.Skipf("the filesystem under %s is case-SENSITIVE: caseprobe is not CaseProbe, so a key file spelled in another case is a different file here and the fold this test is about cannot happen", dir)
	}
	home := filepath.Join(dir, "worker")
	tools := filepath.Join(dir, "toolchains")
	for _, d := range []string{home, tools} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	desc := func(key string) string {
		raw, _ := json.MarshalIndent(map[string]any{
			"name": "w", "provider": "p", "model": "m", "env_var": "K", "key_file": key,
			"usage": "none", "harness": "h", "worker_dir": home, "deadline": "1m",
			"harness_args": []string{"run", "--model", "{model}", "--", "{prompt}"},
			"read_roots":   []string{tools},
		}, "", "  ")
		path := filepath.Join(dir, "worker.json")
		fsx.Write(t, path, string(raw))
		return path
	}
	// The key inside worker_dir, spelled in another case. The write itself goes through the
	// fold -- there is no `<dir>/Worker` directory, only `<dir>/worker` -- so the file this
	// description names IS the file the slot copy would pick up.
	folded := filepath.Join(dir, "Worker", ".key")
	require.NoError(t, os.MkdirAll(filepath.Dir(folded), 0o755))
	fsx.Write(t, folded, "sk-not-a-key\n")
	_, problems := LoadWorker(desc(folded))
	require.Len(t, problems, 1, "a key file inside worker_dir spelled in another case reported %d problems, want 1: %v", len(problems), problems)
	said := problems[0].Error()
	for _, want := range []string{folded, home, "copied into the slot", "--read", "Keep the key file outside worker_dir"} {
		assert.Contains(t, said, want, "the refusal does not say %q:\n%s", want, said)
	}
	// And the other list: a read root spelled in another case is the same hole without the
	// copy, because every job of this worker may read that directory directly.
	foldedRoot := filepath.Join(dir, "Toolchains", "key")
	require.NoError(t, os.MkdirAll(filepath.Dir(foldedRoot), 0o755))
	fsx.Write(t, foldedRoot, "sk-not-a-key\n")
	_, problems = LoadWorker(desc(foldedRoot))
	require.Len(t, problems, 1, "a key file inside a read root spelled in another case is not refused: %v", problems)
	require.Contains(t, problems[0].Error(), "read_roots[0]", "a key file inside a read root spelled in another case is not refused: %v", problems)
	// And the placement the README teaches is still sound with the new comparison: a
	// directory that is not the worker directory under any spelling is not inside it.
	outside := filepath.Join(dir, "keys", "provider")
	require.NoError(t, os.MkdirAll(filepath.Dir(outside), 0o700))
	fsx.Write(t, outside, "sk-not-a-key\n")
	_, problems = LoadWorker(desc(outside))
	assert.Empty(t, problems, "a key file outside both lists is refused: %v", problems)
}
