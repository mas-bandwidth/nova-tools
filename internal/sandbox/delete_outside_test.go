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

// DEMANDED (docs/SPEC-SANDBOX.md, "deletes-only-in-the-job-dir-p.w1"). Two children ran
// rm -rf on a variable path and the wall let it through, because every --write carried
// the remove rights. The outside directory here is itself a --write, the shape of a shared
// cache or a config dir: writing there must still work, and rm -rf of it, an rm of a file
// in it and a rename of a file out of it must each be refused with the file left in place.
// A delete in the job dir succeeds. A mutation that hands every write the whole handled
// set again (the bug) turns this red at rmrf=0.
func TestTheWallRefusesDeletesOutsideTheJob(t *testing.T) {
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
	// Outside the job, in a write that is not the job's: refused, and nothing is gone.
	assert.Contains(t, got, "rmrf=1", "rm -rf of a directory outside the job was not refused")
	assert.Contains(t, got, "rm=1", "rm of a file outside the job was not refused")
	assert.Contains(t, got, "mv=1", "rename of a file out of a directory outside the job was not refused")
	assert.Contains(t, got+errb.String(), "Permission denied", "the refusal was not the kernel's")
	assert.FileExists(t, keep, "the wall let a file outside the job be deleted")
	assert.FileExists(t, deep, "the wall let rm -rf delete beneath a directory outside the job")
	assert.NoFileExists(t, filepath.Join(job, "stolen"), "a file was renamed out of a directory outside the job")
	// Writing there is still allowed: the wall refuses the delete, not the write.
	assert.Contains(t, got, "write=0", "a write in the outside --write was refused, and only deletes should be")
	assert.FileExists(t, filepath.Join(outside, "written"))
}

// runDeleteHelper is the walled child: it builds the policy with the outside directory
// as a second --write and runs one shell that tries each delete and prints each status.
func runDeleteHelper() int {
	job, outside := os.Getenv("DELETE_JOB"), os.Getenv("DELETE_OUTSIDE")
	script := strings.Join([]string{
		`rm -f "$1/scratch"; echo inside=$?`,
		`rm -f "$2/keep"; [ $? -eq 0 ] && echo rm=0 || echo rm=1`,
		`rm -rf "$2"; [ $? -eq 0 ] && echo rmrf=0 || echo rmrf=1`,
		`mv "$2/keep" "$1/stolen"; [ $? -eq 0 ] && echo mv=0 || echo mv=1`,
		`echo new > "$2/written"; echo write=$?`,
	}, "\n")
	p, bad := Build(Input{
		Writes: []string{job, outside},
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

// The masks, without a wall: the job dir and its tmp keep REMOVE_FILE and REMOVE_DIR, any
// other write loses exactly those two and keeps every write right, and the printed
// ruleset and the darwin profile say the same thing.
func TestWritesOutsideTheJobCarryNoRemoveRights(t *testing.T) {
	t.Parallel()
	job := realDir(t, t.TempDir())
	outside := realDir(t, t.TempDir())
	tmp := filepath.Join(job, ".nova-sandbox-tmp")
	p := &Policy{Writes: []string{job, outside}, Tmp: tmp, Cwd: job, Home: job}

	abi := maxKnownABI
	assert.Equal(t, writeSubset(abi), writeRuleMask(p, job, abi))
	assert.Equal(t, writeSubset(abi), writeRuleMask(p, tmp, abi))
	assert.Equal(t, writeSubset(abi)&^uint64(fsRemoveFile|fsRemoveDir), writeRuleMask(p, outside, abi))
	assert.NotZero(t, writeRuleMask(p, outside, abi)&fsWriteFile, "an outside write lost its write right")
	assert.NotZero(t, writeRuleMask(p, outside, abi)&fsMakeReg, "an outside write lost its create right")

	text, err := LandlockPolicyText(p)
	require.NoError(t, err)
	assert.Contains(t, text, "write="+job+"\n")
	assert.Contains(t, text, "write-nodelete="+outside+"\n")

	profile, params, err := DarwinProfile(p)
	require.NoError(t, err)
	assert.Contains(t, profile, `(deny file-write-unlink (subpath (param "WRITE1")))`)
	assert.NotContains(t, profile, `(deny file-write-unlink (subpath (param "WRITE0")))`)
	deny := strings.Index(profile, `(deny file-write-unlink`)
	allow := strings.Index(profile, `(allow file-write-unlink (subpath (param "WRITE0")))`)
	assert.Greater(t, allow, deny, "the job dir's unlink must be given back after the denies (last match wins)")
	assert.Greater(t, deny, strings.Index(profile, `(subpath (param "HOME"))`), "the denies must come after every write grant")
	assert.NotContains(t, strings.Join(params, " "), "JOBTMP=", "a tmp inside the job dir needs no param of its own")

	// A tmp outside the job dir is the second place deletes are allowed.
	p.Tmp = outside
	assert.Equal(t, writeSubset(abi), writeRuleMask(p, outside, abi))
	only := &Policy{Writes: []string{job}, Tmp: tmp, Cwd: job, Home: job}
	plain, _, err := DarwinProfile(only)
	require.NoError(t, err)
	assert.NotContains(t, plain, "file-write-unlink", "a wall with only the job dir writable needs no delete rule")
}

func realDir(t *testing.T, dir string) string {
	t.Helper()
	got, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	return got
}
