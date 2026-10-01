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

// suiteTree is a checkout with three cases: one that passes, one whose
// invariant is violated on purpose and one temporal counterexample.
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

// script answers each case with a recorded TLC output and its exit status.
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
		// TLC writes an error trace beside the spec it was given.
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

func TestRunSuiteRecordsEachCaseAndKeepsTheCheckoutClean(t *testing.T) {
	t.Parallel()
	root, cases := suiteTree(t)
	out := filepath.Join(t.TempDir(), "out")
	var seen []Run
	var reported []Record
	clock := &fakeClock{now: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC), step: 250 * time.Millisecond}
	o := suiteOptions(root, cases, out, script(t, &seen), clock)
	o.OnCase = func(r Record) { reported = append(reported, r) }
	res, err := RunSuite(o)
	if err != nil || res.Failed || res.Refused != "" {
		t.Fatalf("suite = %+v, %v", res, err)
	}
	require.Equal(t, res.Records, reported, "reported %d records, kept %d", len(reported), len(res.Records))
	require.Len(t, res.Records, 3, "reported %d records, kept %d", len(reported), len(res.Records))
	src, err := SourceAt(root)
	require.NoError(t, err)
	fp, files, err := src.Fingerprint("MCA.cfg")
	require.NoError(t, err)
	first := res.Records[0]
	want := Record{Config: "MCA.cfg", Module: "MCA.tla", InputSHA256: fp, InputFiles: files, JarSHA256: strings.Repeat("b", 64), JavaVersion: "21.0.12.1", Workers: 2, Host: "linux-amd64", CPUs: 8,
		StartedUTC: "2026-09-28T12:00:00.250000+00:00", Generated: "15518", Distinct: "263", Seconds: "0.250",
		Exit: 0, Result: "PASS", Expected: "pass", Property: "-", Budget: "110", Mode: "bounded"}
	if first != want {
		t.Fatalf("first record\n got %+v\nwant %+v", first, want)
	}
	if res.Records[1].Exit != 12 || res.Records[2].Exit != 13 || res.Records[1].Result != "PASS" || res.Records[2].Result != "PASS" {
		t.Fatalf("records = %+v", res.Records)
	}

	// The commands: two workers only for a case expected to pass, the
	// terminal-stutter models with -deadlock, final liveness checking, and a
	// private copy of the models as the working directory.
	wantFlags := []struct {
		workers    int
		noDeadlock bool
	}{{2, false}, {1, true}, {1, false}}
	for i, r := range seen {
		if r.Workers != wantFlags[i].workers || r.NoDeadlock != wantFlags[i].noDeadlock || !r.LnCheckFinal {
			t.Errorf("%s: workers=%d noDeadlock=%v lncheck=%v", r.Config, r.Workers, r.NoDeadlock, r.LnCheckFinal)
		}
		assert.Equal(t, []string{"-XX:+UseParallelGC", "-XX:ActiveProcessorCount=2", "-Xmx2g"}, r.JVM, "%s: JVM = %v", r.Config, r.JVM)
		if r.Dir != res.Work || r.Jar != "/j/tla2tools.jar" || r.Java != "/usr/bin/java" {
			t.Errorf("%s: dir=%s jar=%s java=%s", r.Config, r.Dir, r.Jar, r.Java)
		}
		if !strings.HasPrefix(r.TmpDir, out) || !strings.HasPrefix(r.MetaDir, r.TmpDir) {
			t.Errorf("%s: tmp=%s meta=%s are not private under the output", r.Config, r.TmpDir, r.MetaDir)
		}
		if _, err := os.Stat(r.TmpDir); err == nil {
			t.Errorf("%s: the temporary directory was left behind", r.Config)
		}
	}

	// TLC's error traces landed in the private copy, never in tla/.
	m, _ := filepath.Glob(filepath.Join(root, "tla", "*TTrace*"))
	assert.Empty(t, m, "the checkout gained %v", m)
	m, _ = filepath.Glob(filepath.Join(res.Work, "*TTrace*"))
	assert.NotEmpty(t, m, "no error trace was kept with the private copy")
	_, err = os.Stat(filepath.Join(res.Work, "CASES.tsv"))
	assert.Error(t, err, "the plan was copied into the working copy")

	recs, err := ReadRecordsFile(filepath.Join(out, RunsFile))
	require.NoError(t, err, "RUNS.tsv = %v, %v", recs, err)
	require.Equal(t, res.Records, recs, "RUNS.tsv = %v, %v", recs, err)
	if raw, err := os.ReadFile(filepath.Join(out, "MCA.cfg.log")); err != nil || string(raw) != fixture(t, "pass.log") {
		t.Fatalf("the case's log was not kept: %v", err)
	}
}

