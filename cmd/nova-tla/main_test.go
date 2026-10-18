package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/tlc"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

type result struct {
	code           int
	stdout, stderr string
}

// scriptedTLC answers each case with a recorded TLC output; nothing starts java.
func scriptedTLC(t *testing.T, code map[string]int, runs *[]tlc.Run) tlc.Executor {
	t.Helper()
	logs := map[int]string{}
	for status, name := range map[int]string{0: "pass.log", 12: "invariant.log", 13: "action.log"} {
		logs[status] = string(mustRead(t, filepath.Join("testdata", name)))
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

// testEnv is the command with nothing of the machine: no java, no environment,
// a clock that starts at 1000 and never moves.
func testEnv(t *testing.T, exec tlc.Executor) env {
	t.Helper()
	return env{
		getenv:      func(string) string { return "" },
		lookPath:    func(name string) (string, error) { return "/usr/bin/" + name, nil },
		cpus:        func() int { return 8 },
		javaVersion: func(string) (string, error) { return "21.0.12.1", nil },
		now:         func() time.Time { return time.Unix(1000, 0) },
		exec:        exec, goos: "linux", goarch: "amd64",
	}
}

// do runs one invocation of the command over e, in process.
func do(e env, args ...string) result {
	var out, errs bytes.Buffer
	code := newTool(e).RunContext(context.Background(), args, strings.NewReader(""), &out, &errs)
	return result{code, out.String(), errs.String()}
}

// call runs one verb invocation through a fresh test environment: it stands in
// for the setup pair that repeats before a check when nothing tweaks the env.
func call(t *testing.T, exec tlc.Executor, args ...string) result {
	t.Helper()
	return do(testEnv(t, exec), args...)
}

// parseOnly is the command with every verb stopped once its flags are read and
// checked: the tests hold documented commands to the real parser with it.
func parseOnly(e env, args ...string) result {
	tl := newTool(e)
	for i := range tl.Verbs {
		tl.Verbs[i].Run = func(*tool.Call) *tool.Out { return tool.Exit(0) }
	}
	var out, errs bytes.Buffer
	code := tl.RunContext(context.Background(), args, strings.NewReader(""), &out, &errs)
	return result{code, out.String(), errs.String()}
}

const header = "config\tmodule\texpected\tproperty\tdeadlock\tgroup\tgate\tdebt\n"

// checkedSources is the runner files CheckRunner compares, read from this
// module. The test binary is built from these files, so they are the bytes
// the embed holds (pkg/tlc TestEmbeddedSourcesAreTheCheckedFiles).
func checkedSources(t *testing.T) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, name := range tlc.CheckedFiles() {
		out[tlc.RunnerDir+"/"+name] = mustRead(t, filepath.Join("..", "..", "pkg", "tlc", name))
	}
	return out
}

// writeFiles writes each name under dir, making its directories.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(text), 0o644))
	}
}

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
	writeFiles(t, filepath.Join(root, "tla"), map[string]string{"CASES.tsv": plan, "MCA.tla": "model\n", "MCA.cfg": "c\n", "MCABroken.cfg": "c\n", "MCAStale.cfg": "c\n", "MCCard.cfg": "c\n"})
	jar = filepath.Join(t.TempDir(), "tla2tools.jar")
	require.NoError(t, os.WriteFile(jar, []byte("jar"), 0o644))
	return root, jar
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return raw
}

// lines is the non-empty lines of s.
func lines(s string) []string {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

var eventRE = regexp.MustCompile(`^[A-Z]+ (OK|FAIL)( [a-z0-9-]+=\S*)*$`)

// verbNames are the verbs the command declares, in banner order.
func verbNames() []string {
	var names []string
	for _, v := range newTool(env{}).Verbs {
		names = append(names, v.Name)
	}
	return names
}

func TestTheToolMeetsTheSkeletonsStandard(t *testing.T) {
	t.Parallel()
	assert.Empty(t, newTool(env{}).Problems(), "the banner, the how text, every effect and every flag's description")
	assert.Equal(t, []string{"run", "groups", "merge", "inputs", "table", "member", "replay", "witnesses"}, verbNames())
}

func TestHelpIsOnStdoutAtExitZeroForTheToolAndEveryVerb(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		r := call(t, nil, args...)
		assert.Equal(t, 0, r.code, "%v", args)
		assert.Empty(t, r.stderr, "%v", args)
		assert.Contains(t, r.stdout, "usage:\n  nova-tla run --dir <dir>", "%v", args)
		assert.Contains(t, r.stdout, "topics: runner", "%v", args)
		for _, v := range append(verbNames(), "version") {
			assert.Contains(t, r.stdout, "\n  nova-tla "+v, "%v: the banner does not list %s", args, v)
		}
	}
	for _, v := range verbNames() {
		for _, args := range [][]string{{v, "-h"}, {v, "--help"}, {"help", v}} {
			r := call(t, nil, args...)
			assert.Equal(t, 0, r.code, "%v", args)
			assert.Empty(t, r.stderr, "%v", args)
			assert.True(t, strings.HasPrefix(r.stdout, "usage: nova-tla "+v+" "), "%v: %q", args, r.stdout)
			for _, want := range []string{"output:", "example:\n", "  nova-tla " + v + " ", "flags:", "exit codes: 0 ", "effect: "} {
				assert.Contains(t, r.stdout, want, "%v", args)
			}
		}
	}
}

