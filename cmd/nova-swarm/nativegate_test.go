package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcontract"
	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// gateClasses answers the gate decision by the failing test its state names: test -> p per
// class; a test it does not name is caused. err, when set, is every answer.
type gateClasses struct {
	p    map[string]map[string]float64
	err  error
	mu   sync.Mutex
	asks int
}

func (g *gateClasses) Name() string { return "fake" }

func (g *gateClasses) Ask(_ context.Context, _ decide.Schema, state string) (map[string]decide.Answer, decide.Usage, error) {
	g.mu.Lock()
	g.asks++
	g.mu.Unlock()
	if g.err != nil {
		return nil, decide.Usage{}, g.err
	}
	test, _, _ := strings.Cut(strings.SplitN(state, "\n", 3)[1], " ")
	p, ok := g.p[test]
	if !ok {
		p = map[string]float64{decide.Caused: 0.9, decide.Flaky: 0.05, decide.PreExisting: 0.05}
	}
	best := decide.Caused
	for k, v := range p {
		if v > p[best] {
			best = k
		}
	}
	return map[string]decide.Answer{"class": {Type: decide.Choice, Value: best, P: p}}, decide.Usage{}, nil
}

// fakeRun is a gateRunner: the tests red in the base worktree (a dir ending .gate-base) and
// at the head, and the runs it was asked for. A run that holds the test hang never ends on
// its own: it waits out its context's deadline (on synctest's clock) and is read as the real
// runner reads a run its deadline killed, its output partial.
type fakeRun struct {
	baseRed, headRed map[string]bool
	err              error
	hang, partial    string
	mu               sync.Mutex
	runs             []string // "base TestA,TestB" or "head TestA"
	unbounded        int      // runs handed a context with no deadline, or one past gateRunWait
}

func (f *fakeRun) run(ctx context.Context, dir string, fs []decide.Failure) (map[string]bool, error) {
	if dl, ok := ctx.Deadline(); !ok || time.Until(dl) > gateRunWait {
		f.mu.Lock()
		f.unbounded++
		f.mu.Unlock()
	}
	where, red := "head", f.headRed
	if filepath.Base(dir) == ".gate-base" {
		where, red = "base", f.baseRed
		if _, err := os.Stat(filepath.Join(dir, "x.go")); err != nil {
			return nil, errors.New("the base worktree holds no checkout")
		}
	}
	f.mu.Lock()
	f.runs = append(f.runs, where+" "+strings.ReplaceAll(decide.Names(fs), " ", ""))
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	for _, x := range fs {
		if x.Test == f.hang && f.hang != "" {
			<-ctx.Done()
			return gateRed(fs, f.partial, subproc.Bounded{Ctx: ctx, Budget: gateRunWait}.Wrap("the gate's rerun", errors.New("signal: killed")))
		}
	}
	out := map[string]bool{}
	for _, x := range fs {
		out[x.Key()] = red[x.Test]
	}
	return out, nil
}

// gateJob is a framed work card's job whose child ended not-done on a red gate: its repo
// (start, then the work's commit), its RESULT.md naming gate.log, and the log of output.
func gateJob(t *testing.T, verdict, output string, bars bool) (cfg nativeRunConfig, job, start string) {
	t.Helper()
	root := t.TempDir()
	job = filepath.Join(root, "slot", "jobs", "c1.w2")
	repo := filepath.Join(job, swarm.JobRepo)
	runGit(t, "", "init", "-q", "-b", "main", "--", repo)
	write(t, filepath.Join(repo, "x.go"), "package x\n")
	gitAs(t, repo, "add", ".")
	gitAs(t, repo, "commit", "-q", "-m", "base")
	start = gitAs(t, repo, "rev-parse", "HEAD")
	write(t, filepath.Join(repo, "x.go"), "package x\n\nvar Y = 1\n")
	gitAs(t, repo, "commit", "-q", "-am", "work")
	head := gitAs(t, repo, "rev-parse", "HEAD")
	write(t, filepath.Join(job, "gate.log"), output)
	write(t, filepath.Join(job, "RESULT.md"), "head: "+head+"\nbranch: sprint/c1.w2\nverdict: "+verdict+"\ngate: go test ./...\noutput: gate.log\nreport: tests red in m/p\n")
	fr := &cardcontract.Frame{Kind: "work", Card: "c1.w2", Attempt: 2}
	if bars {
		fr.DecideGateFlaky, fr.DecideGatePreexisting = "0.8", "0.8"
	}
	cfg = nativeRunConfig{root: root, label: "c1.w2.g1.e1", frame: fr, card: []byte("c1: fix x (s1) tier: flash\n\nPATHS: x.go\n\nThe task.\n")}
	return cfg, job, start
}

