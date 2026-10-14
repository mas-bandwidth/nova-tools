package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit/fsx"
	"github.com/mas-bandwidth/nova-tools/internal/testkit/must"
	tverb "github.com/mas-bandwidth/nova-tools/internal/testkit/verb"
	"github.com/mas-bandwidth/nova-tools/pkg/tlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		require.NoError(t, err)
		logs[status] = string(raw)
	}
	return func(ctx context.Context, r tlc.Run, log string) int {
		if runs != nil {
			*runs = append(*runs, r)
		}
		status := code[strings.TrimSuffix(r.Config, ".cfg")]
		assert.NoError(t, os.WriteFile(log, []byte(logs[status]), 0o644))
		return status
	}
}

// newTestEnv builds a test env with buffers and safe stubs.
func newTestEnv(exec tlc.Executor) env {
	var out, errs bytes.Buffer
	return env{
		stdout: &out, stderr: &errs,
		getenv:      func(string) string { return "" },
		lookPath:    func(name string) (string, error) { return "/usr/bin/" + name, nil },
		cpus:        func() int { return 8 },
		javaVersion: func(string) (string, error) { return "21.0.12.1", nil },
		exec:        exec, goos: "linux", goarch: "amd64",
	}
}

func testEnv(t *testing.T, exec tlc.Executor) (env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	e := newTestEnv(exec)
	return e, e.stdout.(*bytes.Buffer), e.stderr.(*bytes.Buffer)
}

// testMain adapts tlacheck's run to the verb mini-module's Main.
func testMain(exec tlc.Executor) tverb.Main {
	return func(args []string, _ io.Reader, stdout, stderr io.Writer) int {
		e := newTestEnv(exec)
		e.stdout, e.stderr = stdout, stderr
		return run(args, e)
	}
}

func do(e env, out, errs *bytes.Buffer, args ...string) result {
	code := run(args, e)
	return result{code, out.String(), errs.String()}
}

// call runs one verb invocation through a fresh test environment: it stands in
// for the setup pair that repeats before a check when nothing tweaks the env.
func call(t *testing.T, exec tlc.Executor, args ...string) result {
	t.Helper()
	e, out, errs := testEnv(t, exec)
	return do(e, out, errs, args...)
}

const header = "config\tmodule\texpected\tproperty\tdeadlock\tgroup\tgate\tdebt\n"

// checkout writes a tla/ with three cases (a pass, an invariant counterexample
// and an action counterexample) and returns its root and a jar.
func checkout(t *testing.T) (root, jar string) {
	t.Helper()
	root = fsx.Tree(t, map[string]string{
		"tla/CASES.tsv": header +
			"MCA.cfg\tMCA.tla\tpass\t-\tcheck\talpha\trequired\t-\n" +
			"MCABroken.cfg\tMCA.tla\tinvariant\tOnePlacePerTable\tignore-terminal\talpha\trequired\t-\n" +
			"MCAStale.cfg\tMCA.tla\taction\tStaleWritesRefuse\tignore-terminal\tbeta\trequired\t-\n" +
			"MCCard.cfg\tMCA.tla\tpass\t-\tcheck\tgamma\tbench\tslow\n",
		"tla/MCA.tla":       "model\n",
		"tla/MCA.cfg":       "c\n",
		"tla/MCABroken.cfg": "c\n",
		"tla/MCAStale.cfg":  "c\n",
		"tla/MCCard.cfg":    "c\n",
	})
	jar = filepath.Join(t.TempDir(), "tla2tools.jar")
	require.NoError(t, os.WriteFile(jar, []byte("jar"), 0o644))
	return root, jar
}

var eventRE = regexp.MustCompile(`^[A-Z]+ (OK|FAIL)( [a-z0-9-]+=\S*)*$`)

func TestHelpIsOnStdoutAtExitZeroForTheToolAndEveryVerb(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		r := call(t, nil, args...)
		assert.Equal(t, 0, r.code, "%v: %+v", args, r)
		assert.Empty(t, r.stderr)
		assert.Contains(t, r.stdout, "usage: tlacheck <verb>")
		for _, v := range verbs() {
			assert.Contains(t, r.stdout, v.name, "%v: the banner does not list %s", args, v.name)
		}
	}
	for _, v := range verbs() {
		for _, args := range [][]string{{v.name, "-h"}, {v.name, "--help"}, {"help", v.name}} {
			r := call(t, nil, args...)
			assert.Equal(t, 0, r.code, "%v: code=%d stderr=%q", args, r.code, r.stderr)
			assert.Empty(t, r.stderr)
			assert.Equal(t, v.help, r.stdout)
			assert.True(t, strings.HasPrefix(r.stdout, "tlacheck "+v.name+":"), "%v", args)
			assert.Contains(t, r.stdout, "first run:")
			assert.Condition(t, func() bool {
				return strings.Contains(r.stdout, "output:") || strings.Contains(r.stdout, "Payload:")
			}, "%v: the help has no output line or first run", args)
		}
	}
}

func TestHelpHasNoSideEffects(t *testing.T) {
	t.Parallel()
	e, out, errs := testEnv(t, func(context.Context, tlc.Run, string) int { assert.Fail(t, "TLC ran for help"); return 0 })
	e.lookPath = func(string) (string, error) {
		assert.Fail(t, "a program was looked up for help")
		return "", errors.New("no")
	}
	e.getenv = func(string) string { assert.Fail(t, "the environment was read for help"); return "" }
	for _, v := range verbs() {
		do(e, out, errs, v.name, "-h")
	}
}

