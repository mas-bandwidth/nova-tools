//go:build functional

package secrets

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The --no-pr road walked against a real git: what it pins is the store's own state
// after each injected failure (its branch, HEAD and seat file, and that exec would
// still take it), which only a real repository has. It waits on git processes, a
// second or more a case, so it is a functional test, never a skipped unit one.

// TestSealNoPRMakesNoGHCalls: --no-pr must not leave the store on the seal
// branch (#2016). exec requires HEAD to match the remote-tracking ref, and a
// leftover seal/* branch has no upstream, so every later card on that bench
// is refused. After success or an injected git failure the store is back on
// its starting branch with the starting worktree; a successful seal commit
// remains retrievable on the named branch. No gh, no push, no pull. The
// placeholder is a fixture, not a credential.
func TestSealNoPRMakesNoGHCalls(t *testing.T) {
	t.Parallel()

	skipPOSIXFakesOnWindows(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	const placeholder = "placeholder-value"
	wantBranch := "seal/rowan-TARGET-20260917-120000"
	cases := []struct {
		name       string
		failGit    []string
		wantErr    bool
		wantCommit bool
	}{
		{name: "success", wantCommit: true},
		{name: "checkout -b fails", failGit: []string{"checkout", "-b"}, wantErr: true},
		{name: "add fails", failGit: []string{"add"}, wantErr: true},
		{name: "commit fails", failGit: []string{"commit"}, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newSealFixture(t, "TARGET: old\n")
			initTrackedGitStore(t, f.storeDir)

			gitBin, err := exec.LookPath("git")
			require.NoError(t, err)
			origBranch := gitC(t, f.storeDir, "rev-parse", "--abbrev-ref", "HEAD")
			origHEAD := gitC(t, f.storeDir, "rev-parse", "HEAD")
			origSeat, err := os.ReadFile(filepath.Join(f.storeDir, "rowan.yaml"))
			require.NoError(t, err)

			var gitLog []string
			opts := f.options(t, "TARGET", placeholder+"\n", true)
			opts.GitPath = gitBin
			opts.Exec = func(stdin io.Reader, env []string, dir, name string, args ...string) ([]byte, error) {
				if name == gitBin || filepath.Base(name) == "git" {
					gitLog = append(gitLog, strings.Join(args, " "))
					if gitArgsHavePrefix(args, tc.failGit) {
						return nil, fmt.Errorf("injected git failure")
					}
					return realExecCommand(stdin, env, dir, gitBin, args...)
				}
				return realExecCommand(stdin, env, dir, name, args...)
			}

			line, runErr := RunSeal(opts)
			assert.NotContains(t, line, placeholder, "value leaked into the OK line: %s", line)
			if runErr != nil {
				assert.NotContains(t, runErr.Error(), placeholder, "value leaked into the error: %v", runErr)
			}
			got := readMaybe(t, f.ghArgs)
			assert.Empty(t, got, "--no-pr called gh:\n%s", got)
			joined := strings.Join(gitLog, "\n")
			assert.NotContains(t, joined, "push", "--no-pr pushed:\n%s", joined)
			assert.NotContains(t, joined, "pull", "--no-pr pulled:\n%s", joined)

			gotBranch := gitC(t, f.storeDir, "rev-parse", "--abbrev-ref", "HEAD")
			gotHEAD := gitC(t, f.storeDir, "rev-parse", "HEAD")
			gotSeat, err := os.ReadFile(filepath.Join(f.storeDir, "rowan.yaml"))
			require.NoError(t, err)
			assert.Equal(t, origBranch, gotBranch, "final branch %s, want starting branch %s", gotBranch, origBranch)
			assert.Equal(t, origHEAD, gotHEAD, "final HEAD %s, want starting HEAD %s", gotHEAD, origHEAD)
			assert.Equal(t, string(origSeat), string(gotSeat), "final worktree seat file differs from the starting tree")
			{
				st, err := CheckGitWorkingCopy(f.storeDir)
				if assert.NoError(t, err, "exec would refuse this store after --no-pr: %v", err) {
					assert.True(t, st.Clean, "exec would refuse this store after --no-pr: %v", err)
				}
			}

			if tc.wantErr {
				require.Error(t, runErr, "RunSeal succeeded, want injected git failure")
			} else {
				require.NoError(t, runErr, "RunSeal: %v", runErr)
			}

			if !tc.wantErr {
				assert.Contains(t, joined, "checkout", "--no-pr did not checkout/commit:\n%s", joined)
				assert.Contains(t, joined, "commit", "--no-pr did not checkout/commit:\n%s", joined)
				assert.Contains(t, line, "committed", "--no-pr line should say committed: %s", line)
				assert.Contains(t, line, "branch="+wantBranch, "--no-pr line should name the seal branch: %s", line)
			}

			sealSHA, sealErr := gitCErr(t, f.storeDir, "rev-parse", wantBranch)
			if tc.wantCommit {
				require.NoError(t, sealErr, "seal commit is not retrievable on %s: %v", wantBranch, sealErr)
				assert.NotEqual(t, origHEAD, sealSHA, "seal branch %s still points at the starting commit", wantBranch)
				blob := gitC(t, f.storeDir, "show", wantBranch+":rowan.yaml")
				assert.Contains(t, blob, "ENC[marker]", "seal commit does not hold the new ciphertext")
				assert.NotContains(t, blob, placeholder, "placeholder leaked into the committed seat file")
			} else if sealErr == nil {
				assert.Equal(t, origHEAD, sealSHA, "injected failure still moved %s to %s", wantBranch, sealSHA)
			}
		})
	}
}

func gitArgsHavePrefix(args, prefix []string) bool {
	if len(prefix) == 0 || len(args) < len(prefix) {
		return false
	}
	for i, p := range prefix {
		if args[i] != p {
			return false
		}
	}
	return true
}
