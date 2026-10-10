package update

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWatchStderrOnRefusal(t *testing.T) {
	t.Parallel()

	checksDir := t.TempDir()
	checks := filepath.Join(checksDir, "checks.tsv")

	// Create a check that will fail
	failScript := filepath.Join(checksDir, "fail.sh")
	require.NoError(t, os.WriteFile(failScript, []byte("#!/bin/sh\necho fail\nexit 1\n"), 0755))

	rows := []string{
		"check\tcommand\towner",
		"bad-check\t" + failScript + "\towner",
	}
	require.NoError(t, os.WriteFile(checks, []byte(strings.Join(rows, "\n")+"\n"), 0600))

	var outBuf, errBuf bytes.Buffer
	env := Environment{}
	rc := watchMain("nova-update", []string{"--adopt", checks}, &outBuf, &errBuf, env)

	require.Equal(t, 1, rc, "want exit 1 with refused check")

	// The ADOPT DONE line should be on stderr when any check is refused
	doneOnStdout := strings.Contains(outBuf.String(), "ADOPT DONE")
	doneOnStderr := strings.Contains(errBuf.String(), "ADOPT DONE")

	require.True(t, doneOnStderr, "ADOPT DONE should be on stderr when checks are refused")
	require.False(t, doneOnStdout, "ADOPT DONE should not be on stdout when checks are refused")
}