func TestAnUnknownVerbIsNamedAndTheVerbsAreListed(t *testing.T) {
	t.Parallel()
	r := call(t, nil, "bogus")
	require.Equal(t, 2, r.code)
	require.Empty(t, r.stdout)
	require.Equal(t, 1, strings.Count(r.stderr, "\n"))
	must.ContainsAll(t, r.stderr, `unknown verb "bogus"`, "run, groups, merge, inputs, table, member, replay, witnesses", "run: tlacheck help")
	tverb.Run(t, testMain(nil), "help", "bogus").Code(2).Stderr(`unknown verb "bogus"`)
	bare := tverb.Run(t, testMain(nil))
	bare.Code(2).Stderr("usage: tlacheck")
	assert.Empty(t, bare.Result.Stdout, bare)
}

func TestAMalformedInvocationIsRefusedInOneLine(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	cases := []struct {
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
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := call(t, nil, tc.args...)
			require.Equal(t, 2, r.code)
			require.Equal(t, 1, strings.Count(r.stderr, "\n"))
			assert.NotContains(t, r.stdout, "OK")
			must.ContainsAll(t, r.stderr, tc.want...)
			assert.True(t, strings.HasPrefix(r.stderr, "tlacheck "+tc.args[0]+": "), "refusal starts with tool+verb")
		})
	}
}

func TestALiteralDoubleDashEndsTheFlags(t *testing.T) {
	t.Parallel()
	// After "--" a leading dash is a file name, not a flag; the verb then
	// refuses the file for what it is.
	r := call(t, nil, "witnesses", "--", "-table.lua")
	assert.NotContains(t, r.stderr, "comes after a positional")
}

func TestGroupsPrintsTheRequiredGroupsAsOneLineOfJSON(t *testing.T) {
	t.Parallel()
	root, _ := checkout(t)
	r := call(t, nil, "groups", "--root", root)
	require.Equal(t, 0, r.code)
	require.Empty(t, r.stderr)
	var groups []string
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &groups))
	assert.Equal(t, []string{"alpha", "beta"}, groups)
	assert.Equal(t, 1, strings.Count(r.stdout, "\n"))
	// The repository's own plan.
	r = call(t, nil, "groups", "--root", filepath.Join("..", ".."))
	require.Equal(t, 0, r.code, "the repository's groups: %+v", r)
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &groups), "the repository's groups: %+v", r)
	assert.GreaterOrEqual(t, len(groups), 5, "the repository's groups: %+v", r)
	// A root with no plan is a refusal and prints no payload.
	r = call(t, nil, "groups", "--root", t.TempDir())
	require.Equal(t, 2, r.code, "a root with no plan: %+v", r)
	assert.Empty(t, r.stdout, "a root with no plan: %+v", r)
	assert.Contains(t, r.stderr, "the case plan is refused", "a root with no plan: %+v", r)
}

func TestRunWritesRecordsAndReportsEachCase(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	var runs []tlc.Run
	dir := filepath.Join(t.TempDir(), "out")
	r := call(t, scriptedTLC(t, map[string]int{"MCA": 0, "MCABroken": 12, "MCAStale": 13}, &runs), "run", "--root", root, "--jar", jar, "--dir", dir, "--group", "alpha", "--timeout", "100s", "--workers", "1")
	require.Equal(t, 0, r.code)
	require.Empty(t, r.stderr)
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	want := []string{"HELPER OK name=jar", "HELPER OK name=java path=/usr/bin/java", "HELPER OK name=java-version version=21.0.12.1", "CASE OK config=MCA.cfg result=PASS", "CASE OK config=MCABroken.cfg result=PASS", "RUN OK cases=2 records="}
	require.Len(t, lines, len(want))
	for i, w := range want {
		assert.True(t, strings.HasPrefix(lines[i], w), "line %d = %q, want prefix %q", i, lines[i], w)
		assert.Regexp(t, eventRE, lines[i])
	}
	assert.Contains(t, lines[0], "source=flag")
	assert.Contains(t, lines[3], "exit=0 generated=15518 distinct=263")
	recs, err := tlc.ReadRecordsFile(filepath.Join(dir, tlc.RunsFile))
	require.NoError(t, err)
	require.Len(t, recs, 2)
	assert.Equal(t, "linux-amd64", recs[0].Host)
	assert.Equal(t, 8, recs[0].CPUs)
	assert.Equal(t, "100", recs[0].Budget)
	assert.Equal(t, 12, recs[1].Exit)
	require.Len(t, runs, 2)
	assert.Equal(t, 1, runs[0].Workers)
}

func TestRunRecordsTheJavaVersionAndWorkersAndRefusesWhenJavaHasNone(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	dir := filepath.Join(t.TempDir(), "run")
	r := call(t, scriptedTLC(t, map[string]int{"MCA": 0, "MCABroken": 12}, nil), "run", "--root", root, "--jar", jar, "--dir", dir, "--group", "alpha", "--workers", "1")
	require.Equal(t, 0, r.code, "%+v", r)
	assert.Contains(t, r.stdout, "HELPER OK name=java-version version=21.0.12.1", "%+v", r)
	recs, err := tlc.ReadRecordsFile(filepath.Join(dir, tlc.RunsFile))
	require.NoError(t, err)
	require.Len(t, recs, 2)
	assert.Equal(t, "21.0.12.1", recs[0].JavaVersion)
	assert.Equal(t, 1, recs[0].Workers)
	assert.Equal(t, 1, recs[1].Workers)
	e, out, errs := testEnv(t, func(context.Context, tlc.Run, string) int { assert.Fail(t, "TLC ran without a java version"); return 0 })
	e.javaVersion = func(string) (string, error) { return "", errors.New("java -version printed no quoted version") }
	dir2 := filepath.Join(t.TempDir(), "run2")
	r = do(e, out, errs, "run", "--root", root, "--jar", jar, "--dir", dir2, "--group", "alpha")
	require.Equal(t, 2, r.code, "%+v", r)
	must.ContainsAll(t, r.stderr, "the java version cannot be read", "nothing was run")
	assert.NoFileExists(t, filepath.Join(dir2, tlc.RunsFile))
}