func TestRunSuiteFailsACaseThatIsNotWhatItDeclares(t *testing.T) {
	t.Parallel()
	root, cases := suiteTree(t)
	var seen []Run
	exec := script(t, &seen)
	wrong := func(ctx context.Context, r Run, log string) int {
		code := exec(ctx, r, log)
		if r.Config == "MCABroken.cfg" {
			return 0 // the counterexample was not found: the model passed
		}
		return code
	}
	clock := &fakeClock{now: time.Now(), step: time.Millisecond}
	res, err := RunSuite(suiteOptions(root, cases, filepath.Join(t.TempDir(), "o"), wrong, clock))
	require.NoError(t, err, "suite = %+v, %v", res, err)
	require.True(t, res.Failed, "suite = %+v, %v", res, err)
	if got := res.Records[1]; got.Result != "FAIL" || got.Exit != 0 {
		t.Fatalf("record = %+v", got)
	}
	require.Equal(t, "PASS", res.Records[0].Result, "a failing case failed its neighbours")
	require.Equal(t, "PASS", res.Records[2].Result, "a failing case failed its neighbours")
}

func TestRunSuiteWritesNothingWhenItsInputsMoveUnderIt(t *testing.T) {
	t.Parallel()
	root, cases := suiteTree(t)
	out := filepath.Join(t.TempDir(), "o")
	var seen []Run
	exec := script(t, &seen)
	meddle := func(ctx context.Context, r Run, log string) int {
		if r.Config == "MCABroken.cfg" {
			_ = os.WriteFile(filepath.Join(root, "tla", "MCA.tla"), []byte("edited while running\n"), 0o644)
		}
		return exec(ctx, r, log)
	}
	clock := &fakeClock{now: time.Now(), step: time.Millisecond}
	res, err := RunSuite(suiteOptions(root, cases, out, meddle, clock))
	require.NoError(t, err)
	require.True(t, res.Failed, "suite = %+v", res)
	require.Equal(t, "model inputs changed during execution", res.Refused, "suite = %+v", res)
	_, err = os.Stat(filepath.Join(out, RunsFile))
	require.Error(t, err, "records were written for inputs that changed under the run")
}

func TestRunSuiteEndsWhenTheBudgetDoes(t *testing.T) {
	t.Parallel()
	root, cases := suiteTree(t)
	out := filepath.Join(t.TempDir(), "o")
	var seen []Run
	// Each reading of the clock is a minute later: the first case starts
	// inside the budget and ends outside it.
	clock := &fakeClock{now: time.Now(), step: time.Minute}
	o := suiteOptions(root, cases, out, script(t, &seen), clock)
	o.Budget = 90 * time.Second
	res, err := RunSuite(o)
	require.NoError(t, err)
	if !res.Failed || len(res.Records) >= len(cases) || len(seen) != len(res.Records) {
		t.Fatalf("records=%d ran=%d failed=%v", len(res.Records), len(seen), res.Failed)
	}
	recs, err := ReadRecordsFile(filepath.Join(out, RunsFile))
	require.NoError(t, err, "the records of the cases that ran were not kept: %v, %v", recs, err)
	require.Len(t, recs, len(res.Records), "the records of the cases that ran were not kept: %v, %v", recs, err)
}