func TestHelpHasNoSideEffects(t *testing.T) {
	t.Parallel()
	e := testEnv(t, func(context.Context, tlc.Run, string) int { t.Error("TLC ran for help"); return 0 })
	e.lookPath = func(string) (string, error) { t.Error("a program was looked up for help"); return "", errors.New("no") }
	e.getenv = func(string) string { t.Error("the environment was read for help"); return "" }
	for _, v := range verbNames() {
		assert.Equal(t, 0, do(e, v, "-h").code, v)
	}
}

func TestAnUnknownVerbIsNamedAndTheVerbsAreListed(t *testing.T) {
	t.Parallel()
	r := call(t, nil, "bogus")
	assert.Equal(t, 2, r.code)
	assert.Empty(t, r.stdout)
	assert.Len(t, lines(r.stderr), 1)
	for _, want := range []string{`unknown verb "bogus"`, "run, groups, merge, inputs, table, member, replay, witnesses", "the help topics are runner", "run: nova-tla help"} {
		assert.Contains(t, r.stderr, want)
	}
	r = call(t, nil, "help", "bogus")
	assert.Equal(t, 2, r.code, "help of an unknown verb")
	assert.Contains(t, r.stderr, `unknown verb "bogus"`)
	r = call(t, nil)
	assert.Equal(t, 2, r.code, "no arguments")
	assert.Empty(t, r.stdout, "no arguments")
	assert.Contains(t, r.stderr, "no verb given; the verbs are run, groups,")
	assert.Contains(t, r.stderr, "run: nova-tla help")
}

func TestAMalformedInvocationIsRefusedNamingEveryProblem(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"an unknown flag", []string{"groups", "--nope"}, []string{"unknown flag --nope", "run: nova-tla groups -h"}},
		{"a late flag", []string{"witnesses", "table.lua", "--timeout", "1s"}, []string{`flag "--timeout" comes after a positional argument`}},
		{"a bad value", []string{"run", "--timeout", "soon"}, []string{"invalid value for --timeout", "run: nova-tla run -h"}},
		{"every missing flag at once", []string{"replay", "--jar", jar}, []string{"--source is required", "--dir is required"}},
		{"a missing flag", []string{"run", "--root", root, "--jar", jar}, []string{"--dir is required; it wants the working directory"}},
		{"two positionals for witnesses", []string{"witnesses", "a.lua", "b.lua"}, []string{"want exactly one table.lua after the flags, got 2"}},
		{"no records to merge", []string{"merge", "--out", "x.tsv", "--root", root}, []string{"no records files named"}},
		{"a zero timeout", []string{"table", "--dir", "d", "--timeout", "0s"}, []string{"--timeout must be positive"}},
		{"a trough flag without --bench", []string{"run", "--dir", "d", "--trough-poll", "5s"}, []string{"--trough-poll is taken only with --bench"}},
		{"a bench with a parallel width", []string{"run", "--dir", "d", "--bench", "b", "--parallel", "2"}, []string{"--parallel is not taken with --bench"}},
		{"a negative load", []string{"run", "--dir", "d", "--bench", "b", "--load-below", "-1"}, []string{"--load-below, --trough-wait and --trough-poll take a load of 0 or more"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := call(t, nil, tc.args...)
			assert.Equal(t, 2, r.code)
			assert.Empty(t, r.stdout)
			for _, l := range lines(r.stderr) {
				assert.True(t, strings.HasPrefix(l, strings.ToUpper(tc.args[0])+" REFUSED: "), "the refusal does not start with the verb's token: %q", l)
				assert.Contains(t, l, "; run: nova-tla ", "every refusal names the next command")
			}
			for _, want := range tc.want {
				assert.Contains(t, r.stderr, want)
			}
		})
	}
}

func TestALiteralDoubleDashEndsTheFlags(t *testing.T) {
	t.Parallel()
	// After "--" a leading dash is a file name, not a flag; the verb then
	// refuses the file for what it is.
	e := testEnv(t, nil)
	e.lookPath = func(name string) (string, error) { return "/nonexistent/" + name, nil }
	r := do(e, "witnesses", "--", "-table.lua")
	assert.NotContains(t, r.stderr, "comes after a positional", "a literal after -- was refused as a late flag")
	assert.Contains(t, r.stderr, "-table.lua")
}

func TestGroupsPrintsTheRequiredGroupsAsOneLineOfJSON(t *testing.T) {
	t.Parallel()
	root, _ := checkout(t)
	r := call(t, nil, "groups", "--root", root)
	require.Equal(t, 0, r.code, "%+v", r)
	assert.Empty(t, r.stderr)
	assert.Equal(t, "[\"alpha\",\"beta\"]\n", r.stdout)
	// --json carries the same line as the result's payload.
	r = call(t, nil, "groups", "--root", root, "--json")
	require.Equal(t, 0, r.code, "%+v", r)
	var res struct {
		Payload string `json:"payload"`
	}
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &res))
	assert.Equal(t, `["alpha","beta"]`, res.Payload)
	// The repository's own plan.
	r = call(t, nil, "groups", "--root", filepath.Join("..", ".."))
	require.Equal(t, 0, r.code, "%+v", r)
	var groups []string
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &groups))
	assert.GreaterOrEqual(t, len(groups), 5, "the repository's groups")
	// A root with no plan is a refusal and prints no payload.
	r = call(t, nil, "groups", "--root", t.TempDir())
	assert.Equal(t, 2, r.code, "a root with no plan")
	assert.Empty(t, r.stdout)
	assert.Contains(t, r.stderr, "GROUPS REFUSED: the case plan is refused")
}