// The host column is the tool's own platform label, and there is no way to
// write a machine name: no flag, no environment variable.
func TestRunWritesThePlatformLabelAndNeverAHostName(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	e, out, errs := testEnv(t, scriptedTLC(t, map[string]int{"MCA": 0, "MCABroken": 12}, nil))
	e.goarch, e.cpus = "arm64", func() int { return 64 }
	e.getenv = func(name string) string { return "HOSTNAME-or-" + name }
	dir := filepath.Join(t.TempDir(), "run")
	r := do(e, out, errs, "run", "--root", root, "--jar", jar, "--dir", dir, "--group", "alpha")
	require.Equal(t, 0, r.code, "%+v", r)
	recs, err := tlc.ReadRecordsFile(filepath.Join(dir, tlc.RunsFile))
	require.NoError(t, err)
	require.Len(t, recs, 2)
	assert.Equal(t, "linux-arm64", recs[0].Host)
	assert.Equal(t, 64, recs[0].CPUs)
	assert.Equal(t, "linux-arm64", recs[1].Host)
	raw := fsx.Read(t, filepath.Join(dir, tlc.RunsFile))
	assert.NotContains(t, raw, "HOSTNAME")
	assert.NotContains(t, raw, "bench")
	e, out, errs = testEnv(t, func(context.Context, tlc.Run, string) int {
		assert.Fail(t, "TLC ran on an unlisted platform")
		return 0
	})
	e.goarch = "sparc"
	r = do(e, out, errs, "run", "--root", root, "--jar", jar, "--dir", filepath.Join(t.TempDir(), "r2"), "--group", "alpha")
	assert.Equal(t, 2, r.code, "an unlisted architecture: %+v", r)
	assert.Contains(t, r.stderr, `platform "linux-sparc" is not one the runner records`, "an unlisted architecture: %+v", r)
	r = call(t, nil, "run", "--root", root, "--jar", jar, "--dir", filepath.Join(t.TempDir(), "r3"), "--group", "alpha", "--host", "x")
	assert.Equal(t, 2, r.code, "a --host flag exists: %+v", r)
	assert.Contains(t, r.stderr, "unknown flag", "a --host flag exists: %+v", r)
}

func TestRunReportsAFailingCaseOnStderrAndExitsOne(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	// The counterexample case's model passes: not what it declares.
	r := call(t, scriptedTLC(t, map[string]int{"MCA": 0, "MCABroken": 0}, nil), "run", "--root", root, "--jar", jar, "--dir", filepath.Join(t.TempDir(), "o"), "--group", "alpha")
	require.Equal(t, 1, r.code)
	assert.Contains(t, r.stdout, "CASE OK config=MCA.cfg")
	assert.NotContains(t, r.stdout, "MCABroken")
	assert.NotContains(t, r.stdout, "RUN OK")
	assert.Contains(t, r.stderr, "CASE FAIL config=MCABroken.cfg result=FAIL")
	assert.Contains(t, r.stderr, "RUN FAIL cases=2")
	for _, l := range strings.Split(strings.TrimSpace(r.stderr), "\n") {
		assert.Regexp(t, eventRE, strings.SplitN(l, ": ", 2)[0])
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
		{"more shards than the group has cases", []string{"--group", "alpha", "--shards", "3"}, nil, "shard count exceeds the 2 cases of group alpha"},
		{"a timeout over the cap", []string{"--timeout", "111s"}, nil, "at most 1m50s"},
		{"a zero timeout", []string{"--timeout", "0s"}, nil, "must be positive"},
		{"three workers", []string{"--workers", "3"}, nil, "workers must be 1 or 2"},
		{"a trough flag without --bench", []string{"--group", "alpha", "--trough-poll", "5s"}, nil, "--trough-poll is taken only with --bench"},
		{"a manual run in CI", []string{"--manual", "--group", "alpha"}, func(e *env) { e.getenv = ci }, "--manual is forbidden in CI"},
		{"a platform that is not Linux", []string{"--group", "alpha"}, func(e *env) { e.goos = "darwin" }, "TLC runs on a Linux bench, and this is darwin"},
		{"no java", []string{"--group", "alpha"}, func(e *env) { e.lookPath = func(string) (string, error) { return "", errors.New("no") } }, "java is not on PATH"},
	}
	for _, tc := range tests {
		e, out, errs := testEnv(t, func(context.Context, tlc.Run, string) int { assert.Failf(t, "TLC ran", "%s", tc.name); return 0 })
		if tc.tweak != nil {
			tc.tweak(&e)
		}
		args := append([]string{"run", "--root", root, "--jar", jar, "--dir", dir()}, tc.args...)
		r := do(e, out, errs, args...)
		require.Equal(t, 2, r.code, tc.name)
		assert.Contains(t, r.stderr, tc.want, tc.name)
		assert.Equal(t, 1, strings.Count(r.stderr, "\n"), tc.name)
		assert.NotContains(t, r.stdout, "CASE", tc.name)
	}
	// No jar: neither the flag nor TLC_JAR.
	r := call(t, nil, "run", "--root", root, "--dir", dir(), "--group", "alpha")
	require.Equal(t, 2, r.code)
	assert.Contains(t, r.stderr, "neither --jar nor TLC_JAR")
	assert.Contains(t, r.stderr, "run: tlacheck run --jar /path/to/tla2tools.jar")
	// The environment names the jar.
	e, out, errs := testEnv(t, scriptedTLC(t, map[string]int{"MCA": 0, "MCABroken": 12}, nil))
	e.getenv = func(k string) string {
		if k == tlc.JarEnv {
			return jar
		}
		return ""
	}
	r = do(e, out, errs, "run", "--root", root, "--dir", dir(), "--group", "alpha")
	require.Equal(t, 0, r.code)
	assert.Contains(t, r.stdout, "source=env:TLC_JAR")
}

