package friend

import (
	"context"
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

// Requirement (3) at the loop: a run that ends within InstantFail with no REPORT.md and no
// RESULT.md and whose output names a filesystem error outside its lane reaches laneDone and
// ends as the friend's ENVIRONMENT fault: the card stays in the lane's hand, its attempt is
// not counted, her lanes pause at once, and one judgment naming the error line is pushed to
// the seat (lanes.go laneDone, lane_tmpdir.go environmentFault). Remove the environment-fault
// branch from laneDone and this test sees a counted attempt and no pause; remove the pause
// from environmentFault and it sees no pause.
func TestALaneFastFilesystemErrorPausesTheFriendAndKeepsTheCard(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.d.m = Start(t0)
	r.d.Coordinator = "ada"
	now := t0.Add(2 * time.Minute)
	jobDir := r.d.Dir
	card := Card{ID: "c1", Brief: "brief", Outbox: filepath.Join(jobDir, "outbox")}
	ln := &lane{n: 1, card: &card, job: LaneJob{Dir: jobDir, Card: card}}
	ls := &laneSet{}
	tail := &outputTail{}
	tail.add([]byte("starting\nEEXIST: file already exists, mkdir '/tmp/claude-501'\n"))
	turn := &turn{subjects: `"card c1"`, started: now.Add(-time.Second), tail: tail}
	l := &loop{d: r.d, b: r.bus, ctx: context.Background(), lanes: ls, inHand: map[string]bool{}}

	l.laneDone(laneResult{ln: ln, turn: LaneTurn{Exit: 1}, t: turn}, now)

	require.NotNil(t, ln.card, "the card stays in the lane's hand")
	assert.Same(t, &card, ln.card)
	assert.Equal(t, 0, ln.attempts, "an environment fault is not the card's failed attempt")
	assert.True(t, ls.gov.Paused(now), "her lanes pause at once on an environment fault")
	assert.Empty(t, ls.given, "the card is not set aside")
	assert.NoFileExists(t, card.Report(), "nothing was finished")
	judged := strings.Join(r.adaGot(t), "\n")
	assert.Contains(t, judged, "EEXIST", "the judgment names the error line")
	assert.Contains(t, judged, "/tmp/claude-501")
}

// The functional proof: a fake harness that writes to $TMPDIR and fails if $TMPDIR is unset or
// /tmp, run through a lane, succeeds; a write outside the lane is refused. The wall makes the
// lane's temp, points TMPDIR, TMP, TEMP and CLAUDE_CODE_TMPDIR at it, and two lanes get two
// directories.
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
	config := filepath.Join(root, "config")
	job1, job2 := filepath.Join(friendDir, "jobs", "card~1"), filepath.Join(friendDir, "jobs", "card~2")
	for _, d := range []string{filepath.Join(self, "memory"), job1, job2, config} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	run := envExec(laneWallChild+"=1", "HOME="+home)
	wall := Wall{Self: []string{bin}, Dir: friendDir, ConfigDir: config, Deny: []string{"~/rowan-working/rowan-new"}}

	// the fake harness: it writes its scratch under $TMPDIR, fails when $TMPDIR is unset, /tmp
	// or not named by all four variables, and its attempt at an outside write must be refused
	// by the wall.
	script := `test -n "$TMPDIR" || { echo "TMPDIR unset"; exit 3; }; ` +
		`test "$TMPDIR" != /tmp || { echo "TMPDIR is /tmp"; exit 4; }; ` +
		`test "$TMPDIR" = "$TMP" && test "$TMPDIR" = "$TEMP" && test "$TMPDIR" = "$CLAUDE_CODE_TMPDIR" || { echo "TMPDIR=$TMPDIR TMP=$TMP TEMP=$TEMP CLAUDE=$CLAUDE_CODE_TMPDIR"; exit 7; }; ` +
		`printf x > "$TMPDIR/probe" || exit 5; ` +
		`if printf x > /tmp/lane-probe-$$ 2>/dev/null; then echo "outside write allowed"; exit 6; fi; ` +
		`echo "HARNESS TMPDIR=$TMPDIR CLAUDE_CODE_TMPDIR=$CLAUDE_CODE_TMPDIR"`
	exec := wall.Exec(run)
	var tmps []string
	for _, job := range []string{job1, job2} {
		out, exit, err := exec(LaneContext(t.Context()), job, "/bin/sh", []string{"-c", script}, "")
		require.NoError(t, err, out)
		require.Equal(t, 0, exit, out)
		assert.Contains(t, out, "HARNESS TMPDIR="+LaneTmpDir(job))
		assert.Contains(t, out, "CLAUDE_CODE_TMPDIR="+LaneTmpDir(job))
		assert.NoDirExists(t, LaneTmpDir(job), "the lane's temp is removed after the run")
		tmps = append(tmps, LaneTmpDir(job))
	}
	assert.NotEqual(t, tmps[0], tmps[1], "two lanes, two directories")
}
