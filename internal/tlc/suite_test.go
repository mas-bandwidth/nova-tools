package tlc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeClock struct {
	now  time.Time
	step time.Duration
}

func (c *fakeClock) Now() time.Time {
	t := c.now
	c.now = c.now.Add(c.step)
	return t
}

func testClock(step time.Duration) *fakeClock {
	return &fakeClock{now: time.Now(), step: step}
}

func suiteTree(t *testing.T) (string, []Case) {
	t.Helper()
	plan := header +
		row("MCA.cfg", "MCA.tla", "pass", "-", "check", "alpha", "required", "-") +
		row("MCABroken.cfg", "MCA.tla", "invariant", "OnePlacePerTable", "ignore-terminal", "alpha", "required", "-") +
		row("MCATerm.cfg", "MCA.tla", "temporal", "TermEnds", "check", "alpha", "required", "-")
	root := tree(t, map[string]string{
		"CASES.tsv": plan, "MCA.tla": "model\n", "A.tla": "module\n",
		"MCA.cfg": "SPECIFICATION Spec\n", "MCABroken.cfg": "SPECIFICATION Spec\n", "MCATerm.cfg": termCfg,
	})
	cases, err := LoadCases(root)
	require.NoError(t, err)
	return root, cases
}

func script(t *testing.T, seen *[]Run) Executor {
	t.Helper()
	answers := map[string]struct {
		code int
		log  string
	}{
		"MCA.cfg":       {0, fixture(t, "pass.log")},
		"MCABroken.cfg": {12, fixture(t, "invariant.log")},
		"MCATerm.cfg":   {13, fixture(t, "temporal-new.log")},
	}
	return func(ctx context.Context, r Run, log string) int {
		*seen = append(*seen, r)
		_ = os.WriteFile(filepath.Join(r.Dir, strings.TrimSuffix(r.Module, ".tla")+"_TTrace_1.tla"), []byte("trace\n"), 0o644)
		a := answers[r.Config]
		_ = os.WriteFile(log, []byte(a.log), 0o644)
		return a.code
	}
}

func suiteOptions(root string, cases []Case, out string, exec Executor, clock *fakeClock) Options {
	return Options{
		Root: root, Cases: cases, Out: out, Budget: 110 * time.Second, Workers: 2, Platform: "linux-amd64", CPUs: 8,
		Jar:  Jar{Path: "/j/tla2tools.jar", Source: "flag", SHA256: strings.Repeat("b", 64)},
		Java: "/usr/bin/java", JavaVer: "21.0.12.1", Clock: clock.Now, Exec: exec,
	}
}

// suiteFix is one suite's repeated setup: the checkout, the output dir and the
// options a test then edits.
type suiteFix struct {
	root, out string
	cases     []Case
	seen      []Run
	opt       Options
}

func openSuite(t *testing.T) *suiteFix {
	t.Helper()
	root, cases := suiteTree(t)
	s := &suiteFix{root: root, cases: cases, out: filepath.Join(t.TempDir(), "o")}
	s.opt = suiteOptions(root, cases, s.out, script(t, &s.seen), testClock(time.Millisecond))
	return s
}

// refusedRun is the sentence a suite that must not run repeats.
func refusedRun(t *testing.T, res Result, err error, ran int, out, want, why string) {
	t.Helper()
	require.NoError(t, err)
	require.True(t, res.Failed, "suite = %+v, ran %d cases", res, ran)
	require.Equal(t, want, res.Refused, "suite = %+v, ran %d cases", res, ran)
	require.Zero(t, ran, "suite = %+v, ran %d cases", res, ran)
	require.Empty(t, res.Records, "suite = %+v, ran %d cases", res, ran)
	_, statErr := os.Stat(filepath.Join(out, RunsFile))
	require.Error(t, statErr, why)
}