func TestRunNeverGrowsTheCheckout(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	exec := func(ctx context.Context, r tlc.Run, log string) int {
		// TLC's error trace lands beside the spec it was given.
		_ = os.WriteFile(filepath.Join(r.Dir, "MCA_TTrace_1.tla"), []byte("trace\n"), 0o644)
		return scriptedTLC(t, map[string]int{"MCA": 0, "MCABroken": 12}, nil)(ctx, r, log)
	}
	r := call(t, exec, "run", "--root", root, "--jar", jar, "--dir", filepath.Join(t.TempDir(), "o"), "--group", "alpha")
	require.Equal(t, 0, r.code)
	m, _ := filepath.Glob(filepath.Join(root, "tla", "*TTrace*"))
	assert.Len(t, m, 0)
}

func TestMergeJoinsTheRecordsOfEveryGroup(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	var dirs []string
	for _, group := range []string{"alpha", "beta", "gamma"} {
		dir := filepath.Join(t.TempDir(), group)
		r := call(t, scriptedTLC(t, map[string]int{"MCA": 0, "MCABroken": 12, "MCAStale": 13, "MCCard": 0}, nil), "run", "--root", root, "--jar", jar, "--dir", dir, "--group", group)
		require.Equal(t, 0, r.code, group)
		dirs = append(dirs, filepath.Join(dir, tlc.RunsFile))
	}
	merged := filepath.Join(t.TempDir(), "RUNS.tsv")
	r := call(t, nil, append([]string{"merge", "--root", root, "--out", merged}, dirs...)...)
	require.Equal(t, 0, r.code)
	assert.Contains(t, r.stdout, "MERGE OK runs=3 records=4 out=")
	recs, err := tlc.ReadRecordsFile(merged)
	require.NoError(t, err)
	require.Len(t, recs, 4)
	assert.Equal(t, "MCA.cfg", recs[0].Config)
	assert.Equal(t, "MCCard.cfg", recs[3].Config)
	// A run that is missing is a refusal, and the output is left alone.
	before, err := os.ReadFile(merged)
	require.NoError(t, err)
	r = call(t, nil, "merge", "--root", root, "--out", merged, dirs[0], dirs[1])
	require.Equal(t, 1, r.code, "an incomplete merge: %+v", r)
	must.ContainsAll(t, r.stderr, "MERGE FAIL", "no record for 1 declared cases")
	must.FileReads(t, merged, string(before))
	// A file that is not records is a refusal.
	junk := filepath.Join(t.TempDir(), "junk.tsv")
	require.NoError(t, os.WriteFile(junk, []byte("not\trecords\n"), 0o644))
	r = call(t, nil, "merge", "--root", root, "--out", merged, junk)
	require.Equal(t, 2, r.code, "junk: %+v", r)
	assert.Contains(t, r.stderr, "cannot read records", "junk: %+v", r)
}

// A merge holds every record to the fingerprint of its own case at this
// checkout: a model edited since the runs refuses the records of the cases that
// read it and only those.
func TestMergeRefusesRecordsOfAModelEditedSinceTheRuns(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	var runs []string
	for _, group := range []string{"alpha", "beta", "gamma"} {
		dir := filepath.Join(t.TempDir(), group)
		r := call(t, scriptedTLC(t, map[string]int{"MCA": 0, "MCABroken": 12, "MCAStale": 13, "MCCard": 0}, nil), "run", "--root", root, "--jar", jar, "--dir", dir, "--group", group)
		require.Equal(t, 0, r.code, "%s: %+v", group, r)
		runs = append(runs, filepath.Join(dir, tlc.RunsFile))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "tla", "MCAStale.cfg"), []byte("edited since\n"), 0o644))
	merged := filepath.Join(t.TempDir(), "RUNS.tsv")
	r := call(t, nil, append([]string{"merge", "--root", root, "--out", merged}, runs...)...)
	require.Equal(t, 1, r.code, "%+v", r)
	assert.Contains(t, r.stderr, "1 records were measured on other inputs than these: MCAStale.cfg (", "%+v", r)
	assert.NotContains(t, r.stderr, "MCABroken", "%+v", r)
	assert.NoFileExists(t, merged, "a refused merge wrote its output")
}

