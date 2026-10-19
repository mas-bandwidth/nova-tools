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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/bench"
	"github.com/mas-bandwidth/nova-tools/pkg/tlc"
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
	require.NoError(t, err)
	assert.Equal(t, 12.5, load, "51.2 is not under 51.2")
	assert.Equal(t, 30*time.Second, waited, "two polls of 15s")
	assert.Len(t, slept, 2)
	slept = nil
	load, waited, err = trough(context.Background(), loads(0.5), 51.2, 15*time.Second, time.Hour, sleep)
	require.NoError(t, err)
	assert.Equal(t, 0.5, load)
	assert.Zero(t, waited, "a quiet bench waited")
	assert.Empty(t, slept, "a quiet bench slept")
}

// TestTroughGivesUpAfterTheWait: a bench whose load never falls under the
// limit is given up on once the wait is spent, naming the last load, the
// limit and the wait; a load that cannot be read is an error at once.
func TestTroughGivesUpAfterTheWait(t *testing.T) {
	t.Parallel()
	polls := 0
	_, waited, err := trough(context.Background(), loads(90), 51.2, 15*time.Second, time.Minute, func(time.Duration) { polls++ })
	require.Error(t, err)
	assert.Equal(t, 4, polls)
	assert.Equal(t, time.Minute, waited)
	assert.Contains(t, err.Error(), "the 1-minute load stayed at or over 51.20 for 1m0s (last 90.00)")
	broken := func(context.Context) (float64, error) {
		return 0, errors.New("ssh: connect to host bench-a port 22: timed out")
	}
	_, _, err = trough(context.Background(), broken, 51.2, time.Second, time.Minute, func(time.Duration) { t.Error("slept on a load it could not read") })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
}

// TestTheDefaultLimitIsFourFifthsOfTheBenchsCores: --load-below 0 is 0.8 x
// the bench's logical CPUs; a value given is taken as it is.
func TestTheDefaultLimitIsFourFifthsOfTheBenchsCores(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 51.2, loadLimit(0, 64))
	assert.Equal(t, 3.0, loadLimit(3, 64))
}