func assertCaseRun(t *testing.T, r Run, workers int, noDeadlock bool, work, out string) {
	t.Helper()
	flags := "%s: workers=%d noDeadlock=%v lncheck=%v"
	assert.Equal(t, workers, r.Workers, flags, r.Config, r.Workers, r.NoDeadlock, r.LnCheckFinal)
	assert.Equal(t, noDeadlock, r.NoDeadlock, flags, r.Config, r.Workers, r.NoDeadlock, r.LnCheckFinal)
	assert.True(t, r.LnCheckFinal, flags, r.Config, r.Workers, r.NoDeadlock, r.LnCheckFinal)
	assert.Equal(t, []string{"-XX:+UseParallelGC", "-XX:ActiveProcessorCount=2", "-Xmx2g"}, r.JVM, "%s: JVM = %v", r.Config, r.JVM)
	where := "%s: dir=%s jar=%s java=%s"
	assert.Equal(t, work, r.Dir, where, r.Config, r.Dir, r.Jar, r.Java)
	assert.Equal(t, "/j/tla2tools.jar", r.Jar, where, r.Config, r.Dir, r.Jar, r.Java)
	assert.Equal(t, "/usr/bin/java", r.Java, where, r.Config, r.Dir, r.Jar, r.Java)
	priv := "%s: tmp=%s meta=%s are not private under the output"
	assert.True(t, strings.HasPrefix(r.TmpDir, out), priv, r.Config, r.TmpDir, r.MetaDir)
	assert.True(t, strings.HasPrefix(r.MetaDir, r.TmpDir), priv, r.Config, r.TmpDir, r.MetaDir)
	_, err := os.Stat(r.TmpDir)
	assert.Error(t, err, "%s: the temporary directory was left behind", r.Config)
}

func TestRunSuiteRecordsEachCaseAndKeepsTheCheckoutClean(t *testing.T) {
	t.Parallel()
	s := openSuite(t)
	var reported []Record
	clock := &fakeClock{now: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC), step: 250 * time.Millisecond}
	s.opt.Clock = clock.Now
	s.opt.OnCase = func(r Record) { reported = append(reported, r) }
	res, err := RunSuite(s.opt)
	require.NoError(t, err, "suite = %+v, %v", res, err)
	require.False(t, res.Failed, "suite = %+v, %v", res, err)
	require.Empty(t, res.Refused, "suite = %+v, %v", res, err)
	require.Equal(t, reported, res.Records, "reported %d records, kept %d", len(reported), len(res.Records))
	require.Len(t, res.Records, 3, "reported %d records, kept %d", len(reported), len(res.Records))
	src, err := SourceAt(s.root)
	require.NoError(t, err)
	fp, files, err := src.Fingerprint("MCA.cfg")
	require.NoError(t, err)
	first := res.Records[0]
	want := Record{Config: "MCA.cfg", Module: "MCA.tla", InputSHA256: fp, InputFiles: files, JarSHA256: strings.Repeat("b", 64), JavaVersion: "21.0.12.1", Workers: 2, Host: "linux-amd64", CPUs: 8,
		StartedUTC: "2026-09-28T12:00:00.250000+00:00", Generated: "15518", Distinct: "263", Seconds: "0.250",
		Exit: 0, Result: "PASS", Expected: "pass", Property: "-", Budget: "110", Mode: "bounded"}
	require.Equal(t, want, first, "first record\n got %+v\nwant %+v", first, want)
	require.Equal(t, 12, res.Records[1].Exit, "records = %+v", res.Records)
	require.Equal(t, 13, res.Records[2].Exit, "records = %+v", res.Records)
	require.Equal(t, "PASS", res.Records[1].Result, "records = %+v", res.Records)
	require.Equal(t, "PASS", res.Records[2].Result, "records = %+v", res.Records)
	wantFlags := []struct {
		workers    int
		noDeadlock bool
	}{{2, false}, {1, true}, {1, false}}
	for i, r := range s.seen {
		assertCaseRun(t, r, wantFlags[i].workers, wantFlags[i].noDeadlock, res.Work, s.out)
	}
	m, _ := filepath.Glob(filepath.Join(s.root, "tla", "*TTrace*"))
	assert.Empty(t, m, "the checkout gained %v", m)
	m, _ = filepath.Glob(filepath.Join(res.Work, "*TTrace*"))
	assert.NotEmpty(t, m, "no error trace was kept with the private copy")
	_, err = os.Stat(filepath.Join(res.Work, "CASES.tsv"))
	assert.Error(t, err, "the plan was copied into the working copy")
	recs, err := ReadRecordsFile(filepath.Join(s.out, RunsFile))
	require.NoError(t, err, "RUNS.tsv = %v, %v", recs, err)
	require.Equal(t, recs, res.Records, "RUNS.tsv = %v, %v", recs, err)
	raw, err := os.ReadFile(filepath.Join(s.out, "MCA.cfg.log"))
	require.NoError(t, err, "the case's log was not kept: %v", err)
	require.Equal(t, fixture(t, "pass.log"), string(raw), "the case's log was not kept: %v", err)
}

