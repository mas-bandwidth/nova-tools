package ci

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
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

// usesRe matches an action reference pinned by its 40-hex commit SHA.
var usesRe = regexp.MustCompile(`uses:\s*([^/\s]+/[^@\s]+)@([0-9a-fA-F]{40})`)

// usesDirectiveRe matches a line that IS a `uses:` step key — either a list
// item (`- uses:`) or a map key (`uses:`), at any indentation. It deliberately
// will not match a string that merely CONTAINS "uses:" in the middle of a word
// (the smoke gate lists an "unreadable deny-list refuses:" step, whose
// "refuses:" is not an action reference).
var usesDirectiveRe = regexp.MustCompile(`^\s*(-\s*)?uses:\s*\S`)

func TestEveryCIJobIsCappedAtTwoMinutes(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	files, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflow files under .github/workflows: %v", err)
	}
	for _, file := range files {
		src := readFile(t, file)
		names := jobNames(src)
		if len(names) == 0 {
			t.Errorf("%s: no jobs parsed; the parser is looking in the wrong place", filepath.Base(file))
			continue
		}
		timeouts := jobTimeouts(src)
		for _, name := range names {
			mins, ok := timeouts[name]
			if !ok {
				t.Errorf("%s: job %q declares no literal timeout-minutes; every job is `timeout-minutes: %d`, no expression, no per-leg ceiling", filepath.Base(file), name, twoMinuteCap)
				continue
			}
			if mins > twoMinuteCap {
				t.Errorf("%s: job %q has timeout-minutes %d, want %d: the cap is permanent and platform-wide; split the work into parallel functional programs instead of raising it", filepath.Base(file), name, mins, twoMinuteCap)
			}
		}
		if strings.Contains(src, "timeout-minutes: ${{") {
			t.Errorf("%s: a timeout-minutes is an expression; the cap is the literal %d on every job", filepath.Base(file), twoMinuteCap)
		}
	}
}

// goTestTimeoutRe reads the sharded test job's `go test -timeout`, which must
// end the run with a Go stack before the job cap kills it without one.
var goTestTimeoutRe = regexp.MustCompile(`GOTEST_TIMEOUT="([0-9]+)s"`)

