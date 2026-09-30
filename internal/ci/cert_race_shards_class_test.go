package ci

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// cert_race_shards_class_test.go holds certification.yml's `test` job, the
// whole-tree race run on the GitHub-hosted runners, under the two-minute cap by
// shard count. Unsharded (`go build ./...`, `go vet ./...` and
// `go test -race ./...` on one runner) it was cancelled by the cap on every dev
// push since the cap landed in 27c9ffc66: runs 36355661583, 36356115183,
// 36357522017, 36357749379 and 36360296846 (2026-09-27).

// certRaceMinShards is the shard count floor per hosted OS: at least one shard
// for each certRaceHeavy package, and eight from the 2026-09-27 measurement.
var certRaceMinShards = map[string]int{
	"ubuntu-latest": 8,
	"macos-latest":  8,
}

// certRaceHeavy are the packages the race deal places first, one per shard:
// every live package over 15 s in this job's own hosted legs (the larger of
// ubuntu-latest and macos-latest, run 36375296705 at 3314df055 plus this
// change): internal/ci 49.3 s, cmd/nova-tokens 46.4, cmd/nova-sandbox 34.2,
// cmd/nova-self-talk 21.3, internal/update 19.1, cmd/nova-secrets 16.7,
// internal/bus 16.6.
// certification.yml's deal step spells the same list.
var certRaceHeavy = []string{
	"internal/ci", "cmd/nova-tokens", "cmd/nova-sandbox", "cmd/nova-self-talk",
	"internal/update", "cmd/nova-secrets", "internal/bus",
}

const certRaceDealStep = "deal this shard's packages"