func TestRunSuiteFailsACaseThatIsNotWhatItDeclares(t *testing.T) {
	t.Parallel()
	s := openSuite(t)
	inner := s.opt.Exec
	s.opt.Exec = func(ctx context.Context, r Run, log string) int {
		code := inner(ctx, r, log)
		if r.Config == "MCABroken.cfg" {
			return 0
		}
		return code
	}
	res, err := RunSuite(s.opt)
	require.NoError(t, err, "suite = %+v, %v", res, err)
	require.True(t, res.Failed, "suite = %+v, %v", res, err)
	got := res.Records[1]
	require.Equal(t, "FAIL", got.Result, "record = %+v", got)
	require.Zero(t, got.Exit, "record = %+v", got)
	require.Equal(t, "PASS", res.Records[0].Result, "a failing case failed its neighbours")
	require.Equal(t, "PASS", res.Records[2].Result, "a failing case failed its neighbours")
}

func TestRunSuiteWritesNothingWhenItsInputsMoveUnderIt(t *testing.T) {
	t.Parallel()
	s := openSuite(t)
	inner := s.opt.Exec
	s.opt.Exec = func(ctx context.Context, r Run, log string) int {
		if r.Config == "MCABroken.cfg" {
			_ = os.WriteFile(filepath.Join(s.root, "tla", "MCA.tla"), []byte("edited while running\n"), 0o644)
		}
		return inner(ctx, r, log)
	}
	res, err := RunSuite(s.opt)
	require.NoError(t, err)
	require.True(t, res.Failed, "suite = %+v", res)
	require.Equal(t, "model inputs changed during execution", res.Refused, "suite = %+v", res)
	_, err = os.Stat(filepath.Join(s.out, RunsFile))
	require.Error(t, err, "records were written for inputs that changed under the run")
}

func TestRunSuiteEndsWhenTheBudgetDoes(t *testing.T) {
	t.Parallel()
	s := openSuite(t)
	s.opt.Clock = testClock(time.Minute).Now
	s.opt.Budget = 90 * time.Second
	res, err := RunSuite(s.opt)
	require.NoError(t, err)
	require.True(t, res.Failed, "records=%d ran=%d failed=%v", len(res.Records), len(s.seen), res.Failed)
	require.Less(t, len(res.Records), len(s.cases), "records=%d ran=%d failed=%v", len(res.Records), len(s.seen), res.Failed)
	require.Equal(t, len(res.Records), len(s.seen), "records=%d ran=%d failed=%v", len(res.Records), len(s.seen), res.Failed)
	recs, err := ReadRecordsFile(filepath.Join(s.out, RunsFile))
	require.NoError(t, err, "the records of the cases that ran were not kept: %v, %v", recs, err)
	require.Len(t, recs, len(res.Records), "the records of the cases that ran were not kept: %v, %v", recs, err)
}

func TestRunSuiteNeverStartsACaseAfterTheBudget(t *testing.T) {
	t.Parallel()
	s := openSuite(t)
	s.opt.Clock = testClock(time.Hour).Now
	s.opt.Budget = time.Second
	res, err := RunSuite(s.opt)
	require.NoError(t, err, "ran %d cases, failed=%v, err=%v", len(s.seen), res.Failed, err)
	require.Empty(t, s.seen, "ran %d cases, failed=%v, err=%v", len(s.seen), res.Failed, err)
	require.True(t, res.Failed, "ran %d cases, failed=%v, err=%v", len(s.seen), res.Failed, err)
	got := res.Records[0]
	require.Equal(t, ExitTimeout, got.Exit, "record = %+v", got)
	require.Equal(t, "FAIL", got.Result, "record = %+v", got)
	require.Equal(t, "-", got.Generated, "record = %+v", got)
	raw, err := os.ReadFile(filepath.Join(s.opt.Out, "MCA.cfg.log"))
	require.NoError(t, err)
	require.Contains(t, string(raw), "before starting", "log = %q", raw)
}

func TestRunSuiteRecordsAManualRun(t *testing.T) {
	t.Parallel()
	s := openSuite(t)
	s.opt.Cases = s.cases[:1]
	s.opt.Selection = Selection{Shards: 3, Shard: 0}
	s.opt.Manual, s.opt.Budget = true, 1500*time.Millisecond
	res, err := RunSuite(s.opt)
	require.NoError(t, err, "records = %+v, %v", res.Records, err)
	require.Len(t, res.Records, 1, "records = %+v, %v", res.Records, err)
	require.Equal(t, "manual", res.Records[0].Mode, "records = %+v, %v", res.Records, err)
	require.Equal(t, "1.5", res.Records[0].Budget, "records = %+v, %v", res.Records, err)
}

