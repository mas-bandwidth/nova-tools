//go:build linux

package sandbox

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const deleteOutsideHelperEnv = "NOVA_TEST_DELETE_OUTSIDE_HELPER"

// TestTheWallRefusesDeletesOutsideTheJob verifies that the Landlock wall refuses
// unlink, rmdir, and rename-away outside the job directory and its tmp directory,
// even where writes are otherwise allowed.
func TestTheWallRefusesDeletesOutsideTheJob(t *testing.T) {
	if os.Getenv(deleteOutsideHelperEnv) == "1" {
		runDeleteOutsideHelper()
		return
	}

	if runtime.GOOS != "linux" {
		t.Skip("Landlock is Linux only")
	}
	abi, ok := landlockABI()
	if !ok || abi < 1 {
		t.Skip("Landlock is not available on this kernel")
	}

	jobDir := t.TempDir()
	home := filepath.Join(jobDir, "home")
	require.NoError(t, os.MkdirAll(home, 0o755))

	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "outside_file.txt")
	require.NoError(t, os.WriteFile(outsideFile, []byte("outside content\n"), 0o644))

	insideFile := filepath.Join(jobDir, "inside_file.txt")
	require.NoError(t, os.WriteFile(insideFile, []byte("inside content\n"), 0o644))

	cmd := exec.Command(os.Args[0], "-test.run=^TestTheWallRefusesDeletesOutsideTheJob$")
	cmd.Env = append(os.Environ(),
		deleteOutsideHelperEnv+"=1",
		"TEST_JOB_DIR="+jobDir,
		"TEST_OUTSIDE_DIR="+outsideDir,
		"TEST_OUTSIDE_FILE="+outsideFile,
		"TEST_INSIDE_FILE="+insideFile,
	)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()

	// The outside file and directory must still be there
	assert.FileExists(t, outsideFile, "file in outside directory must not be deleted: output: %s %s", out.String(), errb.String())
	assert.DirExists(t, outsideDir, "outside directory must not be deleted")

	// The inside file must have been deleted
	assert.NoFileExists(t, insideFile, "file in job directory must be deleted")

	// The command should report refusal when rm -rf fails
	assert.Error(t, err, "rm -rf of outside directory must exit with an error; stdout=%s stderr=%s", out.String(), errb.String())
}

func runDeleteOutsideHelper() {
	jobDir := os.Getenv("TEST_JOB_DIR")
	outsideDir := os.Getenv("TEST_OUTSIDE_DIR")
	insideFile := os.Getenv("TEST_INSIDE_FILE")

	// Inside the wall:
	// 1. Delete inside file (should succeed)
	// 2. rm -rf outside dir (should fail / be refused)
	script := fmt.Sprintf("rm -f %s && rm -rf %s", insideFile, outsideDir)
	p, bad := Build(Input{
		Writes: []string{jobDir, outsideDir},
		Home:   filepath.Join(jobDir, "home"),
		Argv:   []string{"/bin/sh", "-c", script},
	})
	if len(bad) > 0 {
		fmt.Fprintf(os.Stderr, "build failed: %v\n", bad)
		os.Exit(1)
	}

	code, err := Run(p, os.Environ(), strings.NewReader(""), os.Stdout, os.Stderr, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run failed: %v\n", err)
	}
	os.Exit(code)
}
