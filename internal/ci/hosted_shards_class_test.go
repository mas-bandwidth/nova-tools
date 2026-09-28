package ci

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"gopkg.in/yaml.v3"
)

// hosted_shards_class_test.go holds test-hosted, the GitHub-hosted legs of
// dev's push run, under the two-minute cap by shard count. Dev push run
// 36269122367 at f7aa36530 (2026-09-26): at four shards per OS, ubuntu-latest's
// shard 3 was cancelled at 123 s and all four macos-latest shards at 125-173 s.
// The cap stays two minutes; the legs meet it by more shards.

// hostedMinShards is the shard count per hosted OS the four-shard run above
// justifies: ubuntu-latest from four to six, macos-latest from four to eight.
var hostedMinShards = map[string]int{
	"ubuntu-latest": 6,
	"macos-latest":  8,
}

const hostedDealStep = "deal this shard's packages"

// hostedHeavy are the packages the deal places first, one per shard, before the
// round-robin: cmd/nova-bus (46.4 s -short on the Studio) and cmd/nova-merge
// (28.6 s) held shard 3 of 4 together, the ubuntu leg cancelled at 123 s
// (reader measurement, #4421 round 2); cmd/nova-swarm is the reader's other named heavy
// command. cmd/nova-merge is under deprecated/ and in no deal. ci.yml's deal step spells
// the same list.
var hostedHeavy = []string{"cmd/nova-bus", "cmd/nova-swarm"}

type hostedMatrix struct {
	OS      []string         `yaml:"os"`
	Shard   []int            `yaml:"shard"`
	Exclude []map[string]any `yaml:"exclude"`
	Include []map[string]any `yaml:"include"`
}

// hostedLegs expands test-hosted's matrix the way GitHub does: the product of
// os and shard, less each exclude, then each include's keys added to every leg
// whose values it does not overwrite. It returns, per OS, the shard indices and
// the `shards` value each leg carries.
func hostedLegs(t *testing.T, m hostedMatrix) map[string]map[int]int {
	t.Helper()
	out := make(map[string]map[int]int)
	for _, runner := range m.OS {
		for _, shard := range m.Shard {
			excluded := false
			for _, ex := range m.Exclude {
				if fmt.Sprint(ex["os"]) == runner && fmt.Sprint(ex["shard"]) == strconv.Itoa(shard) {
					excluded = true
				}
			}
			if excluded {
				continue
			}
			shards := 0
			for _, in := range m.Include {
				if fmt.Sprint(in["os"]) != runner {
					continue
				}
				if s, ok := in["shard"]; ok && fmt.Sprint(s) != strconv.Itoa(shard) {
					continue
				}
				n, ok := in["shards"].(int)
				if !ok {
					t.Fatalf("test-hosted include for %s carries no integer shards: %v", runner, in)
				}
				shards = n
			}
			if out[runner] == nil {
				out[runner] = make(map[int]int)
			}
			out[runner][shard] = shards
		}
	}
	return out
}

