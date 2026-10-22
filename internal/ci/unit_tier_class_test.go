package ci

import (
	"errors"
	"go/ast"
	"go/token"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/mas-bandwidth/nova-tools/internal/ci/slowtests"
	"github.com/mas-bandwidth/nova-tools/pkg/pkgselect"
)

// unit_tier_class_test.go holds the two CI tiers of nova-tools#4328 to the
// workflow and the Makefile. Glenn 2026-09-26 11:20 AM ET: "Only run tests
// that you actually need to, according to the changes being made." "unit
// tests be < 2s (ideally <1) but also they must not be so aggressive that they
// fill a whole machine cores." "we should run functional tests, not on every
// small PR being merged or worked on, but only as we merge whole work
// streams."

// ciStep is one step of a ci.yml job, as far as these tests read it.
type ciStep struct {
	Name string `yaml:"name"`
	If   string `yaml:"if"`
	Run  string `yaml:"run"`
}

// ciJob is one ci.yml job, as far as these tests read it.
type ciJob struct {
	If             string            `yaml:"if"`
	Needs          any               `yaml:"needs"`
	RunsOn         any               `yaml:"runs-on"`
	TimeoutMinutes int               `yaml:"timeout-minutes"`
	Outputs        map[string]string `yaml:"outputs"`
	Steps          []ciStep          `yaml:"steps"`
}

