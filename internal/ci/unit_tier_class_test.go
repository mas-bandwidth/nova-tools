package ci

import (
	"errors"
	"go/ast"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/slowtests"
	"gopkg.in/yaml.v3"
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
	if err := yaml.Unmarshal([]byte(raw), &wf); err != nil {
		t.Fatal(err)
	}
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

// runStep runs one step's script under bash with a scrubbed environment and
// the extra variables given; ${{ }} expressions become x.
func runStep(t *testing.T, script string, env ...string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the step runs under bash on the self-hosted Linux and macOS runners")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	script = regexp.MustCompile(`\$\{\{[^}]*\}\}`).ReplaceAllString(script, "x")
	cmd := exec.Command(bash, "-e", "-c", script)
	cmd.Dir = t.TempDir()
	cmd.Env = append([]string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "HOME=" + t.TempDir()}, env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the step failed: %v\n%s", err, out)
	}
	return string(out)
}

const unitShimStep = "the unit tier refuses redis-server"

// unitGoStepMarker names ci.yml's Go toolchain step (the same text
// ci_gosetup_functional_test.go's goSetupMarker reads, which the unit build
// does not compile).
const unitGoStepMarker = `sdk=$(ls -d "$HOME"/sdk/go*/bin`
const unitShimMessage = "unit tier: redis-server is functional-only (build tag functional)"

// TestUnitTierRefusesRedisServer: the unit legs put a redis-server FIRST on
// PATH that prints why and exits 86 and install no real one, so a redis-backed
// test that was left untagged fails closed in testutil.Start under NOVA_CI=1
// with that line (TestStartFailsClosedOnTheUnitTierShim). The shim is run
// here, not read.
func TestUnitTierRefusesRedisServer(t *testing.T) {
	t.Parallel()

	test := ciJobs(t)["test"]
	shim := stepIndex(test, unitShimStep)
	if shim < 0 {
		t.Fatalf("ci.yml job test has no step %q", unitShimStep)
	}
	goSetup, testStep := -1, stepIndex(test, "test")
	for i, s := range test.Steps {
		if strings.Contains(s.Run, unitGoStepMarker) {
			goSetup = i
		}
		if strings.Contains(s.Run, "install-redis-server.sh") {
			t.Errorf("ci.yml job test step %q installs redis-server; the unit tier refuses one", s.Name)
		}
	}
	// GITHUB_PATH prepends, so the step written last is first on PATH: after
	// the Go step (which may add /opt/homebrew/bin, where a real one lives).
	if !(goSetup >= 0 && goSetup < shim && shim < testStep) {
		t.Errorf("ci.yml job test: the shim step is step %d, the Go step %d, the test step %d; want Go < shim < test", shim, goSetup, testStep)
	}
	if !strings.Contains(test.Steps[testStep].Run, "unit-tier-bin/redis-server") {
		t.Error("ci.yml job test's test step does not check that redis-server on PATH is the shim")
	}

	dir := unitTierShim(t)
	out, err := exec.Command(filepath.Join(dir, "redis-server"), "--port", "0").CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 86 || !strings.Contains(string(out), unitShimMessage) {
		t.Fatalf("the shim: err %v, output %q; want exit 86 and %q", err, out, unitShimMessage)
	}
	// testutil.Start failing closed on this shim is
	// TestStartFailsClosedOnTheUnitTierShim, in the functional-tagged
	// redis_ci_test.go: a file that calls Start is functional.
}

