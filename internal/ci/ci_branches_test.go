package ci

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/pkgselect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ci_branches_test.go pins the per-event job set: what each trigger runs, and
// that the merge queue's gate runs what a push to dev runs. Rule T (pit stop 4,
// 2026-09-16): the -short hosted-pr leg passed #858 while the full Windows leg
// on the dev push failed three of its new tests (dev runs 35142587149,
// 35143418489). A PR that changes a package with hosted-only tests cannot land
// on the -short result alone, so the group commit must run the full hosted legs
// for the packages it changes.
//
// It reads ci.yml as text, like ci_budget_test.go: the file is the contract and
// there is no YAML library in go.mod.

// TestSelfHostedShardsSelectThePackagesAChangeTouches pins the self-hosted
// test fan-out's price law: on pull_request and merge_group the shards carry
// only the packages the change touches and their in-module importers, while
// the push to dev and the nightly schedule keep the whole tree. The selection
// reads the diff against the event's own base (pull_request.base.sha,
// merge_group.base_sha); a change that touches no Go package — docs or lisp
// only — selects nothing and the fan-out collapses to one leg that prints
// "nothing to test for this change" and exits 0. Without the selection a docs
// change pays twenty-four shards, which is what held the merge queue.
func TestSelfHostedShardsSelectThePackagesAChangeTouches(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	block := jobBody(src, "test-packages")
	require.NotEmpty(t, block, "no test-packages job in ci.yml; the self-hosted fan-out has no selection to check")
	require.False(t, runsOnHosted(block), "test-packages is not the self-hosted fan-out; the test is looking at the wrong job")
	// The step hands the event's own bases to the verb that selects and deals;
	// the verb holds the rest of the law.
	for _, want := range []string{
		ciRunner + " test-matrix",
		`--event "${{ github.event_name }}"`,
		`--pull-request-base "${{ github.event.pull_request.base.sha }}"`,
		`--merge-group-base "${{ github.event.merge_group.base_sha }}"`,
	} {
		assert.Contains(t, block, want, "test-packages does not carry %q: the self-hosted shards must select the packages a change touches on pull_request and merge_group, against the event's own base", want)
	}
	verb := readFile(t, filepath.Join(root, "tools", "ci", "sel_matrix.go"))
	for _, want := range []string{
		`"nothing to test for this change"`,
		`pkgselect.Select(`,
		`WholeTreeOnError: *event != "pull_request"`,
		`base = *prBase`,
		`base = *mgBase`,
	} {
		assert.Contains(t, verb, want, "tools/ci/sel_matrix.go does not carry %q: a change that touches no Go package runs nothing, and the selection reads the diff against the event's own base", want)
	}
}

// jobBlocks returns each job's body, keyed by job name, with the job's own
// `if:` and steps but without the next job's. It starts at `jobs:` and stops at
// the first top-level key, so the keys under `on:` (`push:`, `pull_request:`)
// are not read as jobs.
func jobBlocks(src string) map[string]string {
	out := make(map[string]string)
	inJobs := false
	cur := ""
	var b strings.Builder
	flush := func() {
		if cur != "" {
			out[cur] = b.String()
			cur = ""
			b.Reset()
		}
	}
	for _, line := range strings.Split(src, "\n") {
		if !strings.HasPrefix(line, " ") && strings.TrimSpace(line) == "jobs:" {
			inJobs = true
			continue
		}
		if !inJobs {
			continue
		}
		if line == "" {
			// Blank lines separate jobs; they do not end the section.
			if cur != "" {
				b.WriteString("\n")
			}
			continue
		}
		if !strings.HasPrefix(line, " ") {
			break
		}
		if m := jobKeyRe.FindStringSubmatch(line); m != nil {
			flush()
			cur = m[1]
			continue
		}
		if cur != "" {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	flush()
	return out
}

// runsOnHosted reports whether a job's runs-on names a GitHub-hosted OS. The
// self-hosted legs name the machine (space, studio), never these.
func runsOnHosted(block string) bool {
	return strings.Contains(block, "ubuntu-latest") ||
		strings.Contains(block, "macos-latest") ||
		strings.Contains(block, "windows-latest")
}

// jobRunsOnEvent reports whether a job's own `if:` guard names the event.
func jobRunsOnEvent(block, event string) bool {
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "    if:") && strings.Contains(line, "github.event_name == '"+event+"'") {
			return true
		}
	}
	return false
}