const twoRed = "--- FAIL: TestA (0.01s)\n    a_test.go:3: timed out after 1s\nFAIL\nFAIL\tm/p\t1.1s\n--- FAIL: TestB (0.00s)\n    b_test.go:9: bind: operation not permitted\nFAIL\nFAIL\tm/q\t0.1s\n"

// A not-done child's red gate is decided failure by failure, with the base run in a
// worktree at the start: flaky ones are rerun once at the head, green on the rerun is
// the gate green (the member takes the work as done), red again is caused; pre-existing
// ones are reported pre-existing and never rerun; one caused failure is the gate caused.
// The NATIVE GATE line names the route, the tests and each failure's class, and each
// rerun's result is its decision's outcome in <root>/decide/gate.jsonl under
// <primary>@<attempt>@gate. With the bars unset (the sprint row's default) the decisions are
// recorded and shown after the base run, and nothing is rerun or reclassified: the take is
// the child's.
func TestNativeGateRoutesARedGate(t *testing.T) {
	t.Parallel()
	flaky := map[string]float64{decide.Flaky: 0.9, decide.Caused: 0.05, decide.PreExisting: 0.05}
	old := map[string]float64{decide.Flaky: 0.05, decide.Caused: 0.05, decide.PreExisting: 0.9}
	for _, tc := range []struct {
		name     string
		unset    bool
		classes  map[string]map[string]float64
		run      *fakeRun
		line     string
		runs     []string
		outcomes map[string]string
	}{
		{"flaky, green on the rerun", false, map[string]map[string]float64{"TestA": flaky, "TestB": flaky}, &fakeRun{},
			"route=green tests=TestA,TestB classes=TestA:flaky:0.90,TestB:flaky:0.90", []string{"base TestA,TestB", "head TestA,TestB"}, map[string]string{"m/p.TestA": decide.Flaky, "m/q.TestB": decide.Flaky}},
		{"flaky and pre-existing, green on the rerun", false, map[string]map[string]float64{"TestA": flaky, "TestB": old}, &fakeRun{baseRed: map[string]bool{"TestB": true}},
			"route=pre-existing tests=TestB classes=TestA:flaky:0.90,TestB:pre-existing:0.90", []string{"base TestA,TestB", "head TestA"}, map[string]string{"m/p.TestA": decide.Flaky, "m/q.TestB": ""}},
		{"flaky, red again, green at the base", false, map[string]map[string]float64{"TestA": flaky, "TestB": flaky}, &fakeRun{headRed: map[string]bool{"TestB": true}},
			"route=caused tests=TestB classes=TestA:flaky:0.90,TestB:flaky:0.90", []string{"base TestA,TestB", "head TestA,TestB"}, map[string]string{"m/p.TestA": decide.Flaky, "m/q.TestB": decide.Caused}},
		{"pre-existing", false, map[string]map[string]float64{"TestA": old, "TestB": old}, &fakeRun{baseRed: map[string]bool{"TestA": true, "TestB": true}},
			"route=pre-existing tests=TestA,TestB classes=TestA:pre-existing:0.90,TestB:pre-existing:0.90", []string{"base TestA,TestB"}, map[string]string{"m/p.TestA": "", "m/q.TestB": ""}},
		{"one caused", false, map[string]map[string]float64{"TestA": flaky}, &fakeRun{},
			"route=caused tests=TestB classes=TestA:flaky:0.90,TestB:caused:0.90", []string{"base TestA,TestB"}, map[string]string{"m/p.TestA": "", "m/q.TestB": ""}},
		{"bars unset: recorded and shown, nothing rerun", true, map[string]map[string]float64{"TestA": flaky, "TestB": old}, &fakeRun{baseRed: map[string]bool{"TestB": true}},
			"route=caused tests=TestA,TestB classes=TestA:flaky:0.90,TestB:pre-existing:0.90", []string{"base TestA,TestB"}, map[string]string{"m/p.TestA": "", "m/q.TestB": ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, job, start := gateJob(t, "not-done", twoRed, !tc.unset)
			cfg.decider = &decider{backend: &gateClasses{p: tc.classes}, now: func() time.Time { return time.Date(2026, 10, 3, 8, 0, 0, 0, time.UTC) }}
			var out, errOut bytes.Buffer
			nativeGate(cfg, job, "", start, tc.run.run, &out, &errOut)
			assert.Equal(t, "NATIVE GATE label=c1.w2.g1.e1 op=c1@2@gate "+tc.line+"\n", out.String(), errOut.String())
			assert.Empty(t, errOut.String())
			assert.Equal(t, tc.runs, tc.run.runs)
			assert.Zero(t, tc.run.unbounded, "each run of the failing tests (the base, the rerun) is bounded by gateRunWait")
			ds, err := decide.Load(gateRecord(cfg.root))
			require.NoError(t, err)
			got := map[string]string{}
			for _, d := range ds {
				label := ""
				if d.Outcome != nil {
					label = d.Outcome.Label
				}
				got[strings.TrimPrefix(d.ID, "c1@2@gate/")] = label
				assert.Contains(t, d.State, "CARD PATHS (the files the card may change): x.go\n")
				assert.Contains(t, d.State, "x.go +2 -0\n", "the diff from the start, summarised")
			}
			assert.Equal(t, tc.outcomes, got)
			_, err = os.Stat(filepath.Join(job, ".gate-base"))
			assert.True(t, os.IsNotExist(err), "the base worktree is removed")
		})
	}
}

