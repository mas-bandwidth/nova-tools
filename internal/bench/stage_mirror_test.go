package bench

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testSha = "0123456789abcdef0123456789abcdef01234567"
	testRun = DefaultRoot + "/run.AbCd1234"
)

// fakeBench is the bench's shell: it answers the make step with a run directory, the
// stage line with stageCode and stageErr on its stderr, and every other line with 0. It
// records each line and each copy, and moves the fake clock a second a line.
type fakeBench struct {
	lines, copies []string
	stageCode     int
	stageErr      string
	copyErr       error
	clock         *fakeClock
}

func (f *fakeBench) Shell(_ context.Context, _, line string, stdout, stderr io.Writer) (int, error) {
	f.lines = append(f.lines, line)
	f.clock.tick(time.Second)
	switch {
	case strings.HasPrefix(line, "mkdir -p "):
		_, _ = io.WriteString(stdout, testRun+"\n") // ignored: a bytes.Buffer's write
		return 0, nil
	case strings.HasPrefix(line, "test -f "):
		_, _ = io.WriteString(stderr, f.stageErr) // ignored: a bytes.Buffer's write
		return f.stageCode, nil
	}
	return 0, nil
}

func (f *fakeBench) Copy(_ context.Context, host, src, dst string, _ bool, stderr io.Writer) error {
	f.copies = append(f.copies, host+" "+src+" -> "+dst)
	f.clock.tick(2 * time.Second)
	if f.copyErr != nil {
		_, _ = io.WriteString(stderr, "tar: Unexpected EOF in archive\ntar: Error is not recoverable: exiting now\n") // ignored: a bytes.Buffer's write
	}
	return f.copyErr
}

// fakeClock is a run's clock that moves only when told.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time       { return c.t }
func (c *fakeClock) tick(d time.Duration) { c.t = c.t.Add(d) }
func newClock() *fakeClock                { return &fakeClock{t: time.Unix(1_800_000_000, 0)} }
func (f *fakeBench) opts(st *MirrorStage) Options {
	return Options{Hosts: []string{"vision"}, Stage: st, Argv: []string{"go", "build", "./..."}, Now: f.clock.now}
}

func testStage() *MirrorStage {
	return &MirrorStage{Mirror: MirrorDir("nova-tools"), Remote: "/srv/git/nova-tools.git", Ref: GateRef("sprint/mechanical", testSha), Sha: testSha}
}

// fakeRefs is the lander's git: the pushes and deletes it was asked for.
type fakeRefs struct {
	calls   []string
	pushErr error
	ctxErr  error // the delete's context error, as it was called
}

func (g *fakeRefs) Push(_ context.Context, sha, ref string) error {
	g.calls = append(g.calls, "push "+sha+":"+ref)
	return g.pushErr
}

func (g *fakeRefs) Delete(ctx context.Context, ref string) error {
	g.calls = append(g.calls, "delete "+ref)
	g.ctxErr = ctx.Err()
	return nil
}

// A mirror stage fetches the one commit into the bench's mirror by its temporary ref and
// makes the run's tree a clone borrowing the mirror, detached at the sha: no copy, only
// the line crosses the wire, and the stage says so.
func TestAMirrorStageFetchesOneShaAndChecksItOutFromTheMirror(t *testing.T) {
	t.Parallel()
	f := &fakeBench{clock: newClock()}
	st := testStage()
	var said []string
	o := f.opts(st)
	o.Staged = func(s Stage) { said = append(said, s.Line()) }
	res, err := Run(context.Background(), f, o)
	require.NoError(t, err)
	assert.Empty(t, f.copies, "a mirror stage copies nothing")
	require.Len(t, f.lines, 4)
	assert.Equal(t, MakeLine(DefaultRoot), f.lines[0])
	assert.Equal(t, StageLine(*st, testRun+"/repo"), f.lines[1])
	assert.Equal(t, ExecLine(testRun, DefaultCache, o.Argv), f.lines[2])
	assert.Equal(t, RemoveLine(testRun), f.lines[3])
	assert.True(t, res.Removed)

	line := f.lines[1]
	assert.True(t, strings.HasPrefix(line, "test -f 'nova-bench/mirror/nova-tools.git'/HEAD || "), line)
	assert.Contains(t, line, `git -C 'nova-bench/mirror/nova-tools.git' cat-file -e '`+testSha+`^{commit}' 2>/dev/null || `, "a commit the mirror holds is not fetched again")
	assert.Contains(t, line, `src=$(git -C 'nova-bench/mirror/nova-tools.git' config --get remote.origin.url) || src='/srv/git/nova-tools.git'`)
	assert.Contains(t, line, `git -C 'nova-bench/mirror/nova-tools.git' fetch --quiet --no-tags --no-write-fetch-head "$src" 'refs/nova-gate/sprint-mechanical-0123456789ab'`)
	assert.Equal(t, 1, strings.Count(line, " fetch "), "one fetch, of the one ref")
	assert.Contains(t, line, `git clone --quiet --shared --no-checkout 'nova-bench/mirror/nova-tools.git' '`+testRun+`/repo'`)
	assert.True(t, strings.HasSuffix(line, `git -C '`+testRun+`/repo' checkout --quiet --detach '`+testSha+`'`), line)

	assert.Equal(t, "mirror", res.Stage.Via)
	assert.Equal(t, "copy vision 0MB 1.0s via mirror", res.Stage.Line())
	assert.Equal(t, []string{"copy vision 0MB 1.0s via mirror"}, said, "the stage is said before the command runs")
}