// After an edit only the groups that hold a case that reads it are run again,
// and the merge keeps the current records of the others: the records file the
// checkout commits is the same one a run of every group makes.
func TestOnlyTheStaleGroupsAreRunAndMergedOntoTheCommittedRecords(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	codes := map[string]int{"MCA": 0, "MCABroken": 12, "MCAStale": 13, "MCCard": 0}
	runGroup := func(group string) string {
		dir := filepath.Join(t.TempDir(), group)
		r := call(t, scriptedTLC(t, codes, nil), "run", "--root", root, "--jar", jar, "--dir", dir, "--group", group)
		require.Equal(t, 0, r.code, "%s: %+v", group, r)
		return filepath.Join(dir, tlc.RunsFile)
	}
	runs := filepath.Join(root, "tla", tlc.RunsFile)
	first := filepath.Join(t.TempDir(), "first.tsv")
	r := call(t, nil, "merge", "--root", root, "--out", first, runGroup("alpha"), runGroup("beta"), runGroup("gamma"))
	require.Equal(t, 0, r.code, "%+v", r)
	raw, err := os.ReadFile(first)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(runs, raw, 0o644))
	groups := func() string {
		g := call(t, nil, "groups", "--root", root, "--stale")
		require.Equal(t, 0, g.code, "%+v", g)
		require.Empty(t, g.stderr, "%+v", g)
		return strings.TrimSpace(g.stdout)
	}
	require.Equal(t, "[]", groups(), "stale groups of current records")
	// MCAStale.cfg is the only case of group beta; MCABroken.cfg and MCA.cfg are alpha's.
	require.NoError(t, os.WriteFile(filepath.Join(root, "tla", "MCAStale.cfg"), []byte("edited\n"), 0o644))
	require.Equal(t, `["beta"]`, groups(), "stale groups after editing one configuration")
	// Without --keep the one group is an incomplete merge; with it, a complete one.
	merged := filepath.Join(t.TempDir(), "merged.tsv")
	beta := runGroup("beta")
	r = call(t, nil, "merge", "--root", root, "--out", merged, beta)
	require.Equal(t, 1, r.code, "no --keep: %+v", r)
	assert.Contains(t, r.stderr, "no record for 3 declared cases", "no --keep: %+v", r)
	r = call(t, nil, "merge", "--root", root, "--keep", runs, "--out", merged, beta)
	require.Equal(t, 0, r.code, "--keep: %+v", r)
	assert.Contains(t, r.stdout, "MERGE OK runs=1 records=4 ", "--keep: %+v", r)
	got, err := tlc.ReadRecordsFile(merged)
	require.NoError(t, err)
	old, err := tlc.ReadRecordsFile(runs)
	require.NoError(t, err)
	for i := range got {
		same := got[i] == old[i]
		assert.NotEqual(t, got[i].Config == "MCAStale.cfg", same, "%s: kept=%v, want kept only for the cases that did not read the edit", got[i].Config, same)
	}
	require.NoError(t, os.WriteFile(runs, []byte(fsx.Read(t, merged)), 0o644))
	require.Equal(t, "[]", groups(), "stale groups after the merge")
	// A kept record that is not current is not carried.
	require.NoError(t, os.WriteFile(filepath.Join(root, "tla", "MCA.tla"), []byte("edited model\n"), 0o644))
	r = call(t, nil, "merge", "--root", root, "--keep", runs, "--out", merged, runGroup("beta"))
	require.Equal(t, 1, r.code, "a stale kept record: %+v", r)
	must.ContainsAll(t, r.stderr,
		"3 kept records are stale, measured on inputs their cases no longer read: MCA.cfg (group alpha), MCABroken.cfg (group alpha), MCCard.cfg (group gamma)",
		"tlacheck run --root "+root+" --jar <jar> --dir <a clean directory>/alpha --group alpha",
		"--group gamma",
		"then merge again with --keep")
	assert.NotContains(t, r.stderr, "no record for", "a stale kept record: %+v", r)
	require.Equal(t, `["alpha","beta","gamma"]`, groups(), "stale groups after editing the module every case reads")
}

// The messages of a merge that cannot carry what it was asked to keep.
func TestMergeKeepNamesWhatItCannotCarry(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	codes := map[string]int{"MCA": 0, "MCABroken": 12, "MCAStale": 13, "MCCard": 0}
	var runs []string
	for _, group := range []string{"alpha", "beta", "gamma"} {
		dir := filepath.Join(t.TempDir(), group)
		r := call(t, scriptedTLC(t, codes, nil), "run", "--root", root, "--jar", jar, "--dir", dir, "--group", group)
		require.Equal(t, 0, r.code, "%s: %+v", group, r)
		runs = append(runs, filepath.Join(dir, tlc.RunsFile))
	}
	keep := filepath.Join(t.TempDir(), "keep.tsv")
	r := call(t, nil, append([]string{"merge", "--root", root, "--out", keep}, runs...)...)
	require.Equal(t, 0, r.code, "%+v", r)
	merged := filepath.Join(t.TempDir(), "merged.tsv")

	// A kept record of a case the plan no longer declares is dropped, by name.
	raw := fsx.Read(t, keep)
	extra := strings.Split(strings.Split(raw, "\n")[1], "\t")
	extra[0] = "MCGone.cfg"
	require.NoError(t, os.WriteFile(keep, []byte(raw+strings.Join(extra, "\t")+"\n"), 0o644))
	r = call(t, nil, "merge", "--root", root, "--keep", keep, "--out", merged, runs[0], runs[1], runs[2])
	require.Equal(t, 0, r.code, "a record for a case no longer in the plan: %+v", r)
	assert.Contains(t, r.stdout, "DROP OK config=MCGone.cfg why=not-in-the-plan keep=", "a record for a case no longer in the plan: %+v", r)
	recs, err := tlc.ReadRecordsFile(merged)
	require.NoError(t, err, "want the plan's four")
	assert.Len(t, recs, 4, "want the plan's four")

	// A file in the old column layout is refused naming both layouts.
	old := filepath.Join(t.TempDir(), "old.tsv")
	oldText := "config\tmodule\tinput_sha256\tjar_sha256\thost\tstarted_utc\tgenerated\tdistinct\tseconds\texit\tresult\texpected\tproperty\tbudget\tmode\n"
	require.NoError(t, os.WriteFile(old, []byte(oldText), 0o644))
	r = call(t, nil, "merge", "--root", root, "--keep", old, "--out", merged, runs[0])
	assert.Equal(t, 2, r.code, "an old layout: %q", r.stderr)
	must.ContainsAll(t, r.stderr, "cannot read the records to keep", "another layout: found 15 columns", "reads and writes 19", "take the tla/RUNS.tsv of the base branch")
	// groups --stale reads the committed records in the same way.
	require.NoError(t, os.WriteFile(filepath.Join(root, "tla", tlc.RunsFile), []byte(oldText), 0o644))
	r = call(t, nil, "groups", "--root", root, "--stale")
	assert.Equal(t, 2, r.code, "groups --stale on an old layout: %+v", r)
	must.ContainsAll(t, r.stderr, "another layout", "take the tla/RUNS.tsv of the base branch")
}