func TestRunSuiteNeverStartsACaseAfterTheBudget(t *testing.T) {
	t.Parallel()
	root, cases := suiteTree(t)
	var seen []Run
	clock := &fakeClock{now: time.Now(), step: time.Hour}
	o := suiteOptions(root, cases, filepath.Join(t.TempDir(), "o"), script(t, &seen), clock)
	o.Budget = time.Second
	res, err := RunSuite(o)
	if err != nil || len(seen) != 0 || !res.Failed {
		t.Fatalf("ran %d cases, failed=%v, err=%v", len(seen), res.Failed, err)
	}
	if got := res.Records[0]; got.Exit != ExitTimeout || got.Result != "FAIL" || got.Generated != "-" {
		t.Fatalf("record = %+v", got)
	}
	raw, _ := os.ReadFile(filepath.Join(o.Out, "MCA.cfg.log"))
	require.Contains(t, string(raw), "before starting", "log = %q", raw)
}

func TestRunSuiteRecordsAManualRun(t *testing.T) {
	t.Parallel()
	root, cases := suiteTree(t)
	var seen []Run
	clock := &fakeClock{now: time.Now(), step: time.Millisecond}
	o := suiteOptions(root, cases[:1], filepath.Join(t.TempDir(), "o"), script(t, &seen), clock)
	o.Selection = Selection{Shards: 3, Shard: 0} // the first of three: MCA.cfg
	o.Manual, o.Budget = true, 1500*time.Millisecond
	res, err := RunSuite(o)
	if err != nil || len(res.Records) != 1 || res.Records[0].Mode != "manual" || res.Records[0].Budget != "1.5" {
		t.Fatalf("records = %+v, %v", res.Records, err)
	}
}

func TestRunSuiteRefusesModelsEditedBetweenTheDigestAndTheCopy(t *testing.T) {
	t.Parallel()
	root, cases := suiteTree(t)
	out := filepath.Join(t.TempDir(), "o")
	ran := 0
	clock := &fakeClock{now: time.Now(), step: time.Millisecond}
	exec := func(context.Context, Run, string) int { ran++; return 0 }
	o := suiteOptions(root, cases, out, exec, clock)
	// TLC would check the edited bytes while the records named the old digest.
	o.beforeCopy = func() {
		_ = os.WriteFile(filepath.Join(root, "tla", "MCA.tla"), []byte("edited before the copy\n"), 0o644)
	}
	res, err := RunSuite(o)
	require.NoError(t, err)
	if !res.Failed || res.Refused != "model inputs changed while the models were copied" || ran != 0 || len(res.Records) != 0 {
		t.Fatalf("suite = %+v, ran %d cases", res, ran)
	}
	_, err = os.Stat(filepath.Join(out, RunsFile))
	require.Error(t, err, "records were written for models that were not the digest's")
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
	if got, err := fingerprints(copied, cases); err != nil || !sameFingerprints(got, want) {
		t.Fatalf("copy fingerprints %v (%v), source %v", got, err, want)
	}
	require.NoError(t, os.WriteFile(filepath.Join(work, "MCA.tla"), []byte("other\n"), 0o644))
	got, _ := fingerprints(copied, cases)
	require.False(t, sameFingerprints(got, want), "an edited copy has its source's fingerprints")
}

