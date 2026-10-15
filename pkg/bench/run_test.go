package bench

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Run, Validate, the make step and the remote lines (carried from nova-sprint pkg/bench
// bench_test.go): a run is driven end to end over a recording fake transport, and a
// remote line runs under a local sh in a temporary directory; no host, no network.

// benchCall is one call a run made through a fakeTransport.
type benchCall struct {
	op, host, line, src, dst string
	withGit                  bool
}

// fakeTransport is a recording Transport: it remembers every call and answers
// each with the test's closures, so a run is driven end to end without ssh.
type fakeTransport struct {
	mu    sync.Mutex
	calls []benchCall
	shell func(ctx context.Context, host, line string, stdout, stderr io.Writer) (int, error)
	copy  func(ctx context.Context, host, src, dst string, withGit bool, stderr io.Writer) error
}

func (f *fakeTransport) record(c benchCall) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
}

func (f *fakeTransport) Shell(ctx context.Context, host, line string, stdout, stderr io.Writer) (int, error) {
	f.record(benchCall{op: "shell", host: host, line: line})
	if f.shell != nil {
		return f.shell(ctx, host, line, stdout, stderr)
	}
	return 0, nil
}

func (f *fakeTransport) Copy(ctx context.Context, host, src, dst string, withGit bool, stderr io.Writer) error {
	f.record(benchCall{op: "copy", host: host, src: src, dst: dst, withGit: withGit})
	if f.copy != nil {
		return f.copy(ctx, host, src, dst, withGit, stderr)
	}
	return nil
}

func (f *fakeTransport) snapshot() []benchCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]benchCall(nil), f.calls...)
}

// testRunDir is the one directory a fake make step prints, under the default
// root, shaped like mktemp's output.
const testRunDir = DefaultRoot + "/run.abc123"

// answerRun is a fake shell for the make, exec and remove steps: make prints
// testRunDir, exec returns execCode, remove returns removeCode. A line that is
// none of those (a mirror stage) gets stageCode.
func answerRun(stageCode, execCode, removeCode int) func(context.Context, string, string, io.Writer, io.Writer) (int, error) {
	return func(ctx context.Context, host, line string, stdout, stderr io.Writer) (int, error) {
		switch {
		case line == MakeLine(DefaultRoot):
			io.WriteString(stdout, testRunDir+"\n")
			return 0, nil
		case strings.HasPrefix(line, "rm -rf "):
			if removeCode != 0 {
				io.WriteString(stderr, "boom\n")
			}
			return removeCode, nil
		case strings.Contains(line, "nice -n "):
			return execCode, nil
		default:
			return stageCode, nil
		}
	}
}

// runOptions is one valid run on one bench, its local tree a fresh temp dir.
func runOptions(t *testing.T, argv ...string) Options {
	t.Helper()
	if len(argv) == 0 {
		argv = []string{"go", "test", "./..."}
	}
	return Options{Hosts: []string{"bench1"}, Dir: t.TempDir(), Argv: argv}
}

func TestRunMakesCopiesRunsAndRemovesInOrder(t *testing.T) {
	t.Parallel()
	argv := []string{"go", "test", "./..."}
	ft := &fakeTransport{shell: answerRun(0, 0, 0)}
	o := runOptions(t, argv...)
	res, err := Run(context.Background(), ft, o)
	require.NoError(t, err, "a run that answers")
	assert.Equal(t, "bench1", res.Host)
	assert.Equal(t, testRunDir, res.RunDir)
	assert.Zero(t, res.Code)
	assert.True(t, res.Removed)
	assert.NoError(t, res.RemoveErr)
	assert.Equal(t, "tar", res.Stage.Via)
	assert.Nil(t, res.Stage.Err)
	assert.Equal(t, []benchCall{
		{op: "shell", host: "bench1", line: MakeLine(DefaultRoot)},
		{op: "copy", host: "bench1", src: o.Dir, dst: testRunDir + "/repo"},
		{op: "shell", host: "bench1", line: ExecLine(testRunDir, DefaultCache, argv)},
		{op: "shell", host: "bench1", line: RemoveLine(testRunDir)},
	}, ft.snapshot(), "make, copy without .git, exec, remove")
}