// The temporary ref is pushed before the gate and deleted after it, whatever the gate
// did and even when the caller's context has ended; a push that fails runs no gate and
// leaves nothing to delete.
func TestTheTemporaryRefIsRemovedAfterTheGate(t *testing.T) {
	t.Parallel()
	ref := GateRef("sprint/mechanical", testSha)
	assert.Equal(t, "refs/nova-gate/sprint-mechanical-0123456789ab", ref)

	t.Run("green", func(t *testing.T) {
		t.Parallel()
		g := &fakeRefs{}
		ran := false
		require.NoError(t, WithGateRef(context.Background(), g, testSha, ref, func() error { ran = true; return nil }))
		assert.True(t, ran)
		assert.Equal(t, []string{"push " + testSha + ":" + ref, "delete " + ref}, g.calls)
	})
	t.Run("the gate failed and the caller gave up", func(t *testing.T) {
		t.Parallel()
		g := &fakeRefs{}
		ctx, cancel := context.WithCancel(context.Background())
		err := WithGateRef(ctx, g, testSha, ref, func() error { cancel(); return errors.New("the bench went away") })
		require.EqualError(t, err, "the bench went away")
		assert.Equal(t, []string{"push " + testSha + ":" + ref, "delete " + ref}, g.calls)
		assert.NoError(t, g.ctxErr, "the delete runs under a context the caller's cancel does not end")
	})
	t.Run("the push is refused", func(t *testing.T) {
		t.Parallel()
		g := &fakeRefs{pushErr: errors.New("remote: permission denied")}
		ran := false
		err := WithGateRef(context.Background(), g, testSha, ref, func() error { ran = true; return nil })
		require.EqualError(t, err, "copy refused: push "+ref+": remote: permission denied")
		assert.False(t, ran)
		assert.Equal(t, []string{"push " + testSha + ":" + ref}, g.calls, "nothing pushed, nothing to delete")
	})
}