func TestShardGoTestTimeoutIsUnderTheJobCap(t *testing.T) {
	t.Parallel()

	job := jobBody(readFile(t, filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml")), "test")
	m := goTestTimeoutRe.FindStringSubmatch(job)
	if m == nil {
		t.Fatal("the test job passes no literal GOTEST_TIMEOUT=\"<n>s\" to make test")
	}
	secs, _ := strconv.Atoi(m[1])
	if secs >= twoMinuteCap*60 {
		t.Errorf("go test -timeout %ds is not under the %d-minute job cap", secs, twoMinuteCap)
	}
	mk := parseMakefile(t, filepath.Join(repoRoot(t), "Makefile"))
	for _, v := range []string{"GOTEST_TIMEOUT", "MERGE_TIMEOUT", "DARWIN_TIMEOUT"} {
		raw, ok := mk.vars[v]
		if !ok {
			t.Errorf("the Makefile declares no %s", v)
			continue
		}
		d, err := time.ParseDuration(strings.TrimSpace(raw))
		if err != nil {
			t.Errorf("%s = %q is not a Go duration: %v", v, raw, err)
			continue
		}
		if d >= time.Duration(twoMinuteCap)*time.Minute {
			t.Errorf("Makefile %s = %s is not under the %d-minute job cap", v, d, twoMinuteCap)
		}
	}
}

// macOSEntryRe reads a macOS shard entry's arch and group from test-packages.
var macOSEntryRe = regexp.MustCompile(`entries\+=\(.*\\"os\\":\\"macOS\\",\\"arch\\":\\"([^"\\]+)\\",\\"group\\":\\"([^"\\]+)\\"`)

// TestMacOSShardsRunOnTheStudioForNow pins the 2026-09-25 decision (Glenn: "let's
// have the darwin tests run on studio, so we can move forward"; "running tests
// in under 2 minutes will require modern machines"): the darwin legs select the
// Studio's ARM64 runners until the Mac minis (~2026-10-10) take them. #3634's
// rule (no CI on the Studio) is suspended for the darwin legs only.
func TestMacOSShardsRunOnTheStudioForNow(t *testing.T) {
	t.Parallel()

	src := readFile(t, filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	found := 0
	for _, line := range strings.Split(jobBody(src, "test-packages"), "\n") {
		m := macOSEntryRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		found++
		if m[1] != "ARM64" || m[2] != "studio" {
			t.Errorf("a macOS test shard selects arch %q group %q, want ARM64 on studio (2026-09-25, until the Mac minis): %s", m[1], m[2], strings.TrimSpace(line))
		}
	}
	if found == 0 {
		t.Error("test-packages emits no macOS shard entry this test can read")
	}
	if strings.Contains(jobBody(src, "test-hosted-merge"), "name: darwin") {
		t.Error("the merge group carries a darwin leg again; since 2026-09-25 its darwin coverage is the test (darwin-arm64) shards on the group commit, and a second leg on the same runners crossed the cap (run 36207910988)")
	}
}

func TestJobsThatLeftCIAreStillInCertification(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	cert := readFile(t, filepath.Join(root, ".github", "workflows", "certification.yml"))

	certNames := toSet(jobNames(cert))
	if len(certNames) == 0 {
		t.Fatal("no jobs parsed from certification.yml; the parser is looking in the wrong place")
	}
	inventory := toSet(splitMovedJobs)
	for name, reason := range droppedByRuling {
		if !inventory[name] {
			t.Errorf("droppedByRuling names %q, which is not in splitMovedJobs; an exception to a list must be an entry of that list", name)
		}
		if strings.TrimSpace(reason) == "" {
			t.Errorf("the exception for %q carries no reason; an exception must say why it exists", name)
		}
	}
	for _, name := range splitMovedJobs {
		if _, byRuling := droppedByRuling[name]; byRuling {
			continue
		}
		if !certNames[name] {
			t.Errorf("job %q left ci.yml in the split but is not present in certification.yml; the split must delete nothing", name)
		}
	}

	needs := certificationOKNeeds(cert)
	for _, name := range splitMovedJobs {
		if _, byRuling := droppedByRuling[name]; byRuling {
			continue
		}
		if !needs[name] {
			t.Errorf("certification-ok does not list %q in its needs; every certification job must be aggregated", name)
		}
	}
}

func TestEveryActionIsPinnedBySHA(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	if mins, ok := jobTimeouts(src)["fleet-probe"]; !ok || mins > twoMinuteCap {
		t.Errorf("fleet-probe timeout-minutes = %d (declared=%v), want a cap <= %d; the probe must fit the two-minute CL budget", mins, ok, twoMinuteCap)
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
	t.Parallel()

	tree := repoTree(t)
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
		for _, f := range tree.GoFilesUnder(true, dir) {
			raw := f.Src
			rel := f.Rel
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

// twoMinuteCap is THE CI law, permanent and platform-wide (Glenn 2026-09-25
// 9:40 PM ET: "i want this 2 minute cap to be permanent, and for all new CI
// stuff created, all platforms to have this same 2m cap"): every job in every
// workflow declares timeout-minutes: 2, literally. No expression, no matrix
// leg with its own ceiling, no tier that is exempt, nightly and release
// included. The exceptions this file used to carry (the merge gate at five and
// ten, the lisp job at fifteen, the push studio shards at twenty, the hosted
// tree at fifteen) were "hang detectors with room"; a nine-minute darwin leg
// ran to completion under them on 2026-09-25 and was treated as normal.
// Glenn: "we fix or it doesn't land. that's the right posture. nothing else
// will stop the test creep." Work that needs longer is split into parallel
// functional test programs, each its own job under the cap.
const twoMinuteCap = 2

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

// jobTimeouts returns each job's declared timeout-minutes. A job whose ceiling
// differs per matrix leg declares `timeout-minutes: ${{ matrix.leg.timeout }}`
// and carries the numbers in its matrix; for those the LARGEST leg value is
// returned, because the budget question this answers is "how long can this job
// run", and legTimeouts below is what reads them apart.
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
			continue
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

// droppedByRuling is the dated, quoted exception to "the split must delete
// nothing": the jobs that DID leave ci.yml in the 2026-09-12 split but that
// certification.yml no longer has to carry, because a later ruling retired them
// outright rather than moving them. The class rule is untouched — every other
// job in splitMovedJobs must still be present and still be aggregated by
// certification-ok. Only the names listed here are skipped, and each one carries
// the ruling that struck it.
//
// Glenn, 2026-09-18: "We will not support windows without WSL2. It is not worth
// it." / "let's drop the native windows CI runners. WSL only from now on."
// #1449 removed these three from ci.yml on that ruling but left them in
// certification.yml, so certification-ok was red on every dev sha and
// `nova-update release cut` refused every tip (CUT REFUSED
// certification-ok=failure). The Windows guard that remains is the cross-vet,
// `GOOS=windows go vet`, which needs no Windows machine.
var droppedByRuling = map[string]string{
	"build-windows":    `Glenn 2026-09-18: "let's drop the native windows CI runners. WSL only from now on." (#1449)`,
	"windows-packages": `Glenn 2026-09-18: "let's drop the native windows CI runners. WSL only from now on." (#1449)`,
	"test-windows":     `Glenn 2026-09-18: "let's drop the native windows CI runners. WSL only from now on." (#1449)`,
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
	t.Parallel()

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

// TestMakefileHasNoTargetSpecificConditionalPKGS: under GNU make 3.81 (the
// macOS runners' /usr/bin/make) one `<target>: PKGS ?= ...` line made
// `test: PKGS := $(CL_PKGS)` override `make test PKGS=<shard>`, so every studio
// shard of dev push run 35999520176 ran the whole tree.
func TestMakefileHasNoTargetSpecificConditionalPKGS(t *testing.T) {
	t.Parallel()

	src := readFile(t, filepath.Join(repoRoot(t), "Makefile"))
	re := regexp.MustCompile(`(?m)^[A-Za-z0-9_.-]+:\s*PKGS\s*\?=`)
	if m := re.FindString(src); m != "" {
		t.Errorf("Makefile carries %q; under make 3.81 it lets `test: PKGS :=` beat the shard's PKGS", m)
	}
}
