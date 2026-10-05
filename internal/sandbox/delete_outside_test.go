//go:build linux

package sandbox

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deleteHelperEnv makes the test binary the walled child: Run restricts the process that
// calls it and a Landlock domain cannot be lifted, so the wall is applied in a re-exec of
// this binary and never in the test process itself.
const deleteHelperEnv = "NOVA_TEST_DELETES_IN_EVERY_WRITE_ROOT"

// DEMANDED (docs/SPEC-SANDBOX.md, "wall-deletes-in-every-write-root.w1"). What the wall
// lets a program create it lets it remove: every --write root carries the remove rights.
// On 2026-10-05 the narrow rule (deletes only in the job dir, its tmp and the cwd) took
// the remove rights from the member's data home, and opencode's SQLite database there
// could not unlink its rollback journal: every child died at its first write with "disk
// I/O error". The walled child here has the member's shape -- the job dir as the first
// --write and the cwd, the data home as HOME, a tmp of its own, a shared cache -- and
// creates and deletes in the data home and the cache, neither of them the cwd nor the
// tmp: an unlink, an rm -rf of a directory and a rename away. A wall that withholds the
// remove rights from any --write turns this red at data=1.
func TestTheWallAllowsDeletesInEveryWriteRoot(t *testing.T) {
	t.Parallel()
	if os.Getenv(deleteHelperEnv) == "1" {
		os.Exit(runDeleteHelper())
	}
	if _, ok := landlockABI(); !ok {
		t.Skip("this kernel has no landlock; the wall cannot be built here")
	}
	if os.Geteuid() == 0 {
		t.Skip("the wall does not run as root (rule 2)")
	}

	slot := realDir(t, t.TempDir())
	job := filepath.Join(slot, "jobs", "card")
	data := filepath.Join(slot, "data")
	tmp := filepath.Join(slot, "tmp", "card")
	cache := realDir(t, t.TempDir())
	for _, d := range []string{job, filepath.Join(data, "opencode"), tmp, filepath.Join(cache, "sub")} {
		require.NoError(t, os.MkdirAll(d, 0o700))
	}
	require.NoError(t, os.WriteFile(filepath.Join(cache, "sub", "deep"), []byte("cache\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(data, "opencode", "old"), []byte("data\n"), 0o600))

	cmd := exec.Command(os.Args[0], "-test.run=^TestTheWallAllowsDeletesInEveryWriteRoot$", "-test.count=1")
	cmd.Env = append(os.Environ(), deleteHelperEnv+"=1", "DELETE_JOB="+job, "DELETE_DATA="+data, "DELETE_TMP="+tmp, "DELETE_CACHE="+cache)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	require.NoError(t, cmd.Run(), "the walled child did not run: stdout=%s stderr=%s", out.String(), errb.String())
	got := out.String()
	t.Logf("walled child:\n%s%s", got, errb.String())

	// The data home: neither the cwd nor the tmp, and the place opencode keeps its database.
	assert.Contains(t, got, "data=0", "a file created in the data home could not be deleted there")
	assert.NoFileExists(t, filepath.Join(data, "opencode", "probe"))
	assert.Contains(t, got, "datamv=0", "a file could not be renamed away inside the data home")
	assert.NoFileExists(t, filepath.Join(data, "opencode", "old"))
	assert.FileExists(t, filepath.Join(data, "opencode", "new"))
	// The shared cache: a --write of its own, beside the slot.
	assert.Contains(t, got, "cache=0", "a file created in the shared cache could not be deleted there")
	assert.NoFileExists(t, filepath.Join(cache, "probe"))
	assert.Contains(t, got, "cachermrf=0", "rm -rf of a directory in the shared cache was refused")
	assert.NoDirExists(t, filepath.Join(cache, "sub"))
	// The job dir and the tmp, as before.
	assert.Contains(t, got, "job=0", "a file created in the job dir could not be deleted there")
	assert.Contains(t, got, "tmp=0", "a file created in the tmp could not be deleted there")
	assert.NotContains(t, got+errb.String(), "Permission denied")
}

// runDeleteHelper is the walled child: the member's wall around one shell that creates and
// deletes in each write root and prints each status.
func runDeleteHelper() int {
	job, data, tmp, cache := os.Getenv("DELETE_JOB"), os.Getenv("DELETE_DATA"), os.Getenv("DELETE_TMP"), os.Getenv("DELETE_CACHE")
	script := strings.Join([]string{
		`touch "$2/opencode/probe" && rm "$2/opencode/probe"; echo data=$?`,
		`mv "$2/opencode/old" "$2/opencode/new"; echo datamv=$?`,
		`touch "$4/probe" && rm "$4/probe"; echo cache=$?`,
		`rm -rf "$4/sub"; echo cachermrf=$?`,
		`touch "$1/probe" && rm "$1/probe"; echo job=$?`,
		`touch "$3/probe" && rm "$3/probe"; echo tmp=$?`,
	}, "\n")
	p, bad := Build(Input{
		Writes: []string{job, data, tmp, cache},
		Tmp:    tmp,
		Cwd:    job,
		Home:   data,
		Argv:   []string{"sh", "-c", script, "sh", job, data, tmp, cache},
	})
	if len(bad) > 0 {
		fmt.Fprintf(os.Stderr, "build refused: %v\n", bad)
		return 2
	}
	code, err := Run(p, os.Environ(), strings.NewReader(""), os.Stdout, os.Stderr, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run refused: %v\n", err)
		return 2
	}
	return code
}

// gitStepEnv makes the test binary the walled child of the step's-git-commit test.
const gitStepEnv = "NOVA_TEST_A_STEPS_GIT_COMMIT"

// A step's wall (internal/cardtree, Wall.Argv): its private tmp is the first --write and
// the --tmp, its checkout the second --write and the --cwd. git commits by renaming a new
// index over .git/index, a delete in the checkout, and the commit must succeed under the
// wall; a third --write, a shared directory the step may write, may be deleted from too
// (docs/SPEC-SANDBOX.md, "wall-deletes-in-every-write-root.w1"). A wall that withholds
// the remove rights from the cwd fails here at commit=128 ("unable to write new index
// file").
func TestAStepsGitCommitInsideItsWallSucceeds(t *testing.T) {
	t.Parallel()
	if os.Getenv(gitStepEnv) == "1" {
		os.Exit(runGitStepHelper())
	}
	if _, ok := landlockABI(); !ok {
		t.Skip("this kernel has no landlock; the wall cannot be built here")
	}
	if os.Geteuid() == 0 {
		t.Skip("the wall does not run as root (rule 2)")
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git on this machine")
	}

	tmp := realDir(t, t.TempDir())
	repo := realDir(t, t.TempDir())
	shared := realDir(t, t.TempDir())
	keep := filepath.Join(shared, "keep")
	require.NoError(t, os.WriteFile(keep, []byte("shared\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "home"), 0o700))
	env := append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=step", "GIT_AUTHOR_EMAIL=step@example.com", "GIT_COMMITTER_NAME=step", "GIT_COMMITTER_EMAIL=step@example.com")
	for _, args := range [][]string{{"init", "-q", repo}, {"-C", repo, "commit", "-q", "--allow-empty", "-m", "base"}} {
		c := exec.Command(git, args...)
		c.Env = env
		out, err := c.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.txt"), []byte("step\n"), 0o644))

	cmd := exec.Command(os.Args[0], "-test.run=^TestAStepsGitCommitInsideItsWallSucceeds$", "-test.count=1")
	cmd.Env = append(env, gitStepEnv+"=1", "STEP_TMP="+tmp, "STEP_REPO="+repo, "STEP_SHARED="+shared, "STEP_GIT="+git)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	require.NoError(t, cmd.Run(), "the walled child did not run: stdout=%s stderr=%s", out.String(), errb.String())
	got := out.String()
	t.Logf("walled child:\n%s%s", got, errb.String())

	assert.Contains(t, got, "add=0", "git add in the step's checkout was refused")
	assert.Contains(t, got, "commit=0", "git commit in the step's checkout was refused")
	log := exec.Command(git, "-C", repo, "log", "-1", "--format=%s")
	log.Env = env
	subject, err := log.Output()
	require.NoError(t, err)
	assert.Equal(t, "walled\n", string(subject), "the commit made under the wall is the checkout's head")
	assert.Contains(t, got, "write=0", "a write in the shared --write was refused")
	assert.Contains(t, got, "rm=0", "rm of a file in the shared --write was refused")
	assert.NoFileExists(t, keep, "the file in the shared --write was not deleted")
}

// runGitStepHelper is the walled child: the step's wall around one shell that commits in
// the checkout, writes in the shared directory and deletes there.
func runGitStepHelper() int {
	tmp, repo, shared, git := os.Getenv("STEP_TMP"), os.Getenv("STEP_REPO"), os.Getenv("STEP_SHARED"), os.Getenv("STEP_GIT")
	script := strings.Join([]string{
		`"$4" add a.txt; echo add=$?`,
		`"$4" commit -q -m walled; echo commit=$?`,
		`echo new > "$3/written"; echo write=$?`,
		`rm "$3/keep"; echo rm=$?`,
	}, "\n")
	p, bad := Build(Input{
		Writes: []string{tmp, repo, shared},
		Tmp:    tmp,
		Cwd:    repo,
		Home:   filepath.Join(tmp, "home"),
		Reads:  []string{filepath.Dir(git)},
		Argv:   []string{"sh", "-c", script, "sh", tmp, repo, shared, git},
	})
	if len(bad) > 0 {
		fmt.Fprintf(os.Stderr, "build refused: %v\n", bad)
		return 2
	}
	code, err := Run(p, os.Environ(), strings.NewReader(""), os.Stdout, os.Stderr, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run refused: %v\n", err)
		return 2
	}
	return code
}

// The masks, without a wall: every write root -- the job dir, its tmp, the cwd, the data
// home and a shared cache alike -- carries the whole handled set, REMOVE_FILE and
// REMOVE_DIR included; the printed ruleset says write= for each and never
// write-nodelete=, and the darwin profile denies no unlink.
func TestEveryWriteRootCarriesTheRemoveRights(t *testing.T) {
	t.Parallel()
	slot := realDir(t, t.TempDir())
	job := filepath.Join(slot, "jobs", "card")
	data := filepath.Join(slot, "data")
	tmp := filepath.Join(slot, "tmp", "card")
	cache := realDir(t, t.TempDir())
	p := &Policy{Writes: []string{job, data, tmp, cache}, Tmp: tmp, Cwd: job, Home: data}

	abi := maxKnownABI
	for _, w := range []string{job, data, tmp, cache, filepath.Join(data, "opencode")} {
		assert.True(t, p.DeletesIn(w), "%s is under a write root and deletes are refused there", w)
		assert.Equal(t, writeSubset(abi), writeRuleMask(p, w, abi), "%s lost its remove rights", w)
	}
	assert.False(t, p.DeletesIn(filepath.Dir(slot)), "a directory above every write root is no place to delete in")

	text, err := LandlockPolicyText(p)
	require.NoError(t, err)
	for _, w := range []string{job, data, tmp, cache} {
		assert.Contains(t, text, "write="+w+"\n")
	}
	assert.NotContains(t, text, "write-nodelete=")

	profile, _, err := DarwinProfile(p)
	require.NoError(t, err)
	assert.NotContains(t, profile, "(deny file-write-unlink", "the darwin profile still refuses a delete under a --write")
}

func realDir(t *testing.T, dir string) string {
	t.Helper()
	got, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	return got
}