func TestRunWritesRecordsAndReportsEachCase(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	var runs []tlc.Run
	dir := filepath.Join(t.TempDir(), "out")
	r := call(t, scriptedTLC(t, map[string]int{"MCA": 0, "MCABroken": 12, "MCAStale": 13}, &runs), "run", "--root", root, "--jar", jar, "--dir", dir, "--group", "alpha", "--timeout", "100s", "--workers", "1", "--parallel", "1")
	require.Equal(t, 0, r.code, "%+v", r)
	assert.Empty(t, r.stderr)
	got := lines(r.stdout)
	want := []string{"HELPER OK name=jar", "HELPER OK name=java path=/usr/bin/java", "HELPER OK name=java-version version=21.0.12.1", "CASE OK config=MCA.cfg result=PASS", "CASE OK config=MCABroken.cfg result=PASS", "RUN OK cases=2 records="}
	require.Len(t, got, len(want), "%q", got)
	for i, w := range want {
		assert.True(t, strings.HasPrefix(got[i], w), "line %d = %q, want prefix %q", i, got[i], w)
		assert.Regexp(t, eventRE, got[i], "line %d is not an event", i)
	}
	assert.Contains(t, got[0], "source=flag")
	assert.Contains(t, got[3], "exit=0 generated=15518 distinct=263")
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
	assert.Contains(t, r.stdout, "HELPER OK name=java-version version=21.0.12.1")
	recs, err := tlc.ReadRecordsFile(filepath.Join(dir, tlc.RunsFile))
	require.NoError(t, err)
	require.Len(t, recs, 2)
	assert.Equal(t, "21.0.12.1", recs[0].JavaVersion)
	assert.Equal(t, 1, recs[0].Workers)
	assert.Equal(t, 1, recs[1].Workers)
	e := testEnv(t, func(context.Context, tlc.Run, string) int { t.Error("TLC ran without a java version"); return 0 })
	e.javaVersion = func(string) (string, error) { return "", errors.New("java -version printed no quoted version") }
	dir2 := filepath.Join(t.TempDir(), "run2")
	r = do(e, "run", "--root", root, "--jar", jar, "--dir", dir2, "--group", "alpha")
	assert.Equal(t, 2, r.code, "%+v", r)
	assert.Contains(t, r.stderr, "the java version cannot be read")
	assert.Contains(t, r.stderr, "nothing was run")
	assert.NoFileExists(t, filepath.Join(dir2, tlc.RunsFile), "records written for a java with no version")
}

// The host column is the tool's own platform label, and there is no way to
// write a machine name: no flag, no environment variable.
func TestRunWritesThePlatformLabelAndNeverAHostName(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	e := testEnv(t, scriptedTLC(t, map[string]int{"MCA": 0, "MCABroken": 12}, nil))
	e.goarch, e.cpus = "arm64", func() int { return 64 }
	e.getenv = func(name string) string { return "HOSTNAME-or-" + name }
	dir := filepath.Join(t.TempDir(), "run")
	r := do(e, "run", "--root", root, "--jar", jar, "--dir", dir, "--group", "alpha")
	require.Equal(t, 0, r.code, "%+v", r)
	recs, err := tlc.ReadRecordsFile(filepath.Join(dir, tlc.RunsFile))
	require.NoError(t, err)
	require.Len(t, recs, 2)
	assert.Equal(t, "linux-arm64", recs[0].Host)
	assert.Equal(t, 64, recs[0].CPUs)
	assert.Equal(t, "linux-arm64", recs[1].Host)
	raw := string(mustRead(t, filepath.Join(dir, tlc.RunsFile)))
	assert.NotContains(t, raw, "HOSTNAME", "the records name a host")
	assert.NotContains(t, raw, "bench", "the records name a host")
	e = testEnv(t, func(context.Context, tlc.Run, string) int { t.Error("TLC ran on an unlisted platform"); return 0 })
	e.goarch = "sparc"
	r = do(e, "run", "--root", root, "--jar", jar, "--dir", filepath.Join(t.TempDir(), "r2"), "--group", "alpha")
	assert.Equal(t, 2, r.code, "an unlisted architecture: %+v", r)
	assert.Contains(t, r.stderr, `platform "linux-sparc" is not one the runner records`)
	r = call(t, nil, "run", "--root", root, "--jar", jar, "--dir", filepath.Join(t.TempDir(), "r3"), "--group", "alpha", "--host", "x")
	assert.Equal(t, 2, r.code, "a --host flag exists: %+v", r)
	assert.Contains(t, r.stderr, "unknown flag")
}