// No gate decision is made, and nothing is said, for a child that did not end not-done or a
// gate output with no go test failure; one that cannot be made
// (no key, a backend that fails, bars it cannot read) is one NOTE line, prints no NATIVE GATE
// line, records nothing, and the take is the child's. A base that cannot be run is asked as
// not run; a rerun that cannot run is red again.
func TestNativeGateMakesNoDecisionItCannot(t *testing.T) {
	t.Parallel()
	flaky := map[string]map[string]float64{"TestA": {decide.Flaky: 0.9, decide.Caused: 0.05, decide.PreExisting: 0.05}, "TestB": {decide.Flaky: 0.9, decide.Caused: 0.05, decide.PreExisting: 0.05}}
	for _, tc := range []struct {
		name, verdict, output string
		bars                  bool
		backend               decide.Backend
		frameBars             [2]string
		note                  string
	}{
		{"verdict ok", "ok", twoRed, true, &gateClasses{p: flaky}, [2]string{}, ""},
		{"no go test failure", "not-done", "make: *** [lint] Error 1\n", true, &gateClasses{p: flaky}, [2]string{}, ""},
		{"no key", "not-done", twoRed, true, nil, [2]string{}, "NATIVE NOTE: c1.w2.g1.e1 no gate decision: JEV_API_KEY is absent from this environment (the member loop's nova-secrets keys); the take is reported as the child said\n"},
		{"a backend that fails", "not-done", twoRed, true, &gateClasses{err: errors.New("HTTP 402: no credits")}, [2]string{}, "no gate decision: HTTP 402: no credits; the take is reported as the child said"},
		{"bars it cannot read", "not-done", twoRed, true, &gateClasses{p: flaky}, [2]string{"0.5", "0.5"}, "no gate decision: decide_gate_flaky 0.5 and decide_gate_preexisting 0.5 sum to at most 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, job, start := gateJob(t, tc.verdict, tc.output, tc.bars)
			if tc.frameBars[0] != "" {
				cfg.frame.DecideGateFlaky, cfg.frame.DecideGatePreexisting = tc.frameBars[0], tc.frameBars[1]
			}
			if tc.backend != nil {
				cfg.decider = &decider{backend: tc.backend, now: time.Now}
			} else if os.Getenv(decide.JevSecret) != "" {
				t.Skip("this environment holds " + decide.JevSecret)
			}
			run := &fakeRun{}
			var out, errOut bytes.Buffer
			nativeGate(cfg, job, "", start, run.run, &out, &errOut)
			assert.Empty(t, out.String())
			if tc.note == "" {
				assert.Empty(t, errOut.String())
			} else {
				assert.Contains(t, errOut.String(), tc.note)
			}
			ds, err := decide.Load(gateRecord(cfg.root))
			require.NoError(t, err)
			assert.Empty(t, ds)
		})
	}
	cfg, job, _ := gateJob(t, "not-done", twoRed, true)
	cfg.decider = &decider{backend: &gateClasses{p: flaky}, now: time.Now}
	run := &fakeRun{err: errors.New("go: no toolchain")}
	var out, errOut bytes.Buffer
	nativeGate(cfg, job, "", "0000000000000000000000000000000000000000", run.run, &out, &errOut)
	assert.Equal(t, "NATIVE GATE label=c1.w2.g1.e1 op=c1@2@gate route=caused tests=TestA,TestB classes=TestA:flaky:0.90,TestB:flaky:0.90\n", out.String())
	assert.Contains(t, errOut.String(), "the gate's failures were not run at the base 0000000000000000000000000000000000000000")
	assert.Contains(t, errOut.String(), "the flaky failures' rerun did not run: go: no toolchain; read as red again")
	ds, err := decide.Load(gateRecord(cfg.root))
	require.NoError(t, err)
	require.Len(t, ds, 2)
	assert.Contains(t, ds[0].State, "AT THE BASE (the same test on the commit the card started from): not run\n")
	assert.Equal(t, decide.RedAgain, ds[0].Outcome.Label)
}

