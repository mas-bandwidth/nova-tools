package tlc

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunArgsKeepTheFixedOrder(t *testing.T) {
	t.Parallel()
	full := Run{
		JVM: []string{"-XX:+UseParallelGC", "-Xmx2g"}, TmpDir: "/scratch", Jar: "/j/tla2tools.jar",
		Workers: 2, LnCheckFinal: true, NoDeadlock: true, MetaDir: "/scratch/states",
		Config: "MCA.cfg", Module: "MCA.tla",
	}
	want := []string{"-XX:+UseParallelGC", "-Xmx2g", "-Djava.io.tmpdir=/scratch", "-cp", "/j/tla2tools.jar", "tlc2.TLC",
		"-lncheck", "final", "-workers", "2", "-deadlock", "-metadir", "/scratch/states", "-config", "MCA.cfg", "MCA.tla"}
	got := full.Args()
	require.Equal(t, want, got, "args = %v\nwant   %v", got, want)
	bare := Run{Jar: "j.jar", Workers: 1, Config: "c.cfg", Module: "m.tla"}
	want = []string{"-cp", "j.jar", "tlc2.TLC", "-workers", "1", "-config", "c.cfg", "m.tla"}
	got = bare.Args()
	require.Equal(t, want, got, "bare args = %v, want %v", got, want)
}

func TestRunArgsDoNotShareTheCallersFlags(t *testing.T) {
	t.Parallel()
	jvm := make([]string, 1, 8)
	jvm[0] = "-Xmx1g"
	r := Run{JVM: jvm, Jar: "j", Workers: 1, Config: "c", Module: "m"}
	_ = r.Args()
	got := jvm[:2][1]
	require.Empty(t, got, "Args wrote into the caller's slice: %q", got)
}

// The helper is the test binary itself, run in place of java: everything after
// "--" in its arguments tells it what to be. Nothing here starts java.
func TestExecuteHelper(t *testing.T) {
	t.Parallel()
	code := -1
	for _, a := range os.Args {
		if v, ok := strings.CutPrefix(a, "tlc-helper-exit="); ok {
			code, _ = strconv.Atoi(v)
		}
	}
	if code < 0 {
		t.Skip("not the helper process")
	}
	_, _ = os.Stdout.WriteString("helper output\n") // ignored: the parent reads this line back from the log it asserts
	os.Exit(code)
}

func helperRun(t *testing.T, exit int) Run {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	return Run{Java: exe, Dir: t.TempDir(), Jar: "j", Workers: 1, Config: "c", Module: "m",
		JVM: []string{"-test.run=^TestExecuteHelper$", "tlc-helper-exit=" + strconv.Itoa(exit), "--"}}
}

func TestExecutePassesTheExitStatusAndKeepsTheOutput(t *testing.T) {
	t.Parallel()
	for _, want := range []int{0, 12, 13} {
		run := helperRun(t, want)
		log := filepath.Join(run.Dir, "out.log")
		got := Execute(context.Background(), run, log)
		assert.Equal(t, want, got, "exit = %d, want %d", got, want)
		raw, err := os.ReadFile(log)
		if assert.NoError(t, err, "log for exit %d = %q, %v", want, raw, err) {
			assert.Contains(t, string(raw), "helper output", "log for exit %d = %q, %v", want, raw, err)
		}
	}
}

func TestExecuteReportsATimeoutWithoutStartingAnything(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	run := helperRun(t, 0)
	log := filepath.Join(run.Dir, "out.log")
	got := Execute(ctx, run, log)
	require.Equal(t, ExitTimeout, got, "exit = %d, want %d", got, ExitTimeout)
	raw, _ := os.ReadFile(log)
	require.NotContains(t, string(raw), "helper output", "log = %q", raw)
	require.Contains(t, string(raw), "budget exhausted before starting", "log = %q", raw)
	require.False(t, Parse(string(raw)).HasStats(), "the note was read as a result")
}

func TestExecuteReportsAProgramThatDoesNotStart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := filepath.Join(dir, "out.log")
	got := Execute(context.Background(), Run{Java: filepath.Join(dir, "no-such-java"), Dir: dir, Jar: "j", Workers: 1}, log)
	require.Equal(t, ExitNoStart, got, "exit = %d, want %d", got, ExitNoStart)
	raw, _ := os.ReadFile(log)
	require.NotEmpty(t, raw, "the failure to start left no note in the log")
}

func TestExecuteRefusesALogItCannotCreate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	got := Execute(context.Background(), helperRun(t, 0), filepath.Join(dir, "missing", "out.log"))
	require.Equal(t, ExitNoStart, got, "exit = %d, want %d", got, ExitNoStart)
}

func TestCheckLimits(t *testing.T) {
	t.Parallel()
	tests := []struct {
		budget  string
		workers int
		manual  bool
		ok      bool
	}{
		{"110s", 2, false, true},
		{"1s", 1, false, true},
		{"0s", 2, false, false},
		{"-1s", 2, false, false},
		{"111s", 2, false, false},
		{"3600s", 2, true, true},
		{"3601s", 2, true, false},
		{"110s", 0, false, false},
		{"110s", 3, false, false},
	}
	for _, tc := range tests {
		d, err := parseDuration(tc.budget)
		require.NoError(t, err)
		got := CheckLimits(d, tc.workers, tc.manual)
		assert.Equal(t, tc.ok, got == nil, "CheckLimits(%s, %d, manual=%v) = %v, want ok=%v", tc.budget, tc.workers, tc.manual, got, tc.ok)
	}
}

// Execute starts the program in another directory, so a relative override is
// made absolute before it is checked.
func TestARelativeOverrideRunsInAnotherWorkingDirectory(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege the Windows path does not have")
	}
	exe, err := os.Executable()
	require.NoError(t, err)
	owned := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(owned, "bin"), 0o755))
	require.NoError(t, os.Symlink(exe, filepath.Join(owned, "bin", "java")))
	cwd, err := os.Getwd()
	require.NoError(t, err)
	relative, err := filepath.Rel(cwd, filepath.Join(owned, "bin", "java"))
	if err != nil || filepath.IsAbs(relative) {
		t.Skipf("no relative path from %s to %s", cwd, owned)
	}
	java, err := FindHelper("java", relative, LookPath)
	require.NoError(t, err, "override %q resolved to %q, %v", relative, java, err)
	require.True(t, filepath.IsAbs(java), "override %q resolved to %q, %v", relative, java, err)
	private := t.TempDir()
	run := Run{Java: java, Dir: private, Jar: "j", Workers: 1, Config: "c", Module: "m",
		JVM: []string{"-test.run=^TestExecuteHelper$", "tlc-helper-exit=13", "--"}}
	got := Execute(context.Background(), run, filepath.Join(private, "out.log"))
	require.Equal(t, 13, got, "exit = %d, want 13 (127 is a program that did not start)", got)
}

func TestAPathFoundOnPATHIsAbsoluteToo(t *testing.T) {
	t.Parallel()
	got, err := FindHelper("java", "", func(string) (string, error) { return filepath.Join("bin", "java"), nil })
	require.NoError(t, err, "path = %q, %v", got, err)
	require.True(t, filepath.IsAbs(got), "path = %q, %v", got, err)
}