// fakeBench is a record machine with no ssh: its disk is a temp dir, its
// nova-tla this package's run with a scripted TLC, its load a sequence.
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
	require.NoError(t, os.MkdirAll(filepath.Dir(f.jar), 0o755))
	if jarBytes != nil {
		require.NoError(t, os.WriteFile(f.jar, jarBytes, 0o644))
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
	dir := filepath.Join(f.disk, "tla-runs", fmt.Sprintf("nova-tla-%d", len(f.staged)+1))
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

func (f *fakeBench) Run(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) (int, error) {
	f.ran = append(f.ran, args)
	assert.FileExists(f.t, filepath.Join(dir, "nova-tla"), "the bench ran a nova-tla that was not staged")
	return newTool(testEnv(f.t, f.exec)).RunContext(ctx, args, strings.NewReader(""), stdout, stderr), nil
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
	require.NoError(t, tlc.WriteRecords(&b, nil))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tla", tlc.RunsFile), b.Bytes(), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte(benchTreeGoMod), 0o644))
	sum := sha256.Sum256(jarBytes)
	require.NoError(t, os.WriteFile(filepath.Join(root, "tla", pinnedSumFile), []byte(hex.EncodeToString(sum[:])+"  tla2tools.jar\n"), 0o644))
	return root
}

// benchTreeGoMod is the go.mod of a bench checkout: a tree that requires nova-tools, so
// its runner is the nova-tools its go.mod names (runner.go, case 2), and benchBuilt is
// the binary built at it.
var (
	benchTreeGoMod = "module example.com/tree\n\ngo 1.26\n\nrequire " + toolsModule + " v1.6.0\n"
	benchBuilt     = toolsModule + "@v1.6.0"
)

// benchEnv is testEnv on a Mac with the fake bench as the record machine
// named, the fleet's rows saying which machines are record machines.
func benchEnv(t *testing.T, f *fakeBench, rows []benchRow) (env, *[]time.Duration) {
	t.Helper()
	e := testEnv(t, func(context.Context, tlc.Run, string) int {
		t.Error("TLC ran on the machine running the command")
		return 0
	})
	e.goos, e.goarch = "darwin", "arm64"
	e.built = benchBuilt
	slept := &[]time.Duration{}
	e.bench = benchDeps{
		dial:     func(machine string) remote { return f },
		machines: func(context.Context) ([]benchRow, error) { return rows, nil },
		build: func(_ context.Context, root, pkg, goos, goarch, out string) error {
			assert.Equal(t, "linux-amd64", goos+"-"+goarch, "built for the bench's platform")
			assert.Equal(t, toolsModule+"/cmd/nova-tla", pkg, "built the tree's runner, nova-tools' cmd/nova-tla at its go.mod's version")
			return os.WriteFile(out, []byte("#!nova-tla "+root+"\n"), 0o755)
		},
		sleep: func(d time.Duration) { *slept = append(*slept, d) },
	}
	return e, slept
}

// TestRunOnABenchMeasuresEachStaleCaseInATroughAndMergesTheRecords: from a
// Mac, run --bench stages the tree's tla/ and a nova-tla built for the bench,
// runs every case of every stale group as its own bounded run (a group's
// shards of its own size), niced, after the bench's load falls under the
// limit, fetches each run's records, removes what it staged, and merges the
// records into the checkout's tla/RUNS.tsv, which is then current.
func TestRunOnABenchMeasuresEachStaleCaseInATroughAndMergesTheRecords(t *testing.T) {
	t.Parallel()
	jar := []byte("the pinned jar")
	root := benchCheckout(t, jar)
	f := newFakeBench(t, jar, loads(0.5, 70, 60, 3, 2, 1))
	e, slept := benchEnv(t, f, nil)
	e.bench.machines = func(context.Context) ([]benchRow, error) {
		t.Error("a named record machine read the fleet's rows: its namer chose it by them")
		return nil, errors.New("no store")
	}
	dir := filepath.Join(t.TempDir(), "o")

	r := do(e, "run", "--root", root, "--dir", dir, "--bench", "bench-a", "--jar", f.jar, "--trough-poll", "20s")
	require.Equal(t, 0, r.code, "%+v", r)
	assert.Equal(t, []time.Duration{20 * time.Second, 20 * time.Second}, *slept, "two polls of 20s while the load was 70 and 60 (limit 51.2)")
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
	assert.Equal(t, want, f.ran)
	assert.Equal(t, f.staged, f.gone, "removed the one staged directory")
	for _, w := range []string{
		"BENCH OK bench=bench-a platform=linux-amd64 cpus=64 load=0.50 below=51.20 jar=" + f.jar,
		"TROUGH OK bench=bench-a group=alpha case=1/2 load=3.00 below=51.20 waited=40s",
		"TROUGH OK bench=bench-a group=gamma case=1/1 load=1.00 below=51.20 waited=0s",
		"CASE OK config=MCABroken.cfg result=PASS",
		"MERGE OK runs=4 records=4 out=" + filepath.Join(root, "tla", tlc.RunsFile),
	} {
		assert.Contains(t, r.stdout, w)
	}
	e2 := testEnv(t, nil)
	e2.built = benchBuilt
	g := do(e2, "groups", "--root", root, "--stale")
	assert.Equal(t, 0, g.code, "%+v", g)
	assert.Equal(t, "[]\n", g.stdout, "after the bench run the records are current")
	for _, l := range lines(r.stdout) {
		assert.Regexp(t, eventRE, l, "not an event")
	}
	// Run again: nothing is stale, so the bench is not reached.
	e3, _ := benchEnv(t, f, nil)
	again := do(e3, "run", "--root", root, "--dir", filepath.Join(t.TempDir(), "o"), "--bench", "bench-a", "--jar", f.jar)
	assert.Equal(t, 0, again.code, "a run with nothing stale: %+v", again)
	assert.Equal(t, "BENCH OK bench=bench-a stale=0 records="+filepath.Join(root, "tla", tlc.RunsFile)+"\n", again.stdout)
	assert.Len(t, f.staged, 1, "a run with nothing stale staged nothing")
}

// TestRunOnABenchRefusesWhatIsNotARecordMachine: a bench whose jar is not
// the pinned one (or missing), a flag the bench form does not take, and for
// --bench any a fleet whose rows cannot be read or name no record machine
// with the pinned jar, are refused before anything is staged or run, each
// naming the way out.
func TestRunOnABenchRefusesWhatIsNotARecordMachine(t *testing.T) {
	t.Parallel()
	jar := []byte("the pinned jar")
	rows := []benchRow{{Name: "bench-a", TLA: true}, {Name: "desk"}}
	tests := []struct {
		name  string
		bytes []byte
		args  []string
		rows  func(context.Context) ([]benchRow, error)
		want  string
	}{
		{"no record machine", []byte("another jar"), []string{"--bench", "any"}, nil, "no record machine holds the pinned jar: the jar at "},
		{"no row says tla", jar, []string{"--bench", "any"}, func(context.Context) ([]benchRow, error) { return []benchRow{{Name: "desk"}}, nil }, "no record machine holds the pinned jar; run: nova-config machine list (a record machine says tla=true;"},
		{"another jar", []byte("another jar"), []string{"--bench", "bench-a"}, nil, "is not the pinned TLC jar"},
		{"no jar", nil, []string{"--bench", "bench-a"}, nil, "holds no jar at"},
		{"shards", jar, []string{"--bench", "bench-a", "--shards", "2"}, nil, "--shards is not taken with --bench"},
		{"rows unreadable", jar, []string{"--bench", "any"}, func(context.Context) ([]benchRow, error) {
			return nil, errors.New("nova-config machine list: no store named")
		}, "the fleet's machine rows cannot be read: nova-config machine list: no store named"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := benchCheckout(t, jar)
			f := newFakeBench(t, tc.bytes, loads(0.5))
			e, _ := benchEnv(t, f, rows)
			if tc.rows != nil {
				e.bench.machines = tc.rows
			}
			args := append([]string{"run", "--root", root, "--dir", filepath.Join(t.TempDir(), "o"), "--jar", f.jar}, tc.args...)
			r := do(e, args...)
			assert.Equal(t, 2, r.code, "%+v", r)
			assert.Contains(t, r.stderr, tc.want)
			assert.Len(t, lines(r.stderr), 1)
			assert.Empty(t, f.staged, "staged")
			assert.Empty(t, f.ran, "ran")
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
	before := mustRead(t, filepath.Join(root, "tla", tlc.RunsFile))
	f := newFakeBench(t, jar, loads(0.5))
	f.exec = scriptedTLC(t, map[string]int{"MCABroken": 0, "MCAStale": 13}, nil) // MCABroken is declared to violate an invariant
	e, _ := benchEnv(t, f, []benchRow{{Name: "bench-a", TLA: true}})
	r := do(e, "run", "--root", root, "--dir", filepath.Join(t.TempDir(), "o"), "--bench", "bench-a", "--jar", f.jar, "--group", "alpha")
	require.Equal(t, 1, r.code, "%+v", r)
	assert.Contains(t, r.stderr, "BENCH FAIL bench=bench-a")
	assert.NotContains(t, r.stdout, "MERGE OK")
	assert.Equal(t, string(before), string(mustRead(t, filepath.Join(root, "tla", tlc.RunsFile))), "the records changed after a failed case")
	assert.Len(t, f.gone, 1, "the staged directory is removed")
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
	e, _ := benchEnv(t, busy, []benchRow{{Name: "bench-busy", TLA: true}, {Name: "bench-quiet", TLA: true}, {Name: "bench-stale", TLA: true}, {Name: "desk"}})
	e.bench.dial = func(m string) remote {
		return map[string]remote{"bench-busy": busy, "bench-quiet": quiet, "bench-stale": stale}[m]
	}
	r := do(e, "run", "--root", root, "--dir", filepath.Join(t.TempDir(), "o"), "--bench", "any", "--jar", busy.jar)
	require.Equal(t, 0, r.code, "%+v", r)
	assert.Len(t, quiet.ran, 4)
	assert.Empty(t, busy.ran)
	assert.Empty(t, stale.ran)
	assert.Contains(t, r.stdout, "BENCH OK bench=bench-quiet ")
}

// TestTheSSHCommandsAreQuotedAndNiced: the shell lines the real transport
// sends are the operations the fake answers: the run is niced, every word is
// quoted, the staged directory is the bench's own under ~/tla-runs, and a
// directory that is not one is never removed.
func TestTheSSHCommandsAreQuotedAndNiced(t *testing.T) {
	t.Parallel()
	got := runLine("/home/u/tla-runs/nova-tla-ab12", []string{"run", "--root", "/home/u/tla-runs/nova-tla-ab12", "--group", "it's"})
	want := `cd '/home/u/tla-runs/nova-tla-ab12' && exec nice -n 15 '/home/u/tla-runs/nova-tla-ab12/nova-tla' 'run' '--root' '/home/u/tla-runs/nova-tla-ab12' '--group' 'it'\''s'`
	assert.Equal(t, want, got)
	assert.Contains(t, stageLine, `mktemp -d "$HOME/tla-runs/nova-tla-XXXXXX"`)
	assert.Contains(t, stageLine, "-mmin +1440")
	assert.Contains(t, stageLine, `-name 'tlacheck-*'`, "a directory the runner's old name staged is still cleaned up")
	for _, d := range []string{"/", "/home/u", "/home/u/tla-runs", "/home/u/tla-runs/other", "/home/u/tla-runs/nova-tla-ab/../../x"} {
		_, err := removeLine(d)
		assert.Error(t, err, "removeLine(%q) would remove it", d)
	}
	l, err := removeLine("/home/u/tla-runs/nova-tla-ab12")
	require.NoError(t, err)
	assert.Equal(t, `rm -r -- '/home/u/tla-runs/nova-tla-ab12'`, l)
	p, err := parseProbe("Linux x86_64\n64\n0.86 1.62 3.01 1/123 456\n936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88  /opt/tla/tla2tools.jar\n")
	require.NoError(t, err)
	assert.Equal(t, benchProbe{GOOS: "linux", GOARCH: "amd64", CPUs: 64, Load: 0.86, JarSHA256: "936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88"}, p)
	p, err = parseProbe("Linux aarch64\n8\n0.10 0.2 0.3 1/2 3\n")
	require.NoError(t, err, "parseProbe with no jar")
	assert.Equal(t, "arm64", p.GOARCH)
	assert.Empty(t, p.JarSHA256)
}

// shellCall is one line the fake shell was asked to run.
type shellCall struct {
	host, line, stdin string
}

// fakeShell answers each line it is given with the next reply: its status, its
// stdout and its stderr, or err for a transport that could not start; it
// records what it was asked, and reaches no host.
func fakeShell(calls *[]shellCall, replies ...struct {
	code     int
	out, err string
	fail     error
}) shellIn {
	i := 0
	return func(_ context.Context, host, line string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
		in := ""
		if stdin != nil {
			raw, _ := io.ReadAll(stdin) // ignored: a bytes reader, which cannot fail
			in = string(raw)
		}
		*calls = append(*calls, shellCall{host, line, in})
		r := replies[min(i, len(replies)-1)]
		i++
		_, _ = io.WriteString(stdout, r.out) // ignored: a buffer the caller owns
		_, _ = io.WriteString(stderr, r.err) // ignored: a buffer the caller owns
		return r.code, r.fail
	}
}

// TestTheSSHRemoteGoesThroughTheOneBenchDoor: each operation of the real
// remote is one line through pkg/bench's shell: the stage sends the archive on
// stdin and reads back the directory it made, a non-zero status is an error
// naming the first line of stderr, and a bench that does not answer (ssh's own
// 255) fails a run rather than reading as the case's exit.
func TestTheSSHRemoteGoesThroughTheOneBenchDoor(t *testing.T) {
	t.Parallel()
	type reply = struct {
		code     int
		out, err string
		fail     error
	}
	var calls []shellCall
	r := benchRemote{machine: "bench-a", sh: fakeShell(&calls, reply{out: "/home/u/tla-runs/nova-tla-ab12\n"})}
	dir, err := r.Stage(context.Background(), strings.NewReader("the archive"))
	require.NoError(t, err)
	assert.Equal(t, "/home/u/tla-runs/nova-tla-ab12", dir)
	assert.Equal(t, []shellCall{{"bench-a", stageLine, "the archive"}}, calls)

	calls = nil
	r.sh = fakeShell(&calls, reply{code: 2, err: "tar: short read\nmore\n"})
	_, err = r.Stage(context.Background(), strings.NewReader("x"))
	require.Error(t, err)
	assert.Equal(t, "ssh bench-a: exit status 2: tar: short read", err.Error())

	calls = nil
	r.sh = fakeShell(&calls, reply{out: "/elsewhere\n"})
	_, err = r.Stage(context.Background(), strings.NewReader("x"))
	require.Error(t, err, "a directory that is not one the stage makes")
	assert.Contains(t, err.Error(), "not a directory under ~/tla-runs")

	calls = nil
	r.sh = fakeShell(&calls, reply{out: "Linux x86_64\n16\n1.50 1 1 1/2 3\n"})
	p, err := r.Probe(context.Background(), "/opt/tla/tla2tools.jar")
	require.NoError(t, err)
	assert.Equal(t, 16, p.CPUs)
	assert.Equal(t, probeLine("/opt/tla/tla2tools.jar"), calls[0].line)

	calls = nil
	var out, errs strings.Builder
	r.sh = fakeShell(&calls, reply{code: 1, out: "CASE FAIL\n"})
	code, err := r.Run(context.Background(), dir, []string{"run"}, &out, &errs)
	require.NoError(t, err, "a run that said no is its exit, not an error")
	assert.Equal(t, 1, code)
	assert.Equal(t, "CASE FAIL\n", out.String())
	assert.Equal(t, runLine(dir, []string{"run"}), calls[0].line)

	r.sh = fakeShell(&calls, reply{code: bench.NoAnswer})
	_, err = r.Run(context.Background(), dir, []string{"run"}, &out, &errs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the bench did not answer")

	r.sh = fakeShell(&calls, reply{fail: errors.New("ssh not found")})
	_, err = r.Load(context.Background())
	require.Error(t, err)
	assert.Equal(t, "ssh bench-a: ssh not found", err.Error())

	calls = nil
	r.sh = fakeShell(&calls, reply{})
	require.NoError(t, r.Remove(context.Background(), dir))
	assert.Equal(t, "rm -r -- '"+dir+"'", calls[0].line)
	require.Error(t, r.Remove(context.Background(), "/home/u"), "a directory the stage did not make is never removed")
	assert.Len(t, calls, 1, "the refused remove reached no host")
}

// TestTheMachineRowsAreReadFromNovaConfigsList: the record machines are the
// rows whose line says tla=true; a row of a nova-config that has no tla field
// is none.
func TestTheMachineRowsAreReadFromNovaConfigsList(t *testing.T) {
	t.Parallel()
	rows := parseMachineList("MACHINE name=bench-a user=u seat=bench-a slots=64 runners=0 width=32 tla=true\nMACHINE name=desk user=u seat=desk slots=64 runners=1 width=0 tla=false\nMACHINE name=old user=u seat=s slots=1 runners=0 width=0\nCONFIG LIST kind=machine rows=3\n")
	assert.Equal(t, []benchRow{{"bench-a", true}, {"desk", false}, {"old", false}}, rows)
}
