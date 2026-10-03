package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tlc"
)

// loads reads the given 1-minute loads in turn, the last one again and again.
func loads(seq ...float64) func(context.Context) (float64, error) {
	i := 0
	return func(context.Context) (float64, error) {
		l := seq[min(i, len(seq)-1)]
		i++
		return l, nil
	}
}

// TestTroughWaitsUntilTheLoadIsUnderTheLimit: the trough reads the load, and
// while it is at or over the limit sleeps one poll and reads again; it goes on
// at the first load under the limit and says how long it waited, counted in
// polls, never read off a clock.
func TestTroughWaitsUntilTheLoadIsUnderTheLimit(t *testing.T) {
	t.Parallel()
	var slept []time.Duration
	sleep := func(d time.Duration) { slept = append(slept, d) }
	load, waited, err := trough(context.Background(), loads(60, 51.2, 12.5), 51.2, 15*time.Second, time.Hour, sleep)
	if err != nil || load != 12.5 || waited != 30*time.Second || len(slept) != 2 {
		t.Fatalf("load=%v waited=%v slept=%v err=%v; want 12.5 after two polls of 15s (51.2 is not under 51.2)", load, waited, slept, err)
	}
	slept = nil
	load, waited, err = trough(context.Background(), loads(0.5), 51.2, 15*time.Second, time.Hour, sleep)
	if err != nil || load != 0.5 || waited != 0 || len(slept) != 0 {
		t.Fatalf("a quiet bench waited: load=%v waited=%v slept=%v err=%v", load, waited, slept, err)
	}
}

// TestTroughGivesUpAfterTheWait: a bench whose load never falls under the
// limit is given up on once the wait is spent, naming the last load, the
// limit and the wait; a load that cannot be read is an error at once.
func TestTroughGivesUpAfterTheWait(t *testing.T) {
	t.Parallel()
	polls := 0
	_, waited, err := trough(context.Background(), loads(90), 51.2, 15*time.Second, time.Minute, func(time.Duration) { polls++ })
	if err == nil || polls != 4 || waited != time.Minute || !strings.Contains(err.Error(), "the 1-minute load stayed at or over 51.20 for 1m0s (last 90.00)") {
		t.Fatalf("polls=%d waited=%v err=%v", polls, waited, err)
	}
	broken := func(context.Context) (float64, error) {
		return 0, errors.New("ssh: connect to host vision port 22: timed out")
	}
	if _, _, err := trough(context.Background(), broken, 51.2, time.Second, time.Minute, func(time.Duration) { t.Error("slept on a load it could not read") }); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err=%v", err)
	}
}

// TestTheDefaultLimitIsFourFifthsOfTheBenchsCores: --load-below 0 is 0.8 x
// the bench's logical CPUs; a value given is taken as it is.
func TestTheDefaultLimitIsFourFifthsOfTheBenchsCores(t *testing.T) {
	t.Parallel()
	if got := loadLimit(0, 64); got != 51.2 {
		t.Errorf("loadLimit(0, 64) = %v, want 51.2", got)
	}
	if got := loadLimit(3, 64); got != 3 {
		t.Errorf("loadLimit(3, 64) = %v, want 3", got)
	}
}

// fakeBench is a record machine with no ssh: its disk is a temp dir, its
// tlacheck this package's run with a scripted TLC, its load a sequence.
type fakeBench struct {
	t      *testing.T
	disk   string
	jar    string // the pinned jar's path on the machine
	cores  int
	load   func(context.Context) (float64, error)
	exec   tlc.Executor
	staged []string // the directories Stage made
	ran    [][]string
	gone   []string // the directories Remove removed
}