func TestRunSuiteRefusesModelsEditedBetweenTheDigestAndTheCopy(t *testing.T) {
	t.Parallel()
	s := openSuite(t)
	ran := 0
	s.opt.Exec = func(context.Context, Run, string) int { ran++; return 0 }
	s.opt.beforeCopy = func() {
		_ = os.WriteFile(filepath.Join(s.root, "tla", "MCA.tla"), []byte("edited before the copy\n"), 0o644)
	}
	res, err := RunSuite(s.opt)
	refusedRun(t, res, err, ran, s.out, "model inputs changed while the models were copied", "records were written for models that were not the digest's")
}

func TestACopyOfTheModelsHasTheFingerprintsOfItsSource(t *testing.T) {
	t.Parallel()
	root, cases := suiteTree(t)
	work := filepath.Join(t.TempDir(), "work")
	require.NoError(t, CopyModels(filepath.Join(root, "tla"), work))
	src, err := SourceAt(root)
	require.NoError(t, err)
	want, err := fingerprints(src, cases)
	require.NoError(t, err)
	copied := src
	copied.TLADir = work
	got, err := fingerprints(copied, cases)
	require.NoError(t, err, "copy fingerprints %v (%v), source %v", got, err, want)
	require.True(t, sameFingerprints(got, want), "copy fingerprints %v (%v), source %v", got, err, want)
	require.NoError(t, os.WriteFile(filepath.Join(work, "MCA.tla"), []byte("other\n"), 0o644))
	got, _ = fingerprints(copied, cases)
	require.False(t, sameFingerprints(got, want), "an edited copy has its source's fingerprints")
}

func TestRunSuiteRefusesACasePlanEditedAfterItWasRead(t *testing.T) {
	t.Parallel()
	s := openSuite(t)
	edited := header +
		row("MCA.cfg", "MCA.tla", "invariant", "OnePlacePerTable", "check", "alpha", "required", "-") +
		row("MCABroken.cfg", "MCA.tla", "invariant", "OnePlacePerTable", "ignore-terminal", "alpha", "required", "-") +
		row("MCATerm.cfg", "MCA.tla", "temporal", "TermEnds", "check", "alpha", "required", "-")
	require.NoError(t, os.WriteFile(filepath.Join(s.root, "tla", CasesFile), []byte(edited), 0o644))
	ran := 0
	s.opt.Exec = func(context.Context, Run, string) int { ran++; return 0 }
	res, err := RunSuite(s.opt)
	want := "CASES.tsv changed after the cases were read (MCA.cfg is not as it was)"
	refusedRun(t, res, err, ran, s.out, want, "records were written for a plan that was not the one read")
	_, err = os.Stat(filepath.Join(s.out, workDir))
	require.Error(t, err, "the models were copied for a suite that was refused")
}

func TestRunSuiteRunsCasesTheEditedPlanStillHolds(t *testing.T) {
	t.Parallel()
	root, _ := suiteTree(t)
	planWith := func(term string) string {
		return header +
			row("MCA.cfg", "MCA.tla", "pass", "-", "check", "alpha", "required", "-") +
			row("MCABroken.cfg", "MCA.tla", "invariant", "OnePlacePerTable", "ignore-terminal", "alpha", "required", "-") +
			row("MCATerm.cfg", "MCA.tla", "temporal", term, "check", "beta", "required", "-")
	}
	write := func(text string) {
		require.NoError(t, os.WriteFile(filepath.Join(root, "tla", CasesFile), []byte(text), 0o644))
	}
	write(planWith("TermEnds"))
	all, err := LoadCases(root)
	require.NoError(t, err)
	selected, err := Select(all, "alpha", 1, 0)
	require.NoError(t, err, "selected %v, %v", selected, err)
	require.Len(t, selected, 2, "selected %v, %v", selected, err)
	write(planWith("Another"))
	var seen []Run
	o := suiteOptions(root, selected, filepath.Join(t.TempDir(), "o"), script(t, &seen), testClock(time.Millisecond))
	o.Selection = Selection{Group: "alpha", Shards: 1}
	res, err := RunSuite(o)
	require.NoError(t, err, "suite = %+v, ran %d, %v", res, len(seen), err)
	require.False(t, res.Failed, "suite = %+v, ran %d, %v", res, len(seen), err)
	require.Len(t, res.Records, 2, "suite = %+v, ran %d, %v", res, len(seen), err)
	require.Len(t, seen, 2, "suite = %+v, ran %d, %v", res, len(seen), err)
}