// ci_branches_test.go is class B of pit stop 3 (#828) applied to CI:
// ONE FACT WRITTEN TWICE. Two rows of that bug table are read off these
// workflow files as text, the same way ci_budget_test.go reads the two-minute
// law:
//
//	row 8 — "dev inherited main's cancellation bug: concurrency keyed dev on
//	the ref, the next merge cancelled dev's run, the gate read cancelled as red
//	and froze the bench". main had been taught the sha-keyed group and the
//	no-cancel rule in two separate expressions; dev was added to one of them
//	and not the other, and the bench froze for six minutes;
//
//	row 9 — "Fair-share divisor stayed 4 when runners went to 8 per machine;
//	Space load 39 on 15 cores from CI alone". The number of runners sharing a
//	machine lived in the workflow AND on the machines, and only the machines
//	were changed.
//
// So: the integration refs are ONE list, and the runners per machine are ONE
// number the machine itself exports.
//
// A NOTE ON WHERE THE LIST HAS TO LIVE. `concurrency` is the one workflow key
// whose expressions cannot read `env` — the contexts available there are
// github, inputs and vars and nothing else (GitHub's context reference) — so
// the list cannot be an `env: INTEGRATION_BRANCHES` that concurrency reads. It
// is therefore defined where it cannot be defined anywhere else: as the single
// `fromJSON('[...]')` list inside the concurrency group. `on: push: branches:`
// takes no expression at all, in this file or in certification.yml, so those
// two spellings cannot read the list either — and that is exactly the drift
// that froze the bench, so the test below holds them to the list instead: a
// branch added to the list and nowhere else is a red test that names the lines
// to change, never a silent half-rule.

// ciRunner is how a workflow step runs tools/ci: the binary its job built once
// into $RUNNER_TEMP, never `go run`, which would compile it again in each step.
const ciRunner = `"$RUNNER_TEMP/ci"`

// integrationListRe matches THE list definition: a fromJSON of a JSON array of
// refs, single-quoted inside the concurrency expression.
var integrationListRe = regexp.MustCompile(`fromJSON\('(\[[^']*refs/heads/[^']*\])'\)`)

// pushBranchesRe matches the `branches: [a, b]` line of a push trigger.
var pushBranchesRe = regexp.MustCompile(`^\s*branches:\s*\[([^\]]*)\]\s*$`)

// TestIntegrationBranchesAreOneList pins that main and dev are named once.
// It fails while any refs/heads/main or refs/heads/dev literal exists outside
// the list definition, and while either workflow's push trigger names a branch
// the list does not.
func TestIntegrationBranchesAreOneList(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	const ciFile = ".github/workflows/ci.yml"
	const certFile = ".github/workflows/certification.yml"
	src := readFile(t, filepath.Join(root, ciFile))

	// (1) Exactly one definition, and it parses as a list of refs.
	defs := integrationListRe.FindAllStringSubmatch(src, -1)
	require.Len(t, defs, 1, "%s: found %d fromJSON lists of refs/heads/... refs, want exactly 1: the integration branches are one list, so every rule reads the same one", ciFile, len(defs))
	var refs []string
	err := json.Unmarshal([]byte(defs[0][1]), &refs)
	require.NoError(t, err, "%s: the integration branch list %q is not a JSON array: %v", ciFile, defs[0][1], err)
	require.NotEmpty(t, refs, "%s: the integration branch list is empty", ciFile)
	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		if !assert.True(t, strings.HasPrefix(ref, "refs/heads/"), "%s: integration branch %q is not a refs/heads/ ref; github.ref is compared against this list", ciFile, ref) {
			continue
		}
		names = append(names, strings.TrimPrefix(ref, "refs/heads/"))
	}

	// (2) No literal anywhere else, in either file. The definition is cut out
	// of the text first; what is left must not name an integration ref.
	for _, file := range []string{ciFile, certFile} {
		text := readFile(t, filepath.Join(root, file))
		if file == ciFile {
			text = strings.Replace(text, defs[0][0], "<the one list>", 1)
		}
		for i, line := range strings.Split(text, "\n") {
			for _, ref := range refs {
				assert.NotContains(t, line, ref, "%s:%d: %q is written outside the one list: %q. Every rule that treats an integration branch differently must read the list in ci.yml's concurrency group (row 8 of #828: dev was taught the sha-keyed group and not the no-cancel rule, and the bench froze)", file, i+1, ref, strings.TrimSpace(line))
			}
		}
	}

	// (3) cancel-in-progress carries no branch of its own. It was the second
	// copy in row 8; it is a constant now, because a group keyed on the sha is
	// shared with no other run and so has nothing to cancel.
	for i, line := range strings.Split(src, "\n") {
		if !strings.HasPrefix(line, "  cancel-in-progress:") {
			continue
		}
		for _, name := range names {
			assert.False(t, regexp.MustCompile(`\b`+regexp.QuoteMeta(name)+`\b`).MatchString(line), "%s:%d: cancel-in-progress names %q: %q. The no-cancel rule must not hold a second copy of the branch list", ciFile, i+1, name, strings.TrimSpace(line))
		}
	}

	// (4) The push triggers, which take no expression, hold exactly the list
	// (ci.yml) or a subset of it (certification.yml runs on the branch merges
	// land on; main is promoted, #778). Nothing else may appear there.
	inList := toSet(names)
	ciPush := pushTriggerBranches(src)
	require.NotEmpty(t, ciPush, "%s: no push trigger branches parsed; the parser is looking in the wrong place", ciFile)
	assert.True(t, sameSet(ciPush, names), "%s: push trigger branches %v do not match the integration branch list %v. `on: push: branches:` takes no expression, so it is the one spelling the list cannot reach: change it in the same edit", ciFile, ciPush, names)
	certPush := pushTriggerBranches(readFile(t, filepath.Join(root, certFile)))
	require.NotEmpty(t, certPush, "%s: no push trigger branches parsed; certification runs on a push to the integration branch", certFile)
	for _, b := range certPush {
		assert.True(t, inList[b], "%s: certification triggers on a push to %q, which is not in ci.yml's integration branch list %v", certFile, b, names)
	}
}