func newFakeBench(t *testing.T, jarBytes []byte, load func(context.Context) (float64, error)) *fakeBench {
	t.Helper()
	f := &fakeBench{t: t, disk: t.TempDir(), cores: 64, load: load, exec: scriptedTLC(t, map[string]int{"MCABroken": 12, "MCAStale": 13}, nil)}
	f.jar = filepath.Join(f.disk, "opt", "tla", "tla2tools.jar")
	if err := os.MkdirAll(filepath.Dir(f.jar), 0o755); err != nil {
		t.Fatal(err)
	}
	if jarBytes != nil {
		if err := os.WriteFile(f.jar, jarBytes, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// Probe answers for the jar this machine holds, whatever path is asked: on
// the fleet every record machine holds it at the same path.
func (f *fakeBench) Probe(ctx context.Context, _ string) (benchProbe, error) {
	l, err := f.load(ctx)
	if err != nil {
		return benchProbe{}, err
	}
	p := benchProbe{GOOS: "linux", GOARCH: "amd64", CPUs: f.cores, Load: l}
	if raw, err := os.ReadFile(f.jar); err == nil {
		sum := sha256.Sum256(raw)
		p.JarSHA256 = hex.EncodeToString(sum[:])
	}
	return p, nil
}

func (f *fakeBench) Load(ctx context.Context) (float64, error) { return f.load(ctx) }

func (f *fakeBench) Stage(_ context.Context, archive io.Reader) (string, error) {
	dir := filepath.Join(f.disk, "tla-runs", fmt.Sprintf("tlacheck-%d", len(f.staged)+1))
	tr := tar.NewReader(archive)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		p := filepath.Join(dir, filepath.FromSlash(h.Name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return "", err
		}
		raw, err := io.ReadAll(tr)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(p, raw, os.FileMode(h.Mode)); err != nil {
			return "", err
		}
	}
	f.staged = append(f.staged, dir)
	return dir, nil
}

func (f *fakeBench) Run(_ context.Context, dir string, args []string, stdout, stderr io.Writer) (int, error) {
	f.ran = append(f.ran, args)
	if _, err := os.Stat(filepath.Join(dir, "tlacheck")); err != nil {
		f.t.Errorf("the bench ran a tlacheck that was not staged: %v", err)
	}
	e, _, _ := testEnv(f.t, f.exec)
	e.stdout, e.stderr = stdout, stderr
	return run(args, e), nil
}

func (f *fakeBench) Fetch(_ context.Context, dir string) ([]byte, error) {
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, en := range entries {
		if en.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, en.Name()))
		if err != nil {
			return nil, err
		}
		if err := tw.WriteHeader(&tar.Header{Name: "./" + en.Name(), Mode: 0o644, Size: int64(len(raw)), Typeflag: tar.TypeReg}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(raw); err != nil {
			return nil, err
		}
	}
	return b.Bytes(), tw.Close()
}

func (f *fakeBench) Remove(_ context.Context, dir string) error {
	f.gone = append(f.gone, dir)
	return os.RemoveAll(dir)
}

// benchCheckout is checkout's tree with the records file a base branch would
// hold (here none measured yet, so every group is stale) and the pinned sum of
// the jar the fake bench holds.
func benchCheckout(t *testing.T, jarBytes []byte) string {
	t.Helper()
	root, _ := checkout(t)
	var b bytes.Buffer
	if err := tlc.WriteRecords(&b, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tla", tlc.RunsFile), b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(jarBytes)
	if err := os.WriteFile(filepath.Join(root, "tla", pinnedSumFile), []byte(hex.EncodeToString(sum[:])+"  tla2tools.jar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// benchEnv is testEnv on a Mac with the fake bench as the record machine
// named, the fleet's rows saying which machines are record machines.
func benchEnv(t *testing.T, f *fakeBench, rows []benchRow) (env, *bytes.Buffer, *bytes.Buffer, *[]time.Duration) {
	t.Helper()
	e, out, errs := testEnv(t, func(context.Context, tlc.Run, string) int {
		t.Error("TLC ran on the machine running the command")
		return 0
	})
	e.goos, e.goarch = "darwin", "arm64"
	slept := &[]time.Duration{}
	e.bench = benchDeps{
		dial:     func(machine string) remote { return f },
		machines: func(context.Context) ([]benchRow, error) { return rows, nil },
		build: func(_ context.Context, root, goos, goarch, out string) error {
			if goos != "linux" || goarch != "amd64" {
				t.Errorf("built for %s-%s, want the bench's linux-amd64", goos, goarch)
			}
			return os.WriteFile(out, []byte("#!tlacheck "+root+"\n"), 0o755)
		},
		sleep: func(d time.Duration) { *slept = append(*slept, d) },
	}
	return e, out, errs, slept
}

// TestRunOnABenchMeasuresEachStaleCaseInATroughAndMergesTheRecords: from a
// Mac, run --bench stages the tree's tla/ and a tlacheck built for the bench,
// runs every case of every stale group as its own bounded run (a group's
// shards of its own size), niced, after the bench's load falls under the
// limit, fetches each run's records, removes what it staged, and merges the
// records into the checkout's tla/RUNS.tsv, which is then current.
func TestRunOnABenchMeasuresEachStaleCaseInATroughAndMergesTheRecords(t *testing.T) {
	t.Parallel()
	jar := []byte("the pinned jar")
	root := benchCheckout(t, jar)
	f := newFakeBench(t, jar, loads(0.5, 70, 60, 3, 2, 1))
	e, out, errs, slept := benchEnv(t, f, nil)
	e.bench.machines = func(context.Context) ([]benchRow, error) {
		t.Error("a named record machine read the fleet's rows: its namer chose it by them")
		return nil, errors.New("no store")
	}
	dir := filepath.Join(t.TempDir(), "o")

	r := do(e, out, errs, "run", "--root", root, "--dir", dir, "--bench", "vision", "--jar", f.jar, "--trough-poll", "20s")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if len(*slept) != 2 || (*slept)[0] != 20*time.Second {
		t.Errorf("slept %v, want two polls of 20s while the load was 70 and 60 (limit 51.2)", *slept)
	}
	want := [][]string{}
	for _, g := range []struct {
		name string
		n    int
	}{{"alpha", 2}, {"beta", 1}, {"gamma", 1}} {
		for i := 0; i < g.n; i++ {
			want = append(want, []string{"run", "--root", f.staged[0], "--jar", f.jar, "--dir", filepath.Join(f.staged[0], "runs", g.name, fmt.Sprint(i)),
				"--group", g.name, "--shards", fmt.Sprint(g.n), "--shard", fmt.Sprint(i), "--workers", "2", "--timeout", "1m50s"})
		}
	}
	if fmt.Sprint(f.ran) != fmt.Sprint(want) {
		t.Errorf("ran\n%v\nwant\n%v", f.ran, want)
	}
	if len(f.gone) != 1 || f.gone[0] != f.staged[0] {
		t.Errorf("removed %v, want the staged %v", f.gone, f.staged)
	}
	for _, w := range []string{
		"BENCH OK bench=vision platform=linux-amd64 cpus=64 load=0.50 below=51.20 jar=" + f.jar,
		"TROUGH OK bench=vision group=alpha case=1/2 load=3.00 below=51.20 waited=40s",
		"TROUGH OK bench=vision group=gamma case=1/1 load=1.00 below=51.20 waited=0s",
		"CASE OK config=MCABroken.cfg result=PASS",
		"MERGE OK runs=4 records=4 out=" + filepath.Join(root, "tla", tlc.RunsFile),
	} {
		if !strings.Contains(r.stdout, w) {
			t.Errorf("stdout lacks %q:\n%s", w, r.stdout)
		}
	}
	e2, out2, errs2 := testEnv(t, nil)
	if g := do(e2, out2, errs2, "groups", "--root", root, "--stale"); g.code != 0 || g.stdout != "[]\n" {
		t.Errorf("after the bench run the records are not current: %+v", g)
	}
	for _, l := range strings.Split(strings.TrimSpace(r.stdout), "\n") {
		if !eventRE.MatchString(l) {
			t.Errorf("not an event: %q", l)
		}
	}
	// Run again: nothing is stale, so the bench is not reached.
	e3, out3, errs3, _ := benchEnv(t, f, nil)
	if again := do(e3, out3, errs3, "run", "--root", root, "--dir", filepath.Join(t.TempDir(), "o"), "--bench", "vision", "--jar", f.jar); again.code != 0 || again.stdout != "BENCH OK bench=vision stale=0 records="+filepath.Join(root, "tla", tlc.RunsFile)+"\n" || len(f.staged) != 1 {
		t.Errorf("a run with nothing stale: %+v staged %v", again, f.staged)
	}
}

// TestRunOnABenchRefusesWhatIsNotARecordMachine: a bench whose jar is not
// the pinned one (or missing), a flag the bench form does not take, and for
// --bench any a fleet whose rows cannot be read or name no record machine
// with the pinned jar, are refused before anything is staged or run, each
// naming the way out.
func TestRunOnABenchRefusesWhatIsNotARecordMachine(t *testing.T) {
	t.Parallel()
	jar := []byte("the pinned jar")
	rows := []benchRow{{Name: "vision", TLA: true}, {Name: "studio"}}
	tests := []struct {
		name  string
		bytes []byte
		args  []string
		rows  func(context.Context) ([]benchRow, error)
		want  string
	}{
		{"no record machine", []byte("another jar"), []string{"--bench", "any"}, nil, "no record machine holds the pinned jar: the jar at "},
		{"no row says tla", jar, []string{"--bench", "any"}, func(context.Context) ([]benchRow, error) { return []benchRow{{Name: "studio"}}, nil }, "no record machine holds the pinned jar; run: nova-config machine list (a record machine says tla=true;"},
		{"another jar", []byte("another jar"), []string{"--bench", "vision"}, nil, "is not the pinned TLC jar"},
		{"no jar", nil, []string{"--bench", "vision"}, nil, "holds no jar at"},
		{"shards", jar, []string{"--bench", "vision", "--shards", "2"}, nil, "--shards is not taken with --bench"},
		{"rows unreadable", jar, []string{"--bench", "any"}, func(context.Context) ([]benchRow, error) {
			return nil, errors.New("nova-config machine list: --pg is required")
		}, "the fleet's machine rows cannot be read: nova-config machine list: --pg is required"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := benchCheckout(t, jar)
			f := newFakeBench(t, tc.bytes, loads(0.5))
			e, out, errs, _ := benchEnv(t, f, rows)
			if tc.rows != nil {
				e.bench.machines = tc.rows
			}
			args := append([]string{"run", "--root", root, "--dir", filepath.Join(t.TempDir(), "o"), "--jar", f.jar}, tc.args...)
			r := do(e, out, errs, args...)
			if r.code != 2 || !strings.Contains(r.stderr, tc.want) || strings.Count(r.stderr, "\n") != 1 {
				t.Errorf("%+v, want exit 2 and %q", r, tc.want)
			}
			if len(f.staged) != 0 || len(f.ran) != 0 {
				t.Errorf("staged %v ran %v", f.staged, f.ran)
			}
		})
	}
}

// TestRunOnABenchLeavesTheRecordsAloneWhenACaseFails: a case that does not
// reach its declared result fails the command and nothing is merged; what
// was staged on the bench is still removed.
func TestRunOnABenchLeavesTheRecordsAloneWhenACaseFails(t *testing.T) {
	t.Parallel()
	jar := []byte("the pinned jar")
	root := benchCheckout(t, jar)
	before, err := os.ReadFile(filepath.Join(root, "tla", tlc.RunsFile))
	if err != nil {
		t.Fatal(err)
	}
	f := newFakeBench(t, jar, loads(0.5))
	f.exec = scriptedTLC(t, map[string]int{"MCABroken": 0, "MCAStale": 13}, nil) // MCABroken is declared to violate an invariant
	e, out, errs, _ := benchEnv(t, f, []benchRow{{Name: "vision", TLA: true}})
	r := do(e, out, errs, "run", "--root", root, "--dir", filepath.Join(t.TempDir(), "o"), "--bench", "vision", "--jar", f.jar, "--group", "alpha")
	if r.code != 1 || !strings.Contains(r.stderr, "BENCH FAIL bench=vision") || strings.Contains(r.stdout, "MERGE OK") {
		t.Fatalf("%+v", r)
	}
	after, err := os.ReadFile(filepath.Join(root, "tla", tlc.RunsFile))
	if err != nil || !bytes.Equal(before, after) {
		t.Errorf("the records changed after a failed case: %v", err)
	}
	if len(f.gone) != 1 {
		t.Errorf("removed %v, want the staged directory", f.gone)
	}
}

// TestBenchAnyPicksTheQuietestRecordMachine: --bench any reads the fleet's
// rows, probes every record machine, and takes the one with the lowest load
// per CPU among those holding the pinned jar.
func TestBenchAnyPicksTheQuietestRecordMachine(t *testing.T) {
	t.Parallel()
	jar := []byte("the pinned jar")
	root := benchCheckout(t, jar)
	busy := newFakeBench(t, jar, loads(40))
	quiet := newFakeBench(t, jar, loads(10))
	quiet.cores = 32 // 0.31 a CPU, against busy's 40 on 64 (0.63)
	stale := newFakeBench(t, []byte("another jar"), loads(0))
	e, out, errs, _ := benchEnv(t, busy, []benchRow{{Name: "space", TLA: true}, {Name: "vision", TLA: true}, {Name: "hetzner", TLA: true}, {Name: "studio"}})
	e.bench.dial = func(m string) remote {
		return map[string]remote{"space": busy, "vision": quiet, "hetzner": stale}[m]
	}
	r := do(e, out, errs, "run", "--root", root, "--dir", filepath.Join(t.TempDir(), "o"), "--bench", "any", "--jar", busy.jar)
	if r.code != 0 || len(quiet.ran) != 4 || len(busy.ran) != 0 || len(stale.ran) != 0 {
		t.Fatalf("%+v quiet=%v busy=%v stale=%v", r, quiet.ran, busy.ran, stale.ran)
	}
	if !strings.Contains(r.stdout, "BENCH OK bench=vision ") {
		t.Errorf("stdout: %s", r.stdout)
	}
}

// TestTheSSHCommandsAreQuotedAndNiced: the shell lines the real transport
// sends are the operations the fake answers: the run is niced, every word is
// quoted, the staged directory is the bench's own under ~/tla-runs, and a
// directory that is not one is never removed.
func TestTheSSHCommandsAreQuotedAndNiced(t *testing.T) {
	t.Parallel()
	got := runLine("/home/u/tla-runs/tlacheck-ab12", []string{"run", "--root", "/home/u/tla-runs/tlacheck-ab12", "--group", "it's"})
	want := `cd '/home/u/tla-runs/tlacheck-ab12' && exec nice -n 15 '/home/u/tla-runs/tlacheck-ab12/tlacheck' 'run' '--root' '/home/u/tla-runs/tlacheck-ab12' '--group' 'it'\''s'`
	if got != want {
		t.Errorf("runLine\n got %s\nwant %s", got, want)
	}
	if !strings.Contains(stageLine, `mktemp -d "$HOME/tla-runs/tlacheck-XXXXXX"`) || !strings.Contains(stageLine, "-mmin +1440") {
		t.Errorf("stageLine: %s", stageLine)
	}
	for _, d := range []string{"/", "/home/u", "/home/u/tla-runs", "/home/u/tla-runs/other", "/home/u/tla-runs/tlacheck-ab/../../x"} {
		if _, err := removeLine(d); err == nil {
			t.Errorf("removeLine(%q) would remove it", d)
		}
	}
	if l, err := removeLine("/home/u/tla-runs/tlacheck-ab12"); err != nil || l != `rm -r -- '/home/u/tla-runs/tlacheck-ab12'` {
		t.Errorf("removeLine = %q %v", l, err)
	}
	p, err := parseProbe("Linux x86_64\n64\n0.86 1.62 3.01 1/123 456\n936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88  /opt/tla/tla2tools.jar\n")
	if err != nil || p != (benchProbe{GOOS: "linux", GOARCH: "amd64", CPUs: 64, Load: 0.86, JarSHA256: "936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88"}) {
		t.Errorf("parseProbe = %+v %v", p, err)
	}
	if p, err := parseProbe("Linux aarch64\n8\n0.10 0.2 0.3 1/2 3\n"); err != nil || p.GOARCH != "arm64" || p.JarSHA256 != "" {
		t.Errorf("parseProbe with no jar = %+v %v", p, err)
	}
}

// TestTheMachineRowsAreReadFromNovaConfigsList: the record machines are the
// rows whose line says tla=true; a row of a nova-config that has no tla field
// is none.
func TestTheMachineRowsAreReadFromNovaConfigsList(t *testing.T) {
	t.Parallel()
	rows := parseMachineList("MACHINE name=vision user=glenn seat=vision slots=64 runners=0 width=32 tla=true\nMACHINE name=studio user=glenn seat=studio slots=64 runners=1 width=0 tla=false\nMACHINE name=old user=u seat=s slots=1 runners=0 width=0\nCONFIG LIST kind=machine rows=3\n")
	if fmt.Sprint(rows) != fmt.Sprint([]benchRow{{"vision", true}, {"studio", false}, {"old", false}}) {
		t.Errorf("rows = %v", rows)
	}
}