// The cases are parsed before the digest is taken. A plan edited between the
// two must not run the old cases under the new digest: the suite refuses, runs
// nothing and writes no records.
func TestRunSuiteRefusesACasePlanEditedAfterItWasRead(t *testing.T) {
	t.Parallel()
	root, cases := suiteTree(t)
	edited := header +
		row("MCA.cfg", "MCA.tla", "invariant", "OnePlacePerTable", "check", "alpha", "required", "-") +
		row("MCABroken.cfg", "MCA.tla", "invariant", "OnePlacePerTable", "ignore-terminal", "alpha", "required", "-") +
		row("MCATerm.cfg", "MCA.tla", "temporal", "TermEnds", "check", "alpha", "required", "-")
	require.NoError(t, os.WriteFile(filepath.Join(root, "tla", CasesFile), []byte(edited), 0o644))
	out := filepath.Join(t.TempDir(), "o")
	ran := 0
	clock := &fakeClock{now: time.Now(), step: time.Millisecond}
	res, err := RunSuite(suiteOptions(root, cases, out, func(context.Context, Run, string) int { ran++; return 0 }, clock))
	require.NoError(t, err)
	want := "CASES.tsv changed after the cases were read (MCA.cfg is not as it was)"
	if !res.Failed || res.Refused != want || ran != 0 || len(res.Records) != 0 {
		t.Fatalf("suite = %+v, ran %d cases", res, ran)
	}
	_, err = os.Stat(filepath.Join(out, RunsFile))
	require.Error(t, err, "records were written for a plan that was not the one read")
	_, err = os.Stat(filepath.Join(out, workDir))
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
	// An edit to a case outside the selection is not a change to the cases run.
	write(planWith("Another"))
	var seen []Run
	clock := &fakeClock{now: time.Now(), step: time.Millisecond}
	o := suiteOptions(root, selected, filepath.Join(t.TempDir(), "o"), script(t, &seen), clock)
	o.Selection = Selection{Group: "alpha", Shards: 1}
	res, err := RunSuite(o)
	if err != nil || res.Failed || len(res.Records) != 2 || len(seen) != 2 {
		t.Fatalf("suite = %+v, ran %d, %v", res, len(seen), err)
	}
}

// What runs is the selection of the plan the digest names. A field of a
// selected case that changed between the load and the run (the deadlock policy
// is on the command line, the property is what the result is held to) makes the
// suite refuse; the old fields are never executed.
func TestRunSuiteNeverExecutesTheFieldsItWasHanded(t *testing.T) {
	t.Parallel()
	// alphaTree is the suite tree with MCATerm in its own group, and the alpha
	// group as a caller reads it: MCA and MCABroken.
	alphaTree := func() (string, []Case) {
		root, _ := suiteTree(t)
		planPath := filepath.Join(root, "tla", CasesFile)
		raw, _ := os.ReadFile(planPath)
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
	runIn := func(root string, alpha []Case) (Result, []Run, error) {
		var seen []Run
		clock := &fakeClock{now: time.Now(), step: time.Millisecond}
		o := suiteOptions(root, alpha, filepath.Join(t.TempDir(), "o"), script(t, &seen), clock)
		o.Selection = Selection{Group: "alpha", Shards: 1}
		res, err := RunSuite(o)
		return res, seen, err
	}
	// The control: with no edit the same call runs both cases.
	if res, seen, err := runIn(alphaTree()); err != nil || res.Failed || len(seen) != 2 {
		t.Fatalf("the unedited plan: %+v, ran %d, %v", res, len(seen), err)
	}
	for name, edit := range map[string]func(string) string{
		"the command-line field (deadlock policy)": func(p string) string {
			return strings.Replace(p, "ignore-terminal", "check", 1)
		},
		"the configuration the result is held to (property)": func(p string) string {
			return strings.Replace(p, "OnePlacePerTable", "SomethingElse", 1)
		},
		"the module the case instantiates": func(p string) string {
			return strings.Replace(p, "MCABroken.cfg\tMCA.tla", "MCABroken.cfg\tMCB.tla", 1)
		},
		"a case dropped from the selection's group": func(p string) string {
			return strings.Replace(p, "MCABroken.cfg\tMCA.tla\tinvariant\tOnePlacePerTable\tignore-terminal\talpha", "MCABroken.cfg\tMCA.tla\tinvariant\tOnePlacePerTable\tignore-terminal\tbeta", 1)
		},
	} {
		root, alpha := alphaTree()
		planPath := filepath.Join(root, "tla", CasesFile)
		raw, _ := os.ReadFile(planPath)
		edited := edit(string(raw))
		require.NotEqual(t, string(raw), edited, "%s: the edit changed nothing", name)
		require.NoError(t, os.WriteFile(planPath, []byte(edited), 0o644))
		res, seen, err := runIn(root, alpha)
		if err != nil || !res.Failed || !strings.HasPrefix(res.Refused, "CASES.tsv changed after the cases were read") || len(seen) != 0 || len(res.Records) != 0 {
			t.Errorf("%s: suite = %+v, ran %d cases, %v", name, res, len(seen), err)
		}
	}
}

// The cases that run are the digested plan's own: the fields TLC is started
// with and the fields the result is held to equal the plan's, case by case.
func TestRunSuiteRunsTheDigestedPlansCases(t *testing.T) {
	t.Parallel()
	root, cases := suiteTree(t)
	var seen []Run
	clock := &fakeClock{now: time.Now(), step: time.Millisecond}
	o := suiteOptions(root, cases, filepath.Join(t.TempDir(), "o"), script(t, &seen), clock)
	res, err := RunSuite(o)
	if err != nil || res.Failed || len(seen) != len(cases) {
		t.Fatalf("suite = %+v, ran %d, %v", res, len(seen), err)
	}
	plan, err := LoadCases(root)
	require.NoError(t, err)
	for i, r := range seen {
		c := plan[i]
		if r.Config != c.Config || r.Module != c.Module || r.NoDeadlock != (c.Deadlock == "ignore-terminal") ||
			res.Records[i].Expected != c.Expected || res.Records[i].Property != c.Property {
			t.Errorf("case %d ran as %+v / %+v, the plan says %+v", i, r, res.Records[i], c)
		}
	}
}

// A suite holds itself to the inputs of the cases it runs. A model that only
// another group reads may be edited under it; one of its own may not.
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
			chosen, _ := Select(cases, "alpha", 1, 0)
			exec := func(_ context.Context, r Run, log string) int {
				_ = os.WriteFile(filepath.Join(root, "tla", tc.edited), []byte("edited while running\n"), 0o644)
				_ = os.WriteFile(log, []byte(fixture(t, "pass.log")), 0o644)
				return 0
			}
			clock := &fakeClock{now: time.Now(), step: time.Millisecond}
			o := suiteOptions(root, chosen, filepath.Join(t.TempDir(), "o"), exec, clock)
			o.Selection = Selection{Group: "alpha"}
			res, err := RunSuite(o)
			require.NoError(t, err)
			require.Equal(t, tc.refused, res.Refused == "model inputs changed during execution", "suite = %+v", res)
			require.Equal(t, tc.refused, res.Failed, "suite = %+v", res)
		})
	}
}

