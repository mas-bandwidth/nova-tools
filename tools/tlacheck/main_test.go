package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/tlc"
)

type result struct {
	code           int
	stdout, stderr string
}

// exec answers each case with a recorded TLC output; nothing starts java.
func scriptedTLC(t *testing.T, code map[string]int, runs *[]tlc.Run) tlc.Executor {
	t.Helper()
	logs := map[int]string{}
	for status, name := range map[int]string{0: "pass.log", 12: "invariant.log", 13: "action.log"} {
		raw, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		logs[status] = string(raw)
	}
	return func(ctx context.Context, r tlc.Run, log string) int {
		if runs != nil {
			*runs = append(*runs, r)
		}
		status := code[strings.TrimSuffix(r.Config, ".cfg")]
		if err := os.WriteFile(log, []byte(logs[status]), 0o644); err != nil {
			t.Error(err)
		}
		return status
	}
}

func testEnv(t *testing.T, exec tlc.Executor) (env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var out, errs bytes.Buffer
	return env{
		stdout: &out, stderr: &errs,
		getenv:      func(string) string { return "" },
		lookPath:    func(name string) (string, error) { return "/usr/bin/" + name, nil },
		hostname:    func() (string, error) { return "bench", nil },
		javaVersion: func(string) (string, error) { return "21.0.12.1", nil },
		exec:        exec, goos: "linux",
	}, &out, &errs
}

func do(e env, out, errs *bytes.Buffer, args ...string) result {
	code := run(args, e)
	return result{code, out.String(), errs.String()}
}

const header = "config\tmodule\texpected\tproperty\tdeadlock\tgroup\tgate\tdebt\n"

// checkout writes a tla/ with three cases (a pass, an invariant counterexample
// and an action counterexample) and returns its root and a jar.
func checkout(t *testing.T) (root, jar string) {
	t.Helper()
	root = t.TempDir()
	plan := header +
		"MCA.cfg\tMCA.tla\tpass\t-\tcheck\talpha\trequired\t-\n" +
		"MCABroken.cfg\tMCA.tla\tinvariant\tOnePlacePerTable\tignore-terminal\talpha\trequired\t-\n" +
		"MCAStale.cfg\tMCA.tla\taction\tStaleWritesRefuse\tignore-terminal\tbeta\trequired\t-\n" +
		"MCCard.cfg\tMCA.tla\tpass\t-\tcheck\tgamma\tbench\tslow\n"
	files := map[string]string{"CASES.tsv": plan, "MCA.tla": "model\n", "MCA.cfg": "c\n", "MCABroken.cfg": "c\n", "MCAStale.cfg": "c\n", "MCCard.cfg": "c\n"}
	for name, text := range files {
		p := filepath.Join(root, "tla", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	jar = filepath.Join(t.TempDir(), "tla2tools.jar")
	if err := os.WriteFile(jar, []byte("jar"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, jar
}

var eventRE = regexp.MustCompile(`^[A-Z]+ (OK|FAIL)( [a-z0-9-]+=\S*)*$`)

func TestHelpIsOnStdoutAtExitZeroForTheToolAndEveryVerb(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		e, out, errs := testEnv(t, nil)
		r := do(e, out, errs, args...)
		if r.code != 0 || r.stderr != "" || !strings.Contains(r.stdout, "usage: tlacheck <verb>") {
			t.Errorf("%v: %+v", args, r)
		}
		for _, v := range verbs() {
			if !strings.Contains(r.stdout, v.name) {
				t.Errorf("%v: the banner does not list %s", args, v.name)
			}
		}
	}
	for _, v := range verbs() {
		for _, args := range [][]string{{v.name, "-h"}, {v.name, "--help"}, {"help", v.name}} {
			e, out, errs := testEnv(t, nil)
			r := do(e, out, errs, args...)
			if r.code != 0 || r.stderr != "" || r.stdout != v.help || !strings.HasPrefix(r.stdout, "tlacheck "+v.name+":") {
				t.Errorf("%v: code=%d stderr=%q", args, r.code, r.stderr)
			}
			if !strings.Contains(r.stdout, "first run:") || !(strings.Contains(r.stdout, "output:") || strings.Contains(r.stdout, "Payload:")) {
				t.Errorf("%v: the help has no output line or first run", args)
			}
		}
	}
}

func TestHelpHasNoSideEffects(t *testing.T) {
	t.Parallel()
	e, out, errs := testEnv(t, func(context.Context, tlc.Run, string) int { t.Error("TLC ran for help"); return 0 })
	e.lookPath = func(string) (string, error) { t.Error("a program was looked up for help"); return "", errors.New("no") }
	e.getenv = func(string) string { t.Error("the environment was read for help"); return "" }
	for _, v := range verbs() {
		do(e, out, errs, v.name, "-h")
	}
}

func TestAnUnknownVerbIsNamedAndTheVerbsAreListed(t *testing.T) {
	t.Parallel()
	e, out, errs := testEnv(t, nil)
	r := do(e, out, errs, "bogus")
	if r.code != 2 || r.stdout != "" || strings.Count(r.stderr, "\n") != 1 {
		t.Fatalf("%+v", r)
	}
	for _, want := range []string{`unknown verb "bogus"`, "run, groups, merge, inputs, table, member, replay, witnesses", "run: tlacheck help"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("stderr %q lacks %q", r.stderr, want)
		}
	}
	e, out, errs = testEnv(t, nil)
	if r := do(e, out, errs, "help", "bogus"); r.code != 2 || !strings.Contains(r.stderr, `unknown verb "bogus"`) {
		t.Fatalf("help of an unknown verb: %+v", r)
	}
	e, out, errs = testEnv(t, nil)
	if r := do(e, out, errs); r.code != 2 || !strings.Contains(r.stderr, "usage: tlacheck") || r.stdout != "" {
		t.Fatalf("no arguments: %+v", r)
	}
}

func TestAMalformedInvocationIsRefusedInOneLine(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"an unknown flag", []string{"groups", "--nope"}, []string{"unknown flag -nope", "run: tlacheck groups -h"}},
		{"a late flag", []string{"witnesses", "table.lua", "--timeout", "1s"}, []string{`flag "--timeout" comes after a positional`, "run: tlacheck witnesses -h"}},
		{"a bad value", []string{"run", "--timeout", "soon"}, []string{"run: tlacheck run -h"}},
		{"every missing flag at once", []string{"replay", "--jar", jar}, []string{"missing required --source, --dir"}},
		{"a missing flag", []string{"run", "--root", root, "--jar", jar}, []string{"missing required --dir", "run: tlacheck run -h"}},
		{"two positionals for witnesses", []string{"witnesses", "a.lua", "b.lua"}, []string{"want exactly one table.lua, got 2"}},
		{"no records to merge", []string{"merge", "--out", "x.tsv", "--root", root}, []string{"no records files named"}},
	}
	for _, tc := range tests {
		e, out, errs := testEnv(t, nil)
		r := do(e, out, errs, tc.args...)
		if r.code != 2 || strings.Count(r.stderr, "\n") != 1 || strings.Contains(r.stdout, "OK") {
			t.Errorf("%s: %+v", tc.name, r)
		}
		for _, want := range tc.want {
			if !strings.Contains(r.stderr, want) {
				t.Errorf("%s: stderr %q lacks %q", tc.name, r.stderr, want)
			}
		}
		if !strings.HasPrefix(r.stderr, "tlacheck "+tc.args[0]+": ") {
			t.Errorf("%s: the refusal does not start with the tool and verb: %q", tc.name, r.stderr)
		}
	}
}