func TestRunSuiteNeverExecutesTheFieldsItWasHanded(t *testing.T) {
	t.Parallel()
	alphaTree := func(t *testing.T) (string, []Case) {
		t.Helper()
		root, _ := suiteTree(t)
		planPath := filepath.Join(root, "tla", CasesFile)
		raw, err := os.ReadFile(planPath)
		require.NoError(t, err)
		text := strings.Replace(string(raw), "MCATerm.cfg\tMCA.tla\ttemporal\tTermEnds\tcheck\talpha", "MCATerm.cfg\tMCA.tla\ttemporal\tTermEnds\tcheck\tbeta", 1)
		require.NoError(t, os.WriteFile(planPath, []byte(text), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(root, "tla", "MCB.tla"), []byte("other\n"), 0o644))
		all, err := LoadCases(root)
		require.NoError(t, err)
		alpha, err := Select(all, "alpha", 1, 0)
		require.NoError(t, err, "alpha = %v, %v", alpha, err)
		require.Len(t, alpha, 2, "alpha = %v, %v", alpha, err)
		return root, alpha
	}
	runIn := func(t *testing.T, root string, alpha []Case) (Result, []Run, error) {
		t.Helper()
		var seen []Run
		o := suiteOptions(root, alpha, filepath.Join(t.TempDir(), "o"), script(t, &seen), testClock(time.Millisecond))
		o.Selection = Selection{Group: "alpha", Shards: 1}
		res, err := RunSuite(o)
		return res, seen, err
	}
	root, alpha := alphaTree(t)
	res, seen, err := runIn(t, root, alpha)
	require.NoError(t, err, "the unedited plan: %+v, ran %d, %v", res, len(seen), err)
	require.False(t, res.Failed, "the unedited plan: %+v, ran %d, %v", res, len(seen), err)
	require.Len(t, seen, 2, "the unedited plan: %+v, ran %d, %v", res, len(seen), err)
	for _, tc := range []struct {
		name string
		edit func(string) string
	}{
		{"the command-line field (deadlock policy)", func(p string) string {
			return strings.Replace(p, "ignore-terminal", "check", 1)
		}},
		{"the configuration the result is held to (property)", func(p string) string {
			return strings.Replace(p, "OnePlacePerTable", "SomethingElse", 1)
		}},
		{"the module the case instantiates", func(p string) string {
			return strings.Replace(p, "MCABroken.cfg\tMCA.tla", "MCABroken.cfg\tMCB.tla", 1)
		}},
		{"a case dropped from the selection's group", func(p string) string {
			return strings.Replace(p, "MCABroken.cfg\tMCA.tla\tinvariant\tOnePlacePerTable\tignore-terminal\talpha", "MCABroken.cfg\tMCA.tla\tinvariant\tOnePlacePerTable\tignore-terminal\tbeta", 1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root, alpha := alphaTree(t)
			planPath := filepath.Join(root, "tla", CasesFile)
			raw, err := os.ReadFile(planPath)
			require.NoError(t, err)
			edited := tc.edit(string(raw))
			require.NotEqual(t, string(raw), edited, "%s: the edit changed nothing", tc.name)
			require.NoError(t, os.WriteFile(planPath, []byte(edited), 0o644))
			res, seen, err := runIn(t, root, alpha)
			msg := "%s: suite = %+v, ran %d cases, %v"
			assert.NoError(t, err, msg, tc.name, res, len(seen), err)
			assert.True(t, res.Failed, msg, tc.name, res, len(seen), err)
			assert.True(t, strings.HasPrefix(res.Refused, "CASES.tsv changed after the cases were read"), msg, tc.name, res, len(seen), err)
			assert.Empty(t, seen, msg, tc.name, res, len(seen), err)
			assert.Empty(t, res.Records, msg, tc.name, res, len(seen), err)
		})
	}
}

func TestRunSuiteRunsTheDigestedPlansCases(t *testing.T) {
	t.Parallel()
	s := openSuite(t)
	res, err := RunSuite(s.opt)
	require.NoError(t, err, "suite = %+v, ran %d, %v", res, len(s.seen), err)
	require.False(t, res.Failed, "suite = %+v, ran %d, %v", res, len(s.seen), err)
	require.Equal(t, len(s.cases), len(s.seen), "suite = %+v, ran %d, %v", res, len(s.seen), err)
	plan, err := LoadCases(s.root)
	require.NoError(t, err)
	for i, r := range s.seen {
		c := plan[i]
		msg := "case %d ran as %+v / %+v, the plan says %+v"
		assert.Equal(t, c.Config, r.Config, msg, i, r, res.Records[i], c)
		assert.Equal(t, c.Module, r.Module, msg, i, r, res.Records[i], c)
		assert.Equal(t, c.Deadlock == "ignore-terminal", r.NoDeadlock, msg, i, r, res.Records[i], c)
		assert.Equal(t, c.Expected, res.Records[i].Expected, msg, i, r, res.Records[i], c)
		assert.Equal(t, c.Property, res.Records[i].Property, msg, i, r, res.Records[i], c)
	}
}

func TestRunSuiteIgnoresAnEditToAModelNoChosenCaseReads(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		edited  string
		refused bool
	}{{"MCB.tla", false}, {"MCB.cfg", false}, {"Unread.tla", false}, {"Shared.tla", true}, {"MCA.tla", true}, {"MCA.cfg", true}} {
		t.Run(tc.edited, func(t *testing.T) {
			t.Parallel()
			root := tree(t, map[string]string{
				"CASES.tsv": header +
					row("MCA.cfg", "MCA.tla", "pass", "-", "check", "alpha", "required", "-") +
					row("MCB.cfg", "MCB.tla", "pass", "-", "check", "beta", "required", "-"),
				"MCA.tla": "EXTENDS Shared\n", "MCB.tla": "model\n", "Shared.tla": "shared\n", "Unread.tla": "unread\n",
				"MCA.cfg": "c\n", "MCB.cfg": "c\n",
			})
			cases, err := LoadCases(root)
			require.NoError(t, err)
			chosen, err := Select(cases, "alpha", 1, 0)
			require.NoError(t, err)
			exec := func(_ context.Context, r Run, log string) int {
				_ = os.WriteFile(filepath.Join(root, "tla", tc.edited), []byte("edited while running\n"), 0o644)
				_ = os.WriteFile(log, []byte(fixture(t, "pass.log")), 0o644)
				return 0
			}
			o := suiteOptions(root, chosen, filepath.Join(t.TempDir(), "o"), exec, testClock(time.Millisecond))
			o.Selection = Selection{Group: "alpha"}
			res, err := RunSuite(o)
			require.NoError(t, err)
			require.Equal(t, tc.refused, res.Refused == "model inputs changed during execution", "suite = %+v", res)
			require.Equal(t, tc.refused, res.Failed, "suite = %+v", res)
		})
	}
}