// A base run its deadline killed is not run, though its partial output names a failure:
// every failure is asked as not run at the base, the one that never finished never as green
// there. The deadline is gateRunWait on synctest's clock: no real time passes.
func TestNativeGateAsksAHungBaseRunAsNotRun(t *testing.T) {
	t.Parallel()
	cfg, job, start := gateJob(t, "not-done", twoRed, true)
	cfg.decider = &decider{backend: &gateClasses{}, now: func() time.Time { return time.Date(2026, 10, 3, 8, 0, 0, 0, time.UTC) }}
	run := &fakeRun{hang: "TestB", partial: "--- FAIL: TestA (0.01s)\n    a_test.go:3: boom\nFAIL\nFAIL\tm/p\t1.1s\n"}
	var out, errOut bytes.Buffer
	synctest.Test(t, func(t *testing.T) {
		nativeGate(cfg, job, "", start, run.run, &out, &errOut)
	})
	assert.Equal(t, "NATIVE GATE label=c1.w2.g1.e1 op=c1@2@gate route=caused tests=TestA,TestB classes=TestA:caused:0.90,TestB:caused:0.90\n", out.String())
	assert.Contains(t, errOut.String(), "the gate's failures were not run at the base "+start+": the gate's rerun did not finish within 3m0s and was killed; asked as not run")
	assert.Equal(t, []string{"base TestA,TestB"}, run.runs)
	assert.Zero(t, run.unbounded)
	ds, err := decide.Load(gateRecord(cfg.root))
	require.NoError(t, err)
	require.Len(t, ds, 2)
	for _, d := range ds {
		assert.Contains(t, d.State, "AT THE BASE (the same test on the commit the card started from): not run\n", d.ID)
	}
}

// The gate output is the file the result's output: names inside the job (or the child's
// temp), else the result's body; a path outside both is never read.
func TestGateTextReadsTheOutputInsideTheJobOnly(t *testing.T) {
	t.Parallel()
	_, job, _ := gateJob(t, "not-done", twoRed, true)
	verdict, text := gateText(job, "")
	assert.Equal(t, "not-done", verdict)
	assert.Equal(t, twoRed, text)
	outside := filepath.Join(t.TempDir(), "gate.log")
	write(t, outside, twoRed)
	write(t, filepath.Join(job, "RESULT.md"), "head: -\nbranch: b\nverdict: not-done\ngate: g\noutput: "+outside+"\nreport: red\n\n## Body\n\nno output here\n")
	_, text = gateText(job, "")
	assert.Empty(t, decide.ParseGateOutput(text), "a path outside the job is not read; the body holds none")
	_, text = gateText(job, filepath.Dir(outside))
	assert.Equal(t, twoRed, text, "inside the child's temp it is")
}