func TestRunReportsAFailingCaseOnStderrAndExitsOne(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	// The counterexample case's model passes: not what it declares.
	r := call(t, scriptedTLC(t, map[string]int{"MCA": 0, "MCABroken": 0}, nil), "run", "--root", root, "--jar", jar, "--dir", filepath.Join(t.TempDir(), "o"), "--group", "alpha")
	require.Equal(t, 1, r.code, "%+v", r)
	assert.Contains(t, r.stdout, "CASE OK config=MCA.cfg")
	assert.NotContains(t, r.stdout, "MCABroken")
	assert.NotContains(t, r.stdout, "RUN OK")
	assert.Contains(t, r.stderr, "CASE FAIL config=MCABroken.cfg result=FAIL")
	assert.Contains(t, r.stderr, "RUN FAIL cases=2")
	for _, l := range lines(r.stderr) {
		assert.Regexp(t, eventRE, strings.SplitN(l, ": ", 2)[0], "not an event")
	}
}

func TestRunRefusesWhatItCannotRunAndRunsNothing(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
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
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := testEnv(t, func(context.Context, tlc.Run, string) int { t.Error("TLC ran"); return 0 })
			if tc.tweak != nil {
				tc.tweak(&e)
			}
			args := append([]string{"run", "--root", root, "--jar", jar, "--dir", filepath.Join(t.TempDir(), "o")}, tc.args...)
			r := do(e, args...)
			assert.Equal(t, 2, r.code, "%+v", r)
			assert.Contains(t, r.stderr, tc.want)
			assert.Len(t, lines(r.stderr), 1)
			assert.NotContains(t, r.stdout, "CASE")
		})
	}
	// No jar: neither the flag nor TLC_JAR.
	r := call(t, nil, "run", "--root", root, "--dir", filepath.Join(t.TempDir(), "o"), "--group", "alpha")
	assert.Equal(t, 2, r.code, "no jar: %+v", r)
	assert.Contains(t, r.stderr, "neither --jar nor TLC_JAR")
	assert.Contains(t, r.stderr, "run: nova-tla run --jar /path/to/tla2tools.jar")
	// The environment names the jar.
	e := testEnv(t, scriptedTLC(t, map[string]int{"MCA": 0, "MCABroken": 12}, nil))
	e.getenv = func(k string) string {
		if k == tlc.JarEnv {
			return jar
		}
		return ""
	}
	r = do(e, "run", "--root", root, "--dir", filepath.Join(t.TempDir(), "o"), "--group", "alpha")
	assert.Equal(t, 0, r.code, "jar from the environment: %+v", r)
	assert.Contains(t, r.stdout, "source=env:TLC_JAR")
}

func TestRunNeverGrowsTheCheckout(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	exec := func(ctx context.Context, r tlc.Run, log string) int {
		// TLC's error trace lands beside the spec it was given.
		assert.NoError(t, os.WriteFile(filepath.Join(r.Dir, "MCA_TTrace_1.tla"), []byte("trace\n"), 0o644))
		return scriptedTLC(t, map[string]int{"MCA": 0, "MCABroken": 12}, nil)(ctx, r, log)
	}
	r := call(t, exec, "run", "--root", root, "--jar", jar, "--dir", filepath.Join(t.TempDir(), "o"), "--group", "alpha")
	require.Equal(t, 0, r.code, "%+v", r)
	m, err := filepath.Glob(filepath.Join(root, "tla", "*TTrace*"))
	require.NoError(t, err)
	assert.Empty(t, m, "the checkout gained a trace")
}

// runGroups runs each group of the checkout's plan into its own directory and
// returns the records file of each.
func runGroups(t *testing.T, root, jar string, groups ...string) []string {
	t.Helper()
	codes := map[string]int{"MCA": 0, "MCABroken": 12, "MCAStale": 13, "MCCard": 0}
	var files []string
	for _, group := range groups {
		dir := filepath.Join(t.TempDir(), group)
		r := call(t, scriptedTLC(t, codes, nil), "run", "--root", root, "--jar", jar, "--dir", dir, "--group", group)
		require.Equal(t, 0, r.code, "%s: %+v", group, r)
		files = append(files, filepath.Join(dir, tlc.RunsFile))
	}
	return files
}

func TestMergeJoinsTheRecordsOfEveryGroup(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	dirs := runGroups(t, root, jar, "alpha", "beta", "gamma")
	merged := filepath.Join(t.TempDir(), "RUNS.tsv")
	r := call(t, nil, append([]string{"merge", "--root", root, "--out", merged}, dirs...)...)
	require.Equal(t, 0, r.code, "%+v", r)
	assert.Contains(t, r.stdout, "MERGE OK runs=3 records=4 out=")
	recs, err := tlc.ReadRecordsFile(merged)
	require.NoError(t, err)
	require.Len(t, recs, 4)
	assert.Equal(t, "MCA.cfg", recs[0].Config)
	assert.Equal(t, "MCCard.cfg", recs[3].Config)
	// A run that is missing is a refusal, and the output is left alone.
	before := mustRead(t, merged)
	r = call(t, nil, "merge", "--root", root, "--out", merged, dirs[0], dirs[1])
	assert.Equal(t, 1, r.code, "an incomplete merge: %+v", r)
	assert.Contains(t, r.stderr, "MERGE FAIL")
	assert.Contains(t, r.stderr, "no record for 1 declared cases")
	assert.Equal(t, string(before), string(mustRead(t, merged)), "a refused merge changed the output")
	// A file that is not records is a refusal.
	junk := filepath.Join(t.TempDir(), "junk.tsv")
	require.NoError(t, os.WriteFile(junk, []byte("not\trecords\n"), 0o644))
	r = call(t, nil, "merge", "--root", root, "--out", merged, junk)
	assert.Equal(t, 2, r.code, "junk: %+v", r)
	assert.Contains(t, r.stderr, "MERGE REFUSED: cannot read records")
}

