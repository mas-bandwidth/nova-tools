package ci

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/pkgselect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
// round-robin: two heavy packages once held shard 3 of 4 together and the ubuntu
// leg was cancelled at 123 s (reader measurement, #4421 round 2); cmd/nova-worker
// is the one heavy command left (the git bus, 46.4 s -short, is removed).
// ci.yml's deal step spells the same list.
var hostedHeavy = []string{"cmd/nova-worker"}

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
				require.True(t, ok, "test-hosted include for %s carries no integer shards: %v", runner, in)
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
	err := yaml.Unmarshal([]byte(raw), &wf)
	require.NoError(t, err)
	return hostedLegs(t, wf.Jobs["test-hosted"].Strategy.Matrix)
}

// TestHostedShardsUnderTheCap: every ci.yml job declares timeout-minutes 2;
// test-hosted keeps both hosted OSes; each OS runs exactly shards 1..n with n
// at least hostedMinShards and every leg carrying that n; the deal step reads
// matrix.shards, and vet and test both read the deal it writes.
func TestHostedShardsUnderTheCap(t *testing.T) {
	t.Parallel()

	for name, job := range ciJobs(t) {
		assert.Equal(t, twoMinuteCap, job.TimeoutMinutes, "ci.yml job %s: timeout-minutes %d, want %d", name, job.TimeoutMinutes, twoMinuteCap)
	}

	legs := hostedWorkflowLegs(t)
	for runner, min := range hostedMinShards {
		shards, ok := legs[runner]
		if !assert.True(t, ok, "test-hosted has no %s legs", runner) {
			continue
		}
		n := len(shards)
		assert.GreaterOrEqual(t, n, min, "test-hosted runs %d %s shards, want at least %d", n, runner, min)
		for i := 1; i <= n; i++ {
			got, ok := shards[i]
			if !assert.True(t, ok, "test-hosted %s has no shard %d of %d; its packages would run nowhere", runner, i, n) {
				continue
			}
			assert.Equal(t, n, got, "test-hosted %s shard %d deals over %d shards, but the OS runs %d", runner, i, got, n)
		}
	}
	assert.Equal(t, len(hostedMinShards), len(legs), "test-hosted runs OSes %v, want exactly %v", legs, hostedMinShards)

	job := ciJobs(t)["test-hosted"]
	deal := stepIndex(job, hostedDealStep)
	require.GreaterOrEqual(t, deal, 0, "test-hosted has no %q step", hostedDealStep)
	assert.Contains(t, job.Steps[deal].Run, ciRunner+" deal --shards ${{ matrix.shards }} --shard ${{ matrix.shard }}", "the deal step does not deal this shard of matrix.shards through `ci deal`:\n%s", job.Steps[deal].Run)
	vet, test := -1, -1
	for i, s := range job.Steps {
		switch {
		case strings.HasPrefix(s.Name, "vet"):
			vet = i
		case strings.HasPrefix(s.Name, "test (shard"):
			test = i
		}
	}
	require.GreaterOrEqual(t, vet, deal, "test-hosted's vet (%d) and test (%d) steps must follow the deal (%d)", vet, test, deal)
	require.GreaterOrEqual(t, test, deal, "test-hosted's vet (%d) and test (%d) steps must follow the deal (%d)", vet, test, deal)
	for _, i := range []int{vet, test} {
		assert.Contains(t, job.Steps[i].Run, `PKGS="$HOSTED_PKGS"`, "test-hosted step %q does not read the deal's HOSTED_PKGS", job.Steps[i].Name)
	}
}

// dealHeavyRe reads the heavy list the deal step passes.
var dealHeavyRe = regexp.MustCompile(`--heavy "([^"]*)"`)

// dealHeavy is the heavy packages a workflow's deal step names.
func dealHeavy(t *testing.T, run string) []string {
	t.Helper()
	m := dealHeavyRe.FindStringSubmatch(run)
	require.NotNil(t, m, "the deal step names no --heavy list:\n%s", run)
	return strings.Fields(m[1])
}

// liveRepoPackages is the repository's own `go list ./...` through the same
// deprecated filter the deal reads: the live tree.
func liveRepoPackages(t *testing.T) []string {
	t.Helper()
	dep, err := pkgselect.LoadDeprecated(repoRoot(t))
	require.NoError(t, err)
	live := dep.Live(strings.Fields(string(repoGoList(t))))
	require.NotEmpty(t, live, "the live tree is empty")
	return live
}

// TestHostedDealPartitionsTheTree deals a stand-in package list with the
// deal's own function and heavy list at each OS's actual shard count: every
// package lands in exactly one shard, so more shards never drops a package.
func TestHostedDealPartitionsTheTree(t *testing.T) {
	t.Parallel()

	job := ciJobs(t)["test-hosted"]
	deal := stepIndex(job, hostedDealStep)
	require.GreaterOrEqual(t, deal, 0, "test-hosted has no %q step", hostedDealStep)
	heavy := dealHeavy(t, job.Steps[deal].Run)
	const packages = 23
	var list []string
	for j := 1; j <= packages; j++ {
		list = append(list, fmt.Sprintf("p%02d", j))
	}
	for runner, shards := range hostedWorkflowLegs(t) {
		n := len(shards)
		seen := make(map[string]int)
		for i := 1; i <= n; i++ {
			mine, err := pkgselect.Deal(list, heavy, n, i)
			require.NoError(t, err)
			for _, p := range mine {
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
		ok := len(bad) == 0 && len(seen) == packages
		assert.True(t, ok, "%s at %d shards: the deal is not a partition: %v (%d distinct)", runner, n, bad, len(seen))
	}
}

// TestHostedDealSplitsTheHeavyPackages deals the real live tree at each OS's
// actual shard count (reader repro, #4421 round 2): no two hostedHeavy packages
// share a shard, every heavy package is in the tree, and the step's heavy list is
// hostedHeavy.
func TestHostedDealSplitsTheHeavyPackages(t *testing.T) {
	t.Parallel()
	job := ciJobs(t)["test-hosted"]
	deal := stepIndex(job, hostedDealStep)
	require.GreaterOrEqual(t, deal, 0, "test-hosted has no %q step", hostedDealStep)
	want := `--heavy "` + strings.Join(hostedHeavy, " ") + `"`
	assert.Contains(t, job.Steps[deal].Run, want, "the deal step does not spell %s", want)
	heavy := dealHeavy(t, job.Steps[deal].Run)
	live := liveRepoPackages(t)
	for runner, shards := range hostedWorkflowLegs(t) {
		n := len(shards)
		home := map[string]int{}
		for i := 1; i <= n; i++ {
			mine, err := pkgselect.Deal(live, heavy, n, i)
			require.NoError(t, err)
			for _, p := range mine {
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
			if !assert.True(t, ok, "%s at %d shards: heavy package %s is dealt to no shard (not in go list?)", runner, n, h) {
				continue
			}
			other, dup := shardOf[i]
			assert.False(t, dup, "%s at %d shards: %s and %s share shard %d", runner, n, other, h, i)
			shardOf[i] = h
		}
	}
}