// The commands tla/README.md shows are held to the real flag parser: each
// tlacheck invocation in a fenced block is run through the verb's own flag set
// with the verb stopped once its flags are read, so a flag that is renamed,
// dropped or misplaced turns this red. The README also has to show the
// refresh procedure's four verbs.
func TestReadmeCommandsParseUnderTheRealFlagParser(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "tla", "README.md"))
	require.NoError(t, err)
	invocation := regexp.MustCompile(`tlacheck\s+([a-z]+)((?:\s+[^\s|)&;><]+)*)`)
	var commands [][]string
	inBlock := false
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "```") {
			inBlock = !inBlock
			continue
		}
		if !inBlock {
			continue
		}
		for _, m := range invocation.FindAllStringSubmatch(line, -1) {
			args := []string{m[1]}
			args = append(args, strings.Fields(strings.ReplaceAll(m[2], `"`, ""))...)
			commands = append(commands, args)
		}
	}
	shown := map[string]int{}
	for _, args := range commands {
		shown[args[0]]++
		e, out, errs := testEnv(t, func(context.Context, tlc.Run, string) int { assert.Failf(t, "TLC ran", "%v", args); return 0 })
		e.parseOnly = true
		r := do(e, out, errs, args...)
		assert.Equal(t, 0, r.code, "tlacheck %s: the real parser refuses it: %+v", strings.Join(args, " "), r)
		assert.Empty(t, r.stdout, "tlacheck %s: the real parser refuses it: %+v", strings.Join(args, " "), r)
		assert.Empty(t, r.stderr, "tlacheck %s: the real parser refuses it: %+v", strings.Join(args, " "), r)
	}
	for _, verb := range []string{"groups", "run", "merge", "inputs"} {
		assert.NotZero(t, shown[verb], "the README shows no tlacheck %s command", verb)
	}
	assert.GreaterOrEqual(t, len(commands), 8, "only %d commands found in the README's blocks: %v", len(commands), commands)
	// The control: the parser does refuse a flag it does not have, a flag after a
	// positional and a missing flag value, so a pass above means something.
	for _, args := range [][]string{{"run", "--dir", "x", "--bogus"}, {"merge", "--out", "o", "a.tsv", "--keep", "k"}, {"groups", "--root"}} {
		e, out, errs := testEnv(t, nil)
		e.parseOnly = true
		r := do(e, out, errs, args...)
		assert.Equal(t, 2, r.code, "%v was parsed: %+v", args, r)
	}
}