// TestRunnersPerMachineIsOneNumber pins row 9: the fair-share step divides by
// the number of runners the MACHINE says it runs (NOVA_RUNNERS_PER_MACHINE,
// exported by the runner service), never by a number written into the
// workflow. A literal divisor is a copy of a fact that lives on the machines,
// and it went stale the day the fleet went from four runners to eight.
func TestRunnersPerMachineIsOneNumber(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	const ciFile = ".github/workflows/ci.yml"
	src := readFile(t, filepath.Join(root, ciFile))

	step := stepText(src, "share of the machine")
	require.NotEmpty(t, step, "no fair-share step in ci.yml (a step whose name says it takes this runner's share of the machine); GOMAXPROCS per leg is what row 9 of #828 got wrong")
	assert.Contains(t, step, ciRunner+" runner-share", "the fair-share step does not call `ci runner-share`, which holds the division")
	// The divisor is the runner service's fact, and 8 is today's fleet as the
	// default when a machine does not say.
	assert.Equal(t, "NOVA_RUNNERS_PER_MACHINE", pkgselect.RunnersEnv, "the runner count is %s with default %d, want NOVA_RUNNERS_PER_MACHINE with default 8: the runner count is the runner service's fact", pkgselect.RunnersEnv, pkgselect.DefaultRunners)
	assert.Equal(t, 8, pkgselect.DefaultRunners, "the runner count is %s with default %d, want NOVA_RUNNERS_PER_MACHINE with default 8: the runner count is the runner service's fact", pkgselect.RunnersEnv, pkgselect.DefaultRunners)
	// The number a leg divided by is printed, so a leg's own log answers "how
	// many runners did this machine claim" without a trip to the machine.
	verb := readFile(t, filepath.Join(root, "tools", "ci", "sel_share.go"))
	assert.Contains(t, verb, "runners per machine", "`ci runner-share` does not print the runner count it used; the number a leg divided by must be readable in the leg's own log")
	// The divisor is what the machine says: 6 cores over 3 runners is 2, over 4
	// runners is 1, and a machine that says nothing divides by 8.
	for _, tc := range []struct {
		cores             int
		says              string
		wantRunners, want int
	}{{6, "3", 3, 2}, {6, "4", 4, 1}, {6, "", 8, 1}, {64, "", 8, 2}} {
		runners, got := pkgselect.RunnerShare(tc.cores, tc.says)
		assert.Equal(t, tc.wantRunners, runners, "%d cores, NOVA_RUNNERS_PER_MACHINE=%q: %d runners, a share of %d; want %d and %d", tc.cores, tc.says, runners, got, tc.wantRunners, tc.want)
		assert.Equal(t, tc.want, got, "%d cores, NOVA_RUNNERS_PER_MACHINE=%q: %d runners, a share of %d; want %d and %d", tc.cores, tc.says, runners, got, tc.wantRunners, tc.want)
	}
	for _, stale := range []string{"4 runners", "four runners", "FOUR runners"} {
		assert.NotContains(t, step, stale, "the fair-share step still says %q; the runner count is not written in this file", stale)
	}
}

// pushTriggerBranches returns the branches of the workflow's `push:` trigger,
// read as text: the `on:` block, the `push:` key inside it, and that key's
// `branches: [...]` line.
func pushTriggerBranches(src string) []string {
	inOn, inPush := false, false
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(line, " ") && trimmed != "" {
			// A top-level key: enters `on:` and leaves it again.
			inOn = trimmed == "on:"
			inPush = false
			continue
		}
		if !inOn || trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") {
			inPush = trimmed == "push:"
			continue
		}
		if !inPush {
			continue
		}
		if m := pushBranchesRe.FindStringSubmatch(line); m != nil {
			var out []string
			for _, b := range strings.Split(m[1], ",") {
				if b = strings.Trim(strings.TrimSpace(b), `"'`); b != "" {
					out = append(out, b)
				}
			}
			return out
		}
	}
	return nil
}

// stepText returns the text of the step whose `- name:` line contains marker,
// from that line to the next step or job key.
func stepText(src, marker string) string {
	lines := strings.Split(src, "\n")
	start := -1
	for i, line := range lines {
		if strings.Contains(line, "- name:") && strings.Contains(line, marker) {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}
	for i := start + 1; i < len(lines); i++ {
		if strings.Contains(lines[i], "- name:") || strings.Contains(lines[i], "- uses:") || jobKeyRe.MatchString(lines[i]) {
			return strings.Join(lines[start:i], "\n")
		}
	}
	return strings.Join(lines[start:], "\n")
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := toSet(b)
	for _, x := range a {
		if !set[x] {
			return false
		}
	}
	return true
}