// certJobs parses certification.yml's jobs, and its test job's matrix.
func certJobs(t *testing.T) (map[string]ciJob, hostedMatrix) {
	t.Helper()
	raw := readFile(t, filepath.Join(repoRoot(t), ".github", "workflows", "certification.yml"))
	var wf struct {
		Jobs map[string]ciJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(raw), &wf); err != nil {
		t.Fatal(err)
	}
	var mx struct {
		Jobs map[string]struct {
			Strategy struct {
				Matrix hostedMatrix `yaml:"matrix"`
			} `yaml:"strategy"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(raw), &mx); err != nil {
		t.Fatal(err)
	}
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
	if !ok {
		t.Fatal("certification.yml has no test job")
	}
	if job.TimeoutMinutes != twoMinuteCap {
		t.Errorf("certification.yml test: timeout-minutes %d, want %d", job.TimeoutMinutes, twoMinuteCap)
	}
	legs := hostedLegs(t, matrix)
	if len(legs) != len(certRaceMinShards) {
		t.Errorf("certification.yml test runs OSes %v, want exactly %v", legs, certRaceMinShards)
	}
	for runner, min := range certRaceMinShards {
		shards := legs[runner]
		n := len(shards)
		if n < min {
			t.Errorf("certification.yml test runs %d %s shards, want at least %d", n, runner, min)
		}
		for i := 1; i <= n; i++ {
			if got, ok := shards[i]; !ok || got != n {
				t.Errorf("certification.yml test %s shard %d of %d: present=%v, deals over %d", runner, i, n, ok, got)
			}
		}
	}

	deal := stepIndex(job, certRaceDealStep)
	if deal < 0 {
		t.Fatalf("certification.yml test has no %q step", certRaceDealStep)
	}
	script := job.Steps[deal].Run
	if want := `heavy="` + strings.Join(certRaceHeavy, " ") + `"`; !strings.Contains(script, want) {
		t.Errorf("the deal step does not spell %s", want)
	}
	if !strings.Contains(script, "go list ./... | bash .github/scripts/live-packages.sh | awk") || !strings.Contains(script, "n=${{ matrix.shards }}") {
		t.Fatalf("the deal step does not deal the live list over matrix.shards:\n%s", script)
	}

	list := repoGoList(t)
	listFile := filepath.Join(t.TempDir(), "pkgs")
	if err := os.WriteFile(listFile, list, 0o644); err != nil {
		t.Fatal(err)
	}
	live := exec.Command("bash", liveScript(t))
	live.Stdin = strings.NewReader(string(list))
	liveOut, err := live.Output()
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Fields(string(liveOut))
	if len(want) == 0 {
		t.Fatalf("live-packages.sh kept none of the %d packages", len(strings.Fields(string(list))))
	}
	// The script reads deprecated/PACKAGES: a package under its internal/nsprint
	// prefix that no keep line names is dropped. No such package is in the tree
	// any more, so the control is one that is not.
	probe := "github.com/mas-bandwidth/nova-tools/internal/nsprint/deprecatedprobe"
	pr := exec.Command("bash", liveScript(t))
	pr.Stdin = strings.NewReader(string(list) + probe + "\n")
	prOut, err := pr.Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(prOut), probe) {
		t.Fatalf("live-packages.sh kept %s; the stand-in for deprecated/PACKAGES is not being read", probe)
	}

	// One deal per distinct shard count: two OSes at the same n deal the same.
	// n is the EXPANDED MATRIX's count, never certRaceMinShards: the minimum is
	// only the floor asserted above. Reversed witness (Stella's read of #4487,
	// stella-d0fb88089706): with the matrix at shard 1..9, shards: 9, and the
	// deal's awk printing `if (s == i - 1 && !(n == 9 && s == 8))`, a deal at
	// the floor of eight passed while the real nine-shard deal lost 13 live
	// packages; dealing at len(legs[runner]) fails it naming all 13.
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
			s := strings.ReplaceAll(script, "${{ matrix.shards }}", strconv.Itoa(n))
			s = strings.ReplaceAll(s, "${{ matrix.shard }}", strconv.Itoa(i))
			s = strings.ReplaceAll(s, "go list ./...", "cat "+listFile)
			s = strings.ReplaceAll(s, ".github/scripts/live-packages.sh", liveScript(t))
			env := filepath.Join(t.TempDir(), "env")
			runStep(t, s, "GITHUB_ENV="+env)
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
				for _, h := range certRaceHeavy {
					if strings.HasSuffix(p, "/"+h) {
						if prev, dup := home[h]; dup {
							t.Errorf("%s at %d shards: %s dealt twice (shards %d and %d)", runner, n, h, prev, i)
						}
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
		if len(bad) > 0 {
			t.Errorf("%s at %d shards: the deal is not a partition of the %d live packages: %v", runner, n, len(want), bad)
		}
		shardOf := map[int]string{}
		for _, h := range certRaceHeavy {
			i, ok := home[h]
			if !ok {
				t.Errorf("%s at %d shards: heavy package %s is dealt to no shard (not a live package?)", runner, n, h)
				continue
			}
			if other, dup := shardOf[i]; dup {
				t.Errorf("%s at %d shards: %s and %s share shard %d", runner, n, other, h, i)
			}
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
	if restore < 0 || deps < 0 || save < 0 || test < 0 {
		t.Fatalf("certification.yml test is missing a step: restore=%d deps=%d save=%d test=%d", restore, deps, save, test)
	}
	if !(restore < deps && deps < save && save < test) {
		t.Errorf("certification.yml test must restore (%d), build the race dependencies (%d), save (%d), then test (%d), in that order", restore, deps, save, test)
	}
	if !strings.Contains(job.Steps[deps].Run, "go build -race") {
		t.Errorf("the dependency build is not -race, so the saved cache would not serve the race tests:\n%s", job.Steps[deps].Run)
	}
	if !strings.Contains(job.Steps[save].If, "cache-hit != 'true'") {
		t.Errorf("the save runs on an exact hit too: if: %q", job.Steps[save].If)
	}
	run := job.Steps[test].Run
	for _, w := range []string{"go test -race", "-count=1", "$HOSTED_PKGS"} {
		if !strings.Contains(run, w) {
			t.Errorf("the test step does not carry %q: %s", w, run)
		}
	}
}