func TestRunRemovesTheRunDirectoryWhenTheCommandFails(t *testing.T) {
	t.Parallel()
	ft := &fakeTransport{shell: answerRun(0, 7, 0)}
	res, err := Run(context.Background(), ft, runOptions(t))
	require.NoError(t, err, "a command that ran and failed is not a run error")
	assert.Equal(t, 7, res.Code, "the command's exit")
	assert.True(t, res.Removed)
	assert.NoError(t, res.RemoveErr)
	last := ft.snapshot()
	assert.Equal(t, RemoveLine(testRunDir), last[len(last)-1].line, "the last call removes the run directory")
}

func TestRunRemovesTheRunDirectoryWhenStagingFails(t *testing.T) {
	t.Parallel()
	ft := &fakeTransport{
		shell: answerRun(0, 0, 0),
		copy: func(context.Context, string, string, string, bool, io.Writer) error {
			return errors.New("the bench stopped reading")
		},
	}
	res, err := Run(context.Background(), ft, runOptions(t))
	require.Error(t, err, "a stage that refused")
	assert.NotNil(t, res.Stage.Err)
	assert.True(t, res.Removed, "the run directory was left behind after a failed stage")
	for _, c := range ft.snapshot() {
		assert.False(t, c.op == "shell" && strings.Contains(c.line, "nice -n "), "the command ran although the stage refused: %q", c.line)
	}
}

func TestRunRemovesTheRunDirectoryWhenTheContextIsCancelledMidRun(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	ft := &fakeTransport{shell: func(c context.Context, host, line string, stdout, stderr io.Writer) (int, error) {
		switch {
		case line == MakeLine(DefaultRoot):
			io.WriteString(stdout, testRunDir+"\n")
			return 0, nil
		case strings.Contains(line, "nice -n "):
			cancel()
			<-c.Done()
			return -1, c.Err()
		case strings.HasPrefix(line, "rm -rf "):
			// the remove runs on a context the cancel does not reach, as a real
			// ssh would not start on a cancelled one
			if err := c.Err(); err != nil {
				return -1, err
			}
			return 0, nil
		default:
			return 0, nil
		}
	}}
	res, err := Run(ctx, ft, runOptions(t))
	require.Error(t, err, "a cancelled run is an error")
	assert.True(t, res.Removed, "the directory is removed even though the run was cancelled")
	assert.NoError(t, res.RemoveErr)
	last := ft.snapshot()
	assert.Equal(t, RemoveLine(testRunDir), last[len(last)-1].line, "the last call removes the run directory")
}

func TestRunRemovesTheRunDirectoryWhenTheExecTransportErrors(t *testing.T) {
	t.Parallel()
	ft := &fakeTransport{shell: func(c context.Context, host, line string, stdout, stderr io.Writer) (int, error) {
		switch {
		case line == MakeLine(DefaultRoot):
			io.WriteString(stdout, testRunDir+"\n")
			return 0, nil
		case strings.Contains(line, "nice -n "):
			return -1, errors.New("ssh could not start")
		default:
			return 0, nil
		}
	}}
	res, err := Run(context.Background(), ft, runOptions(t))
	assert.ErrorContains(t, err, "starting the command")
	assert.True(t, res.Removed, "the directory is removed anyway")
	assert.NoError(t, res.RemoveErr)
}

func TestRunReportsARemoveFailureWithoutLosingTheExitCode(t *testing.T) {
	t.Parallel()
	ft := &fakeTransport{shell: answerRun(0, 7, 1)}
	res, err := Run(context.Background(), ft, runOptions(t))
	require.NoError(t, err, "a failed remove is reported in Result, not as a run error")
	assert.Equal(t, 7, res.Code, "the command's exit is kept although remove failed")
	assert.False(t, res.Removed)
	assert.ErrorContains(t, res.RemoveErr, "rm exit 1")
}