// hostedWorkflowLegs reads the actual expanded matrix. The minimum shard counts
// are lower bounds checked separately, never the counts used to test a deal.
func hostedWorkflowLegs(t *testing.T) map[string]map[int]int {
	t.Helper()
	raw := readFile(t, filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	var wf struct {
		Jobs map[string]struct {
			Strategy struct {
				Matrix hostedMatrix `yaml:"matrix"`
			} `yaml:"strategy"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(raw), &wf); err != nil {
		t.Fatal(err)
	}
	return hostedLegs(t, wf.Jobs["test-hosted"].Strategy.Matrix)
}

// TestHostedShardsUnderTheCap: every ci.yml job declares timeout-minutes 2;
// test-hosted keeps both hosted OSes; each OS runs exactly shards 1..n with n
// at least hostedMinShards and every leg carrying that n; the deal step reads
// matrix.shards, and vet and test both read the deal it writes.
func TestHostedShardsUnderTheCap(t *testing.T) {
	t.Parallel()

	for name, job := range ciJobs(t) {
		if job.TimeoutMinutes != twoMinuteCap {
			t.Errorf("ci.yml job %s: timeout-minutes %d, want %d", name, job.TimeoutMinutes, twoMinuteCap)
		}
	}

	legs := hostedWorkflowLegs(t)
	for runner, min := range hostedMinShards {
		shards, ok := legs[runner]
		if !ok {
			t.Errorf("test-hosted has no %s legs", runner)
			continue
		}
		n := len(shards)
		if n < min {
			t.Errorf("test-hosted runs %d %s shards, want at least %d", n, runner, min)
		}
		for i := 1; i <= n; i++ {
			got, ok := shards[i]
			if !ok {
				t.Errorf("test-hosted %s has no shard %d of %d; its packages would run nowhere", runner, i, n)
				continue
			}
			if got != n {
				t.Errorf("test-hosted %s shard %d deals over %d shards, but the OS runs %d", runner, i, got, n)
			}
		}
	}
	if len(legs) != len(hostedMinShards) {
		t.Errorf("test-hosted runs OSes %v, want exactly %v", legs, hostedMinShards)
	}

	job := ciJobs(t)["test-hosted"]
	deal := stepIndex(job, hostedDealStep)
	if deal < 0 {
		t.Fatalf("test-hosted has no %q step", hostedDealStep)
	}
	if !strings.Contains(job.Steps[deal].Run, "n=${{ matrix.shards }}") {
		t.Errorf("the deal step does not divide by matrix.shards:\n%s", job.Steps[deal].Run)
	}
	vet, test := -1, -1
	for i, s := range job.Steps {
		switch {
		case strings.HasPrefix(s.Name, "vet"):
			vet = i
		case strings.HasPrefix(s.Name, "test (shard"):
			test = i
		}
	}
	if vet < deal || test < deal {
		t.Fatalf("test-hosted's vet (%d) and test (%d) steps must follow the deal (%d)", vet, test, deal)
	}
	for _, i := range []int{vet, test} {
		if !strings.Contains(job.Steps[i].Run, `PKGS="$HOSTED_PKGS"`) {
			t.Errorf("test-hosted step %q does not read the deal's HOSTED_PKGS", job.Steps[i].Name)
		}
	}
}

// liveScript is the deal step's filter by its real path: runStep runs the step
// in an empty directory, and the step reads the tree's list through
// .github/scripts/live-packages.sh (deprecated packages are never dealt).
func liveScript(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), ".github", "scripts", "live-packages.sh")
}

// TestHostedDealPartitionsTheTree runs the deal step itself over a stand-in
// package list at each OS's actual shard count: every package lands in exactly one
// shard, so more shards never drops a package.
func TestHostedDealPartitionsTheTree(t *testing.T) {
	t.Parallel()

	job := ciJobs(t)["test-hosted"]
	deal := stepIndex(job, hostedDealStep)
	if deal < 0 {
		t.Fatalf("test-hosted has no %q step", hostedDealStep)
	}
	const packages = 23
	for runner, shards := range hostedWorkflowLegs(t) {
		n := len(shards)
		seen := make(map[string]int)
		for i := 1; i <= n; i++ {
			script := job.Steps[deal].Run
			script = strings.ReplaceAll(script, "${{ matrix.shards }}", strconv.Itoa(n))
			script = strings.ReplaceAll(script, "${{ matrix.shard }}", strconv.Itoa(i))
			script = strings.ReplaceAll(script, "go list ./...", fmt.Sprintf("seq -f 'p%%02g' 1 %d", packages))
			script = strings.ReplaceAll(script, ".github/scripts/live-packages.sh", liveScript(t))
			env := filepath.Join(t.TempDir(), "env")
			runStep(t, script, "GITHUB_ENV="+env)
			b, err := os.ReadFile(env)
			if err != nil {
				t.Fatal(err)
			}
			line := strings.TrimSpace(string(b))
			if !strings.HasPrefix(line, "HOSTED_PKGS=") || strings.Contains(line, "\n") {
				t.Fatalf("%s shard %d wrote %q, want one HOSTED_PKGS= line", runner, i, line)
			}
			for _, p := range strings.Fields(strings.TrimPrefix(line, "HOSTED_PKGS=")) {
				seen[p]++
			}
		}
		var bad []string
		for j := 1; j <= packages; j++ {
			p := fmt.Sprintf("p%02d", j)
			if seen[p] != 1 {
				bad = append(bad, fmt.Sprintf("%s x%d", p, seen[p]))
			}
		}
		sort.Strings(bad)
		if len(bad) > 0 || len(seen) != packages {
			t.Errorf("%s at %d shards: the deal is not a partition: %v (%d distinct)", runner, n, bad, len(seen))
		}
	}
}

// TestHostedDealSplitsTheHeavyPackages runs the deal step over the real
// `go list ./...` at each OS's actual shard count (reader repro, #4421 round 2): no two
// hostedHeavy packages share a shard, every heavy package is in the tree, and
// the step's heavy list is hostedHeavy.
func TestHostedDealSplitsTheHeavyPackages(t *testing.T) {
	t.Parallel()
	job := ciJobs(t)["test-hosted"]
	deal := stepIndex(job, hostedDealStep)
	if deal < 0 {
		t.Fatalf("test-hosted has no %q step", hostedDealStep)
	}
	if want := `heavy="` + strings.Join(hostedHeavy, " ") + `"`; !strings.Contains(job.Steps[deal].Run, want) {
		t.Errorf("the deal step does not spell %s", want)
	}
	cmd := exec.Command("go", "list", "./...")
	cmd.Dir = repoRoot(t)
	cmd.Env = goenv.Clean(os.Environ())
	list, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	listFile := filepath.Join(t.TempDir(), "pkgs")
	if err := os.WriteFile(listFile, list, 0o644); err != nil {
		t.Fatal(err)
	}
	for runner, shards := range hostedWorkflowLegs(t) {
		n := len(shards)
		home := map[string]int{}
		for i := 1; i <= n; i++ {
			script := job.Steps[deal].Run
			script = strings.ReplaceAll(script, "${{ matrix.shards }}", strconv.Itoa(n))
			script = strings.ReplaceAll(script, "${{ matrix.shard }}", strconv.Itoa(i))
			script = strings.ReplaceAll(script, "go list ./...", "cat "+listFile)
			script = strings.ReplaceAll(script, ".github/scripts/live-packages.sh", liveScript(t))
			env := filepath.Join(t.TempDir(), "env")
			runStep(t, script, "GITHUB_ENV="+env)
			b, err := os.ReadFile(env)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range strings.Fields(strings.TrimPrefix(strings.TrimSpace(string(b)), "HOSTED_PKGS=")) {
				for _, h := range hostedHeavy {
					if strings.HasSuffix(p, "/"+h) {
						home[h] = i
					}
				}
			}
		}
		shardOf := map[int]string{}
		for _, h := range hostedHeavy {
			i, ok := home[h]
			if !ok {
				t.Errorf("%s at %d shards: heavy package %s is dealt to no shard (not in go list?)", runner, n, h)
				continue
			}
			if other, dup := shardOf[i]; dup {
				t.Errorf("%s at %d shards: %s and %s share shard %d", runner, n, other, h, i)
			}
			shardOf[i] = h
		}
	}
}