// A merge holds every record to the fingerprint of its own case at this
// checkout: a model edited since the runs refuses the records of the cases that
// read it and only those.
func TestMergeRefusesRecordsOfAModelEditedSinceTheRuns(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	runs := runGroups(t, root, jar, "alpha", "beta", "gamma")
	require.NoError(t, os.WriteFile(filepath.Join(root, "tla", "MCAStale.cfg"), []byte("edited since\n"), 0o644))
	merged := filepath.Join(t.TempDir(), "RUNS.tsv")
	r := call(t, nil, append([]string{"merge", "--root", root, "--out", merged}, runs...)...)
	assert.Equal(t, 1, r.code, "%+v", r)
	assert.Contains(t, r.stderr, "1 records were measured on other inputs than these: MCAStale.cfg (")
	assert.NotContains(t, r.stderr, "MCABroken")
	assert.NoFileExists(t, merged, "a refused merge wrote its output")
}

// After an edit only the groups that hold a case that reads it are run again,
// and the merge keeps the current records of the others: the records file the
// checkout commits is the same one a run of every group makes.
func TestOnlyTheStaleGroupsAreRunAndMergedOntoTheCommittedRecords(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	runs := filepath.Join(root, "tla", tlc.RunsFile)
	first := filepath.Join(t.TempDir(), "first.tsv")
	r := call(t, nil, append([]string{"merge", "--root", root, "--out", first}, runGroups(t, root, jar, "alpha", "beta", "gamma")...)...)
	require.Equal(t, 0, r.code, "%+v", r)
	require.NoError(t, os.WriteFile(runs, mustRead(t, first), 0o644))
	groups := func() string {
		r := call(t, nil, "groups", "--root", root, "--stale")
		require.Equal(t, 0, r.code, "%+v", r)
		require.Empty(t, r.stderr)
		return strings.TrimSpace(r.stdout)
	}
	assert.Equal(t, "[]", groups(), "stale groups of current records")
	// MCAStale.cfg is the only case of group beta; MCABroken.cfg and MCA.cfg are alpha's.
	require.NoError(t, os.WriteFile(filepath.Join(root, "tla", "MCAStale.cfg"), []byte("edited\n"), 0o644))
	assert.Equal(t, `["beta"]`, groups(), "stale groups after editing one configuration")
	// Without --keep the one group is an incomplete merge; with it, a complete one.
	merged := filepath.Join(t.TempDir(), "merged.tsv")
	beta := runGroups(t, root, jar, "beta")[0]
	r = call(t, nil, "merge", "--root", root, "--out", merged, beta)
	assert.Equal(t, 1, r.code, "no --keep: %+v", r)
	assert.Contains(t, r.stderr, "no record for 3 declared cases")
	r = call(t, nil, "merge", "--root", root, "--keep", runs, "--out", merged, beta)
	require.Equal(t, 0, r.code, "--keep: %+v", r)
	assert.Contains(t, r.stdout, "MERGE OK runs=1 records=4 ")
	got, err := tlc.ReadRecordsFile(merged)
	require.NoError(t, err)
	old, err := tlc.ReadRecordsFile(runs)
	require.NoError(t, err)
	for i := range got {
		assert.Equal(t, got[i].Config != "MCAStale.cfg", got[i] == old[i], "%s: kept only for the cases that did not read the edit", got[i].Config)
	}
	require.NoError(t, os.WriteFile(runs, mustRead(t, merged), 0o644))
	assert.Equal(t, "[]", groups(), "stale groups after the merge")
	// A kept record that is not current is not carried.
	require.NoError(t, os.WriteFile(filepath.Join(root, "tla", "MCA.tla"), []byte("edited model\n"), 0o644))
	r = call(t, nil, "merge", "--root", root, "--keep", runs, "--out", merged, runGroups(t, root, jar, "beta")[0])
	assert.Equal(t, 1, r.code, "a stale kept record: %+v", r)
	for _, want := range []string{
		"3 kept records are stale, measured on inputs their cases no longer read: MCA.cfg (group alpha), MCABroken.cfg (group alpha), MCCard.cfg (group gamma)",
		"nova-tla run --root " + root + " --jar <jar> --dir <a clean directory>/alpha --group alpha",
		"--group gamma", "then merge again with --keep",
	} {
		assert.Contains(t, r.stderr, want)
	}
	assert.NotContains(t, r.stderr, "no record for")
	assert.Equal(t, `["alpha","beta","gamma"]`, groups(), "stale groups after editing the module every case reads")
}