func TestRunFallsBackWhenAHostDoesNotAnswer(t *testing.T) {
	t.Parallel()
	ft := &fakeTransport{shell: func(c context.Context, host, line string, stdout, stderr io.Writer) (int, error) {
		switch {
		case line == MakeLine(DefaultRoot) && host == "dead":
			return NoAnswer, nil
		case line == MakeLine(DefaultRoot):
			io.WriteString(stdout, testRunDir+"\n")
			return 0, nil
		default:
			return 0, nil
		}
	}}
	var notes bytes.Buffer
	o := runOptions(t)
	o.Hosts = []string{"dead", "bench1"}
	o.Notes = &notes
	res, err := Run(context.Background(), ft, o)
	require.NoError(t, err, "the fallback answered")
	assert.Equal(t, "bench1", res.Host, "the run is on the fallback")
	assert.Contains(t, notes.String(), "CI BENCH PASSED host=dead")
	assert.Contains(t, notes.String(), "next=bench1")
}

func TestRunDoesNotFallBackWhenAHostAnswersAndRefuses(t *testing.T) {
	t.Parallel()
	ft := &fakeTransport{shell: func(c context.Context, host, line string, stdout, stderr io.Writer) (int, error) {
		if line == MakeLine(DefaultRoot) {
			io.WriteString(stderr, "mkdir: permission denied\n")
			return 1, nil
		}
		return 0, nil
	}}
	o := runOptions(t)
	o.Hosts = []string{"refuser", "bench1"}
	_, err := Run(context.Background(), ft, o)
	assert.ErrorContains(t, err, "making the run directory", "a host that answered and refused is the end of the run")
	for _, c := range ft.snapshot() {
		assert.NotEqual(t, "bench1", c.host, "the run fell back to bench1 after a refusal: %+v", c)
	}
}

func TestRunReturnsErrNoBenchWhenNoHostAnswers(t *testing.T) {
	t.Parallel()
	ft := &fakeTransport{shell: func(context.Context, string, string, io.Writer, io.Writer) (int, error) {
		return NoAnswer, nil
	}}
	var notes bytes.Buffer
	o := runOptions(t)
	o.Hosts = []string{"dead1", "dead2"}
	o.Notes = &notes
	_, err := Run(context.Background(), ft, o)
	assert.ErrorIs(t, err, ErrNoBench)
	assert.Contains(t, notes.String(), "next=dead2", "the notes name the next host")
}

func TestRunValidatesBeforeItReachesTheTransport(t *testing.T) {
	t.Parallel()
	ft := &fakeTransport{}
	_, err := Run(context.Background(), ft, Options{})
	assert.Error(t, err, "an empty Options passed validation")
	assert.Empty(t, ft.snapshot(), "the transport was reached before validation")
}

func TestValidateRefusesEachMissingOrMalformedField(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		o    Options
		want string
	}{
		{"no host", Options{Dir: "/tmp/x", Argv: []string{"true"}}, "no bench named"},
		{"bad host", Options{Hosts: []string{"host;rm -rf /"}, Dir: "/tmp/x", Argv: []string{"true"}}, "is not a host name"},
		{"bad root", Options{Hosts: []string{"b"}, Root: "../escape", Dir: "/tmp/x", Argv: []string{"true"}}, "climbs with a .. element"},
		{"bad cache", Options{Hosts: []string{"b"}, Cache: "c he'q", Dir: "/tmp/x", Argv: []string{"true"}}, "a bench path is letters"},
		{"no tree", Options{Hosts: []string{"b"}, Argv: []string{"true"}}, "no local tree named"},
		{"no command", Options{Hosts: []string{"b"}, Dir: "/tmp/x"}, "no command given"},
		{"empty command", Options{Hosts: []string{"b"}, Dir: "/tmp/x", Argv: []string{""}}, "no command given"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.ErrorContains(t, tc.o.Validate(), tc.want)
		})
	}
}

func TestValidateRefusesAStageInsteadOfTheLocalTree(t *testing.T) {
	t.Parallel()
	o := Options{Hosts: []string{"b"}, Stage: &MirrorStage{}, Argv: []string{"true"}}
	err := o.Validate()
	require.Error(t, err, "a stage in place of Dir")
	assert.NotContains(t, err.Error(), "no local tree named")
	assert.Contains(t, err.Error(), "mirror", "the stage's own refusal")
}