// unitTierShim runs ci.yml's shim step and returns the directory it put on
// GITHUB_PATH.
func unitTierShim(t *testing.T) string {
	t.Helper()
	test := ciJobs(t)["test"]
	i := stepIndex(test, unitShimStep)
	if i < 0 {
		t.Fatalf("ci.yml job test has no step %q", unitShimStep)
	}
	tmp := t.TempDir()
	ghPath := filepath.Join(tmp, "github_path")
	runStep(t, test.Steps[i].Run, "RUNNER_TEMP="+tmp, "GITHUB_PATH="+ghPath)
	added, err := os.ReadFile(ghPath)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(added))
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
		if i < 0 {
			t.Errorf("ci.yml job %s has no step %q", name, share)
			continue
		}
		env := filepath.Join(t.TempDir(), "github_env")
		runStep(t, job.Steps[i].Run, "NOVA_RUNNERS_PER_MACHINE=1", "GITHUB_ENV="+env)
		raw, err := os.ReadFile(env)
		if err != nil {
			t.Fatal(err)
		}
		want := "GOMAXPROCS=2"
		if runtime.NumCPU() < 2 {
			want = "GOMAXPROCS=1"
		}
		if got := strings.TrimSpace(string(raw)); got != want {
			t.Errorf("ci.yml job %s, one runner on a %d-core box: %q, want %q (at most two cores a leg)", name, runtime.NumCPU(), got, want)
		}
		for _, s := range job.Steps {
			if strings.Contains(s.Run, "GOTEST_P=") {
				t.Errorf("ci.yml job %s step %q sets GOTEST_P; the leg's cores are the Makefile's two", name, s.Name)
			}
		}
	}

	mk := parseMakefile(t, filepath.Join(repoRoot(t), "Makefile"))
	if got := mk.vars["GOTEST_P"]; got != "2" {
		t.Errorf("Makefile GOTEST_P = %q, want 2", got)
	}
	if recipe := strings.Join(mk.recipeFor("test"), "\n"); !strings.Contains(recipe, " -p 2 -parallel 2 ") {
		t.Errorf("make test does not pass -p 2 -parallel 2 (GOTEST_P):\n%s", recipe)
	}
	if recipe := strings.Join(mk.recipeFor("test-functional"), "\n"); !strings.Contains(recipe, " -p 2 ") {
		t.Errorf("make test-functional does not pass -p 2 (GOTEST_P):\n%s", recipe)
	}
}

