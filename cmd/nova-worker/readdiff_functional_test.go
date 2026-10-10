//go:build functional

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// A read whose base branch cannot be fetched from origin is refused at staging, visibly, and
// writes no JOB.md: the checkout's own branch is the bench mirror's and may be older than
// the work's start, and a start taken from it would be the wrong diff named as exactly the
// work's. The witness stages the stale-mirror read, then points that checkout's origin at a
// directory that does not exist before the frame is installed: native prints a STAGE FAIL
// line naming the base and the fetch error, exits 2, and the member reads the launch as a
// staging refusal (no child ran; the sprint deals the read again). native runs as the member
// runs it: the built binary, the frame in a file, its output in the launch's log.
func TestAReadWhoseBaseCannotBeFetchedIsRefusedAtStaging(t *testing.T) {
	t.Parallel()
	require.NoError(t, buildShared())
	f := newReadFixture(t, []string{"landed-a.txt", "landed-b.txt"}, []string{"landed-c.txt"}, true)
	root, slot := aSlot(t)
	job := f.stage(t, slot)
	runGit(t, filepath.Join(job, swarm.JobRepo), "remote", "set-url", "origin", filepath.Join(t.TempDir(), "no-such-origin.git"))

	framePath, cardPath, logPath := filepath.Join(root, "w.r1"+cardcontract.FrameName), filepath.Join(root, "w.r1.card.md"), filepath.Join(root, "w.r1.native.log")
	require.NoError(t, cardcontract.WriteFrame(framePath, *f.frame("main")))
	write(t, cardPath, "read the work\n")
	logf, err := os.Create(logPath)
	require.NoError(t, err)
	cmd := exec.Command(builtTool, "native", "--harness", builtHarness, "--model", "fake/fake-model", "--card", cardPath, "--frame", framePath,
		"--slot", slot, "--root", root, "--deadline", "30s", "--tokens", "unmetered", "--label", "w.r1", "--no-wall")
	cmd.Stdout, cmd.Stderr = logf, logf
	runErr := cmd.Run()
	require.NoError(t, logf.Close())
	log, err := os.ReadFile(logPath)
	require.NoError(t, err)
	var exit *exec.ExitError
	require.ErrorAs(t, runErr, &exit, "native refuses:\n%s", log)
	assert.Equal(t, 2, exit.ExitCode(), "%s", log)

	m := nativeStageFail.FindSubmatch(log)
	require.NotNil(t, m, "a STAGE FAIL line:\n%s", log)
	reason := string(m[1])
	assert.Contains(t, reason, "staging refused: the read's start")
	assert.Contains(t, reason, "the base branch main could not be fetched from origin")
	assert.Contains(t, reason, "no-such-origin.git", "the fetch error is in the reason")
	assert.Contains(t, string(log), "NATIVE REFUSED: staging refused: the read's start")
	assert.NoFileExists(t, filepath.Join(job, cardcontract.JobName), "no JOB.md names a start the read cannot back")

	c := &nativeChild{card: "w.r1", logPath: logPath, results: filepath.Join(slot, "results"), job: job, done: make(chan struct{})}
	res := c.Result()
	assert.Equal(t, member.EndStaging, res.End, "the member reads the launch as refused at staging")
	assert.Contains(t, res.Staging, "the base branch main could not be fetched from origin")
}