// The messages of a merge that cannot carry what it was asked to keep.
func TestMergeKeepNamesWhatItCannotCarry(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	runs := runGroups(t, root, jar, "alpha", "beta", "gamma")
	keep := filepath.Join(t.TempDir(), "keep.tsv")
	r := call(t, nil, append([]string{"merge", "--root", root, "--out", keep}, runs...)...)
	require.Equal(t, 0, r.code, "%+v", r)
	merged := filepath.Join(t.TempDir(), "merged.tsv")

	// A kept record of a case the plan no longer declares is dropped, by name.
	raw := string(mustRead(t, keep))
	extra := strings.Split(strings.Split(raw, "\n")[1], "\t")
	extra[0] = "MCGone.cfg"
	require.NoError(t, os.WriteFile(keep, []byte(raw+strings.Join(extra, "\t")+"\n"), 0o644))
	r = call(t, nil, "merge", "--root", root, "--keep", keep, "--out", merged, runs[0], runs[1], runs[2])
	require.Equal(t, 0, r.code, "a record for a case no longer in the plan: %+v", r)
	assert.Contains(t, r.stdout, "DROP OK config=MCGone.cfg why=not-in-the-plan keep=")
	recs, err := tlc.ReadRecordsFile(merged)
	require.NoError(t, err)
	assert.Len(t, recs, 4, "the plan's four")

	// A file in the old column layout is refused naming both layouts.
	old := filepath.Join(t.TempDir(), "old.tsv")
	oldText := "config\tmodule\tinput_sha256\tjar_sha256\thost\tstarted_utc\tgenerated\tdistinct\tseconds\texit\tresult\texpected\tproperty\tbudget\tmode\n"
	require.NoError(t, os.WriteFile(old, []byte(oldText), 0o644))
	r = call(t, nil, "merge", "--root", root, "--keep", old, "--out", merged, runs[0])
	assert.Equal(t, 2, r.code, "an old layout: %+v", r)
	for _, want := range []string{"cannot read the records to keep", "another layout: found 15 columns", "reads and writes 19", "take the tla/RUNS.tsv of the base branch"} {
		assert.Contains(t, r.stderr, want)
	}
	// groups --stale reads the committed records in the same way.
	require.NoError(t, os.WriteFile(filepath.Join(root, "tla", tlc.RunsFile), []byte(oldText), 0o644))
	r = call(t, nil, "groups", "--root", root, "--stale")
	assert.Equal(t, 2, r.code, "groups --stale on an old layout: %+v", r)
	assert.Contains(t, r.stderr, "another layout")
	assert.Contains(t, r.stderr, "take the tla/RUNS.tsv of the base branch")
}

// The commands tla/README.md shows are held to the real flag parser: each
// nova-tla invocation in a fenced block is run through the verb's own flags
// with the verb stopped once its flags are read and checked, so a flag that is
// renamed, dropped or misplaced turns this red. The README also has to show
// the refresh procedure's four verbs.
func TestReadmeCommandsParseUnderTheRealFlagParser(t *testing.T) {
	t.Parallel()
	raw := mustRead(t, filepath.Join("..", "..", "tla", "README.md"))
	invocation := regexp.MustCompile(`nova-tla\s+([a-z]+)((?:\s+[^\s|)&;><]+)*)`)
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
		e := testEnv(t, func(context.Context, tlc.Run, string) int { t.Errorf("%v ran TLC", args); return 0 })
		r := parseOnly(e, args...)
		assert.Equal(t, 0, r.code, "nova-tla %s: the real parser refuses it: %+v", strings.Join(args, " "), r)
		assert.Empty(t, r.stdout, "nova-tla %s", strings.Join(args, " "))
		assert.Empty(t, r.stderr, "nova-tla %s", strings.Join(args, " "))
	}
	for _, verb := range []string{"groups", "run", "merge", "inputs"} {
		assert.NotZero(t, shown[verb], "the README shows no nova-tla %s command", verb)
	}
	assert.GreaterOrEqual(t, len(commands), 8, "the commands found in the README's blocks: %v", commands)
	// The control: the parser does refuse a flag it does not have, a flag after a
	// positional and a missing flag value, so a pass above means something.
	for _, args := range [][]string{{"run", "--dir", "x", "--bogus"}, {"merge", "--out", "o", "a.tsv", "--keep", "k"}, {"groups", "--root"}} {
		assert.Equal(t, 2, parseOnly(testEnv(t, nil), args...).code, "%v was parsed", args)
	}
}

