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
const deleteHelperEnv = "NOVA_TEST_DELETES_ONLY_IN_THE_JOB"

// A directory that is not a --write cannot be deleted, even when the command is
// handed its path (docs/SPEC-SANDBOX.md, "deletes-in-every-write-root"). Deletes
// under a --write are the other test, TestTheWallAllowsDeletesInEveryWriteRoot.
// Here the outside directory is not a --write: rm -rf of it, an rm of a file in
// it, a rename out of it and a write into it are each refused and the files stay.
// A delete in the job dir succeeds.
func TestTheWallRefusesDeletesOutsideTheJob(t *testing.T) {
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

	job := realDir(t, t.TempDir())
	outside := realDir(t, t.TempDir())
	keep := filepath.Join(outside, "keep")
	require.NoError(t, os.WriteFile(keep, []byte("outside\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(outside, "sub"), 0o700))
	deep := filepath.Join(outside, "sub", "deep")
	require.NoError(t, os.WriteFile(deep, []byte("outside\n"), 0o600))
	inside := filepath.Join(job, "scratch")
	require.NoError(t, os.WriteFile(inside, []byte("inside\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(job, "home"), 0o700))

	cmd := exec.Command(os.Args[0], "-test.run=^TestTheWallRefusesDeletesOutsideTheJob$", "-test.count=1")
	cmd.Env = append(os.Environ(), deleteHelperEnv+"=1", "DELETE_JOB="+job, "DELETE_OUTSIDE="+outside)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	require.NoError(t, cmd.Run(), "the walled child did not run: stdout=%s stderr=%s", out.String(), errb.String())
	got := out.String()
	t.Logf("walled child:\n%s%s", got, errb.String())

	// Inside the job: the delete succeeds.
	assert.Contains(t, got, "inside=0", "rm of a file in the job dir was refused")
	assert.NoFileExists(t, inside, "the file in the job dir was not deleted")
	// Outside every --write: refused, and nothing is gone.
	assert.Contains(t, got, "rmrf=1", "rm -rf of a directory outside every --write was not refused")
	assert.Contains(t, got, "rm=1", "rm of a file outside every --write was not refused")
	assert.Contains(t, got, "mv=1", "rename of a file out of a directory outside every --write was not refused")
	assert.Contains(t, got+errb.String(), "Permission denied", "the refusal was not the kernel's")
	assert.FileExists(t, keep, "the wall let a file outside every --write be deleted")
	assert.FileExists(t, deep, "the wall let rm -rf delete beneath a directory outside every --write")
	assert.NoFileExists(t, filepath.Join(job, "stolen"), "a file was renamed out of a directory outside every --write")
	assert.Contains(t, got, "write=1", "a write outside every --write was allowed")
	assert.NoFileExists(t, filepath.Join(outside, "written"))
}

// runDeleteHelper is the walled child: the outside directory is not a --write, and
// one shell tries each delete and prints each status.
func runDeleteHelper() int {
	job, outside := os.Getenv("DELETE_JOB"), os.Getenv("DELETE_OUTSIDE")
	script := strings.Join([]string{
		`rm -f "$1/scratch"; echo inside=$?`,
		`rm -f "$2/keep"; [ $? -eq 0 ] && echo rm=0 || echo rm=1`,
		`rm -rf "$2"; [ $? -eq 0 ] && echo rmrf=0 || echo rmrf=1`,
		`mv "$2/keep" "$1/stolen"; [ $? -eq 0 ] && echo mv=0 || echo mv=1`,
		`echo new > "$2/written"; [ $? -eq 0 ] && echo write=0 || echo write=1`,
	}, "\n")
	p, bad := Build(Input{
		Writes: []string{job},
		Home:   filepath.Join(job, "home"),
		Argv:   []string{"sh", "-c", script, "sh", job, outside},
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

// A step's wall (pkg/cardtree, Wall.Argv): its private tmp is the first --write and
// the --tmp, its checkout the second --write and the --cwd. git commits by renaming a new
// index over .git/index, a delete in the checkout, so the commit must succeed under the
// wall. A third --write is a write root too, so a delete there also succeeds
// (docs/SPEC-SANDBOX.md, "deletes-in-every-write-root"). A wall that withholds the remove
// rights from the cwd fails here at commit=128 ("unable to write new index file").
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
	assert.Contains(t, got, "rm=0", "rm of a file in a further --write was refused")
	assert.NoFileExists(t, keep, "a file in a --write was not deleted")
	assert.Contains(t, got, "write=0", "a write in the further --write was refused")
}

// runGitStepHelper is the walled child: the step's wall around one shell that commits in
// the checkout and tries a delete in the shared directory.
func runGitStepHelper() int {
	tmp, repo, shared, git := os.Getenv("STEP_TMP"), os.Getenv("STEP_REPO"), os.Getenv("STEP_SHARED"), os.Getenv("STEP_GIT")
	script := strings.Join([]string{
		`"$4" add a.txt; echo add=$?`,
		`"$4" commit -q -m walled; echo commit=$?`,
		`rm -f "$3/keep"; [ $? -eq 0 ] && echo rm=0 || echo rm=1`,
		`echo new > "$3/written"; echo write=$?`,
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

// Every --write carries REMOVE_FILE and REMOVE_DIR, a second root included, and the
// printed ruleset and the darwin profile say the same thing: write=, and no
// file-write-unlink deny (docs/SPEC-SANDBOX.md, "deletes-in-every-write-root").
func TestEveryWriteRootCarriesRemoveRights(t *testing.T) {
	t.Parallel()
	job := realDir(t, t.TempDir())
	outside := realDir(t, t.TempDir())
	tmp := filepath.Join(job, ".nova-sandbox-tmp")
	p := &Policy{Writes: []string{job, outside}, Tmp: tmp, Cwd: job, Home: job}

	abi := maxKnownABI
	assert.Equal(t, writeSubset(abi), writeRuleMask(p, job, abi))
	assert.Equal(t, writeSubset(abi), writeRuleMask(p, tmp, abi))
	assert.Equal(t, writeSubset(abi), writeRuleMask(p, outside, abi), "a --write lost its remove rights")
	assert.NotZero(t, writeRuleMask(p, outside, abi)&fsRemoveFile, "a --write lost REMOVE_FILE")
	assert.NotZero(t, writeRuleMask(p, outside, abi)&fsRemoveDir, "a --write lost REMOVE_DIR")

	text, err := LandlockPolicyText(p)
	require.NoError(t, err)
	assert.Contains(t, text, "write="+job+"\n")
	assert.Contains(t, text, "write="+outside+"\n")
	assert.NotContains(t, text, "write-nodelete=")

	profile, params, err := DarwinProfile(p)
	require.NoError(t, err)
	assert.NotContains(t, profile, "file-write-unlink", "a --write must not have its unlink taken back")
	assert.NotContains(t, strings.Join(params, " "), "JOBTMP=", "a tmp inside the job dir needs no param of its own")

	// A step's wall: its tmp first, its checkout the cwd, another --write beside them.
	// The checkout and the other root both keep their remove rights.
	repo := realDir(t, t.TempDir())
	step := &Policy{Writes: []string{tmp, repo, outside}, Tmp: tmp, Cwd: repo, Home: tmp}
	assert.Equal(t, writeSubset(abi), writeRuleMask(step, repo, abi), "the step's checkout lost its remove rights")
	assert.Equal(t, writeSubset(abi), writeRuleMask(step, outside, abi), "a further --write lost its remove rights")
	stepText, err := LandlockPolicyText(step)
	require.NoError(t, err)
	assert.Contains(t, stepText, "write="+repo+"\n")
	assert.Contains(t, stepText, "write="+outside+"\n")
	assert.NotContains(t, stepText, "write-nodelete=")
	stepProfile, _, err := DarwinProfile(step)
	require.NoError(t, err)
	assert.NotContains(t, stepProfile, "deny file-write-unlink")

	only := &Policy{Writes: []string{job}, Tmp: tmp, Cwd: job, Home: job}
	plain, _, err := DarwinProfile(only)
	require.NoError(t, err)
	assert.NotContains(t, plain, "file-write-unlink", "a wall whose only --write is the job dir needs no delete deny")
}

func realDir(t *testing.T, dir string) string {
	t.Helper()
	got, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	return got
}