func TestValidateFillsTheDefaultsAndTrimsTheRoot(t *testing.T) {
	t.Parallel()
	o := Options{Hosts: []string{"b"}, Root: "nova-bench/runs/", Dir: "/tmp/x", Argv: []string{"true"}}
	require.NoError(t, o.Validate())
	assert.Equal(t, "nova-bench/runs", o.Root, "the trailing slash is trimmed")
	assert.Equal(t, DefaultCache, o.Cache)
	empty := Options{Hosts: []string{"b"}, Dir: "/tmp/x", Argv: []string{"true"}}
	require.NoError(t, empty.Validate(), "empty root and cache")
	assert.Equal(t, DefaultRoot, empty.Root)
	assert.Equal(t, DefaultCache, empty.Cache)
}

func TestCheckHostRefusesNamesSSHWouldReadAsMoreThanAHost(t *testing.T) {
	t.Parallel()
	for _, h := range []string{"", "-oProxyCommand=x", "h;rm", "h x", "h\nx", "h`x`", "$h"} {
		assert.Error(t, CheckHost(h), "CheckHost(%q) accepted a host ssh would misread", h)
	}
	for _, h := range []string{"hetzner2", "b1", "user@host", "host.example", "A1", "h-ost"} {
		assert.NoError(t, CheckHost(h), "CheckHost(%q) refused a plain host", h)
	}
}

func TestCheckPathRefusesAPathTheRemoteShellWouldReadDifferently(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"", ".", "/", "..", "../x", "a/../b", "~/x", "a b", "a'b", "a\nb", "a`b`", "$x"} {
		assert.Error(t, CheckPath("root", p), "CheckPath(%q) accepted a path the remote shell would misread", p)
	}
	for _, p := range []string{"nova-bench/runs", "/home/nova/x", "a/b-c_d/e", "x.git"} {
		assert.NoError(t, CheckPath("root", p), "CheckPath(%q) refused a plain path", p)
	}
}

func TestMakeRunDirAcceptsAFreshRunDirectoryUnderTheRoot(t *testing.T) {
	t.Parallel()
	ft := &fakeTransport{shell: func(c context.Context, host, line string, stdout, stderr io.Writer) (int, error) {
		io.WriteString(stdout, testRunDir+"\n")
		return 0, nil
	}}
	dir, answered, err := makeRunDir(context.Background(), ft, "bench1", DefaultRoot)
	require.NoError(t, err)
	assert.True(t, answered)
	assert.Equal(t, testRunDir, dir)
	assert.Equal(t, []benchCall{{op: "shell", host: "bench1", line: MakeLine(DefaultRoot)}}, ft.snapshot(), "makeRunDir runs the make line")
}

func TestMakeRunDirTreatsANoAnswerAsNotAnswered(t *testing.T) {
	t.Parallel()
	ft := &fakeTransport{shell: func(c context.Context, host, line string, stdout, stderr io.Writer) (int, error) {
		io.WriteString(stderr, "ssh: connect to host bench1 port 22: Connection refused\n")
		return NoAnswer, nil
	}}
	dir, answered, err := makeRunDir(context.Background(), ft, "bench1", DefaultRoot)
	require.Error(t, err, "not answered with the ssh refusal")
	assert.False(t, answered)
	assert.Empty(t, dir)
	assert.Contains(t, err.Error(), "ssh exit 255", "the no-answer error names the ssh exit")
	assert.Contains(t, err.Error(), "Connection refused", "the no-answer error carries its stderr")
}

func TestMakeRunDirReportsARefusedMakeStepAsAnswered(t *testing.T) {
	t.Parallel()
	ft := &fakeTransport{shell: func(c context.Context, host, line string, stdout, stderr io.Writer) (int, error) {
		io.WriteString(stderr, "mkdir: cannot create directory: Permission denied\n")
		return 1, nil
	}}
	_, answered, err := makeRunDir(context.Background(), ft, "bench1", DefaultRoot)
	require.Error(t, err, "an answering host that refused")
	assert.True(t, answered)
	assert.Contains(t, err.Error(), "making the run directory under", "the refusal names the step")
	assert.Contains(t, err.Error(), "Permission denied", "the refusal carries its stderr")
}

