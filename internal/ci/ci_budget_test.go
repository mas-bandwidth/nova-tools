package ci

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// ci_budget_test.go is the two-minute law, read off the workflow files as text
// rather than from any one job's log. The maintainer's rule is "CI checks per
// every CL, one minute ideal, two minutes maximum", applied to the COMPLETE
// required path, not to each job alone — so the fast tier is a design in
// ci.yml, and this test pins that design so a future edit that quietly pushes a
// check back over the budget is a red run instead of a slow morning.
//
// It reads the files as text on purpose: the repository has no YAML library in
// go.mod (standard library only), so the checks are shape checks over the
// lines, exactly as strict as the shape they assert and nothing more.
//
// Three invariants:
//   (a) every job in ci.yml declares timeout-minutes, and no job ON THE CL PATH
//       exceeds 2 — the aggregate ci-ok may be 1 — so the CL tier cannot
//       silently exceed the budget. A job whose `if:` runs it only on push to
//       main and on the nightly schedule is not on the CL path: no pull request
//       waits on it to merge, so the two-minute law does not reach it. It must
//       still declare a ceiling, which is what (a) checks for every job;
//   (b) every job name that left ci.yml in the split is present in the
//       certification workflow by the same name, and certification-ok needs
//       every one of them, so the split deleted nothing;
//   (c) every `uses:` in both files is owner/action@40-hex-sha, so an action
//       cannot drift under a mutable tag.

// jobKeyRe matches a job name: a key at exactly two spaces under `jobs:`.
var jobKeyRe = regexp.MustCompile(`^  ([a-zA-Z0-9_-]+):$`)

// timeoutRe matches a timeout-minutes line at four spaces.
var timeoutRe = regexp.MustCompile(`^    timeout-minutes:\s*(\d+)$`)

// offCLPathRe matches the `if:` guard that runs a job ONLY on push to main and
// on the nightly schedule. Such a job cannot run on a pull_request, so nothing
// waits on it to merge and the two-minute CL budget does not apply: it is the
// expensive tier — the GitHub-hosted full suite — deliberately off the fast
// path. Every other job in ci.yml is on the CL path by default; the exemption
// has to be written into the workflow as that guard, not assumed here.
var offCLPathRe = regexp.MustCompile(`github\.event_name == 'push' \|\| github\.event_name == 'schedule'`)

// usesRe matches an action reference pinned by its 40-hex commit SHA.
var usesRe = regexp.MustCompile(`uses:\s*([^/\s]+/[^@\s]+)@([0-9a-fA-F]{40})`)

// usesDirectiveRe matches a line that IS a `uses:` step key — either a list
// item (`- uses:`) or a map key (`uses:`), at any indentation. It deliberately
// will not match a string that merely CONTAINS "uses:" in the middle of a word
// (the smoke gate lists an "unreadable deny-list refuses:" step, whose
// "refuses:" is not an action reference).
var usesDirectiveRe = regexp.MustCompile(`^\s*(-\s*)?uses:\s*\S`)

func TestCLTierJobsStayWithinTheBudget(t *testing.T) {
	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	names := jobNames(src)
	if len(names) == 0 {
		t.Fatal("no jobs parsed from ci.yml; the parser is looking in the wrong place")
	}
	timeouts := jobTimeouts(src)
	offPath := jobsOffTheCLPath(src)
	for _, name := range names {
		mins, ok := timeouts[name]
		if !ok {
			t.Errorf("job %q has no timeout-minutes; the per-job cap cannot be guarded on a job that does not declare one", name)
			continue
		}
		if offPath[name] {
			// Guarded to push-to-main and the nightly schedule, so no pull
			// request waits on it and it is not in the CL budget. It did
			// declare a ceiling, which is the part checked just above.
			continue
		}
		// This pins the per-job CAP only. timeout-minutes is a ceiling on one
		// job, not a proof that the whole required path fits two minutes: the
		// path is the CI run, and no per-job ceiling can see that sum.
		want, ok := clTierCeilings[name]
		if !ok {
			want = defaultCLCeiling
		}
		if mins > want {
			t.Errorf("CL-tier job %q has timeout-minutes %d, want a cap <= %d", name, mins, want)
		}
	}
}

