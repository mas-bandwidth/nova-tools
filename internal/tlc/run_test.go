package tlc

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
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
	if got := full.Args(); !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %v\nwant   %v", got, want)
	}
	bare := Run{Jar: "j.jar", Workers: 1, Config: "c.cfg", Module: "m.tla"}
	want = []string{"-cp", "j.jar", "tlc2.TLC", "-workers", "1", "-config", "c.cfg", "m.tla"}
	if got := bare.Args(); !reflect.DeepEqual(got, want) {
		t.Fatalf("bare args = %v, want %v", got, want)
	}
}

func TestRunArgsDoNotShareTheCallersFlags(t *testing.T) {
	t.Parallel()
	jvm := make([]string, 1, 8)
	jvm[0] = "-Xmx1g"
	r := Run{JVM: jvm, Jar: "j", Workers: 1, Config: "c", Module: "m"}
	_ = r.Args()
	if got := jvm[:2][1]; got != "" {
		t.Fatalf("Args wrote into the caller's slice: %q", got)
	}
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
	os.Stdout.WriteString("helper output\n")
	os.Exit(code)
}

func helperRun(t *testing.T, exit int) Run {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Run{Java: exe, Dir: t.TempDir(), Jar: "j", Workers: 1, Config: "c", Module: "m",
		JVM: []string{"-test.run=^TestExecuteHelper$", "tlc-helper-exit=" + strconv.Itoa(exit), "--"}}
}

func TestExecutePassesTheExitStatusAndKeepsTheOutput(t *testing.T) {
	t.Parallel()
	for _, want := range []int{0, 12, 13} {
		run := helperRun(t, want)
		log := filepath.Join(run.Dir, "out.log")
		if got := Execute(context.Background(), run, log); got != want {
			t.Errorf("exit = %d, want %d", got, want)
		}
		raw, err := os.ReadFile(log)
		if err != nil || !strings.Contains(string(raw), "helper output") {
			t.Errorf("log for exit %d = %q, %v", want, raw, err)
		}
	}
}

func TestExecuteReportsATimeoutWithoutStartingAnything(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	run := helperRun(t, 0)
	log := filepath.Join(run.Dir, "out.log")
	if got := Execute(ctx, run, log); got != ExitTimeout {
		t.Fatalf("exit = %d, want %d", got, ExitTimeout)
	}
	raw, _ := os.ReadFile(log)
	if strings.Contains(string(raw), "helper output") || !strings.Contains(string(raw), "budget exhausted before starting") {
		t.Fatalf("log = %q", raw)
	}
	if Parse(string(raw)).HasStats() {
		t.Fatal("the note was read as a result")
	}
}

func TestExecuteReportsAProgramThatDoesNotStart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := filepath.Join(dir, "out.log")
	got := Execute(context.Background(), Run{Java: filepath.Join(dir, "no-such-java"), Dir: dir, Jar: "j", Workers: 1}, log)
	if got != ExitNoStart {
		t.Fatalf("exit = %d, want %d", got, ExitNoStart)
	}
	if raw, _ := os.ReadFile(log); len(raw) == 0 {
		t.Fatal("the failure to start left no note in the log")
	}
}

func TestExecuteRefusesALogItCannotCreate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if got := Execute(context.Background(), helperRun(t, 0), filepath.Join(dir, "missing", "out.log")); got != ExitNoStart {
		t.Fatalf("exit = %d, want %d", got, ExitNoStart)
	}
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
		if err != nil {
			t.Fatal(err)
		}
		if got := CheckLimits(d, tc.workers, tc.manual); (got == nil) != tc.ok {
			t.Errorf("CheckLimits(%s, %d, manual=%v) = %v, want ok=%v", tc.budget, tc.workers, tc.manual, got, tc.ok)
		}
	}
}

// The program is started with another working directory than the caller's, so
// an override that is relative to the caller must be made absolute when it is
// checked. The override is a ./bin/java of an owned directory, a symlink to the
// helper, named relative to this test's own directory.
func TestARelativeOverrideRunsInAnotherWorkingDirectory(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege the Windows path does not have")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	owned := t.TempDir()
	if err := os.Mkdir(filepath.Join(owned, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, filepath.Join(owned, "bin", "java")); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, filepath.Join(owned, "bin", "java"))
	if err != nil || filepath.IsAbs(relative) {
		t.Skipf("no relative path from %s to %s", cwd, owned)
	}
	java, err := FindHelper("java", relative, LookPath)
	if err != nil || !filepath.IsAbs(java) {
		t.Fatalf("override %q resolved to %q, %v", relative, java, err)
	}
	private := t.TempDir() // the private working directory the program starts in
	run := Run{Java: java, Dir: private, Jar: "j", Workers: 1, Config: "c", Module: "m",
		JVM: []string{"-test.run=^TestExecuteHelper$", "tlc-helper-exit=13", "--"}}
	if got := Execute(context.Background(), run, filepath.Join(private, "out.log")); got != 13 {
		t.Fatalf("exit = %d, want 13 (127 is a program that did not start)", got)
	}
}

func TestAPathFoundOnPATHIsAbsoluteToo(t *testing.T) {
	t.Parallel()
	got, err := FindHelper("java", "", func(string) (string, error) { return filepath.Join("bin", "java"), nil })
	if err != nil || !filepath.IsAbs(got) {
		t.Fatalf("path = %q, %v", got, err)
	}
}
