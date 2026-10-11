package friend

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/sandbox"
)

// A lane's command environment names the lane's own temp directory: TMPDIR, TMP, TEMP and
// CLAUDE_CODE_TMPDIR point at <lane dir>/tmp, made 0700 before the run and removed after it,
// and two lanes get two directories, so no harness run reaches the shared /tmp (the seat,
// 2026-10-11: every claude run in a friend lane died EEXIST mkdir '/tmp/claude-501').
func TestALaneCommandEnvironmentNamesItsOwnTmpdir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lane1, lane2 := filepath.Join(root, "card-1"), filepath.Join(root, "card-2")
	base := []string{"PATH=/bin", "HOME=/home/x", "TMPDIR=/tmp", "CLAUDE_CODE_TMPDIR=/tmp"}

	env, cleanup, err := LaneTmpEnv(lane1, base)
	require.NoError(t, err)
	defer cleanup()

	tmp1 := LaneTmpDir(lane1)
	for _, name := range []string{"TMPDIR", "TMP", "TEMP", "CLAUDE_CODE_TMPDIR"} {
		assert.Contains(t, env, name+"="+tmp1, name)
	}
	assert.NotContains(t, env, "TMPDIR=/tmp", "the shared /tmp is never named")
	tmps := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, "TMPDIR=") {
			tmps++
		}
	}
	assert.Equal(t, 1, tmps, "one TMPDIR, not two")
	assert.Contains(t, env, "HOME=/home/x", "every other variable passes through")

	fi, err := os.Stat(tmp1)
	require.NoError(t, err)
	assert.True(t, fi.IsDir())
	assert.Equal(t, os.FileMode(0o700), fi.Mode().Perm(), "the lane's temp is private")

	_, cleanup2, err := LaneTmpEnv(lane2, base)
	require.NoError(t, err)
	defer cleanup2()
	assert.NotEqual(t, tmp1, LaneTmpDir(lane2), "two lanes, two directories")

	cleanup()
	assert.NoDirExists(t, tmp1, "the lane's temp is removed after the run")
}

// The wall carries the lane's temp as --tmp and nothing new outside the lane: the flag is
// the only addition, and it is the lane's own directory.
func TestTheLaneWallCarriesItsTmpAndNothingElse(t *testing.T) {
	t.Parallel()
	job := filepath.Join("/friend", "jobs", "card~1")
	w := Wall{Dir: "/friend", Deny: []string{"/self"}, Tmp: LaneTmpDir(job)}
	args := w.Args()
	assert.Equal(t, LaneTmpDir(job), args[len(args)-2], "the flag's value is <lane dir>/tmp")
	assert.Equal(t, "--tmp", args[len(args)-3])
	assert.Equal(t, "--", args[len(args)-1], "-- still closes the flags")
	// a wall with no lane temp (not a lane's) carries no --tmp
	assert.NotContains(t, Wall{Dir: "/friend", Deny: []string{"/self"}}.Args(), "--tmp")
}

// The classifier: a fast no-report run whose output names EEXIST, EACCES, EPERM or ENOENT on
// a path outside its lane is an environment fault naming that line; a report file, a slow run,
// no filesystem error, or a filesystem error inside the lane is none.
func TestAFilesystemErrorOutsideTheLaneIsAnEnvironmentFault(t *testing.T) {
	t.Parallel()
	lane := "/friend/jobs/card~1"
	errLine := "EEXIST: file already exists, mkdir '/tmp/claude-501'"
	out := "starting\nerror: " + errLine + "\n"

	got, ok := LaneEnvironmentFault(1*time.Second, out, "", "", lane)
	require.True(t, ok)
	assert.Contains(t, got, "EEXIST")
	assert.True(t, strings.Contains(got, "/tmp/claude-501"), got)

	_, ok = LaneEnvironmentFault(2*InstantFail, out, "", "", lane)
	assert.False(t, ok, "a slow run is not an environment fault")
	_, ok = LaneEnvironmentFault(1*time.Second, "all is well\n", "", "", lane)
	assert.False(t, ok, "no filesystem error is no environment fault")
	report := filepath.Join(t.TempDir(), "REPORT.md")
	require.NoError(t, os.WriteFile(report, []byte("Verdict: LAND\n"), 0o644))
	_, ok = LaneEnvironmentFault(1*time.Second, out, report, "", lane)
	assert.False(t, ok, "a report is no fault")
	inside := "EACCES: permission denied, open '" + lane + "/tmp/x'"
	_, ok = LaneEnvironmentFault(1*time.Second, inside, "", "", lane)
	assert.False(t, ok, "an error inside the lane's own temp is the card's own")

	for _, name := range []string{"EEXIST", "EACCES", "EPERM", "ENOENT"} {
		assert.Equal(t, name+": boom /var/tmp/x", LaneFilesystemError(name+": boom /var/tmp/x", lane))
	}
	assert.Empty(t, LaneFilesystemError("all is well\n", lane))
}

// The functional proof: a fake harness that writes to $TMPDIR and fails if $TMPDIR is unset or
// /tmp, run through a lane, succeeds; a write outside the lane is refused. The wall makes the
// lane's temp and points TMPDIR, TMP, TEMP and CLAUDE_CODE_TMPDIR at it.
func TestALaneHarnessWritesItsOwnTmpdirAndNothingOutsideIt(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("the wall has a body on linux and darwin only")
	}
	if _, ok := sandbox.Available(); !ok {
		t.Skip("no OS wall on this machine: " + sandbox.Note())
	}
	if os.Geteuid() == 0 {
		t.Skip("the wall does not run as root")
	}
	bin, err := os.Executable()
	require.NoError(t, err)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	self := filepath.Join(home, "rowan-working", "rowan-new")
	friendDir := filepath.Join(root, "friend")
	job := filepath.Join(friendDir, "jobs", "card~1")
	config := filepath.Join(root, "config")
	for _, d := range []string{filepath.Join(self, "memory"), job, config} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	run := envExec(laneWallChild+"=1", "HOME="+home)
	wall := Wall{Self: []string{bin}, Dir: friendDir, ConfigDir: config, Deny: []string{"~/rowan-working/rowan-new"}}

	// the fake harness: it writes its scratch under $TMPDIR, fails when $TMPDIR is unset or
	// /tmp, and its attempt at an outside write must be refused by the wall.
	script := `test -n "$TMPDIR" || { echo "TMPDIR unset"; exit 3; }; ` +
		`test "$TMPDIR" != /tmp || { echo "TMPDIR is /tmp"; exit 4; }; ` +
		`printf x > "$TMPDIR/probe" || exit 5; ` +
		`if printf x > /tmp/lane-probe-$$ 2>/dev/null; then echo "outside write allowed"; exit 6; fi; ` +
		`echo "HARNESS TMPDIR=$TMPDIR CLAUDE_CODE_TMPDIR=$CLAUDE_CODE_TMPDIR"`
	exec := wall.Exec(run)
	out, exit, err := exec(LaneContext(t.Context()), job, "/bin/sh", []string{"-c", script}, "")
	require.NoError(t, err, out)
	assert.Equal(t, 0, exit, out)
	assert.Contains(t, out, "HARNESS TMPDIR="+LaneTmpDir(job))
	assert.Contains(t, out, "CLAUDE_CODE_TMPDIR="+LaneTmpDir(job))
	assert.NoDirExists(t, LaneTmpDir(job), "the lane's temp is removed after the run")
}