// The member reads native's NATIVE GATE line: green is the verdict ok (the report says the
// rerun), so the finish is judged ok on its push; pre-existing is the failed finish
// `pre-existing: <tests>`, which has no failure class (never the card's).
func TestTheMemberReadsTheGateLine(t *testing.T) {
	t.Parallel()
	done := make(chan struct{})
	close(done)
	const sha = "0123456789abcdef0123456789abcdef01234567"
	for _, tc := range []struct {
		line, verdict, gate, report string
	}{
		{"NATIVE GATE label=c1 op=c1@1@gate route=green tests=TestA,TestB classes=TestA:flaky:0.90,TestB:flaky:0.90", "ok", member.GateGreen, "gate: TestA, TestB flaky, green on the rerun; tests red"},
		{"NATIVE GATE label=c1 op=c1@1@gate route=pre-existing tests=TestB", "not-done", member.GatePreExisting, "tests red"},
		{"NATIVE GATE label=c1 op=c1@1@gate route=caused tests=TestA", "not-done", "caused", "tests red"},
	} {
		dir := t.TempDir()
		logPath := filepath.Join(dir, "c1.native.log")
		write(t, logPath, tc.line+"\nNATIVE OK rc=0 harness=ok\n")
		results := filepath.Join(dir, "results", "c1")
		write(t, filepath.Join(results, "run", "1", "RESULT.md"), "head: "+sha+"\nbranch: b\nverdict: not-done\ngate: g\noutput: -\nreport: tests red\n")
		c := &nativeChild{card: "c1", logPath: logPath, results: results, job: filepath.Join(dir, "job"), done: done}
		r := c.Result()
		assert.Equal(t, []string{tc.verdict, tc.gate, tc.report}, []string{r.Verdict, r.Gate, r.Report}, tc.line)
	}
	fin, why := member.Judge(member.Result{Ran: true, Shaped: true, Verdict: "not-done", Gate: member.GatePreExisting, GateTests: "TestB"}, member.Push{Sha: sha})
	assert.Equal(t, member.FinishFailed, fin)
	assert.Equal(t, "pre-existing: TestB", why)
	fin, _ = member.Judge(member.Result{Ran: true, Shaped: true, Verdict: "ok", Gate: member.GateGreen}, member.Push{Sha: sha})
	assert.Equal(t, member.FinishOK, fin)
}

// The real runner's reading of a rerun: exit 0 is every test green; a test the output names
// failing is red, a package that failed naming none has all its tests red; a run that
// failed with no go test output is an error, never a green; a run its deadline killed is an
// error whatever its partial output names (the deadline gateRunWait on synctest's clock).
func TestGateRedReadsARerun(t *testing.T) {
	t.Parallel()
	fs := []decide.Failure{{Pkg: "m/p", Test: "TestA"}, {Pkg: "m/p", Test: "TestB"}, {Pkg: "m/q", Test: "TestC"}}
	red, err := gateRed(fs, "ok  \tm/p\t0.1s\n", nil)
	require.NoError(t, err)
	assert.Empty(t, red)
	var exit error = &exec.ExitError{} // a go test that exited non-zero
	red, err = gateRed(fs, "--- FAIL: TestB (0s)\nFAIL\nFAIL\tm/p\t0.1s\nFAIL\tm/q [build failed]\n", exit)
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"m/p.TestB": true, "m/q.TestC": true}, red)
	_, err = gateRed(fs, "sandbox: denied\n", exit)
	assert.ErrorContains(t, err, "go test failed with no test output: sandbox: denied")
	synctest.Test(t, func(t *testing.T) {
		b := subproc.Prepare(context.Background(), gateRunWait, "go", "test")
		defer b.Cancel()
		<-b.Ctx.Done()
		red, err := gateRed(fs, "--- FAIL: TestA (0s)\nFAIL\nFAIL\tm/p\t0.1s\n", b.Wrap("the gate's rerun", exit))
		var timeout *subproc.TimeoutError
		require.ErrorAs(t, err, &timeout, "m/q's TestC never finished; it is not green")
		assert.Nil(t, red)
		assert.EqualError(t, err, "the gate's rerun did not finish within 3m0s and was killed")
	})
}