// A binary built from other runner files than the checkout's would call the
// wrong records stale, so every verb that takes a fingerprint refuses it.
func TestVerbsRefuseABinaryBuiltFromOtherRunnerFiles(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	dir := filepath.Join(root, "pkg", "tlc")
	built := checkedSources(t)
	files := map[string]string{}
	for path, raw := range built {
		files[path] = string(raw)
	}
	writeFiles(t, root, files)
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
		assert.NotContains(t, ok(args).stderr, "built from other runner files", "%s refused the matching runner files", name)
	}
	// One result file edited under the root: each refuses, naming it and the remedy.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "run.go"), []byte("another runner\n"), 0o644))
	for name, args := range verbs {
		r := ok(args)
		assert.Equal(t, 2, r.code, "%s: %+v", name, r)
		assert.Contains(t, r.stderr, "built from other runner files than the ones under "+root, name)
		assert.Contains(t, r.stderr, "pkg/tlc/run.go differ", name)
		assert.NotContains(t, r.stderr, "outcome.go", name)
		assert.Contains(t, r.stderr, "build nova-tla from this tree: go build -o /tmp/nova-tla ./cmd/nova-tla", name)
		assert.Contains(t, r.stderr, "; run: nova-tla help runner", name)
		if name != "run" {
			assert.Empty(t, r.stdout, name)
		}
	}
	// The list of inputs is computed by inputs.go: a binary built from another
	// one lists other inputs than the checkout does, and each verb refuses it.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "run.go"), built["pkg/tlc/run.go"], 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inputs.go"), []byte("another input list\n"), 0o644))
	for name, args := range verbs {
		r := ok(args)
		assert.Equal(t, 2, r.code, "%s with another input list: %+v", name, r)
		assert.Contains(t, r.stderr, "pkg/tlc/inputs.go differ", name)
		assert.NotContains(t, r.stderr, "run.go", name)
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inputs.go"), built["pkg/tlc/inputs.go"], 0o644))
	// A bookkeeping file that differs is no concern of the fingerprint.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "records.go"), []byte("another bookkeeping\n"), 0o644))
	assert.Equal(t, 0, ok(verbs["inputs"]).code, "a bookkeeping file that differs")
	// A missing result file is a difference too.
	require.NoError(t, os.Remove(filepath.Join(dir, "outcome.go")))
	r := ok(verbs["inputs"])
	assert.Equal(t, 2, r.code, "a missing result file: %+v", r)
	assert.Contains(t, r.stderr, "pkg/tlc/outcome.go differ")
}

func TestInputsPrintsWhatACaseReadsAndItsFingerprint(t *testing.T) {
	t.Parallel()
	root, _ := checkout(t)
	r := call(t, nil, "inputs", "--root", root, "--case", "MCA.cfg")
	require.Equal(t, 0, r.code, "%+v", r)
	assert.Empty(t, r.stderr)
	got := lines(r.stdout)
	src, err := tlc.SourceAt(root)
	require.NoError(t, err)
	inputs, err := src.Inputs("MCA.cfg")
	require.NoError(t, err)
	require.Len(t, got, len(inputs)+1, "output %q for %v", r.stdout, inputs)
	assert.Equal(t, "INPUTS OK case=MCA.cfg files="+fmt.Sprint(len(inputs))+" fingerprint="+tlc.Digest(inputs), got[len(got)-1])
	for i, in := range inputs {
		assert.Equal(t, "INPUT OK path="+in.Path+" sha256="+in.SHA256, got[i], "line %d", i)
		assert.Regexp(t, eventRE, got[i], "line %d", i)
	}
	for _, want := range []string{"path=tla/MCA.cfg ", "path=tla/MCA.tla ", "path=tla/CASES.tsv#MCA.cfg "} {
		assert.Contains(t, r.stdout, want, "the case's own files are the list")
	}
	assert.NotContains(t, r.stdout, "MCAStale")
	// The case is named as its plan names it, or without the extension.
	again := call(t, nil, "inputs", "--root", root, "--case", "MCA")
	require.Equal(t, 0, again.code, "MCA: %+v", again)
	assert.Equal(t, r.stdout, again.stdout)
	// It is the fingerprint a run records.
	dir := filepath.Join(t.TempDir(), "run")
	_, jar := checkout(t)
	run := call(t, scriptedTLC(t, map[string]int{"MCA": 0, "MCABroken": 12, "MCAStale": 13, "MCCard": 0}, nil), "run", "--root", root, "--jar", jar, "--dir", dir, "--group", "alpha")
	require.Equal(t, 0, run.code, "%+v", run)
	recs, err := tlc.ReadRecordsFile(filepath.Join(dir, tlc.RunsFile))
	require.NoError(t, err)
	assert.Equal(t, tlc.Digest(inputs), recs[0].InputSHA256)
	assert.Equal(t, len(inputs), recs[0].InputFiles)
}