// A record names the workers its case ran with (a counterexample case runs with
// one) and the java version the suite was given; a suite with no java version
// refuses to run.
func TestRunSuiteRecordsTheWorkersAndTheJavaVersion(t *testing.T) {
	t.Parallel()
	root, cases := suiteTree(t)
	var seen []Run
	clock := &fakeClock{now: time.Now(), step: time.Millisecond}
	res, err := RunSuite(suiteOptions(root, cases, filepath.Join(t.TempDir(), "o"), script(t, &seen), clock))
	require.NoError(t, err, "%+v, %v", res, err)
	require.Len(t, res.Records, 3, "%+v, %v", res, err)
	for i, want := range []int{2, 1, 1} {
		if r := res.Records[i]; r.Workers != want || r.JavaVersion != "21.0.12.1" || seen[i].Workers != want {
			t.Errorf("%s: workers %d (ran with %d), java %q; want %d workers", r.Config, r.Workers, seen[i].Workers, r.JavaVersion, want)
		}
	}
	o := suiteOptions(root, cases, filepath.Join(t.TempDir(), "o2"), script(t, &seen), clock)
	o.JavaVer = ""
	_, err = RunSuite(o)
	require.ErrorContains(t, err, "no java version", "a suite with no java version")
	for name, mutate := range map[string]func(*Options){
		"a machine name": func(o *Options) { o.Platform = "build-host-7.example" },
		"no platform":    func(o *Options) { o.Platform = "" },
		"no CPU count":   func(o *Options) { o.CPUs = 0 },
	} {
		o := suiteOptions(root, cases, filepath.Join(t.TempDir(), "o3"), script(t, &seen), clock)
		mutate(&o)
		if _, err := RunSuite(o); err == nil || !strings.Contains(err.Error(), "no platform label and CPU count") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if res.Records[0].Host != "linux-amd64" || res.Records[0].CPUs != 8 {
		t.Errorf("record host %q, cpus %d", res.Records[0].Host, res.Records[0].CPUs)
	}
}
