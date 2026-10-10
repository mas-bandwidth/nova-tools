package ci

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/pkgselect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// cert_race_shards_class_test.go holds certification.yml's `test` job, the
// whole-tree race run on the GitHub-hosted runners, under the two-minute cap by
// shard count. Unsharded (`go build ./...`, `go vet ./...` and
// `go test -race ./...` on one runner) it was cancelled by the cap on every dev
// push since the cap landed in 27c9ffc66: runs 36355661583, 36356115183,
// 36357522017, 36357749379 and 36360296846 (2026-09-27).

// certRaceMinShards is the shard count floor per hosted OS: at least one shard
// for each certRaceHeavy package, now sixteen after run 37165758129.
var certRaceMinShards = map[string]int{
	"ubuntu-latest": 16,
	"macos-latest":  16,
}

// certRaceHeavy are the packages the race deal places first, one per shard:
// every live package over 15 s in this job's own hosted legs (the larger of
// ubuntu-latest and macos-latest, run 36375296705 at 3314df055 plus this
// change): internal/ci 49.3 s, cmd/nova-tokens 46.4, cmd/nova-sandbox 34.2,
// cmd/nova-self-talk 21.3, internal/update 19.1, cmd/nova-secrets 16.7,
// pkg/bus 16.6. Run 37158353472 also shows cmd/nova-sprint reaching the
// 75 s test timeout alongside internal/ci on macOS.
// Run 37159703304 puts internal/sprint/store (65.470 s on Linux) alongside
// cmd/nova-sandbox on a capped leg, so it also has a separate heavy slot.
// Run 37165758129 caps Linux shard 1 with internal/ci 67.708 s,
// internal/sprint/refmodel 60.236, cmd/nova-swarm 44.763 and internal/docs 18.834
// together. Its completed legs also measure internal/sprint 15.951 s,
// pkg/redisconn 18.169, pkg/config 17.741 and pkg/secrets 15.577.
// certification.yml's deal step spells the same list.
var certRaceHeavy = []string{
	"internal/ci", "cmd/nova-tokens", "cmd/nova-sandbox", "cmd/nova-self-talk",
	"internal/update", "cmd/nova-secrets", "cmd/nova-sprint",
	"internal/sprint/store", "cmd/nova-swarm", "internal/sprint/refmodel",
	"internal/docs", "internal/sprint", "pkg/redisconn", "pkg/config",
	"pkg/secrets",
}

const certRaceDealStep = "deal this shard's packages"

// certJobs parses certification.yml's jobs, and its test job's matrix.
func certJobs(t *testing.T) (map[string]ciJob, hostedMatrix) {
	t.Helper()
	raw := readFile(t, filepath.Join(repoRoot(t), ".github", "workflows", "certification.yml"))
	var wf struct {
		Jobs map[string]ciJob `yaml:"jobs"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(raw), &wf))
	var mx struct {
		Jobs map[string]struct {
			Strategy struct {
				Matrix hostedMatrix `yaml:"matrix"`
			} `yaml:"strategy"`
		} `yaml:"jobs"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(raw), &mx))
	return wf.Jobs, mx.Jobs["test"].Strategy.Matrix
}