func TestMakeRunDirRefusesOutputThatIsNotARunDirectory(t *testing.T) {
	t.Parallel()
	for _, out := range []string{"/tmp/evil", DefaultRoot + "/run.x/y", "not/a/run/dir"} {
		t.Run(out, func(t *testing.T) {
			t.Parallel()
			ft := &fakeTransport{shell: func(c context.Context, host, line string, stdout, stderr io.Writer) (int, error) {
				io.WriteString(stdout, out+"\n")
				return 0, nil
			}}
			dir, answered, err := makeRunDir(context.Background(), ft, "bench1", DefaultRoot)
			assert.True(t, answered)
			assert.Empty(t, dir)
			assert.ErrorContains(t, err, "not a run directory")
		})
	}
}

func TestMakeRunDirReportsATransportThatCannotStart(t *testing.T) {
	t.Parallel()
	ft := &fakeTransport{shell: func(context.Context, string, string, io.Writer, io.Writer) (int, error) {
		return -1, errors.New("exec: ssh: executable file not found")
	}}
	_, answered, err := makeRunDir(context.Background(), ft, "bench1", DefaultRoot)
	assert.Error(t, err, "a transport that could not start")
	assert.False(t, answered)
}

func TestMakeLineMakesUnderARootTheShellReadsVerbatim(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "a b'c\nd")
	require.NoError(t, os.MkdirAll(root, 0o755))
	out, code := runLine(t, MakeLine(root))
	require.Zero(t, code, "MakeLine with a space, a quote and a newline:\n%s", out)
	dir := strings.TrimSpace(out)
	assert.True(t, strings.HasPrefix(dir, root+"/run."), "mktemp made %q, not a run directory under %q", dir, root)
	assert.DirExists(t, dir)
}

func TestRemoveLineRemovesExactlyTheQuotedPath(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	p := filepath.Join(base, "x y'z\nw")
	require.NoError(t, os.MkdirAll(p, 0o755))
	keep := filepath.Join(base, "keep")
	require.NoError(t, os.MkdirAll(keep, 0o755))
	out, code := runLine(t, RemoveLine(p))
	require.Zero(t, code, "RemoveLine with a space, a quote and a newline:\n%s", out)
	assert.NoDirExists(t, p, "the path with a space, a quote and a newline is removed")
	assert.DirExists(t, keep, "RemoveLine leaves a sibling")
}

func TestExecLineRunsInTheQuotedDirectory(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "x y'z\nw")
	repo := filepath.Join(dir, "repo")
	require.NoError(t, os.MkdirAll(repo, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "marker"), []byte("here"), 0o644))
	out, code := runLine(t, ExecLine(dir, "/cache", []string{"cat", "marker"}))
	require.Zero(t, code, "ExecLine with a space, a quote and a newline:\n%s", out)
	assert.Equal(t, "here", out, "the command runs in the named repo")
}

func TestExecLineQuotesTheCache(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	dir := filepath.Join(base, "work")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "repo"), 0o755))
	cache := filepath.Join(base, "c he'q\nche")
	out, code := runLine(t, ExecLine(dir, cache, []string{"sh", "-c", `printf '%s' "$GOCACHE"`}))
	require.Zero(t, code, "ExecLine with a cache holding a space, a quote and a newline:\n%s", out)
	assert.Equal(t, cache, out, "GOCACHE")
}

func TestExecLineAnchorsARelativeCacheAtHome(t *testing.T) {
	t.Parallel()
	line := ExecLine("dir", "nova-bench/cache/go-build", []string{"true"})
	assert.Contains(t, line, `GOCACHE="$HOME"/`+Quote("nova-bench/cache/go-build"), "a relative cache is anchored at the login's home")
}

func TestQuoteRoundTripsThroughSh(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"", "plain", "a b", "a'b", "a\nb", "a'b\nc d", `"\$`} {
		t.Run(s, func(t *testing.T) {
			t.Parallel()
			out, code := runLine(t, "printf '%s' "+Quote(s))
			assert.Zero(t, code)
			assert.Equal(t, s, out)
		})
	}
}