// A binary built from other runner files than the checkout's would call the
// wrong records stale, so every verb that takes a fingerprint refuses it.
func TestVerbsRefuseABinaryBuiltFromOtherRunnerFiles(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	dir := filepath.Join(root, "pkg", "tlc")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	built := map[string][]byte{}
	for _, name := range tlc.CheckedFiles() {
		raw, err := os.ReadFile(filepath.Join("..", "..", "pkg", "tlc", name))
		require.NoError(t, err)
		key := tlc.RunnerDir + "/" + name
		built[key] = raw
		p := filepath.Join(root, filepath.FromSlash(key))
		require.NoError(t, os.WriteFile(p, raw, 0o644))
	}
	runs := filepath.Join(root, "tla", tlc.RunsFile)
	require.NoError(t, os.WriteFile(runs, []byte(strings.Join(tlc.RecordsHeader, "\t")+"\n"), 0o644))
	verbs := map[string][]string{
		"groups --stale": {"groups", "--root", root, "--stale"},
		"inputs":         {"inputs", "--root", root, "--case", "MCA.cfg"},
		"merge":          {"merge", "--root", root, "--keep", runs, "--out", filepath.Join(t.TempDir(), "o.tsv")},
		"run":            {"run", "--root", root, "--jar", jar, "--dir", filepath.Join(t.TempDir(), "r"), "--group", "alpha"},
	}
	ok := func(args []string) result {
		return call(t, scriptedTLC(t, map[string]int{"MCA": 0, "MCABroken": 12}, nil), args...)
	}
	// The files as the binary has them are accepted (merge with nothing to keep
	// still needs a record for every case, so its code is 1, not a refusal).
	for name, args := range verbs {
		r := ok(args)
		assert.False(t, r.code == 2 && strings.Contains(r.stderr, "built from other runner files"), "%s refused the matching runner files: %+v", name, r)
	}
	// One result file edited under the root: each refuses, naming it and the remedy.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "run.go"), []byte("another runner\n"), 0o644))
	for name, args := range verbs {
		r := ok(args)
		assert.Equal(t, 2, r.code, "%s: %+v", name, r)
		must.ContainsAll(t, r.stderr,
			"built from other runner files than the ones under "+root,
			"pkg/tlc/run.go differ",
			"build tlacheck from this tree: go build -o /tmp/tlacheck ./tools/tlacheck")
		assert.NotContains(t, r.stderr, "outcome.go", "%s: %+v", name, r)
		assert.True(t, r.stdout == "" || name == "run", "%s: %+v", name, r)
	}
	// The list of inputs is computed by inputs.go: a binary built from another
	// one lists other inputs than the checkout does, and each verb refuses it.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "run.go"), built["pkg/tlc/run.go"], 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inputs.go"), []byte("another input list\n"), 0o644))
	for name, args := range verbs {
		r := ok(args)
		assert.Equal(t, 2, r.code, "%s with another input list: %+v", name, r)
		assert.Contains(t, r.stderr, "pkg/tlc/inputs.go differ", "%s with another input list: %+v", name, r)
		assert.NotContains(t, r.stderr, "run.go", "%s with another input list: %+v", name, r)
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inputs.go"), built["pkg/tlc/inputs.go"], 0o644))
	// A bookkeeping file that differs is no concern of the fingerprint.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "records.go"), []byte("another bookkeeping\n"), 0o644))
	r := ok(verbs["inputs"])
	assert.Equal(t, 0, r.code, "a bookkeeping file that differs: %+v", r)
	// A missing result file is a difference too.
	require.NoError(t, os.Remove(filepath.Join(dir, "outcome.go")))
	r = ok(verbs["inputs"])
	assert.Equal(t, 2, r.code, "a missing result file: %+v", r)
	assert.Contains(t, r.stderr, "pkg/tlc/outcome.go differ", "a missing result file: %+v", r)
}

func TestInputsPrintsWhatACaseReadsAndItsFingerprint(t *testing.T) {
	t.Parallel()
	root, _ := checkout(t)
	r := call(t, nil, "inputs", "--root", root, "--case", "MCA.cfg")
	require.Equal(t, 0, r.code, "%+v", r)
	require.Empty(t, r.stderr, "%+v", r)
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	last := lines[len(lines)-1]
	src, err := tlc.SourceAt(root)
	require.NoError(t, err)
	inputs, err := src.Inputs("MCA.cfg")
	require.NoError(t, err)
	require.Len(t, lines, len(inputs)+1, "output %q for %v", r.stdout, inputs)
	assert.Equal(t, "INPUTS OK case=MCA.cfg files="+fmt.Sprint(len(inputs))+" fingerprint="+tlc.Digest(inputs), last, "output %q for %v", r.stdout, inputs)
	for i, in := range inputs {
		assert.Equal(t, "INPUT OK path="+in.Path+" sha256="+in.SHA256, lines[i], "line %d", i)
		assert.Regexp(t, eventRE, lines[i], "line %d", i)
	}
	must.ContainsAll(t, r.stdout, "path=tla/MCA.cfg ", "path=tla/MCA.tla ", "path=tla/CASES.tsv#MCA.cfg ")
	assert.NotContains(t, r.stdout, "MCAStale", "the case's own files are not the list: %q", r.stdout)
	// The case is named as its plan names it, or without the extension.
	again := call(t, nil, "inputs", "--root", root, "--case", "MCA")
	require.Equal(t, 0, again.code, "MCA: %+v", again)
	assert.Equal(t, r.stdout, again.stdout, "MCA: %+v", again)
	// It is the fingerprint a run records.
	dir := filepath.Join(t.TempDir(), "run")
	_, jar := checkout(t)
	run := call(t, scriptedTLC(t, map[string]int{"MCA": 0, "MCABroken": 12, "MCAStale": 13, "MCCard": 0}, nil), "run", "--root", root, "--jar", jar, "--dir", dir, "--group", "alpha")
	require.Equal(t, 0, run.code, "%+v", run)
	recs, err := tlc.ReadRecordsFile(filepath.Join(dir, tlc.RunsFile))
	require.NoError(t, err, "records %+v, inputs digest %s over %d", recs, tlc.Digest(inputs), len(inputs))
	assert.Equal(t, tlc.Digest(inputs), recs[0].InputSHA256, "records %+v", recs)
	assert.Equal(t, len(inputs), recs[0].InputFiles, "records %+v", recs)
}

func TestInputsRefusesWhatItCannotAnswer(t *testing.T) {
	t.Parallel()
	root, _ := checkout(t)
	cases := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"no case", []string{"inputs", "--root", root}, 2, "missing required --case"},
		{"unknown case", []string{"inputs", "--root", root, "--case", "MCNope.cfg"}, 2, `no case "MCNope.cfg"`},
		{"a positional", []string{"inputs", "--root", root, "--case", "MCA.cfg", "extra"}, 2, "unexpected argument"},
		{"an unknown flag", []string{"inputs", "--all"}, 2, "unknown flag"},
		{"no plan", []string{"inputs", "--root", t.TempDir(), "--case", "MCA.cfg"}, 2, "the case plan is refused"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := call(t, nil, tc.args...)
			require.Equal(t, tc.code, r.code)
			require.Empty(t, r.stdout)
			assert.Contains(t, r.stderr, tc.want)
			assert.Contains(t, r.stderr, "; run: tlacheck ")
		})
	}
	// A module that names one that is nowhere is refused, naming it.
	require.NoError(t, os.WriteFile(filepath.Join(root, "tla", "MCA.tla"), []byte("EXTENDS Nowhere\n"), 0o644))
	r := call(t, nil, "inputs", "--root", root, "--case", "MCA.cfg")
	require.Equal(t, 2, r.code)
	assert.Contains(t, r.stderr, "names Nowhere")
}