func TestALiteralDoubleDashEndsTheFlags(t *testing.T) {
	t.Parallel()
	e, out, errs := testEnv(t, nil)
	// After "--" a leading dash is a file name, not a flag; the verb then
	// refuses the file for what it is.
	r := do(e, out, errs, "witnesses", "--", "-table.lua")
	if strings.Contains(r.stderr, "comes after a positional") {
		t.Fatalf("a literal after -- was refused as a late flag: %+v", r)
	}
}

func TestGroupsPrintsTheRequiredGroupsAsOneLineOfJSON(t *testing.T) {
	t.Parallel()
	root, _ := checkout(t)
	e, out, errs := testEnv(t, nil)
	r := do(e, out, errs, "groups", "--root", root)
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("%+v", r)
	}
	var groups []string
	if err := json.Unmarshal([]byte(r.stdout), &groups); err != nil || !reflect.DeepEqual(groups, []string{"alpha", "beta"}) || strings.Count(r.stdout, "\n") != 1 {
		t.Fatalf("stdout = %q (%v)", r.stdout, err)
	}
	// The repository's own plan.
	e, out, errs = testEnv(t, nil)
	r = do(e, out, errs, "groups", "--root", filepath.Join("..", ".."))
	if r.code != 0 || json.Unmarshal([]byte(r.stdout), &groups) != nil || len(groups) < 5 {
		t.Fatalf("the repository's groups: %+v", r)
	}
	// A root with no plan is a refusal and prints no payload.
	e, out, errs = testEnv(t, nil)
	r = do(e, out, errs, "groups", "--root", t.TempDir())
	if r.code != 2 || r.stdout != "" || !strings.Contains(r.stderr, "the case plan is refused") {
		t.Fatalf("a root with no plan: %+v", r)
	}
}