func ciJobs(t *testing.T) map[string]ciJob {
	t.Helper()
	raw := readFile(t, filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	var wf struct {
		Jobs map[string]ciJob `yaml:"jobs"`
	}
	err := yaml.Unmarshal([]byte(raw), &wf)
	require.NoError(t, err)
	return wf.Jobs
}

// stepIndex is the index of the job's step with that name, or -1.
func stepIndex(job ciJob, name string) int {
	for i, s := range job.Steps {
		if s.Name == name {
			return i
		}
	}
	return -1
}

const unitShimStep = "the unit tier refuses redis-server"

// unitGoStepMarker names ci.yml's Go toolchain step (the same text
// ci_gosetup_functional_test.go's goSetupMarker reads, which the unit build
// does not compile).
const unitGoStepMarker = `sdk=$(ls -d "$HOME"/sdk/go*/bin`
const unitShimMessage = pkgselect.UnitShimMessage

// TestUnitTierRefusesRedisServer: the unit legs put a redis-server FIRST on
// PATH that prints why and exits 86 and install no real one, so a redis-backed
// test that was left untagged fails closed in testutil.Start under NOVA_CI=1
// with that line (TestStartFailsClosedOnTheUnitTierShim). The shim is run
// here, not read.
func TestUnitTierRefusesRedisServer(t *testing.T) {
	t.Parallel()

	test := ciJobs(t)["test"]
	shim := stepIndex(test, unitShimStep)
	require.GreaterOrEqualf(t, shim, 0, "ci.yml job test has no step %q", unitShimStep)
	goSetup, testStep := -1, stepIndex(test, "test")
	for i, s := range test.Steps {
		if strings.Contains(s.Run, unitGoStepMarker) {
			goSetup = i
		}
		assert.NotContainsf(t, s.Run, redisInstallCall, "ci.yml job test step %q installs redis-server; the unit tier refuses one", s.Name)
	}
	// GITHUB_PATH prepends, so the step written last is first on PATH: after
	// the Go step (which may add /opt/homebrew/bin, where a real one lives).
	assert.Truef(t, goSetup >= 0 && goSetup < shim && shim < testStep, "ci.yml job test: the shim step is step %d, the Go step %d, the test step %d; want Go < shim < test", shim, goSetup, testStep)
	assert.Containsf(t, test.Steps[shim].Run, ciRunner+" unit-tier-shim", "ci.yml job test's shim step does not write the shim through `ci unit-tier-shim`: %q", test.Steps[shim].Run)
	assert.Containsf(t, test.Steps[testStep].Run, ciRunner+" unit-test", "ci.yml job test's test step does not run `ci unit-test`, which checks that redis-server on PATH is the shim: %q", test.Steps[testStep].Run)
	root := repoRoot(t)
	verb := readFile(t, filepath.Join(root, "tools", "ci", "sel_unittest.go"))
	assert.True(t, strings.Contains(verb, `h.r.LookPath("redis-server")`) && strings.Contains(verb, "pkgselect.UnitShimDir") && strings.Contains(verb, "not the refusing shim"), "tools/ci/sel_unittest.go does not check that redis-server on PATH is the shim before it runs make test")

	dir := unitTierShim(t)
	out, err := exec.Command(filepath.Join(dir, "redis-server"), "--port", "0").CombinedOutput()
	var exitErr *exec.ExitError
	require.Truef(t, errors.As(err, &exitErr) && exitErr.ExitCode() == 86 && strings.Contains(string(out), unitShimMessage), "the shim: err %v, output %q; want exit 86 and %q", err, out, unitShimMessage)
	// testutil.Start failing closed on this shim is
	// TestStartFailsClosedOnTheUnitTierShim, in the functional-tagged
	// redis_ci_test.go: a file that calls Start is functional.
}

// unitTierShim writes the shim the way `ci unit-tier-shim` does and returns
// the directory it is in.
func unitTierShim(t *testing.T) string {
	t.Helper()
	shim, err := pkgselect.WriteUnitShim(t.TempDir())
	require.NoError(t, err)
	require.Equalf(t, "unit-tier-bin", filepath.Base(filepath.Dir(shim)), "the shim is at %s, want it under unit-tier-bin", shim)
	return filepath.Dir(shim)
}

// TestUnitLegTakesAtMostTwoCores: a leg's GOMAXPROCS share is min(share, 2)
// even on a box with one runner, in the unit legs and the functional legs, and
// `make test` holds go test's -p and -parallel to GOTEST_P, which is 2 and
// which ci.yml never raises.
func TestUnitLegTakesAtMostTwoCores(t *testing.T) {
	t.Parallel()

	const share = "take this runner's share of the machine, not all of it"
	jobs := ciJobs(t)
	for _, name := range []string{"test", "functional"} {
		job := jobs[name]
		i := stepIndex(job, share)
		if !assert.GreaterOrEqualf(t, i, 0, "ci.yml job %s has no step %q", name, share) {
			continue
		}
		got := strings.TrimSpace(job.Steps[i].Run)
		assert.Equalf(t, ciRunner+" runner-share", got, "ci.yml job %s step %q runs %q, want `%s runner-share`", name, share, got, ciRunner)
		for _, s := range job.Steps {
			assert.NotContainsf(t, s.Run, "GOTEST_P=", "ci.yml job %s step %q sets GOTEST_P; the leg's cores are the Makefile's two", name, s.Name)
		}
	}
	// The verb's number: one runner on a box of any size takes at most two
	// cores, and never fewer than one.
	for _, cores := range []int{1, 2, 3, 8, 64, runtime.NumCPU()} {
		want := 2
		if cores < 2 {
			want = 1
		}
		_, got := pkgselect.RunnerShare(cores, "1")
		assert.Equalf(t, want, got, "one runner on a %d-core box: a leg takes %d cores, want %d (at most two cores a leg)", cores, got, want)
	}

	mk := parseMakefile(t, filepath.Join(repoRoot(t), "Makefile"))
	got := mk.vars["GOTEST_P"]
	assert.Equalf(t, "2", got, "Makefile GOTEST_P = %q, want 2", got)
	recipe := strings.Join(mk.recipeFor("test"), "\n")
	assert.Containsf(t, recipe, " -p 2 -parallel 2 ", "make test does not pass -p 2 -parallel 2 (GOTEST_P):\n%s", recipe)
	recipe = strings.Join(mk.recipeFor("test-functional"), "\n")
	assert.Containsf(t, recipe, " --p 2 ", "make test-functional does not pass --p 2 (GOTEST_P) to tools/ci functional-run:\n%s", recipe)
}

// TestFunctionalTierRunsOnlyAsStreamsMerge: the functional job runs on
// merge_group, schedule and workflow_dispatch and never on a pull request, on
// the space pool under the two-minute cap, over test-packages' functional
// list, through `make test-functional`; ci-ok requires it when it ran.
func TestFunctionalTierRunsOnlyAsStreamsMerge(t *testing.T) {
	t.Parallel()

	jobs := ciJobs(t)
	job, ok := jobs["functional"]
	require.True(t, ok, "ci.yml has no functional job")
	for _, ev := range []string{"merge_group", "schedule", "workflow_dispatch"} {
		assert.Containsf(t, job.If, "github.event_name == '"+ev+"'", "functional's if does not run on %s: %s", ev, job.If)
	}
	assert.Falsef(t, strings.Contains(job.If, "pull_request") || strings.Contains(job.If, "!=") && strings.Contains(job.If, "event_name !="), "functional's if must name the events it runs on, never pull_request: %s", job.If)
	assert.Equalf(t, 2, job.TimeoutMinutes, "functional timeout-minutes = %d, want 2", job.TimeoutMinutes)
	runsOn, _ := yaml.Marshal(job.RunsOn)
	assert.Containsf(t, string(runsOn), "space", "functional runs-on %s, want the space pool", runsOn)
	assert.Contains(t, jobs["test-packages"].Outputs["functional"], "steps.list.outputs.functional", "test-packages does not output the functional list")
	assert.NotContainsf(t, jobs["test-packages"].If, "schedule", "test-packages must run on schedule for the nightly functional list: %s", jobs["test-packages"].If)
	last := job.Steps[len(job.Steps)-1].Run
	assert.Containsf(t, last, "make test-functional", "functional's last step runs %q, want make test-functional", last)
	needs, _ := yaml.Marshal(jobs["ci-ok"].Needs)
	assert.Contains(t, string(needs), "functional", "ci-ok does not need functional")
	ciOK := jobBody(readFile(t, filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml")), "ci-ok")
	assert.Contains(t, ciOK, `functional=${{ needs.functional.result }}`, "ci-ok does not read functional's result")

	mk := parseMakefile(t, filepath.Join(repoRoot(t), "Makefile"))
	recipe := strings.Join(mk.recipeFor("test-functional"), "\n")
	// The recipe is one call into tools/ci functional-run, with the tier's timeout; the verb
	// holds the selection (nova-ci functional) and the go test flags.
	for _, want := range []string{"tools/ci functional-run", "--timeout 100s"} {
		assert.Containsf(t, recipe, want, "make test-functional lacks %q:\n%s", want, recipe)
	}
	verb := readFile(t, filepath.Join(repoRoot(t), "tools", "ci", "functionalrun.go"))
	for _, want := range []string{`"./cmd/nova-ci", "functional"`, `"-tags", "functional"`, `"-count=1"`} {
		assert.Contains(t, verb, want, "tools/ci/functionalrun.go lacks %s: the functional tier's selection and go test flags live there", want)
	}
}

// TestSlowAllowlistRowsNameTheirMeasurement: every row of the unit tier's
// slow-test allowlist names the time it was measured at and where
// (`<seconds>s@<where>`), with a budget between that time and
// slowtests.MaxHeadroom times it, so the row count rises only by a measured row
// (the reader refuses any other); each row names a package directory that exists
// and (for a test row) a test declared in it, with a budget above the default it
// raises (2 s a package, 1 s a test); make test reads this file and the SLEEPS
// ledger.
func TestSlowAllowlistRowsNameTheirMeasurement(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	const rel = "internal/ci/slow-tests_allowlist.txt"
	rows, err := slowtests.ParseAllowlist(strings.NewReader(readFile(t, filepath.Join(root, rel))))
	require.NoErrorf(t, err, "%s: %v", rel, err)
	for _, row := range rows {
		dir := filepath.Join(root, filepath.FromSlash(row.Package))
		files, _ := filepath.Glob(filepath.Join(dir, "*_test.go"))
		if !assert.NotEmptyf(t, files, "%s lists %s, which has no tests; delete the row", rel, row.Package) {
			continue
		}
		if row.Test == "" {
			assert.Greaterf(t, row.Seconds, float64(2), "%s: %s's package row is %gs, not above the 2 s default; delete it", rel, row.Package, row.Seconds)
			continue
		}
		assert.Greaterf(t, row.Seconds, float64(1), "%s: %s %s is %gs, not above the 1 s default; delete it", rel, row.Package, row.Test, row.Seconds)
		decl := "func " + row.Test + "("
		found := false
		for _, f := range files {
			if strings.Contains(readFile(t, f), decl) {
				found = true
				break
			}
		}
		assert.Truef(t, found, "%s lists %s %s, but no such test exists any more; delete the row", rel, row.Package, row.Test)
	}

	mk := parseMakefile(t, filepath.Join(root, "Makefile"))
	flags := mk.vars["SLOWTESTS_FLAGS"]
	for _, want := range []string{"--package-budget 2", "--test-budget 1", "--allowlist " + rel, "--sleeps " + sleepsLedger} {
		assert.Containsf(t, flags, want, "Makefile SLOWTESTS_FLAGS = %q, lacks %q", flags, want)
	}
}

// sleepsLedger is the SLEEPS ledger make test hands slowtests.
const sleepsLedger = "internal/ci/sleeps-skips_allowlist.txt"

// TestUnitBudgetsJudgeTheTestNotTheLoad: the unit tier's check, at the
// Makefile's own flags, gives the same verdict at any load (the #4413 ruling).
// The fixture is go test -json as a leg writes it: a test that runs 1.4 s
// prints its CI-SLOW line and a CI-LOAD line and exits 0 at load 2, at load 20
// and with the load unread, because the Makefile passes --enforce only under
// SLOWTESTS_ENFORCE=1, which it defaults to 0; with enforce (the nightly leg) it
// is red at all three. A SLEEPS skip the ledger does not name is red at all
// three on both legs.
func TestUnitBudgetsJudgeTheTestNotTheLoad(t *testing.T) {
	t.Parallel()

	mk := parseMakefile(t, filepath.Join(repoRoot(t), "Makefile"))
	got := strings.TrimSpace(mk.vars["SLOWTESTS_ENFORCE"])
	assert.Equalf(t, "0", got, "Makefile SLOWTESTS_ENFORCE = %q, want 0: a budget is enforced only where a caller asks (the nightly leg)", got)
	assert.Falsef(t, strings.Contains(mk.vars["SLOWTESTS_FLAGS"], "--enforce") || strings.Contains(mk.vars["SLOWTESTS_FLAGS"], "--max-load-per-cpu"), "Makefile SLOWTESTS_FLAGS = %q carries --enforce or a load gate; the verdict never reads the load", mk.vars["SLOWTESTS_FLAGS"])
	budgets := slowtests.Budgets{Package: 2, Test: 1}
	slow := `{"Action":"run","Package":"example.com/busy","Test":"TestTakesOnePointFour"}
{"Action":"pass","Package":"example.com/busy","Test":"TestTakesOnePointFour","Elapsed":1.4}
{"Action":"pass","Package":"example.com/busy","Elapsed":1.5}
`
	sleeps := `{"Action":"run","Package":"example.com/sleepy","Test":"TestWaitsOnTheClock"}
{"Action":"output","Package":"example.com/sleepy","Test":"TestWaitsOnTheClock","Output":"    a_test.go:9: SLEEPS: needs a mocked clock or a functional test\n"}
{"Action":"skip","Package":"example.com/sleepy","Test":"TestWaitsOnTheClock","Elapsed":0}
{"Action":"pass","Package":"example.com/sleepy","Elapsed":0.1}
`
	loads := map[string]slowtests.Load{
		"load 20": {Avg: 20, CPUs: 32, Known: true},
		"load 2":  {Avg: 2, CPUs: 32, Known: true},
		"unread":  {CPUs: 32, Why: "no sysctl and no /proc/loadavg"},
	}
	judge := func(fixture string, load slowtests.Load, enforce bool) (string, int) {
		events, err := slowtests.Parse(strings.NewReader(fixture))
		require.NoError(t, err)
		lines, code := slowtests.Verdict(slowtests.Judge(events, budgets), load, enforce, sleepsLedger)
		return strings.Join(lines, "\n"), code
	}
	const slowLine = "CI-SLOW test=TestTakesOnePointFour package=example.com/busy seconds=1.4s budget=1s"
	for name, load := range loads {
		for _, enforce := range []bool{false, true} {
			want := 0
			if enforce {
				want = 1 // the check ran and said no; 2 is input it could not read
			}
			out, code := judge(slow, load, enforce)
			assert.Truef(t, code == want && strings.Contains(out, slowLine) && strings.Contains(out, "CI-LOAD load="), "a 1.4 s test, %s, enforce %v: exit %d, want %d with its CI-SLOW and CI-LOAD lines:\n%s", name, enforce, code, want, out)
			out, code = judge(sleeps, load, enforce)
			assert.Truef(t, code == 1 && strings.Contains(out, "CI-SLEEPS test=TestWaitsOnTheClock package=example.com/sleepy"), "an unledgered SLEEPS skip, %s, enforce %v: exit %d, want 1:\n%s", name, enforce, code, out)
		}
	}
}

// TestSlowAllowlistRatchetRefusesAnUnmeasuredRow: the allowlist's row count
// rises only by a row that names its measurement. The list as committed with
// one more row in the old three-column shape (no measured time, no place) is
// refused, so the count cannot rise that way; the same row with
// `<seconds>s@<where>` is read and the count is one higher.
func TestSlowAllowlistRatchetRefusesAnUnmeasuredRow(t *testing.T) {
	t.Parallel()

	const rel = "internal/ci/slow-tests_allowlist.txt"
	list := readFile(t, filepath.Join(repoRoot(t), filepath.FromSlash(rel)))
	rows, err := slowtests.ParseAllowlist(strings.NewReader(list))
	require.NoErrorf(t, err, "%s: %v", rel, err)
	_, err = slowtests.ParseAllowlist(strings.NewReader(list + "internal/ci\tTestAnUnmeasuredRow\t1.5\n"))
	assert.Errorf(t, err, "%s plus an unmeasured row was read; the count rose without a measurement", rel)
	more, err := slowtests.ParseAllowlist(strings.NewReader(list + "internal/ci\tTestAMeasuredRow\t1.5\t0.99s@space\n"))
	assert.Truef(t, err == nil && len(more) == len(rows)+1, "%s plus a measured row: %d rows, err %v; want %d", rel, len(more), err, len(rows)+1)
}

// sleepsSkippers names the top-level Test functions in f whose body calls a
// Skip method with a string literal starting with the SLEEPS marker.
func sleepsSkippers(f *ast.File) []string {
	var names []string
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Recv != nil || fd.Body == nil || !strings.HasPrefix(fd.Name.Name, "Test") {
			continue
		}
		found := false
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || found || len(call.Args) == 0 {
				return !found
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Skip" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if v, err := strconv.Unquote(lit.Value); err == nil && strings.HasPrefix(v, slowtests.SleepsMarker) {
				found = true
			}
			return !found
		})
		if found {
			names = append(names, fd.Name.Name)
		}
	}
	return names
}