func TestRunSuiteRecordsTheWorkersAndTheJavaVersion(t *testing.T) {
	t.Parallel()
	s := openSuite(t)
	res, err := RunSuite(s.opt)
	require.NoError(t, err, "%+v, %v", res, err)
	require.Len(t, res.Records, 3, "%+v, %v", res, err)
	for i, want := range []int{2, 1, 1} {
		r := res.Records[i]
		msg := "%s: workers %d (ran with %d), java %q; want %d workers"
		assert.Equal(t, want, r.Workers, msg, r.Config, r.Workers, s.seen[i].Workers, r.JavaVersion, want)
		assert.Equal(t, "21.0.12.1", r.JavaVersion, msg, r.Config, r.Workers, s.seen[i].Workers, r.JavaVersion, want)
		assert.Equal(t, want, s.seen[i].Workers, msg, r.Config, r.Workers, s.seen[i].Workers, r.JavaVersion, want)
	}
	missing := openSuite(t)
	missing.opt.JavaVer = ""
	_, err = RunSuite(missing.opt)
	require.ErrorContains(t, err, "no java version", "a suite with no java version")
	for _, tc := range []struct {
		name   string
		mutate func(*Options)
	}{
		{"a machine name", func(o *Options) { o.Platform = "build-host-7.example" }},
		{"no platform", func(o *Options) { o.Platform = "" }},
		{"no CPU count", func(o *Options) { o.CPUs = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bad := openSuite(t)
			tc.mutate(&bad.opt)
			_, err := RunSuite(bad.opt)
			assert.ErrorContains(t, err, "no platform label and CPU count", "%s: %v", tc.name, err)
		})
	}
	assert.Equal(t, "linux-amd64", res.Records[0].Host, "record host %q, cpus %d", res.Records[0].Host, res.Records[0].CPUs)
	assert.Equal(t, 8, res.Records[0].CPUs, "record host %q, cpus %d", res.Records[0].Host, res.Records[0].CPUs)
}