// TestMergeGateAllowanceCarriesItsReason pins the one exception: test-hosted-merge
// declares exactly mergeGateCeiling minutes (five), and the reason it may is
// recorded in the test as mergeGateReason. Every other job on the critical path
// stays at defaultCLCeiling, which TestCLTierJobsStayWithinTheBudget enforces by
// the map lookup above. If the job drifts back to two the fleet drops groups
// again; if it drifts past five the exception has grown without a record.
func TestMergeGateAllowanceCarriesItsReason(t *testing.T) {
	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	mins, ok := jobTimeouts(src)["test-hosted-merge"]
	if !ok {
		t.Fatal("test-hosted-merge declares no timeout-minutes; the allowance has nothing to hold")
	}
	if mins != mergeGateCeiling {
		t.Errorf("test-hosted-merge timeout-minutes = %d, want %d: the merge gate is the one allowed exception to the two-minute law, at the recorded five minutes", mins, mergeGateCeiling)
	}
	if strings.TrimSpace(mergeGateReason) == "" {
		t.Error("the merge gate allowance carries no reason; the exception must say why it exists")
	}
	if !strings.Contains(src, "the one allowed exception") && !strings.Contains(src, "the one exception") {
		t.Error("the test-hosted-merge comment does not name the gate as the exception to the two-minute law")
	}
}

func TestJobsThatLeftCIAreStillInCertification(t *testing.T) {
	root := repoRoot(t)
	cert := readFile(t, filepath.Join(root, ".github", "workflows", "certification.yml"))

	certNames := toSet(jobNames(cert))
	if len(certNames) == 0 {
		t.Fatal("no jobs parsed from certification.yml; the parser is looking in the wrong place")
	}
	for _, name := range splitMovedJobs {
		if !certNames[name] {
			t.Errorf("job %q left ci.yml in the split but is not present in certification.yml; the split must delete nothing", name)
		}
	}

	needs := certificationOKNeeds(cert)
	for _, name := range splitMovedJobs {
		if !needs[name] {
			t.Errorf("certification-ok does not list %q in its needs; every certification job must be aggregated", name)
		}
	}
}

