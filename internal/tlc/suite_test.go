package tlc

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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
	if err != nil {
		t.Fatal(err)
	}
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
		Root: root, Cases: cases, Out: out, Budget: 110 * time.Second, Workers: 2, Host: "bench",
		Jar:  Jar{Path: "/j/tla2tools.jar", Source: "flag", SHA256: strings.Repeat("b", 64)},
		Java: "/usr/bin/java", Clock: clock.Now, Exec: exec,
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
	if !reflect.DeepEqual(reported, res.Records) || len(res.Records) != 3 {
		t.Fatalf("reported %d records, kept %d", len(reported), len(res.Records))
	}
	fingerprint, _ := Fingerprint(root)
	first := res.Records[0]
	want := Record{Config: "MCA.cfg", Module: "MCA.tla", InputSHA256: fingerprint, JarSHA256: strings.Repeat("b", 64), Host: "bench",
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
		if !reflect.DeepEqual(r.JVM, []string{"-XX:+UseParallelGC", "-XX:ActiveProcessorCount=2", "-Xmx2g"}) {
			t.Errorf("%s: JVM = %v", r.Config, r.JVM)
		}
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
	if m, _ := filepath.Glob(filepath.Join(root, "tla", "*TTrace*")); len(m) != 0 {
		t.Errorf("the checkout gained %v", m)
	}
	if m, _ := filepath.Glob(filepath.Join(res.Work, "*TTrace*")); len(m) == 0 {
		t.Error("no error trace was kept with the private copy")
	}
	if _, err := os.Stat(filepath.Join(res.Work, "CASES.tsv")); err == nil {
		t.Error("the plan was copied into the working copy")
	}

	recs, err := ReadRecordsFile(filepath.Join(out, RunsFile))
	if err != nil || !reflect.DeepEqual(recs, res.Records) {
		t.Fatalf("RUNS.tsv = %v, %v", recs, err)
	}
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
	if err != nil || !res.Failed {
		t.Fatalf("suite = %+v, %v", res, err)
	}
	if got := res.Records[1]; got.Result != "FAIL" || got.Exit != 0 {
		t.Fatalf("record = %+v", got)
	}
	if res.Records[0].Result != "PASS" || res.Records[2].Result != "PASS" {
		t.Fatal("a failing case failed its neighbours")
	}
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
	if err != nil {
		t.Fatal(err)
	}
	if !res.Failed || res.Refused != "model inputs changed during execution" {
		t.Fatalf("suite = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(out, RunsFile)); err == nil {
		t.Fatal("records were written for inputs that changed under the run")
	}
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
	if err != nil {
		t.Fatal(err)
	}
	if !res.Failed || len(res.Records) >= len(cases) || len(seen) != len(res.Records) {
		t.Fatalf("records=%d ran=%d failed=%v", len(res.Records), len(seen), res.Failed)
	}
	recs, err := ReadRecordsFile(filepath.Join(out, RunsFile))
	if err != nil || len(recs) != len(res.Records) {
		t.Fatalf("the records of the cases that ran were not kept: %v, %v", recs, err)
	}
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
	if raw, _ := os.ReadFile(filepath.Join(o.Out, "MCA.cfg.log")); !strings.Contains(string(raw), "before starting") {
		t.Fatalf("log = %q", raw)
	}
}

func TestRunSuiteRecordsAManualRun(t *testing.T) {
	t.Parallel()
	root, cases := suiteTree(t)
	var seen []Run
	clock := &fakeClock{now: time.Now(), step: time.Millisecond}
	o := suiteOptions(root, cases[:1], filepath.Join(t.TempDir(), "o"), script(t, &seen), clock)
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
	if err != nil {
		t.Fatal(err)
	}
	if !res.Failed || res.Refused != "model inputs changed while the models were copied" || ran != 0 || len(res.Records) != 0 {
		t.Fatalf("suite = %+v, ran %d cases", res, ran)
	}
	if _, err := os.Stat(filepath.Join(out, RunsFile)); err == nil {
		t.Fatal("records were written for models that were not the digest's")
	}
}

func TestACopyOfTheModelsHasTheFingerprintOfItsSource(t *testing.T) {
	t.Parallel()
	root, _ := suiteTree(t)
	work := filepath.Join(t.TempDir(), "work")
	if err := CopyModels(filepath.Join(root, "tla"), work); err != nil {
		t.Fatal(err)
	}
	want, _ := Fingerprint(root)
	got, err := fingerprint(root, work)
	if err != nil || got != want {
		t.Fatalf("copy fingerprint %s (%v), source %s", got, err, want)
	}
	if err := os.WriteFile(filepath.Join(work, "MCA.tla"), []byte("other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := fingerprint(root, work); got == want {
		t.Fatal("an edited copy has its source's fingerprint")
	}
}