// TestNightlyIsTheOnlyEnforcingLeg: a CI-SLOW line fails exactly one
// leg, the nightly whole-tree run on the space shards (the #4413 ruling).
// ci.yml's `test` job runs on schedule; test-packages deals the schedule's tree
// onto space only; the test step passes SLOWTESTS_ENFORCE=1 only from its
// schedule branch, gated on the space group, and nowhere spells
// SLOWTESTS_ENFORCE=0 (the push leg's old swallow of the CI-SLEEPS exit). The
// Makefile reads SLOWTESTS_ENFORCE only to pass --enforce, and carries the
// slowtests exit through whatever it says.
func TestNightlyIsTheOnlyEnforcingLeg(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	test := jobBody(src, "test")
	require.NotEmpty(t, test, "no test job in ci.yml")
	for _, line := range strings.Split(test, "\n") {
		assert.Falsef(t, strings.HasPrefix(strings.TrimSpace(line), "if:") && strings.Contains(line, "!= 'schedule'"), "the test job's if excludes schedule (%s); the nightly space legs are where the budgets are enforced", strings.TrimSpace(line))
	}
	assert.Contains(t, test, "NIGHTLY_ENFORCE: ${{ github.event_name == 'schedule' && matrix.entry.group == 'space' && '1' || '0' }}", "the test step's NIGHTLY_ENFORCE is not 1 exactly on a schedule run's space legs")
	var code []string
	for _, line := range strings.Split(src, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			code = append(code, line)
		}
	}
	// The workflow never spells either value: `ci unit-test` passes
	// SLOWTESTS_ENFORCE=1 from its nightly branch alone (pkgselect.UnitMakeArgs).
	assert.NotContains(t, strings.Join(code, "\n"), "SLOWTESTS_ENFORCE", "ci.yml spells SLOWTESTS_ENFORCE; the one place it is passed is the nightly branch of pkgselect.UnitMakeArgs")
	verb := readFile(t, filepath.Join(root, "tools", "ci", "sel_unittest.go"))
	assert.Contains(t, verb, `e.getenv("NIGHTLY_ENFORCE") == "1"`, "tools/ci/sel_unittest.go does not turn NIGHTLY_ENFORCE=1 into the nightly run")
	enforcing := 0
	for name, args := range map[string][]string{
		"the default leg":  pkgselect.UnitMakeArgs("./cmd/a", "", false),
		"a whole-tree run": pkgselect.UnitMakeArgs("./cmd/a", "--budget 60", false),
		"the nightly leg":  pkgselect.UnitMakeArgs("./cmd/a", "", true),
	} {
		joined := strings.Join(args, " ")
		assert.NotContainsf(t, joined, "SLOWTESTS_ENFORCE=0", "%s passes SLOWTESTS_ENFORCE=0; the push leg's CI-SLEEPS exit is red and nothing spells the old swallow", name)
		if strings.Contains(joined, "SLOWTESTS_ENFORCE=1") {
			enforcing++
			assert.Equalf(t, "the nightly leg", name, "%s enforces the budgets; only the nightly whole-tree run on the Linux shards does", name)
		}
	}
	assert.Equalf(t, 1, enforcing, "%d runs pass SLOWTESTS_ENFORCE=1, want exactly the nightly leg", enforcing)
	// The nightly run deals the whole tree onto the Linux legs alone, where the
	// budgets are enforced.
	nightly := pkgselect.Fanout("schedule", []string{"./cmd/a", "./cmd/nova-sandbox", "./internal/b"}, pkgselect.DarwinSensitive{}, pkgselect.Groups{Linux: "linux-legs", Mac: "mac-legs"}, true)
	assert.NotEmpty(t, nightly, "the nightly fan-out is empty")
	for _, leg := range nightly {
		assert.Truef(t, leg.Group == "linux-legs" && !strings.Contains(leg.Packages, "nova-sandbox"), "the nightly fan-out has a leg off the Linux group, or one holding a darwin-only package: %+v", leg)
	}

	mk := parseMakefile(t, filepath.Join(root, "Makefile"))
	recipe := strings.Join(mk.recipes["test"], "\n")
	assert.Truef(t, strings.Count(recipe, "SLOWTESTS_ENFORCE") == 1 && strings.Contains(recipe, "$(if $(filter 1,$(SLOWTESTS_ENFORCE)),--enforce,)"), "the test recipe reads SLOWTESTS_ENFORCE other than to pass --enforce:\n%s", recipe)
	assert.Containsf(t, recipe, `|| { [ "$$status" -ne 0 ] || status=2; }; exit $$status`, "the test recipe does not carry slowtests' exit through:\n%s", recipe)
}

// TestMeasuredBenchesAreCIRunners: the benches a row may name as where it was
// measured are read from ci.yml itself by slowtests.Benches, so the runner
// inventory has the one home ci.yml is and `<seconds>s@<bench>` points at a
// runner a reader can find (docs/STANDARD.md section 4).
func TestMeasuredBenchesAreCIRunners(t *testing.T) {
	t.Parallel()

	src := readFile(t, filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	benches, err := slowtests.Benches([]byte(src))
	require.NoErrorf(t, err, "slowtests.Benches(ci.yml): %v", err)
	assert.NotEmptyf(t, benches, "slowtests.Benches(ci.yml) = %v, want the labels its self-hosted runs-on lists and runner groups name", benches)
}