func TestEveryActionIsPinnedBySHA(t *testing.T) {
	root := repoRoot(t)
	for _, file := range []string{".github/workflows/ci.yml", ".github/workflows/certification.yml"} {
		src := readFile(t, filepath.Join(root, file))
		lines := strings.Split(src, "\n")
		for i, line := range lines {
			if !usesDirectiveRe.MatchString(line) {
				continue
			}
			if usesRe.FindStringSubmatch(line) == nil {
				t.Errorf("%s:%d: uses: is not owner/action@40-hex-sha: %q", file, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// TestFleetProbeRunsTheNetworkProbeInsideNovaSandbox is rule R for the probe:
// nothing enters the loop untested, and #893 is the cost of forgetting it — a
// probe on the host passed while every sandboxed card died. So the fleet-probe
// job, which proves a bench before the loop trusts it, must build nova-sandbox
// from the checkout and run the same network probe INSIDE it, on Linux runners
// only, and still fit the two-minute CL budget. Read as text, like the rest of
// this file: the step's shape is the contract, so the assertion is on the words
// a reviewer would look for.
func TestFleetProbeRunsTheNetworkProbeInsideNovaSandbox(t *testing.T) {
	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	job := jobBody(src, "fleet-probe")
	if job == "" {
		t.Fatal("no fleet-probe job in ci.yml; the bench would enter the loop with no probe at all")
	}
	if !strings.Contains(job, "go build ./cmd/nova-sandbox") {
		t.Errorf("the fleet-probe job does not build nova-sandbox from the checkout; a probe that does not run in the sandbox is the host probe #893 killed")
	}
	if !strings.Contains(job, "nova-sandbox --read") || !strings.Contains(job, "curl -s") {
		t.Errorf("the fleet-probe job does not run the network probe inside nova-sandbox (need `nova-sandbox --read` and `curl -s` in one step); a bench enters the loop only after the sandboxed probe is green")
	}
	if !strings.Contains(job, "runner.os == 'Linux'") {
		t.Errorf("the sandboxed network probe is not guarded to Linux runners only")
	}
	if mins, ok := jobTimeouts(src)["fleet-probe"]; !ok || mins > defaultCLCeiling {
		t.Errorf("fleet-probe timeout-minutes = %d (declared=%v), want a cap <= %d; the probe must fit the two-minute CL budget", mins, ok, defaultCLCeiling)
	}
}

// jobBody returns the source text of one job, from its two-space key to the
// next job key, so a test can assert about one job and not the whole file.
func jobBody(src, name string) string {
	lines := strings.Split(src, "\n")
	start := -1
	for i, line := range lines {
		if start < 0 {
			if line == "  "+name+":" {
				start = i
			}
			continue
		}
		if jobKeyRe.MatchString(line) {
			return strings.Join(lines[start:i], "\n")
		}
	}
	if start >= 0 {
		return strings.Join(lines[start:], "\n")
	}
	return ""
}

// TestNoTestAssertsAWallClockBoundUnderTenSeconds is the wall-clock law for
// tests, read off the test files as text. A wall-clock bound in a test asserts
// the machine's load, not the code: two tests failed under load and passed
// alone because a version probe timed out at five seconds and a stall assertion
// sat on a five-second deadline. So no _test.go line may carry a literal
// duration under ten seconds where the test leans on the wall clock: a context
// deadline (a WithTimeout or WithDeadline call, a time.After or NewTimer
// watchdog) or an elapsed-time assertion (time.Since, elapsed, took, waited).
// Duration inputs to fake-driven code (a runner deadline, a flag table, a
// parsed retry-after) are not assertions about the machine and are out of
// scope; only the shapes above are read. Thirty seconds or more is the generous
// bound; a fake that must stay short carries an allowlist comment
// holding a reason, and the check reads the code before any comment on the
// line, so prose about the rule cannot trip it.
func TestNoTestAssertsAWallClockBoundUnderTenSeconds(t *testing.T) {
	root := repoRoot(t)
	sub10Re := regexp.MustCompile(`(^|[^0-9])([1-9])\s*[\*]\s*time[.]Second\b`)
	anySecRe := regexp.MustCompile(`time[.]Second\b`)
	bigSecRe := regexp.MustCompile(`[0-9]{2,}\s*[\*]\s*time[.]Second\b`)
	trigRe := regexp.MustCompile(`WithTimeout|WithDeadline|time[.]After|NewTimer|time[.]Since|elapsed|took|waited`)
	// A batch deadline or idle window is the other shape the wall-clock law
	// reaches: unlike a runner deadline or a flag table, it drives a REAL
	// subprocess kill. `deadline: time.Second` names no context and no elapsed
	// assertion, so the check above never saw it, yet the kill is still a bet on
	// how loaded the machine is (issue #916). The 8490 seam closed the kill for
	// the labelled `deadline:`/`idle:` shape; this widens the class: within a
	// file that drives the batch, ANY time.Second literal under ten seconds is a
	// bet on the machine, whether it is labelled or handed positionally to
	// runBatch/runBatchIdle. The injected clock (the manualClock seam) makes the
	// duration virtual; a literal that must stay short carries a // wall-ok:
	// reason (issue #916).
	secLitRe := regexp.MustCompile(`(?:([0-9]+)\s*[*]\s*)?time[.]Second\b`)
	for _, dir := range []string{"internal", "cmd"} {
		base := filepath.Join(root, dir)
		err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Errorf("cannot read %s: %v", path, err)
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			// The batch-deadline shape is scoped to the files that drive the batch:
			// only there does a short deadline/idle literal reach a real process.
			// A file drives the batch when it builds a BatchInput -- through the
			// runBatch/runBatchIdle helpers or a direct BatchInput literal.
			batchFile := strings.Contains(string(raw), "runBatch") || strings.Contains(string(raw), "BatchInput{")
			for i, line := range strings.Split(string(raw), "\n") {
				if strings.Contains(line, "// wall-ok:") {
					continue
				}
				code := line
				if j := strings.Index(code, "//"); j >= 0 && (j == 0 || code[j-1] == ' ' || code[j-1] == '\t') {
					code = code[:j]
				}
				if batchFile && wallSecondsUnderTen(secLitRe, code) {
					t.Errorf("%s:%d: batch-driving test carries a wall-clock literal under ten seconds (use thirty seconds or more, or an injected clock with // wall-ok: <reason>): %q", rel, i+1, strings.TrimSpace(line))
					continue
				}
				if !trigRe.MatchString(code) {
					continue
				}
				if !anySecRe.MatchString(code) {
					continue
				}
				if bigSecRe.MatchString(code) && !sub10Re.MatchString(code) {
					continue
				}
				// A bare duration with no multiplier on the line is one second.
				t.Errorf("%s:%d: wall-clock bound under ten seconds in a test assertion or context deadline (use thirty seconds or more, or a fake with // wall-ok: <reason>): %q", rel, i+1, strings.TrimSpace(line))
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// wallSecondsUnderTen reports whether code carries a time.Second literal of
// fewer than ten seconds: a bare time.Second is one, and `n * time.Second` is n.
func wallSecondsUnderTen(re *regexp.Regexp, code string) bool {
	for _, m := range re.FindAllStringSubmatch(code, -1) {
		n := 1
		if m[1] != "" {
			if v, err := strconv.Atoi(m[1]); err == nil {
				n = v
			}
		}
		if n < 10 {
			return true
		}
	}
	return false
}

// defaultCLCeiling is the two-minute law: a CL-tier job caps at 2 minutes.
const defaultCLCeiling = 2

// mergeGateCeiling is the one allowed exception to the two-minute law.
// test-hosted-merge is on the CL path — the merge queue's group commit waits on
// it — but it is not a small check: it runs the FULL hosted suite (no -short) of
// the packages a group changes, on three platforms, sharded by size. Under load
// (the Studio running sixteen self-hosted legs plus three friends, windows-latest
// cold) its darwin shards were cancelled at 2:44 and its windows shards at 2:40
// on 2026-09-17; a cancelled shard drops the whole group and restarts every group
// behind it, so the two-minute cap on this one job was the throughput limit of
// the whole fleet. Five minutes is the allowance; every other CL-path job stays
// at the default two.
const mergeGateCeiling = 5

// mergeGateReason is the record beside that allowance: why the merge gate is
// allowed five minutes. It is asserted in TestMergeGateAllowanceCarriesItsReason,
// so the number and the why cannot drift apart silently.
const mergeGateReason = "the merge gate runs the full suite of the packages a group changes on three platforms; sharded by size; cancelled under load at 2:40 on 2026-09-17"

// clTierCeilings is where a job that does NOT cap at two minutes says so, and
// says why. A number here is a claim about the machine the job runs on, so it
// belongs in the repository beside the law rather than in a commit message.
var clTierCeilings = map[string]int{
	// The aggregate reads results and checks nothing out.
	"ci-ok": 1,

	// The one allowed exception. The merge gate runs the full suite of the
	// packages a group changes, on three platforms, sharded by size; it was
	// cancelled under load at 2:40 (2026-09-17), and a cancelled shard drops the
	// group. mergeGateReason carries the record the budget test asserts.
	"test-hosted-merge": mergeGateCeiling,

	// The sandbox leg a PR runs only when it touches internal/sandbox or a
	// *_linux.go. A hosted runner starts cold (checkout, setup-go, cache
	// restore) before it compiles anything, and 6 is the same cap the sharded
	// matrix carries. A PR that does not touch those paths pays a checkout and
	// skips. This job carried a windows entry too until 2026-09-18, when that
	// entry was retired into test-windows-pr: on integration-4 it was CANCELLED
	// by this very cap at 6 min 20 s, running unsharded the same packages the
	// sharded leg had just passed. The number was never the problem; running
	// them unsharded was. (2026-09-16, amended 2026-09-18)
	"test-hosted-pr": 6,

	// The Windows leg a pull request gets for the packages it changes, and since
	// 2026-09-18 the ONLY Windows leg a PR runs. TEN, and it is a hang detector
	// rather than a budget — the third and last number on this leg to be set by
	// a measurement instead of by a convention.
	//
	// Six minutes sat ON the measurement. In run 35352593117 all three shards
	// FINISHED their last package — ok internal/bus at 13:56:36.1, 13:56:34.6
	// and 13:56:35.3 — and were killed two to four seconds later, in the
	// post-checkout cleanup. The work was done; the cap reported a red about the
	// clock. That is the same mistake the 100 s per-package ceiling made one
	// tier down, at the job level: a limit that close to the thing it measures
	// censors it.
	//
	// The measured whole is 6:00 a shard (13:50:36 to 13:56:36) for the whole
	// tree over three shards, with twenty unmeasured packages needlessly dealt
	// across all of them. The leg now runs four shards with every package
	// measured, which is about 4:03 of the same work: 178 s of tests, ~40 s of
	// compiles and listing, 25 s of setup. Ten minutes is 1.7x the largest whole
	// ever observed and about 2.5x the expected one. What keeps the leg fast is
	// the shard plan and the measurements in
	// testdata/ci/package-sizes-windows.tsv, not this ceiling; this ceiling only
	// stops a wedged process burning a runner. (2026-09-18)
	"test-windows-pr": 10,

	// The sharded test matrix, and the one number the move to self-hosted
	// runners actually changed. The two minutes are the CL FEEDBACK PATH: how
	// long a change waits. On GitHub-hosted runners every leg starts at once,
	// so a leg's ceiling and the run's wall clock are one number. On 4+4 fixed
	// machines they are not: the legs queue, the run is the sum over the waves,
	// and a per-job ceiling cannot see it. Measured in run 35019905236: every
	// studio leg and three of eight space legs were CANCELLED at 2:00 having
	// done nothing wrong, while five space legs passed at 80-118 s. Six is a
	// hang detector for a leg measured, once the legs stopped oversubscribing
	// their machines, at 12 to 126 s over a 233 s run (35025207396). The budget
	// is the run's wall clock; hold the law there.
	"test": 6,
}

func jobNames(src string) []string {
	var names []string
	inJobs := false
	for _, line := range strings.Split(src, "\n") {
		if !strings.HasPrefix(line, " ") && strings.TrimSpace(line) == "jobs:" {
			inJobs = true
			continue
		}
		if !inJobs {
			continue
		}
		if line == "" {
			continue
		}
		// A top-level key (column 0) ends the jobs section.
		if !strings.HasPrefix(line, " ") {
			break
		}
		if m := jobKeyRe.FindStringSubmatch(line); m != nil {
			names = append(names, m[1])
		}
	}
	return names
}

func jobTimeouts(src string) map[string]int {
	out := make(map[string]int)
	cur := ""
	for _, line := range strings.Split(src, "\n") {
		if m := jobKeyRe.FindStringSubmatch(line); m != nil {
			cur = m[1]
			continue
		}
		if cur == "" {
			continue
		}
		if m := timeoutRe.FindStringSubmatch(line); m != nil {
			n, err := strconv.Atoi(m[1])
			if err == nil {
				out[cur] = n
			}
		}
	}
	return out
}

// jobsOffTheCLPath returns the jobs whose `if:` guard runs them only on push to
// main and on the nightly schedule. It reads the workflow as text, like the rest
// of this file: the guard is matched by its exact shape, so a job that wants out
// of the two-minute budget has to carry that guard verbatim and mention no
// pull_request of its own.
func jobsOffTheCLPath(src string) map[string]bool {
	out := make(map[string]bool)
	cur := ""
	for _, line := range strings.Split(src, "\n") {
		if m := jobKeyRe.FindStringSubmatch(line); m != nil {
			cur = m[1]
			continue
		}
		if cur == "" || !strings.HasPrefix(line, "    if:") {
			continue
		}
		if offCLPathRe.MatchString(line) && !strings.Contains(line, "pull_request") {
			out[cur] = true
		}
	}
	return out
}

func toSet(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}

// splitMovedJobs is the inventory of the jobs the 2026-09-12 split moved out of
// ci.yml into certification.yml, read off origin/main's ci.yml at the time of
// the split: the whole-tree `-race` test, the Windows build/vet and per-package
// tests, the three-OS smoke, the release dry-run, and the nightly perf wall
// clock. It is checked in as a fixed list on purpose: an ordinary `go test`
// from a source archive or an offline checkout has no origin/main ref to fetch,
// so the comparison must not need one.
var splitMovedJobs = []string{
	"test",
	"build-windows",
	"windows-packages",
	"test-windows",
	"smoke",
	"release-dry-run",
	"perf",
}

// certificationOKNeeds returns the set of job names listed in certification-ok's
// `needs:` line. A job the aggregate forgot to list is a job whose red no longer
// blocks a release, so the needs list is asserted to cover every moved job.
func certificationOKNeeds(src string) map[string]bool {
	needs := make(map[string]bool)
	inCertOK := false
	for _, line := range strings.Split(src, "\n") {
		if m := jobKeyRe.FindStringSubmatch(line); m != nil {
			inCertOK = m[1] == "certification-ok"
			continue
		}
		if !inCertOK {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "needs:") {
			list := strings.TrimSpace(strings.TrimPrefix(trimmed, "needs:"))
			list = strings.Trim(list, "[]")
			for _, name := range strings.Split(list, ",") {
				if name = strings.TrimSpace(name); name != "" {
					needs[name] = true
				}
			}
			return needs
		}
	}
	return needs
}

// TestEveryTriggeringEventReachesACIOKVerdict is the guard the merge queue
// depends on: ci-ok is the only required check, and a verdict step is gated by
// event name. An event the workflow triggers on (pull_request, merge_group,
// push, workflow_dispatch; schedule runs only the nightly tier and owes no
// verdict) that no ci-ok step names would let ci-ok run zero steps and report
// success over red needs (found on #766 before the queue was turned on).
func TestEveryTriggeringEventReachesACIOKVerdict(t *testing.T) {
	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	i := strings.Index(src, "\n  ci-ok:")
	if i < 0 {
		t.Fatal("no ci-ok job in ci.yml")
	}
	ciok := src[i:]
	for _, ev := range []string{"pull_request", "merge_group", "push", "workflow_dispatch"} {
		want := "github.event_name == '" + ev + "'"
		if !strings.Contains(ciok, want) {
			t.Errorf("ci-ok has no verdict step guarded for %s: the workflow triggers on it, so a run on that event would report success with no step run", ev)
		}
	}
}