func TestTableAndMemberReportEachStepAndStopAtTheFirstFailure(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	// Every table step is answered "the model passes": the contracts hold and
	// the first finding does not violate its invariant.
	passing := scriptedTLC(t, map[string]int{}, nil)
	r := call(t, passing, "table", "--root", root, "--jar", jar, "--dir", filepath.Join(t.TempDir(), "t"), "--mode", "all")
	require.Equal(t, 1, r.code, "%+v", r)
	must.ContainsAll(t, r.stdout, "TABLE OK case=MCTableMachine verdict=pass", "TABLE OK case=MCTableFixedPoint verdict=pass")
	must.ContainsAll(t, r.stderr, "TABLE FAIL case=MCTableOnePlace verdict=fail", "TABLE FAIL mode=all cases=3")
	r = call(t, passing, "table", "--root", root, "--jar", jar, "--dir", filepath.Join(t.TempDir(), "t"), "--mode", "contracts")
	require.Equal(t, 0, r.code, "contracts: %+v", r)
	assert.Contains(t, r.stdout, "TABLE OK mode=contracts cases=2", "contracts: %+v", r)
	// The member suite: its small positive instance passes on a pass exit.
	r = call(t, passing, "member", "--root", root, "--jar", jar, "--dir", filepath.Join(t.TempDir(), "m"), "--suite", "small")
	require.Equal(t, 1, r.code, "member: %+v", r)
	assert.Contains(t, r.stdout, "MEMBER OK case=MCMemberFixedPoint verdict=pass", "member: %+v", r)
	assert.Contains(t, r.stderr, "MEMBER FAIL case=MCMemberBrokenMove verdict=fail", "member: %+v", r)
	for _, args := range [][]string{{"table", "--mode", "nonsense"}, {"member", "--suite", "nonsense"}, {"member", "--workers", "0"}} {
		r = call(t, passing, append([]string{args[0], "--root", root, "--jar", jar, "--dir", t.TempDir()}, args[1:]...)...)
		assert.Equal(t, 2, r.code, "%v: %+v", args, r)
		assert.Equal(t, 1, strings.Count(r.stderr, "\n"), "%v: %+v", args, r)
		assert.NotContains(t, r.stdout, "CASE", "%v: %+v", args, r)
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
	require.Equal(t, 2, r.code, "witnesses without redis: %+v", r)
	must.ContainsAll(t, r.stderr, "redis-server is not on PATH", "run: tlacheck witnesses --redis-server /path/to/redis-server")
	e, out, errs = testEnv(t, nil)
	noRedis(&e)
	r = do(e, out, errs, "replay", "--root", root, "--jar", jar, "--source", "table.lua", "--dir", t.TempDir())
	require.Equal(t, 2, r.code, "replay without redis: %+v", r)
	assert.Contains(t, r.stderr, "redis-server is not on PATH", "replay without redis: %+v", r)
	assert.NotContains(t, r.stdout, "REPLAY", "replay without redis: %+v", r)
	// An unreadable source is a refusal of the environment (exit 2), not a
	// finding that failed.
	e, out, errs = testEnv(t, nil)
	e.lookPath = func(name string) (string, error) { return "/nonexistent/" + name, nil }
	r = do(e, out, errs, "witnesses", filepath.Join(t.TempDir(), "gone.lua"))
	require.Equal(t, 2, r.code, "witnesses of a missing source: %+v", r)
	assert.NotContains(t, r.stderr, "WITNESS FAIL", "witnesses of a missing source: %+v", r)
	e, out, errs = testEnv(t, nil)
	e.lookPath = func(name string) (string, error) { return "/nonexistent/" + name, nil }
	r = do(e, out, errs, "replay", "--root", root, "--jar", jar, "--source", filepath.Join(t.TempDir(), "gone.lua"), "--dir", t.TempDir())
	require.Equal(t, 2, r.code, "replay of a missing source: %+v", r)
	assert.NotContains(t, r.stderr, "REPLAY FAIL", "replay of a missing source: %+v", r)
}

// A remedy is a command that pastes: the ones that point at help run through
// the real CLI and exit 0.
func TestEveryHelpRemedyRunsThroughTheCLI(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	cases := [][]string{
		{"run", "--root", root, "--jar", jar},
		{"replay"},
		{"merge"},
		{"groups", "--bogus"},
		{"table", "--mode", "x", "--root", root, "--jar", jar, "--dir", t.TempDir()},
	}
	remedy := regexp.MustCompile(`run: (tlacheck \S+ -h)$`)
	for _, args := range cases {
		args := args
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			r := call(t, nil, args...)
			m := remedy.FindStringSubmatch(strings.TrimSpace(r.stderr))
			require.NotNil(t, m, "no help remedy in %q", r.stderr)
			again := call(t, nil, strings.Fields(m[1])[1:]...)
			require.Equal(t, 0, again.code)
			assert.NotEmpty(t, again.stdout)
		})
	}
}

func TestEventsEscapeWhatCouldSplitTheLine(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	event(&b, "CASE", "FAIL", "path", "a b=c\nd")
	assert.Equal(t, "CASE FAIL path=a\\x20b\\x3dc\\x0ad\n", b.String())
}

// The README names the runner's files as the code classifies them, and tells the
// author of a model change where the jar comes from and how to check it.
func TestReadmeNamesTheRunnerFilesAndWhereTheJarComesFrom(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "tla", "README.md"))
	require.NoError(t, err)
	var bullet string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "- the runner's result files") {
			bullet = line
		}
	}
	require.NotEmpty(t, bullet, "the README has no bullet for the runner's result files")
	resultPart, bookkeepingPart, ok := strings.Cut(bullet, "The package's other non-test files")
	require.True(t, ok, "the README's bullet does not list the bookkeeping files")
	bookkeepingPart, _, _ = strings.Cut(bookkeepingPart, ";")
	for _, name := range tlc.ResultFiles {
		assert.Contains(t, resultPart, "`"+name+"`")
		assert.NotContains(t, bookkeepingPart, "`"+name+"`")
	}
	for _, name := range tlc.BookkeepingFiles {
		assert.Contains(t, bookkeepingPart, "`"+name+"`")
	}
	for _, want := range []string{"downloads nothing", "so the jar comes from you", "sha256sum /path/to/tla2tools.jar", "has to print the hash the records name", "merge without `--keep`"} {
		assert.Contains(t, string(raw), want)
	}
}
