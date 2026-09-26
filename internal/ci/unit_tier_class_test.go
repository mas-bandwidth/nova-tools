package ci

import (
	"errors"
	"go/ast"
	"go/token"
	"os"
	"os/exec"
	"path"
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
// raises (2 s a package, 1 s a test); make test reads this file, the SLEEPS
// ledger and the load gate.
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
	for _, want := range []string{"--package-budget 2", "--test-budget 1", "--allowlist " + rel, "--sleeps " + sleepsLedger, "--max-load-per-cpu "} {
		if !strings.Contains(flags, want) {
			t.Errorf("Makefile SLOWTESTS_FLAGS = %q, lacks %q", flags, want)
		}
	}
}

// sleepsLedger is the SLEEPS ledger make test hands slowtests.
const sleepsLedger = "internal/ci/sleeps-skips_allowlist.txt"

// loadGate is the --max-load-per-cpu the Makefile's SLOWTESTS_FLAGS passes.
func loadGate(t *testing.T) float64 {
	t.Helper()
	mk := parseMakefile(t, filepath.Join(repoRoot(t), "Makefile"))
	m := regexp.MustCompile(`--max-load-per-cpu ([0-9.]+)`).FindStringSubmatch(mk.vars["SLOWTESTS_FLAGS"])
	if m == nil {
		t.Fatalf("Makefile SLOWTESTS_FLAGS = %q has no --max-load-per-cpu", mk.vars["SLOWTESTS_FLAGS"])
	}
	gate, err := strconv.ParseFloat(m[1], 64)
	if err != nil || gate <= 0 {
		t.Fatalf("--max-load-per-cpu %q is not a positive load", m[1])
	}
	return gate
}

// TestUnitBudgetsJudgeTheTestNotTheLoad: the unit tier's check, at the
// Makefile's own budgets and load gate, fails a test for what it does and never
// for a busy runner. The fixture is go test -json as a leg writes it: (i) a test
// that runs 0.99 s idle and 1.4 s on a 32-CPU box at load 20 (the shape of run
// 36261817989's twelve CI-SLOW lines) prints its CI-SLOW line and a CI-LOAD line
// saying the budgets were measured, not enforced, and exits 0; (ii) the same
// 1.4 s at load 2 is red; (iii) with the load unread (no sysctl, no /proc) the
// CI-LOAD line says measured, not enforced, and why; (iv) a test skipped with
// the SLEEPS marker that the ledger does not name is a CI-SLEEPS line and red at
// load 20, at load 2 and unread.
func TestUnitBudgetsJudgeTheTestNotTheLoad(t *testing.T) {
	t.Parallel()

	gate := loadGate(t)
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
	busy := slowtests.Load{Avg: 20, CPUs: 32, Known: true}
	idle := slowtests.Load{Avg: 2, CPUs: 32, Known: true}
	unread := slowtests.Load{CPUs: 32, Why: "no sysctl and no /proc/loadavg"}
	judge := func(fixture string, load slowtests.Load) (string, int) {
		events, err := slowtests.Parse(strings.NewReader(fixture))
		if err != nil {
			t.Fatal(err)
		}
		lines, code := slowtests.Verdict(slowtests.Judge(events, budgets), load, gate, sleepsLedger)
		return strings.Join(lines, "\n"), code
	}

	const slowLine = "CI-SLOW test=TestTakesOnePointFour package=example.com/busy seconds=1.4s budget=1s"
	out, code := judge(slow, busy)
	if code != 0 || !strings.Contains(out, slowLine) || !strings.Contains(out, "CI-LOAD load=20.00 cpus=32: budgets measured, not enforced") {
		t.Errorf("(i) a 1.4 s test at load 20 of 32 CPUs: exit %d, want 0 with its CI-SLOW line and a CI-LOAD line saying not enforced:\n%s", code, out)
	}
	out, code = judge(slow, idle)
	if code != 2 || !strings.Contains(out, slowLine) || !strings.Contains(out, "CI-LOAD load=2.00 cpus=32: budgets enforced") {
		t.Errorf("(ii) the same test at load 2 of 32 CPUs: exit %d, want 2 (red) with CI-LOAD enforced:\n%s", code, out)
	}
	out, code = judge(slow, unread)
	if code != 0 || !strings.Contains(out, slowLine) || !strings.Contains(out, "CI-LOAD load=unknown cpus=32: budgets measured, not enforced (the load could not be read: no sysctl and no /proc/loadavg") {
		t.Errorf("(iii) the same test with the load unread: exit %d, want 0 with a CI-LOAD line saying measured, not enforced, and why:\n%s", code, out)
	}
	for name, load := range map[string]slowtests.Load{"load 20": busy, "load 2": idle, "unread": unread} {
		out, code = judge(sleeps, load)
		if code != 2 || !strings.Contains(out, "CI-SLEEPS test=TestWaitsOnTheClock package=example.com/sleepy") {
			t.Errorf("(iv) an unledgered SLEEPS skip, %s: exit %d, want 2 (red at any load):\n%s", name, code, out)
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
	more, err := slowtests.ParseAllowlist(strings.NewReader(list + "internal/ci\tTestAMeasuredRow\t1.5\t0.99s@idle-2026-09-26\n"))
	if err != nil || len(more) != len(rows)+1 {
		t.Errorf("%s plus a measured row: %d rows, err %v; want %d", rel, len(more), err, len(rows)+1)
	}
}

// TestSleepsLedgerIsTheTreesSleepsSkips: internal/ci/sleeps-skips_allowlist.txt
// names exactly the top-level tests under cmd/ and internal/ whose body calls
// Skip with a string starting with the SLEEPS marker: a SLEEPS skip with no row
// is red (it would be a red leg at any load), and so is a row whose test no
// longer skips (the wait was fixed: delete the row).
func TestSleepsLedgerIsTheTreesSleepsSkips(t *testing.T) {
	t.Parallel()

	rows, err := slowtests.ParseSleeps(strings.NewReader(readFile(t, filepath.Join(repoRoot(t), filepath.FromSlash(sleepsLedger)))))
	if err != nil {
		t.Fatalf("%s: %v", sleepsLedger, err)
	}
	ledger := map[string]bool{}
	for _, row := range rows {
		ledger[row.Package+"\t"+row.Test] = true
	}
	tree := map[string]string{}
	for _, f := range repoTree(t).GoFilesUnder(true, "cmd", "internal") {
		if f.HasDirNamed("testdata") || f.AST == nil {
			continue
		}
		for _, name := range sleepsSkippers(f.AST) {
			tree[path.Dir(f.Rel)+"\t"+name] = f.Rel
		}
	}
	for key, rel := range tree {
		if !ledger[key] {
			t.Errorf("%s: %s skips with the SLEEPS marker but %s has no row for it; inject a clock or tag it //go:build functional (a new SLEEPS skip is red at any load)",
				rel, strings.Replace(key, "\t", " ", 1), sleepsLedger)
		}
	}
	for _, row := range rows {
		if _, ok := tree[row.Package+"\t"+row.Test]; !ok {
			t.Errorf("%s lists %s %s, but that test no longer skips with the SLEEPS marker; delete the row", sleepsLedger, row.Package, row.Test)
		}
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