// A refused copy is one exact line: the host, the wall time, the step, its exit and the
// tail of its stderr; the run directory is removed all the same and the command never
// runs.
func TestARefusedCopyProducesTheExactLine(t *testing.T) {
	t.Parallel()
	t.Run("no mirror on the bench", func(t *testing.T) {
		t.Parallel()
		f := &fakeBench{clock: newClock(), stageCode: NoMirror, stageErr: "no mirror at nova-bench/mirror/nova-tools.git\n"}
		var said []string
		o := f.opts(testStage())
		o.Staged = func(s Stage) { said = append(said, s.Line()) }
		res, err := Run(context.Background(), f, o)
		want := "copy refused: vision after 1.0s: staging 0123456789ab from nova-bench/mirror/nova-tools.git at " + testRun + "/repo exit 3: no mirror at nova-bench/mirror/nova-tools.git"
		require.EqualError(t, err, want)
		var se *StageError
		require.ErrorAs(t, err, &se)
		assert.Equal(t, want, res.Stage.Line())
		assert.Equal(t, []string{want}, said)
		assert.True(t, res.Removed)
		assert.Equal(t, []string{MakeLine(DefaultRoot), StageLine(*testStage(), testRun+"/repo"), RemoveLine(testRun)}, f.lines, "no command after a refused stage")
	})
	t.Run("the bench's tar exits", func(t *testing.T) {
		t.Parallel()
		f := &fakeBench{clock: newClock(), copyErr: errors.New("tar on the bench exit 2")}
		o := f.opts(nil)
		o.Dir = "/land/clone"
		res, err := Run(context.Background(), f, o)
		require.EqualError(t, err, "copy refused: vision after 2.0s: copying /land/clone to "+testRun+"/repo: tar on the bench exit 2: tar: Unexpected EOF in archive | tar: Error is not recoverable: exiting now")
		assert.Equal(t, "tar", res.Stage.Via)
		assert.True(t, res.Removed)
	})
	t.Run("WriteTree's refusal", func(t *testing.T) {
		t.Parallel()
		e := &StageError{Host: "space", Step: "copying /land/clone to " + testRun + "/repo", Wall: 1500 * time.Millisecond,
			Err: fmt.Errorf("tar stream: %w", errors.New("run/ssh.sock is not a file, a directory or a symlink (Srwxr-xr-x); refusing to copy it"))}
		assert.Equal(t, "copy refused: space after 1.5s: copying /land/clone to "+testRun+"/repo: tar stream: run/ssh.sock is not a file, a directory or a symlink (Srwxr-xr-x); refusing to copy it", Stage{Err: e}.Line())
	})
	t.Run("a stage the line could misread is refused before anything is reached", func(t *testing.T) {
		t.Parallel()
		f := &fakeBench{clock: newClock()}
		bad := testStage()
		bad.Ref, bad.Sha, bad.Remote = "refs/heads/main", "HEAD", "--upload-pack=x"
		_, err := Run(context.Background(), f, f.opts(bad))
		require.Error(t, err)
		assert.Contains(t, err.Error(), `the stage's ref "refs/heads/main" is not under refs/nova-gate/`)
		assert.Contains(t, err.Error(), `the stage's sha "HEAD" is not a full commit id`)
		assert.Contains(t, err.Error(), `the stage's remote "--upload-pack=x" is not a URL`)
		assert.Empty(t, f.lines)
	})
}

// A bench whose stage fails twice in a pass is passed over by the ring for the rest of it,
// with the reason, and the ring's next slot is the first asked; once is not enough.
func TestABenchFailingTwiceIsSkippedWithTheReason(t *testing.T) {
	t.Parallel()
	var s StageSkips
	ring := []string{"hetzner", "space", "vision"}
	assert.False(t, s.Fail("hetzner", "copy refused: hetzner after 61.0s: staging x exit 128: fatal: couldn't find remote ref"))
	keep, notes := s.Ring(ring)
	assert.Equal(t, ring, keep, "one failure skips nothing")
	assert.Empty(t, notes)

	assert.True(t, s.Fail("hetzner", "copy refused: hetzner after 2.0s: staging x exit 3: no mirror at nova-bench/mirror/nova-tools.git"))
	why, skipped := s.Skipped("hetzner")
	require.True(t, skipped)
	assert.Equal(t, "skip hetzner: its stage failed 2 times this pass, last: copy refused: hetzner after 2.0s: staging x exit 3: no mirror at nova-bench/mirror/nova-tools.git", why)
	keep, notes = s.Ring(ring)
	assert.Equal(t, []string{"space", "vision"}, keep, "the next slot is first")
	assert.Equal(t, []string{why}, notes)

	_, skipped = s.Skipped("space")
	assert.False(t, skipped, "another bench is not touched")
	var fresh StageSkips
	keep, _ = fresh.Ring(ring)
	assert.Equal(t, ring, keep, "a new pass starts with every bench")
}

// The temporary ref names the batch and the commit, as one ref component, always under
// refs/nova-gate/.
func TestGateRefIsOneComponentUnderTheGatePrefix(t *testing.T) {
	t.Parallel()
	for key, want := range map[string]string{
		"sprint/mechanical-2026-10-02": "refs/nova-gate/sprint-mechanical-2026-10-02-0123456789ab",
		"a b..c~^:?*[\\":               "refs/nova-gate/a-b-c-0123456789ab",
		"":                             "refs/nova-gate/gate-0123456789ab",
		"...":                          "refs/nova-gate/gate-0123456789ab",
	} {
		got := GateRef(key, testSha)
		assert.Equal(t, want, got, key)
		assert.Regexp(t, gateRefRe, got)
	}
	assert.Equal(t, "nova-bench/mirror/nova-tools.git", MirrorDir("nova-tools.git"))
}