// TestFunctionalTierRunsOnlyAsStreamsMerge: the functional job runs on
// merge_group, schedule and workflow_dispatch and never on a pull request, on
// the space pool under the two-minute cap, over test-packages' functional
// list, through `make test-functional`; ci-ok requires it when it ran.
func TestFunctionalTierRunsOnlyAsStreamsMerge(t *testing.T) {
	t.Parallel()

	jobs := ciJobs(t)
	job, ok := jobs["functional"]
	if !ok {
		t.Fatal("ci.yml has no functional job")
	}
	for _, ev := range []string{"merge_group", "schedule", "workflow_dispatch"} {
		if !strings.Contains(job.If, "github.event_name == '"+ev+"'") {
			t.Errorf("functional's if does not run on %s: %s", ev, job.If)
		}
	}
	if strings.Contains(job.If, "pull_request") || strings.Contains(job.If, "!=") && strings.Contains(job.If, "event_name !=") {
		t.Errorf("functional's if must name the events it runs on, never pull_request: %s", job.If)
	}
	if job.TimeoutMinutes != 2 {
		t.Errorf("functional timeout-minutes = %d, want 2", job.TimeoutMinutes)
	}
	if runsOn, _ := yaml.Marshal(job.RunsOn); !strings.Contains(string(runsOn), "space") {
		t.Errorf("functional runs-on %s, want the space pool", runsOn)
	}
	if !strings.Contains(jobs["test-packages"].Outputs["functional"], "steps.list.outputs.functional") {
		t.Error("test-packages does not output the functional list")
	}
	if strings.Contains(jobs["test-packages"].If, "schedule") {
		t.Errorf("test-packages must run on schedule for the nightly functional list: %s", jobs["test-packages"].If)
	}
	last := job.Steps[len(job.Steps)-1].Run
	if !strings.Contains(last, "make test-functional") {
		t.Errorf("functional's last step runs %q, want make test-functional", last)
	}
	if needs, _ := yaml.Marshal(jobs["ci-ok"].Needs); !strings.Contains(string(needs), "functional") {
		t.Error("ci-ok does not need functional")
	}
	ciOK := jobBody(readFile(t, filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml")), "ci-ok")
	if !strings.Contains(ciOK, `"functional:${{ needs.functional.result }}"`) {
		t.Error("ci-ok does not read functional's result")
	}

	mk := parseMakefile(t, filepath.Join(repoRoot(t), "Makefile"))
	recipe := strings.Join(mk.recipeFor("test-functional"), "\n")
	for _, want := range []string{"nova-ci functional", "-tags functional", "-count=1", "-timeout 100s"} {
		if !strings.Contains(recipe, want) {
			t.Errorf("make test-functional lacks %q:\n%s", want, recipe)
		}
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
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	for _, row := range rows {
		dir := filepath.Join(root, filepath.FromSlash(row.Package))
		files, _ := filepath.Glob(filepath.Join(dir, "*_test.go"))
		if len(files) == 0 {
			t.Errorf("%s lists %s, which has no tests; delete the row", rel, row.Package)
			continue
		}
		if row.Test == "" {
			if row.Seconds <= 2 {
				t.Errorf("%s: %s's package row is %gs, not above the 2 s default; delete it", rel, row.Package, row.Seconds)
			}
			continue
		}
		if row.Seconds <= 1 {
			t.Errorf("%s: %s %s is %gs, not above the 1 s default; delete it", rel, row.Package, row.Test, row.Seconds)
		}
		decl := "func " + row.Test + "("
		found := false
		for _, f := range files {
			if strings.Contains(readFile(t, f), decl) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s lists %s %s, but no such test exists any more; delete the row", rel, row.Package, row.Test)
		}
	}

	mk := parseMakefile(t, filepath.Join(root, "Makefile"))
	flags := mk.vars["SLOWTESTS_FLAGS"]
	for _, want := range []string{"--package-budget 2", "--test-budget 1", "--allowlist " + rel, "--sleeps " + sleepsLedger} {
		if !strings.Contains(flags, want) {
			t.Errorf("Makefile SLOWTESTS_FLAGS = %q, lacks %q", flags, want)
		}
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
	if got := strings.TrimSpace(mk.vars["SLOWTESTS_ENFORCE"]); got != "0" {
		t.Errorf("Makefile SLOWTESTS_ENFORCE = %q, want 0: a budget is enforced only where a caller asks (the nightly leg)", got)
	}
	if strings.Contains(mk.vars["SLOWTESTS_FLAGS"], "--enforce") || strings.Contains(mk.vars["SLOWTESTS_FLAGS"], "--max-load-per-cpu") {
		t.Errorf("Makefile SLOWTESTS_FLAGS = %q carries --enforce or a load gate; the verdict never reads the load", mk.vars["SLOWTESTS_FLAGS"])
	}
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
		if err != nil {
			t.Fatal(err)
		}
		lines, code := slowtests.Verdict(slowtests.Judge(events, budgets), load, enforce, sleepsLedger)
		return strings.Join(lines, "\n"), code
	}
	const slowLine = "CI-SLOW test=TestTakesOnePointFour package=example.com/busy seconds=1.4s budget=1s"
	for name, load := range loads {
		for _, enforce := range []bool{false, true} {
			want := 0
			if enforce {
				want = 2
			}
			out, code := judge(slow, load, enforce)
			if code != want || !strings.Contains(out, slowLine) || !strings.Contains(out, "CI-LOAD load=") {
				t.Errorf("a 1.4 s test, %s, enforce %v: exit %d, want %d with its CI-SLOW and CI-LOAD lines:\n%s", name, enforce, code, want, out)
			}
			out, code = judge(sleeps, load, enforce)
			if code != 2 || !strings.Contains(out, "CI-SLEEPS test=TestWaitsOnTheClock package=example.com/sleepy") {
				t.Errorf("an unledgered SLEEPS skip, %s, enforce %v: exit %d, want 2:\n%s", name, enforce, code, out)
			}
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
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	if _, err := slowtests.ParseAllowlist(strings.NewReader(list + "internal/ci\tTestAnUnmeasuredRow\t1.5\n")); err == nil {
		t.Errorf("%s plus an unmeasured row was read; the count rose without a measurement", rel)
	}
	more, err := slowtests.ParseAllowlist(strings.NewReader(list + "internal/ci\tTestAMeasuredRow\t1.5\t0.99s@space\n"))
	if err != nil || len(more) != len(rows)+1 {
		t.Errorf("%s plus a measured row: %d rows, err %v; want %d", rel, len(more), err, len(rows)+1)
	}
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

// TestNightlySpaceLegIsTheOnlyEnforcingLeg: a CI-SLOW line fails exactly one
// leg, the nightly whole-tree run on the space shards (the #4413 ruling).
// ci.yml's `test` job runs on schedule; test-packages deals the schedule's tree
// onto space only; the test step passes SLOWTESTS_ENFORCE=1 only from its
// schedule branch, gated on the space group, and nowhere spells
// SLOWTESTS_ENFORCE=0 (the push leg's old swallow of the CI-SLEEPS exit). The
// Makefile reads SLOWTESTS_ENFORCE only to pass --enforce, and carries the
// slowtests exit through whatever it says.
func TestNightlySpaceLegIsTheOnlyEnforcingLeg(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	test := jobBody(src, "test")
	if test == "" {
		t.Fatal("no test job in ci.yml")
	}
	for _, line := range strings.Split(test, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "if:") && strings.Contains(line, "!= 'schedule'") {
			t.Errorf("the test job's if excludes schedule (%s); the nightly space legs are where the budgets are enforced", strings.TrimSpace(line))
		}
	}
	if !strings.Contains(test, "NIGHTLY_ENFORCE: ${{ github.event_name == 'schedule' && matrix.entry.group == 'space' && '1' || '0' }}") {
		t.Error("the test step's NIGHTLY_ENFORCE is not 1 exactly on a schedule run's space legs")
	}
	var code []string
	for _, line := range strings.Split(src, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			code = append(code, line)
		}
	}
	if n := strings.Count(strings.Join(code, "\n"), "SLOWTESTS_ENFORCE=1"); n != 1 || !strings.Contains(test, `elif [ "$NIGHTLY_ENFORCE" = 1 ]; then`) {
		t.Errorf("ci.yml passes SLOWTESTS_ENFORCE=1 %d times; want once, from the test step's NIGHTLY_ENFORCE branch", n)
	}
	if strings.Contains(strings.Join(code, "\n"), "SLOWTESTS_ENFORCE=0") {
		t.Error("ci.yml passes SLOWTESTS_ENFORCE=0; the push leg's CI-SLEEPS exit is red and nothing spells the old swallow")
	}
	if !strings.Contains(jobBody(src, "test-packages"), `if [ "${{ github.event_name }}" = "schedule" ]; then`) {
		t.Error("test-packages has no schedule branch dealing the nightly tree onto the space shards")
	}

	mk := parseMakefile(t, filepath.Join(root, "Makefile"))
	recipe := strings.Join(mk.recipes["test"], "\n")
	if strings.Count(recipe, "SLOWTESTS_ENFORCE") != 1 || !strings.Contains(recipe, "$(if $(filter 1,$(SLOWTESTS_ENFORCE)),--enforce,)") {
		t.Errorf("the test recipe reads SLOWTESTS_ENFORCE other than to pass --enforce:\n%s", recipe)
	}
	if !strings.Contains(recipe, `|| { [ "$$status" -ne 0 ] || status=2; }; exit $$status`) {
		t.Errorf("the test recipe does not carry slowtests' exit through:\n%s", recipe)
	}
}

// TestMeasuredBenchesAreCIRunners: every bench an allowlist row may name as
// where it was measured (slowtests.Benches) is a machine ci.yml names, so
// `<seconds>s@<bench>` points at a runner a reader can find.
func TestMeasuredBenchesAreCIRunners(t *testing.T) {
	t.Parallel()

	src := readFile(t, filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	for _, b := range slowtests.Benches {
		if !regexp.MustCompile(`\b` + regexp.QuoteMeta(b) + `\b`).MatchString(src) {
			t.Errorf("slowtests.Benches names %q, which ci.yml never names", b)
		}
	}
}