// TestCertificationRaceShardsPartitionTheLiveTree: the race job keeps both
// hosted OSes at shards 1..n with n at least certRaceMinShards; its deal, run
// over the real `go list ./...` at each OS's n, lands every live package in
// exactly one shard and no deprecated one in any, with no two certRaceHeavy
// packages sharing a shard; and the race build cache is saved BEFORE the tests
// (a save left to a post step never runs in a job the cap cancels), with the
// dependency build and the tests both under -race and the tests uncached.
func TestCertificationRaceShardsPartitionTheLiveTree(t *testing.T) {
	t.Parallel()

	jobs, matrix := certJobs(t)
	job, ok := jobs["test"]
	require.True(t, ok, "certification.yml has no test job")
	assert.Equal(t, twoMinuteCap, job.TimeoutMinutes, "certification.yml test: timeout-minutes %d, want %d", job.TimeoutMinutes, twoMinuteCap)
	legs := hostedLegs(t, matrix)
	assert.Len(t, legs, len(certRaceMinShards), "certification.yml test runs OSes %v, want exactly %v", legs, certRaceMinShards)
	for runner, min := range certRaceMinShards {
		shards := legs[runner]
		n := len(shards)
		assert.GreaterOrEqual(t, n, min, "certification.yml test runs %d %s shards, want at least %d", n, runner, min)
		for i := 1; i <= n; i++ {
			got, ok := shards[i]
			assert.True(t, ok && got == n, "certification.yml test %s shard %d of %d: present=%v, deals over %d", runner, i, n, ok, got)
		}
	}

	deal := stepIndex(job, certRaceDealStep)
	require.GreaterOrEqual(t, deal, 0, "certification.yml test has no %q step", certRaceDealStep)
	script := job.Steps[deal].Run
	wantHeavy := `--heavy "` + strings.Join(certRaceHeavy, " ") + `"`
	assert.Contains(t, script, wantHeavy, "the deal step does not spell %s", wantHeavy)
	require.Contains(t, script, ciRunner+" deal --shards ${{ matrix.shards }} --shard ${{ matrix.shard }}", "the deal step does not deal the live list over matrix.shards through `ci deal`:\n%s", script)
	heavy := dealHeavy(t, script)

	want := liveRepoPackages(t)
	// The deal reads pkgselect.DeprecatedFile: a package under its internal/nsprint
	// prefix that no keep line names is dropped. No such package is in the tree
	// any more, so the control is one that is not.
	probe := "github.com/mas-bandwidth/nova-tools/internal/nsprint/deprecatedprobe"
	dep, err := pkgselect.LoadDeprecated(repoRoot(t))
	require.NoError(t, err)
	require.False(t, dep.LivePackage(probe), "the deprecated list kept %s; the stand-in for %s is not being read", probe, pkgselect.DeprecatedFile)

	// One deal per distinct shard count: two OSes at the same n deal the same.
	// n is the EXPANDED MATRIX's count, never certRaceMinShards: the minimum is
	// only the floor asserted above. Reversed witness (Stella's read of #4487,
	// stella-d0fb88089706): with the matrix at shard 1..9, shards: 9, and a deal
	// that dropped the last shard, a deal at the floor of eight passed while the
	// real nine-shard deal lost 13 live packages; dealing at len(legs[runner])
	// fails it naming all 13.
	runners := make([]string, 0, len(certRaceMinShards))
	for runner := range certRaceMinShards {
		runners = append(runners, runner)
	}
	sort.Strings(runners)
	counts := map[int]string{}
	for _, runner := range runners {
		n := len(legs[runner])
		counts[n] = strings.TrimSpace(counts[n] + " " + runner)
	}
	for n, runner := range counts {
		seen := map[string]int{}
		home := map[string]int{}
		for i := 1; i <= n; i++ {
			mine, err := pkgselect.Deal(want, heavy, n, i)
			require.NoError(t, err)
			for _, p := range mine {
				seen[p]++
				for _, h := range certRaceHeavy {
					if strings.HasSuffix(p, "/"+h) {
						prev, dup := home[h]
						assert.False(t, dup, "%s at %d shards: %s dealt twice (shards %d and %d)", runner, n, h, prev, i)
						home[h] = i
					}
				}
			}
		}
		var bad []string
		for _, p := range want {
			if seen[p] != 1 {
				bad = append(bad, p+" x"+strconv.Itoa(seen[p]))
			}
			delete(seen, p)
		}
		for p := range seen {
			bad = append(bad, p+" (not live)")
		}
		sort.Strings(bad)
		assert.Empty(t, bad, "%s at %d shards: the deal is not a partition of the %d live packages: %v", runner, n, len(want), bad)
		shardOf := map[int]string{}
		for _, h := range certRaceHeavy {
			i, ok := home[h]
			if !assert.True(t, ok, "%s at %d shards: heavy package %s is dealt to no shard (not a live package?)", runner, n, h) {
				continue
			}
			other, dup := shardOf[i]
			assert.False(t, dup, "%s at %d shards: %s and %s share shard %d", runner, n, other, h, i)
			shardOf[i] = h
		}
	}

	restore, deps, save, test := -1, -1, -1, -1
	for i, s := range job.Steps {
		switch {
		case s.Name == "restore the race build cache":
			restore = i
		case s.Name == "build the race-instrumented dependencies":
			deps = i
		case s.Name == "save the race build cache":
			save = i
		case strings.HasPrefix(s.Name, "test (shard"):
			test = i
		}
	}
	require.GreaterOrEqual(t, restore, 0, "certification.yml test is missing a step: restore=%d deps=%d save=%d test=%d", restore, deps, save, test)
	require.GreaterOrEqual(t, deps, 0, "certification.yml test is missing a step: restore=%d deps=%d save=%d test=%d", restore, deps, save, test)
	require.GreaterOrEqual(t, save, 0, "certification.yml test is missing a step: restore=%d deps=%d save=%d test=%d", restore, deps, save, test)
	require.GreaterOrEqual(t, test, 0, "certification.yml test is missing a step: restore=%d deps=%d save=%d test=%d", restore, deps, save, test)
	assert.True(t, restore < deps && deps < save && save < test, "certification.yml test must restore (%d), build the race dependencies (%d), save (%d), then test (%d), in that order", restore, deps, save, test)
	assert.Contains(t, job.Steps[deps].Run, ciRunner+" race-deps", "the dependency build is not `ci race-deps`:\n%s", job.Steps[deps].Run)
	verb := readFile(t, filepath.Join(repoRoot(t), "tools", "ci", "sel_racedeps.go"))
	assert.Contains(t, verb, `"go", "build", "-race"`, "`ci race-deps` does not build under -race, so the saved cache would not serve the race tests")
	assert.Contains(t, job.Steps[save].If, "cache-hit != 'true'", "the save runs on an exact hit too: if: %q", job.Steps[save].If)
	run := job.Steps[test].Run
	for _, w := range []string{"go test -race", "-count=1", "$HOSTED_PKGS"} {
		assert.Contains(t, run, w, "the test step does not carry %q: %s", w, run)
	}
}
