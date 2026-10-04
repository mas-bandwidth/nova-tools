package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/cardtree"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNativeCompletesStepCommitsWithoutChangingTheRawResult(t *testing.T) {
	t.Parallel()
	job := t.TempDir()
	repo := filepath.Join(job, "repo")
	require.NoError(t, os.Mkdir(repo, 0o755))
	runGit(t, repo, "init", "-b", "owned")
	runGit(t, repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "actual work")
	head := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	raw := "head: " + head[:9] + "\nbranch: guessed\nverdict: ok\ngate: -\noutput: -\nreport: measured\n\n## Body\nstep 3: ok " + head[:13] + " completed\n"
	path := filepath.Join(job, "RESULT.md")
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o644))
	require.NoError(t, completeNativeResult(job, ""))
	finish, exists := cardcontract.ReadFinish(job)
	require.True(t, exists)
	assert.Equal(t, head, finish.Head)
	assert.Equal(t, "owned", finish.Branch)
	assert.Equal(t, head, cardtree.ParseVerdicts(finish.Body)["3"].Sha)
	unchanged, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, raw, string(unchanged))
}

func TestNativeRefusesACommitOutsideTheResultHistory(t *testing.T) {
	t.Parallel()
	job := t.TempDir()
	repo := filepath.Join(job, "repo")
	require.NoError(t, os.Mkdir(repo, 0o755))
	runGit(t, repo, "init", "-b", "owned")
	runGit(t, repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "first")
	head := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "checkout", "-b", "other")
	runGit(t, repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "other")
	other := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "checkout", "owned")
	raw := "head: " + head + "\nbranch: owned\nverdict: ok\ngate: -\noutput: -\nreport: measured\n\n## Body\nstep 3: ok " + other[:14] + " claimed\n"
	require.NoError(t, os.WriteFile(filepath.Join(job, "RESULT.md"), []byte(raw), 0o644))
	err := completeNativeResult(job, "")
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "outside HEAD's history"), err)
	_, exists := cardcontract.ReadFinish(job)
	assert.False(t, exists, "unknown execution gets no completed finish")
}

func TestNativeCompletionChecksTheOutputArtifact(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, output string
		wantOK       bool
	}{
		{"existing output", "gate.log", true},
		{"missing output", "missing.log", false},
		{"outside output", "outside/gate.log", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			job := t.TempDir()
			repo := filepath.Join(job, "repo")
			require.NoError(t, os.Mkdir(repo, 0o755))
			runGit(t, repo, "init", "-b", "owned")
			runGit(t, repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "work")
			head := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
			require.NoError(t, os.WriteFile(filepath.Join(repo, "gate.log"), []byte("actual output"), 0o644))
			outside := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(outside, "gate.log"), []byte("unrelated"), 0o644))
			require.NoError(t, os.Symlink(outside, filepath.Join(repo, "outside")))
			raw := "head: " + head + "\nbranch: owned\nverdict: not-done\ngate: gate command\noutput: " + tc.output + "\nreport: actual failure\n"
			require.NoError(t, os.WriteFile(filepath.Join(job, "RESULT.md"), []byte(raw), 0o644))
			err := completeNativeResult(job, "")
			if tc.wantOK {
				require.NoError(t, err)
				finish, exists := cardcontract.ReadFinish(job)
				require.True(t, exists)
				assert.Equal(t, "not-done", finish.Verdict)
			} else {
				require.Error(t, err)
				_, exists := cardcontract.ReadFinish(job)
				assert.False(t, exists)
			}
		})
	}
}
