package ci

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
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

// slowAllowlistCeiling is the row count of internal/ci/slow-tests_allowlist.txt.
// It only goes down: a test made fast deletes its row and lowers this number.
const slowAllowlistCeiling = 37

// TestSlowAllowlistOnlyShrinks: the unit tier's slow-test allowlist has at
// most slowAllowlistCeiling rows, each naming a package directory that exists
// and (for a test row) a test declared in it, each with a budget above the
// default it raises (2 s a package, 1 s a test); make test reads this file.
func TestSlowAllowlistOnlyShrinks(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	const rel = "internal/ci/slow-tests_allowlist.txt"
	rows, err := slowtests.ParseAllowlist(strings.NewReader(readFile(t, filepath.Join(root, rel))))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	if len(rows) > slowAllowlistCeiling {
		t.Errorf("%s has %d rows, over the ceiling of %d; the list only shrinks", rel, len(rows), slowAllowlistCeiling)
	}
	for _, row := range rows {
		dir := filepath.Join(root, filepath.FromSlash(row.Package))
		files, _ := filepath.Glob(filepath.Join(dir, "*_test.go"))
		if len(files) == 0 {
			t.Errorf("%s lists %s, which has no tests; delete the row (the list only shrinks)", rel, row.Package)
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
			t.Errorf("%s lists %s %s, but no such test exists any more; delete the row and lower slowAllowlistCeiling (the list only shrinks)", rel, row.Package, row.Test)
		}
	}

	mk := parseMakefile(t, filepath.Join(root, "Makefile"))
	flags := mk.vars["SLOWTESTS_FLAGS"]
	for _, want := range []string{"--package-budget 2", "--test-budget 1", "--allowlist " + rel} {
		if !strings.Contains(flags, want) {
			t.Errorf("Makefile SLOWTESTS_FLAGS = %q, lacks %q", flags, want)
		}
	}
}