func TestInputsRefusesWhatItCannotAnswer(t *testing.T) {
	t.Parallel()
	root, _ := checkout(t)
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no case":         {[]string{"inputs", "--root", root}, "--case is required"},
		"unknown case":    {[]string{"inputs", "--root", root, "--case", "MCNope.cfg"}, `no case "MCNope.cfg"`},
		"a positional":    {[]string{"inputs", "--root", root, "--case", "MCA.cfg", "extra"}, "unexpected argument"},
		"an unknown flag": {[]string{"inputs", "--all"}, "unknown flag"},
		"no plan":         {[]string{"inputs", "--root", t.TempDir(), "--case", "MCA.cfg"}, "the case plan is refused"},
	} {
		r := call(t, nil, tc.args...)
		assert.Equal(t, 2, r.code, "%s: %+v", name, r)
		assert.Empty(t, r.stdout, name)
		assert.Contains(t, r.stderr, tc.want, name)
		assert.Contains(t, r.stderr, "; run: nova-tla ", name)
	}
	// A module that names one that is nowhere is refused, naming it.
	require.NoError(t, os.WriteFile(filepath.Join(root, "tla", "MCA.tla"), []byte("EXTENDS Nowhere\n"), 0o644))
	r := call(t, nil, "inputs", "--root", root, "--case", "MCA.cfg")
	assert.Equal(t, 2, r.code, "an unresolved module: %+v", r)
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
	assert.Contains(t, r.stdout, "TABLE OK case=MCTableMachine verdict=pass")
	assert.Contains(t, r.stdout, "TABLE OK case=MCTableFixedPoint verdict=pass")
	assert.Contains(t, r.stderr, "TABLE FAIL case=MCTableOnePlace verdict=fail")
	assert.Contains(t, r.stderr, "TABLE FAIL mode=all cases=3 seconds=0.00", "the closing line's seconds are the injected clock's")
	r = call(t, passing, "table", "--root", root, "--jar", jar, "--dir", filepath.Join(t.TempDir(), "t"), "--mode", "contracts")
	assert.Equal(t, 0, r.code, "contracts: %+v", r)
	assert.Contains(t, r.stdout, "TABLE OK mode=contracts cases=2")
	// The member suite: its small positive instance passes on a pass exit.
	r = call(t, passing, "member", "--root", root, "--jar", jar, "--dir", filepath.Join(t.TempDir(), "m"), "--suite", "small")
	assert.Equal(t, 1, r.code, "member: %+v", r)
	assert.Contains(t, r.stdout, "MEMBER OK case=MCMemberFixedPoint verdict=pass")
	assert.Contains(t, r.stderr, "MEMBER FAIL case=MCMemberBrokenMove verdict=fail")
	for _, args := range [][]string{{"table", "--mode", "nonsense"}, {"member", "--suite", "nonsense"}, {"member", "--workers", "0"}} {
		r = call(t, passing, append([]string{args[0], "--root", root, "--jar", jar, "--dir", t.TempDir()}, args[1:]...)...)
		assert.Equal(t, 2, r.code, "%v: %+v", args, r)
		assert.Len(t, lines(r.stderr), 1, "%v", args)
		assert.NotContains(t, r.stdout, "CASE", "%v", args)
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
	e := testEnv(t, nil)
	noRedis(&e)
	r := do(e, "witnesses", "table.lua")
	assert.Equal(t, 2, r.code, "witnesses without redis: %+v", r)
	assert.Contains(t, r.stderr, "redis-server is not on PATH")
	assert.Contains(t, r.stderr, "run: nova-tla witnesses --redis-server /path/to/redis-server")
	e = testEnv(t, nil)
	noRedis(&e)
	r = do(e, "replay", "--root", root, "--jar", jar, "--source", "table.lua", "--dir", t.TempDir())
	assert.Equal(t, 2, r.code, "replay without redis: %+v", r)
	assert.Contains(t, r.stderr, "redis-server is not on PATH")
	assert.NotContains(t, r.stdout, "REPLAY")
	// An unreadable source is a refusal of the environment (exit 2), not a
	// finding that failed.
	e = testEnv(t, nil)
	e.lookPath = func(name string) (string, error) { return "/nonexistent/" + name, nil }
	r = do(e, "witnesses", filepath.Join(t.TempDir(), "gone.lua"))
	assert.Equal(t, 2, r.code, "witnesses of a missing source: %+v", r)
	assert.NotContains(t, r.stderr, "WITNESS FAIL")
	r = do(e, "replay", "--root", root, "--jar", jar, "--source", filepath.Join(t.TempDir(), "gone.lua"), "--dir", t.TempDir())
	assert.Equal(t, 2, r.code, "replay of a missing source: %+v", r)
	assert.NotContains(t, r.stderr, "REPLAY FAIL")
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
	remedy := regexp.MustCompile(`; run: (nova-tla [a-z]+(?: -h|))$`)
	for _, args := range refusals {
		r := call(t, nil, args...)
		for _, l := range lines(r.stderr) {
			m := remedy.FindStringSubmatch(l)
			if !assert.NotNil(t, m, "%v: no help remedy in %q", args, l) {
				continue
			}
			again := call(t, nil, strings.Fields(m[1])[1:]...)
			assert.Equal(t, 0, again.code, "%v: the remedy %q gave %+v", args, m[1], again)
			assert.NotEmpty(t, again.stdout, "%v: the remedy %q", args, m[1])
		}
	}
}

func TestEventsEscapeWhatCouldSplitTheLine(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	event(&b, "CASE", "FAIL", "path", "a b=c\nd")
	assert.Equal(t, "CASE FAIL path=a\\x20b\\x3dc\\x0ad\n", b.String())
	b.Reset()
	eventWhy(&b, "RUN", "FAIL", "two\nlines", "cases", "1")
	assert.Equal(t, "RUN FAIL cases=1: two\\x0alines\n", b.String())
}

// The README names the runner's files as the code classifies them, and tells the
// author of a model change where the jar comes from and how to check it.
func TestReadmeNamesTheRunnerFilesAndWhereTheJarComesFrom(t *testing.T) {
	t.Parallel()
	raw := mustRead(t, filepath.Join("..", "..", "tla", "README.md"))
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
		assert.Contains(t, resultPart, "`"+name+"`", "the README does not name the result file %s as one", name)
		assert.NotContains(t, bookkeepingPart, "`"+name+"`", "the README names the result file %s as bookkeeping", name)
	}
	for _, name := range tlc.BookkeepingFiles {
		assert.Contains(t, bookkeepingPart, "`"+name+"`", "the README does not name the bookkeeping file %s as one", name)
	}
	for _, want := range []string{"downloads nothing", "so the jar comes from you", "sha256sum /path/to/tla2tools.jar", "has to print the hash the records name", "merge without `--keep`"} {
		assert.Contains(t, string(raw), want, "the README does not say this about the jar")
	}
}