func TestRunWritesRecordsAndReportsEachCase(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	var runs []tlc.Run
	e, out, errs := testEnv(t, scriptedTLC(t, map[string]int{"MCA": 0, "MCABroken": 12, "MCAStale": 13}, &runs))
	dir := filepath.Join(t.TempDir(), "out")
	r := do(e, out, errs, "run", "--root", root, "--jar", jar, "--dir", dir, "--group", "alpha", "--timeout", "100s", "--workers", "1")
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("%+v", r)
	}
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	want := []string{"HELPER OK name=jar", "HELPER OK name=java path=/usr/bin/java", "HELPER OK name=java-version version=21.0.12.1", "CASE OK config=MCA.cfg result=PASS", "CASE OK config=MCABroken.cfg result=PASS", "RUN OK cases=2 records="}
	if len(lines) != len(want) {
		t.Fatalf("lines = %q", lines)
	}
	for i, w := range want {
		if !strings.HasPrefix(lines[i], w) {
			t.Errorf("line %d = %q, want prefix %q", i, lines[i], w)
		}
		if !eventRE.MatchString(lines[i]) {
			t.Errorf("line %d is not an event: %q", i, lines[i])
		}
	}
	if !strings.Contains(lines[0], "source=flag") || !strings.Contains(lines[3], "exit=0 generated=15518 distinct=263") {
		t.Errorf("fields: %q / %q", lines[0], lines[2])
	}
	recs, err := tlc.ReadRecordsFile(filepath.Join(dir, tlc.RunsFile))
	if err != nil || len(recs) != 2 || recs[0].Host != "bench" || recs[0].Budget != "100" || recs[1].Exit != 12 {
		t.Fatalf("records = %+v, %v", recs, err)
	}
	if len(runs) != 2 || runs[0].Workers != 1 {
		t.Fatalf("runs = %+v", runs)
	}
}

func TestRunRecordsTheJavaVersionAndWorkersAndRefusesWhenJavaHasNone(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	e, out, errs := testEnv(t, scriptedTLC(t, map[string]int{"MCA": 0, "MCABroken": 12}, nil))
	dir := filepath.Join(t.TempDir(), "run")
	if r := do(e, out, errs, "run", "--root", root, "--jar", jar, "--dir", dir, "--group", "alpha", "--workers", "1"); r.code != 0 || !strings.Contains(r.stdout, "HELPER OK name=java-version version=21.0.12.1") {
		t.Fatalf("%+v", r)
	}
	recs, err := tlc.ReadRecordsFile(filepath.Join(dir, tlc.RunsFile))
	if err != nil || len(recs) != 2 || recs[0].JavaVersion != "21.0.12.1" || recs[0].Workers != 1 || recs[1].Workers != 1 {
		t.Fatalf("records %+v, %v", recs, err)
	}
	e, out, errs = testEnv(t, func(context.Context, tlc.Run, string) int { t.Error("TLC ran without a java version"); return 0 })
	e.javaVersion = func(string) (string, error) { return "", errors.New("java -version printed no quoted version") }
	dir2 := filepath.Join(t.TempDir(), "run2")
	if r := do(e, out, errs, "run", "--root", root, "--jar", jar, "--dir", dir2, "--group", "alpha"); r.code != 2 || !strings.Contains(r.stderr, "the java version cannot be read") || !strings.Contains(r.stderr, "nothing was run") {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir2, tlc.RunsFile)); err == nil {
		t.Fatal("records written for a java with no version")
	}
}