// testMirrorStage is one valid stage of a run from the bench's mirror.
func testMirrorStage() *MirrorStage {
	return &MirrorStage{
		Mirror: "nova-bench/mirror/x.git",
		Remote: "https://example.invalid/x.git",
		Ref:    GateRef("bench", strings.Repeat("a", 40)),
		Sha:    strings.Repeat("a", 40),
	}
}

func TestRunStagesFromTheBenchMirrorInPlaceOfTheCopy(t *testing.T) {
	t.Parallel()
	ft := &fakeTransport{shell: answerRun(0, 0, 0)}
	o := Options{Hosts: []string{"bench1"}, Stage: testMirrorStage(), Argv: []string{"true"}}
	var staged []Stage
	o.Staged = func(s Stage) { staged = append(staged, s) }
	res, err := Run(context.Background(), ft, o)
	require.NoError(t, err, "a mirror stage that answered")
	assert.Equal(t, "mirror", res.Stage.Via)
	assert.Nil(t, res.Stage.Err)
	line := StageLine(*o.Stage, testRunDir+"/repo")
	assert.Equal(t, int64(len(line)), res.Stage.Bytes, "the stage's bytes are the line's")
	require.Len(t, staged, 1, "the run tells Staged once")
	assert.Equal(t, "mirror", staged[0].Via)
	got := ft.snapshot()
	require.GreaterOrEqual(t, len(got), 2)
	assert.Equal(t, line, got[1].line, "the stage call is the mirror's line")
}

func TestRunRemovesTheRunDirectoryWhenTheMirrorStageRefuses(t *testing.T) {
	t.Parallel()
	ft := &fakeTransport{shell: answerRun(NoMirror, 0, 0)}
	o := Options{Hosts: []string{"bench1"}, Stage: testMirrorStage(), Argv: []string{"true"}}
	res, err := Run(context.Background(), ft, o)
	require.Error(t, err, "a mirror stage that refused")
	require.NotNil(t, res.Stage.Err)
	assert.Equal(t, NoMirror, res.Stage.Err.Code)
	assert.Contains(t, res.Stage.Line(), "copy refused")
	assert.True(t, res.Removed, "the run directory is removed after a refused mirror stage")
}

func TestRunReportsARemoveTransportError(t *testing.T) {
	t.Parallel()
	ft := &fakeTransport{shell: func(c context.Context, host, line string, stdout, stderr io.Writer) (int, error) {
		switch {
		case line == MakeLine(DefaultRoot):
			io.WriteString(stdout, testRunDir+"\n")
			return 0, nil
		case strings.HasPrefix(line, "rm -rf "):
			return -1, errors.New("ssh could not start for the remove")
		default:
			return 0, nil
		}
	}}
	res, err := Run(context.Background(), ft, runOptions(t))
	require.NoError(t, err, "a remove transport error is reported in Result, not as a run error")
	assert.False(t, res.Removed)
	assert.Error(t, res.RemoveErr)
}

// sizedFake is a Transport whose copy says how many bytes crossed the wire.
type sizedFake struct {
	fakeTransport
	n int64
}

func (s *sizedFake) CopySized(ctx context.Context, host, src, dst string, withGit bool, stderr io.Writer) (int64, error) {
	s.record(benchCall{op: "copysized", host: host, src: src, dst: dst, withGit: withGit})
	return s.n, nil
}

func TestRunReportsTheByteCountOfASizedCopy(t *testing.T) {
	t.Parallel()
	ft := &sizedFake{fakeTransport: fakeTransport{shell: answerRun(0, 0, 0)}, n: 4096}
	res, err := Run(context.Background(), ft, runOptions(t))
	require.NoError(t, err, "a sized copy that answered")
	assert.Equal(t, int64(4096), res.Stage.Bytes)
	assert.Equal(t, "tar", res.Stage.Via)
}

func TestLastLineTakesTheLastNonBlankLine(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"", ""},
		{"no newline", "no newline"},
		{"a\nb\n", "b"},
		{"a\r\nb\r\n", "b"},
		{"a\n\n", "a"},
		{"  \n  ", ""},
		{"a\nb\r\n", "b"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, lastLine(tc.in))
		})
	}
}
