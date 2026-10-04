//go:build darwin

package bus

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gitProcesses, darwinGitCwd and darwinPIDAlive all call subproc.Command directly
// to run ps and lsof: neither subproc.Command nor the wrappers carry an injectable
// seam variable, so the three wrappers cannot be reached without spawning a
// subprocess and are not exercised here. Each one delegates to a pure function
// that takes every input it needs as an argument — gitProcsFromPS, lsofCwds and
// darwinStatAlive — and those seams are pinned below, table-driven, with the
// shapes the wrappers produce.

// TestRepairProcDarwinCoverGitProcesses covers gitProcsFromPS, the seam
// gitProcesses delegates to after ps and lsof run. gitProcesses itself calls
// subproc.Command("ps", ...) directly — no seam — so it cannot be reached
// without a subprocess; gitProcsFromPS is pinned with the ps output, cwd map,
// cwd error and alive function the call hands it. Main path: a git of this
// account placed by its lsof cwd. Refusal: this account's live git with no cwd
// and no absolute -C is an incomplete scan.
func TestRepairProcDarwinCoverGitProcesses(t *testing.T) {
	t.Parallel()
	self := strconv.Itoa(os.Geteuid())
	for _, c := range []struct {
		name    string
		psOut   string
		cwds    map[string]string
		cwdErr  error
		alive   func(string) (bool, error)
		wantLen int
		wantErr bool
	}{
		{
			name:    "main path: our git placed by cwd from lsof",
			psOut:   self + " 77 git status\n",
			cwds:    map[string]string{"77": "/Users/test/bus"},
			alive:   func(string) (bool, error) { return true, nil },
			wantLen: 1,
			wantErr: false,
		},
		{
			name:    "refusal: our live git with no cwd and no -C is unknown",
			psOut:   self + " 77 git status\n",
			cwds:    map[string]string{},
			alive:   func(string) (bool, error) { return true, nil },
			wantLen: 0,
			wantErr: true,
		},
		{
			name:    "a cwd error does not block a git that locates its checkout absolutely",
			psOut:   self + " 77 git -C /Users/test/bus status\n",
			cwds:    nil,
			cwdErr:  errors.New("lsof failed code=1 lsof: WARNING: can't stat"),
			alive:   func(string) (bool, error) { return true, nil },
			wantLen: 1,
			wantErr: false,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			procs, err := gitProcsFromPS(c.psOut, self, c.cwds, c.cwdErr, c.alive)
			if c.wantErr {
				require.Error(t, err, "procs=%+v", procs)
				assert.True(t, strings.HasPrefix(err.Error(), ownershipUnknown),
					"err=%v, want %q", err, ownershipUnknown)
			} else {
				require.NoError(t, err, "procs=%+v", procs)
				assert.Len(t, procs, c.wantLen, "procs=%+v", procs)
			}
		})
	}
}

// TestRepairProcDarwinCoverDarwinGitCwd covers lsofCwds, the seam darwinGitCwd
// delegates to after lsof runs. darwinGitCwd itself calls subproc.Command("lsof",
// ...) directly — no seam — so it cannot be reached without a subprocess;
// lsofCwds is pinned with the stdout, stderr, exit code and run error the call
// hands it. Main path: a successful lsof with pid and cwd lines. Refusal:
// lsof exits 1 with output, which is a failure, not the empty "no git" scan.
func TestRepairProcDarwinCoverDarwinGitCwd(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		stdout  string
		stderr  string
		code    int
		runErr  error
		wantLen int
		wantErr bool
	}{
		{
			name:    "main path: lsof success with pid and cwd lines",
			stdout:  "p77\nfcwd\nn/bus\np78\nfcwd\nn/other\n",
			stderr:  "",
			code:    0,
			runErr:  nil,
			wantLen: 2,
			wantErr: false,
		},
		{
			name:    "refusal: lsof exits 1 with a warning is a failure, not an empty scan",
			stdout:  "",
			stderr:  "lsof: WARNING: can't stat() nfs file system /Volumes/x\n",
			code:    1,
			runErr:  errors.New("exit status 1"),
			wantLen: 0,
			wantErr: true,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			cwds, err := lsofCwds(c.stdout, c.stderr, c.code, c.runErr)
			if c.wantErr {
				require.Error(t, err, "cwds=%+v", cwds)
				assert.Nil(t, cwds, "cwds should be nil on error")
			} else {
				require.NoError(t, err, "cwds=%+v", cwds)
				assert.Len(t, cwds, c.wantLen, "cwds=%+v", cwds)
			}
		})
	}
}

// TestRepairProcDarwinCoverDarwinPIDAlive covers darwinStatAlive, the seam
// darwinPIDAlive delegates to after ps -p runs. darwinPIDAlive itself calls
// subproc.Command("ps", "-p", pid, ...) directly — no seam — so it cannot be
// reached without a subprocess; darwinStatAlive is pinned with the stat string
// the ps output carries. Main path: a sleeping process is alive. Refusal:
// a zombie is dead.
func TestRepairProcDarwinCoverDarwinPIDAlive(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		stat string
		want bool
	}{
		{name: "main path: sleeping process is alive", stat: "S\n", want: true},
		{name: "main path: running process is alive", stat: "R+\n", want: true},
		{name: "refusal: zombie is dead", stat: "Z\n", want: false},
		{name: "refusal: zombie with flags is dead", stat: "Z+\n", want: false},
		{name: "refusal: no process is not alive", stat: "", want: false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, darwinStatAlive(c.stat))
		})
	}
}