func TestRunReportsAFailingCaseOnStderrAndExitsOne(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	// The counterexample case's model passes: not what it declares.
	e, out, errs := testEnv(t, scriptedTLC(t, map[string]int{"MCA": 0, "MCABroken": 0}, nil))
	r := do(e, out, errs, "run", "--root", root, "--jar", jar, "--dir", filepath.Join(t.TempDir(), "o"), "--group", "alpha")
	if r.code != 1 {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(r.stdout, "CASE OK config=MCA.cfg") || strings.Contains(r.stdout, "MCABroken") || strings.Contains(r.stdout, "RUN OK") {
		t.Errorf("stdout = %q", r.stdout)
	}
	if !strings.Contains(r.stderr, "CASE FAIL config=MCABroken.cfg result=FAIL") || !strings.Contains(r.stderr, "RUN FAIL cases=2") {
		t.Errorf("stderr = %q", r.stderr)
	}
	for _, l := range strings.Split(strings.TrimSpace(r.stderr), "\n") {
		if !eventRE.MatchString(strings.SplitN(l, ": ", 2)[0]) {
			t.Errorf("not an event: %q", l)
		}
	}
}

func TestRunRefusesWhatItCannotRunAndRunsNothing(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	dir := func() string { return filepath.Join(t.TempDir(), "o") }
	ci := func(k string) string {
		if k == "NOVA_CI" {
			return "1"
		}
		return ""
	}
	tests := []struct {
		name  string
		args  []string
		tweak func(*env)
		want  string
	}{
		{"an unknown group", []string{"--group", "nope"}, nil, "unknown group: nope"},
		{"a group with shards", []string{"--group", "alpha", "--shards", "2"}, nil, "--group and shard selection cannot be combined"},
		{"a timeout over the cap", []string{"--timeout", "111s"}, nil, "at most 1m50s"},
		{"a zero timeout", []string{"--timeout", "0s"}, nil, "must be positive"},
		{"three workers", []string{"--workers", "3"}, nil, "workers must be 1 or 2"},
		{"a manual run in CI", []string{"--manual", "--group", "alpha"}, func(e *env) { e.getenv = ci }, "--manual is forbidden in CI"},
		{"a platform that is not Linux", []string{"--group", "alpha"}, func(e *env) { e.goos = "darwin" }, "TLC runs on a Linux bench, and this is darwin"},
		{"no java", []string{"--group", "alpha"}, func(e *env) { e.lookPath = func(string) (string, error) { return "", errors.New("no") } }, "java is not on PATH"},
	}
	for _, tc := range tests {
		e, out, errs := testEnv(t, func(context.Context, tlc.Run, string) int { t.Errorf("%s: TLC ran", tc.name); return 0 })
		if tc.tweak != nil {
			tc.tweak(&e)
		}
		args := append([]string{"run", "--root", root, "--jar", jar, "--dir", dir()}, tc.args...)
		r := do(e, out, errs, args...)
		if r.code != 2 || !strings.Contains(r.stderr, tc.want) || strings.Count(r.stderr, "\n") != 1 || strings.Contains(r.stdout, "CASE") {
			t.Errorf("%s: %+v", tc.name, r)
		}
	}
	// No jar: neither the flag nor TLC_JAR.
	e, out, errs := testEnv(t, nil)
	r := do(e, out, errs, "run", "--root", root, "--dir", dir(), "--group", "alpha")
	if r.code != 2 || !strings.Contains(r.stderr, "neither --jar nor TLC_JAR") || !strings.Contains(r.stderr, "run: tlacheck run --jar /path/to/tla2tools.jar") {
		t.Errorf("no jar: %+v", r)
	}
	// The environment names the jar.
	e, out, errs = testEnv(t, scriptedTLC(t, map[string]int{"MCA": 0, "MCABroken": 12}, nil))
	e.getenv = func(k string) string {
		if k == tlc.JarEnv {
			return jar
		}
		return ""
	}
	r = do(e, out, errs, "run", "--root", root, "--dir", dir(), "--group", "alpha")
	if r.code != 0 || !strings.Contains(r.stdout, "source=env:TLC_JAR") {
		t.Errorf("jar from the environment: %+v", r)
	}
}

func TestRunNeverGrowsTheCheckout(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	exec := func(ctx context.Context, r tlc.Run, log string) int {
		// TLC's error trace lands beside the spec it was given.
		_ = os.WriteFile(filepath.Join(r.Dir, "MCA_TTrace_1.tla"), []byte("trace\n"), 0o644)
		return scriptedTLC(t, map[string]int{"MCA": 0, "MCABroken": 12}, nil)(ctx, r, log)
	}
	e, out, errs := testEnv(t, exec)
	r := do(e, out, errs, "run", "--root", root, "--jar", jar, "--dir", filepath.Join(t.TempDir(), "o"), "--group", "alpha")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if m, _ := filepath.Glob(filepath.Join(root, "tla", "*TTrace*")); len(m) != 0 {
		t.Fatalf("the checkout gained %v", m)
	}
}

func TestMergeJoinsTheRecordsOfEveryGroup(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	var dirs []string
	for _, group := range []string{"alpha", "beta", "gamma"} {
		e, out, errs := testEnv(t, scriptedTLC(t, map[string]int{"MCA": 0, "MCABroken": 12, "MCAStale": 13, "MCCard": 0}, nil))
		dir := filepath.Join(t.TempDir(), group)
		if r := do(e, out, errs, "run", "--root", root, "--jar", jar, "--dir", dir, "--group", group); r.code != 0 {
			t.Fatalf("%s: %+v", group, r)
		}
		dirs = append(dirs, filepath.Join(dir, tlc.RunsFile))
	}
	merged := filepath.Join(t.TempDir(), "RUNS.tsv")
	e, out, errs := testEnv(t, nil)
	r := do(e, out, errs, append([]string{"merge", "--root", root, "--out", merged}, dirs...)...)
	if r.code != 0 || !strings.Contains(r.stdout, "MERGE OK runs=3 records=4 out=") {
		t.Fatalf("%+v", r)
	}
	recs, err := tlc.ReadRecordsFile(merged)
	if err != nil || len(recs) != 4 || recs[0].Config != "MCA.cfg" || recs[3].Config != "MCCard.cfg" {
		t.Fatalf("merged = %+v, %v", recs, err)
	}
	// A run that is missing is a refusal, and the output is left alone.
	before, _ := os.ReadFile(merged)
	e, out, errs = testEnv(t, nil)
	r = do(e, out, errs, "merge", "--root", root, "--out", merged, dirs[0], dirs[1])
	if r.code != 1 || !strings.Contains(r.stderr, "MERGE FAIL") || !strings.Contains(r.stderr, "no record for 1 declared cases") {
		t.Fatalf("an incomplete merge: %+v", r)
	}
	if after, _ := os.ReadFile(merged); string(after) != string(before) {
		t.Fatal("a refused merge changed the output")
	}
	// A file that is not records is a refusal.
	junk := filepath.Join(t.TempDir(), "junk.tsv")
	_ = os.WriteFile(junk, []byte("not\trecords\n"), 0o644)
	e, out, errs = testEnv(t, nil)
	if r := do(e, out, errs, "merge", "--root", root, "--out", merged, junk); r.code != 2 || !strings.Contains(r.stderr, "cannot read records") {
		t.Fatalf("junk: %+v", r)
	}
}

// A merge holds every record to the fingerprint of its own case at this
// checkout: a model edited since the runs refuses the records of the cases that
// read it and only those.
func TestMergeRefusesRecordsOfAModelEditedSinceTheRuns(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	var runs []string
	for _, group := range []string{"alpha", "beta", "gamma"} {
		e, out, errs := testEnv(t, scriptedTLC(t, map[string]int{"MCA": 0, "MCABroken": 12, "MCAStale": 13, "MCCard": 0}, nil))
		dir := filepath.Join(t.TempDir(), group)
		if r := do(e, out, errs, "run", "--root", root, "--jar", jar, "--dir", dir, "--group", group); r.code != 0 {
			t.Fatalf("%s: %+v", group, r)
		}
		runs = append(runs, filepath.Join(dir, tlc.RunsFile))
	}
	if err := os.WriteFile(filepath.Join(root, "tla", "MCAStale.cfg"), []byte("edited since\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	merged := filepath.Join(t.TempDir(), "RUNS.tsv")
	e, out, errs := testEnv(t, nil)
	r := do(e, out, errs, append([]string{"merge", "--root", root, "--out", merged}, runs...)...)
	if r.code != 1 || !strings.Contains(r.stderr, "1 records were measured on other inputs than these: MCAStale.cfg (") || strings.Contains(r.stderr, "MCABroken") {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(merged); err == nil {
		t.Fatal("a refused merge wrote its output")
	}
}

// After an edit only the groups that hold a case that reads it are run again,
// and the merge keeps the current records of the others: the records file the
// checkout commits is the same one a run of every group makes.
func TestOnlyTheStaleGroupsAreRunAndMergedOntoTheCommittedRecords(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	codes := map[string]int{"MCA": 0, "MCABroken": 12, "MCAStale": 13, "MCCard": 0}
	runGroup := func(group string) string {
		e, out, errs := testEnv(t, scriptedTLC(t, codes, nil))
		dir := filepath.Join(t.TempDir(), group)
		if r := do(e, out, errs, "run", "--root", root, "--jar", jar, "--dir", dir, "--group", group); r.code != 0 {
			t.Fatalf("%s: %+v", group, r)
		}
		return filepath.Join(dir, tlc.RunsFile)
	}
	runs := filepath.Join(root, "tla", tlc.RunsFile)
	first := filepath.Join(t.TempDir(), "first.tsv")
	e, out, errs := testEnv(t, nil)
	if r := do(e, out, errs, "merge", "--root", root, "--out", first, runGroup("alpha"), runGroup("beta"), runGroup("gamma")); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	raw, _ := os.ReadFile(first)
	if err := os.WriteFile(runs, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	groups := func() string {
		e, out, errs := testEnv(t, nil)
		r := do(e, out, errs, "groups", "--root", root, "--stale")
		if r.code != 0 || r.stderr != "" {
			t.Fatalf("%+v", r)
		}
		return strings.TrimSpace(r.stdout)
	}
	if got := groups(); got != "[]" {
		t.Fatalf("stale groups of current records = %s", got)
	}
	// MCAStale.cfg is the only case of group beta; MCABroken.cfg and MCA.cfg are alpha's.
	if err := os.WriteFile(filepath.Join(root, "tla", "MCAStale.cfg"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := groups(); got != `["beta"]` {
		t.Fatalf("stale groups after editing one configuration = %s", got)
	}
	// Without --keep the one group is an incomplete merge; with it, a complete one.
	merged := filepath.Join(t.TempDir(), "merged.tsv")
	beta := runGroup("beta")
	e, out, errs = testEnv(t, nil)
	if r := do(e, out, errs, "merge", "--root", root, "--out", merged, beta); r.code != 1 || !strings.Contains(r.stderr, "no record for 3 declared cases") {
		t.Fatalf("no --keep: %+v", r)
	}
	e, out, errs = testEnv(t, nil)
	if r := do(e, out, errs, "merge", "--root", root, "--keep", runs, "--out", merged, beta); r.code != 0 || !strings.Contains(r.stdout, "MERGE OK runs=1 records=4 ") {
		t.Fatalf("--keep: %+v", r)
	}
	got, err := tlc.ReadRecordsFile(merged)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := tlc.ReadRecordsFile(runs)
	for i := range got {
		same := got[i] == old[i]
		if same == (got[i].Config == "MCAStale.cfg") {
			t.Errorf("%s: kept=%v, want kept only for the cases that did not read the edit", got[i].Config, same)
		}
	}
	if err := os.WriteFile(runs, mustRead(t, merged), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := groups(); got != "[]" {
		t.Fatalf("stale groups after the merge = %s", got)
	}
	// A kept record that is not current is not carried.
	if err := os.WriteFile(filepath.Join(root, "tla", "MCA.tla"), []byte("edited model\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, out, errs = testEnv(t, nil)
	if r := do(e, out, errs, "merge", "--root", root, "--keep", runs, "--out", merged, runGroup("beta")); r.code != 1 || !strings.Contains(r.stderr, "3 kept records are stale, measured on inputs their cases no longer read: MCA.cfg (group alpha), MCABroken.cfg (group alpha), MCCard.cfg (group gamma)") || strings.Contains(r.stderr, "no record for") || !strings.Contains(r.stderr, "tlacheck run --root "+root+" --jar <jar> --dir <a clean directory>/alpha --group alpha") || !strings.Contains(r.stderr, "--group gamma") || !strings.Contains(r.stderr, "then merge again with --keep") {
		t.Fatalf("a stale kept record: %+v", r)
	}
	if got := groups(); got != `["alpha","beta","gamma"]` {
		t.Fatalf("stale groups after editing the module every case reads = %s", got)
	}
}

// The messages of a merge that cannot carry what it was asked to keep.
func TestMergeKeepNamesWhatItCannotCarry(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	codes := map[string]int{"MCA": 0, "MCABroken": 12, "MCAStale": 13, "MCCard": 0}
	var runs []string
	for _, group := range []string{"alpha", "beta", "gamma"} {
		e, out, errs := testEnv(t, scriptedTLC(t, codes, nil))
		dir := filepath.Join(t.TempDir(), group)
		if r := do(e, out, errs, "run", "--root", root, "--jar", jar, "--dir", dir, "--group", group); r.code != 0 {
			t.Fatalf("%s: %+v", group, r)
		}
		runs = append(runs, filepath.Join(dir, tlc.RunsFile))
	}
	keep := filepath.Join(t.TempDir(), "keep.tsv")
	e, out, errs := testEnv(t, nil)
	if r := do(e, out, errs, append([]string{"merge", "--root", root, "--out", keep}, runs...)...); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	merged := filepath.Join(t.TempDir(), "merged.tsv")

	// A kept record of a case the plan no longer declares is dropped, by name.
	raw := string(mustRead(t, keep))
	extra := strings.Split(strings.Split(raw, "\n")[1], "\t")
	extra[0] = "MCGone.cfg"
	if err := os.WriteFile(keep, []byte(raw+strings.Join(extra, "\t")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, out, errs = testEnv(t, nil)
	r := do(e, out, errs, "merge", "--root", root, "--keep", keep, "--out", merged, runs[0], runs[1], runs[2])
	if r.code != 0 || !strings.Contains(r.stdout, "DROP OK config=MCGone.cfg why=not-in-the-plan keep=") {
		t.Fatalf("a record for a case no longer in the plan: %+v", r)
	}
	if recs, err := tlc.ReadRecordsFile(merged); err != nil || len(recs) != 4 {
		t.Fatalf("merged %d records (%v), want the plan's four", len(recs), err)
	}

	// A file in the old column layout is refused naming both layouts.
	old := filepath.Join(t.TempDir(), "old.tsv")
	oldText := "config\tmodule\tinput_sha256\tjar_sha256\thost\tstarted_utc\tgenerated\tdistinct\tseconds\texit\tresult\texpected\tproperty\tbudget\tmode\n"
	if err := os.WriteFile(old, []byte(oldText), 0o644); err != nil {
		t.Fatal(err)
	}
	e, out, errs = testEnv(t, nil)
	r = do(e, out, errs, "merge", "--root", root, "--keep", old, "--out", merged, runs[0])
	for _, want := range []string{"cannot read the records to keep", "another layout: found 15 columns", "reads and writes 18", "take the tla/RUNS.tsv of the base branch"} {
		if r.code != 2 || !strings.Contains(r.stderr, want) {
			t.Errorf("an old layout: %q lacks %q (code %d)", r.stderr, want, r.code)
		}
	}
	// groups --stale reads the committed records in the same way.
	if err := os.WriteFile(filepath.Join(root, "tla", tlc.RunsFile), []byte(oldText), 0o644); err != nil {
		t.Fatal(err)
	}
	e, out, errs = testEnv(t, nil)
	if r := do(e, out, errs, "groups", "--root", root, "--stale"); r.code != 2 || !strings.Contains(r.stderr, "another layout") || !strings.Contains(r.stderr, "take the tla/RUNS.tsv of the base branch") {
		t.Errorf("groups --stale on an old layout: %+v", r)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestInputsPrintsWhatACaseReadsAndItsFingerprint(t *testing.T) {
	t.Parallel()
	root, _ := checkout(t)
	e, out, errs := testEnv(t, nil)
	r := do(e, out, errs, "inputs", "--root", root, "--case", "MCA.cfg")
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("%+v", r)
	}
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	last := lines[len(lines)-1]
	src, err := tlc.SourceAt(root)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := src.Inputs("MCA.cfg")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != len(inputs)+1 || last != "INPUTS OK case=MCA.cfg files="+fmt.Sprint(len(inputs))+" fingerprint="+tlc.Digest(inputs) {
		t.Fatalf("output %q for %v", r.stdout, inputs)
	}
	for i, in := range inputs {
		if lines[i] != "INPUT OK path="+in.Path+" sha256="+in.SHA256 || !eventRE.MatchString(lines[i]) {
			t.Errorf("line %d: %q", i, lines[i])
		}
	}
	if !strings.Contains(r.stdout, "path=tla/MCA.cfg ") || !strings.Contains(r.stdout, "path=tla/MCA.tla ") || !strings.Contains(r.stdout, "path=tla/CASES.tsv#MCA.cfg ") || strings.Contains(r.stdout, "MCAStale") {
		t.Errorf("the case's own files are not the list: %q", r.stdout)
	}
	// The case is named as its plan names it, or without the extension.
	e, out, errs = testEnv(t, nil)
	if again := do(e, out, errs, "inputs", "--root", root, "--case", "MCA"); again.code != 0 || again.stdout != r.stdout {
		t.Fatalf("MCA: %+v", again)
	}
	// It is the fingerprint a run records.
	e, out, errs = testEnv(t, scriptedTLC(t, map[string]int{"MCA": 0, "MCABroken": 12, "MCAStale": 13, "MCCard": 0}, nil))
	dir := filepath.Join(t.TempDir(), "run")
	_, jar := checkout(t)
	if run := do(e, out, errs, "run", "--root", root, "--jar", jar, "--dir", dir, "--group", "alpha"); run.code != 0 {
		t.Fatalf("%+v", run)
	}
	recs, err := tlc.ReadRecordsFile(filepath.Join(dir, tlc.RunsFile))
	if err != nil || recs[0].InputSHA256 != tlc.Digest(inputs) || recs[0].InputFiles != len(inputs) {
		t.Fatalf("records %+v (%v), inputs digest %s over %d", recs, err, tlc.Digest(inputs), len(inputs))
	}
}

func TestInputsRefusesWhatItCannotAnswer(t *testing.T) {
	t.Parallel()
	root, _ := checkout(t)
	for name, tc := range map[string]struct {
		args []string
		code int
		want string
	}{
		"no case":         {[]string{"inputs", "--root", root}, 2, "missing required --case"},
		"unknown case":    {[]string{"inputs", "--root", root, "--case", "MCNope.cfg"}, 2, `no case "MCNope.cfg"`},
		"a positional":    {[]string{"inputs", "--root", root, "--case", "MCA.cfg", "extra"}, 2, "unexpected argument"},
		"an unknown flag": {[]string{"inputs", "--all"}, 2, "unknown flag"},
		"no plan":         {[]string{"inputs", "--root", t.TempDir(), "--case", "MCA.cfg"}, 2, "the case plan is refused"},
	} {
		e, out, errs := testEnv(t, nil)
		r := do(e, out, errs, tc.args...)
		if r.code != tc.code || r.stdout != "" || !strings.Contains(r.stderr, tc.want) || !strings.Contains(r.stderr, "; run: tlacheck ") {
			t.Errorf("%s: %+v", name, r)
		}
	}
	// A module that names one that is nowhere is refused, naming it.
	if err := os.WriteFile(filepath.Join(root, "tla", "MCA.tla"), []byte("EXTENDS Nowhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, out, errs := testEnv(t, nil)
	if r := do(e, out, errs, "inputs", "--root", root, "--case", "MCA.cfg"); r.code != 2 || !strings.Contains(r.stderr, "names Nowhere") {
		t.Errorf("an unresolved module: %+v", r)
	}
}

func TestTableAndMemberReportEachStepAndStopAtTheFirstFailure(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	// Every table step is answered "the model passes": the contracts hold and
	// the first finding does not violate its invariant.
	passing := scriptedTLC(t, map[string]int{}, nil)
	e, out, errs := testEnv(t, passing)
	r := do(e, out, errs, "table", "--root", root, "--jar", jar, "--dir", filepath.Join(t.TempDir(), "t"), "--mode", "all")
	if r.code != 1 {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(r.stdout, "TABLE OK case=MCTableMachine verdict=pass") || !strings.Contains(r.stdout, "TABLE OK case=MCTableFixedPoint verdict=pass") {
		t.Errorf("stdout = %q", r.stdout)
	}
	if !strings.Contains(r.stderr, "TABLE FAIL case=MCTableOnePlace verdict=fail") || !strings.Contains(r.stderr, "TABLE FAIL mode=all cases=3") {
		t.Errorf("stderr = %q", r.stderr)
	}
	e, out, errs = testEnv(t, passing)
	r = do(e, out, errs, "table", "--root", root, "--jar", jar, "--dir", filepath.Join(t.TempDir(), "t"), "--mode", "contracts")
	if r.code != 0 || !strings.Contains(r.stdout, "TABLE OK mode=contracts cases=2") {
		t.Fatalf("contracts: %+v", r)
	}
	// The member suite: its small positive instance passes on a pass exit.
	e, out, errs = testEnv(t, passing)
	r = do(e, out, errs, "member", "--root", root, "--jar", jar, "--dir", filepath.Join(t.TempDir(), "m"), "--suite", "small")
	if r.code != 1 || !strings.Contains(r.stdout, "MEMBER OK case=MCMemberFixedPoint verdict=pass") || !strings.Contains(r.stderr, "MEMBER FAIL case=MCMemberBrokenMove verdict=fail") {
		t.Fatalf("member: %+v", r)
	}
	for _, args := range [][]string{{"table", "--mode", "nonsense"}, {"member", "--suite", "nonsense"}, {"member", "--workers", "0"}} {
		e, out, errs = testEnv(t, passing)
		r = do(e, out, errs, append([]string{args[0], "--root", root, "--jar", jar, "--dir", t.TempDir()}, args[1:]...)...)
		if r.code != 2 || strings.Count(r.stderr, "\n") != 1 || strings.Contains(r.stdout, "CASE") {
			t.Errorf("%v: %+v", args, r)
		}
	}
}

func TestReplayAndWitnessesRefuseWhatIsMissingBeforeStartingAnything(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	noRedis := func(e *env) {
		e.lookPath = func(name string) (string, error) {
			if name == "redis-server" {
				return "", errors.New("no")
			}
			return "/usr/bin/" + name, nil
		}
	}
	e, out, errs := testEnv(t, nil)
	noRedis(&e)
	r := do(e, out, errs, "witnesses", "table.lua")
	if r.code != 2 || !strings.Contains(r.stderr, "redis-server is not on PATH") || !strings.Contains(r.stderr, "run: tlacheck witnesses --redis-server /path/to/redis-server") {
		t.Fatalf("witnesses without redis: %+v", r)
	}
	e, out, errs = testEnv(t, nil)
	noRedis(&e)
	r = do(e, out, errs, "replay", "--root", root, "--jar", jar, "--source", "table.lua", "--dir", t.TempDir())
	if r.code != 2 || !strings.Contains(r.stderr, "redis-server is not on PATH") || strings.Contains(r.stdout, "REPLAY") {
		t.Fatalf("replay without redis: %+v", r)
	}
	// An unreadable source is a refusal of the environment (exit 2), not a
	// finding that failed.
	e, out, errs = testEnv(t, nil)
	e.lookPath = func(name string) (string, error) { return "/nonexistent/" + name, nil }
	r = do(e, out, errs, "witnesses", filepath.Join(t.TempDir(), "gone.lua"))
	if r.code != 2 || strings.Contains(r.stderr, "WITNESS FAIL") {
		t.Fatalf("witnesses of a missing source: %+v", r)
	}
	e, out, errs = testEnv(t, nil)
	e.lookPath = func(name string) (string, error) { return "/nonexistent/" + name, nil }
	r = do(e, out, errs, "replay", "--root", root, "--jar", jar, "--source", filepath.Join(t.TempDir(), "gone.lua"), "--dir", t.TempDir())
	if r.code != 2 || strings.Contains(r.stderr, "REPLAY FAIL") {
		t.Fatalf("replay of a missing source: %+v", r)
	}
}

// A remedy is a command that pastes: the ones that point at help run through
// the real CLI and exit 0.
func TestEveryHelpRemedyRunsThroughTheCLI(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	refusals := [][]string{
		{"run", "--root", root, "--jar", jar},
		{"replay"},
		{"merge"},
		{"groups", "--bogus"},
		{"table", "--mode", "x", "--root", root, "--jar", jar, "--dir", t.TempDir()},
	}
	remedy := regexp.MustCompile(`run: (tlacheck \S+ -h)$`)
	for _, args := range refusals {
		e, out, errs := testEnv(t, nil)
		r := do(e, out, errs, args...)
		m := remedy.FindStringSubmatch(strings.TrimSpace(r.stderr))
		if m == nil {
			t.Errorf("%v: no help remedy in %q", args, r.stderr)
			continue
		}
		e, out, errs = testEnv(t, nil)
		if again := do(e, out, errs, strings.Fields(m[1])[1:]...); again.code != 0 || again.stdout == "" {
			t.Errorf("%v: the remedy %q gave %+v", args, m[1], again)
		}
	}
}

func TestEventsEscapeWhatCouldSplitTheLine(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	event(&b, "CASE", "FAIL", "path", "a b=c\nd")
	if got := b.String(); got != "CASE FAIL path=a\\x20b\\x3dc\\x0ad\n" {
		t.Fatalf("event = %q", got)
	}
}
