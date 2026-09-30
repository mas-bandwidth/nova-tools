package ci

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// darwin_gate_class_test.go holds ci.yml's darwin unit shards to the branch
// gate: they run where the target branch is an integration branch (dev, main)
// and on schedule and workflow_dispatch, and never for a change bound for a
// working branch. The Go is the same Go on both OSes, the Linux shards run every
// selected package, and the darwin legs are the slowest and the scarcest: cancelled at
// the two-minute cap whenever their host is loaded, which turned every working
// branch PR red for a reason the change did not cause. certification.yml is
// untouched: it runs on pushes to dev, nightly and on dispatch, and certifies on darwin.
//
// The gate lives in test-packages' `list` step, which deals the matrix: with it
// off, every package rides the Linux shards and the darwin-only packages have no
// leg, as on the nightly schedule, so the `test` job has no darwin entry to run
// and `ci-ok` has no darwin job to wait for. Read as text, like the other
// workflow class tests.

var darwinBranchesRe = regexp.MustCompile(`(?m)^\s*DARWIN_BRANCHES="([^"]*)"\s*$`)

// listStepText returns the `list` step of test-packages, from its `- id: list`
// line to the end of the job.
func listStepText(t *testing.T, src string) string {
	t.Helper()
	block := jobBody(src, "test-packages")
	if block == "" {
		t.Fatal("no test-packages job in ci.yml; the darwin gate has nothing to read")
	}
	i := strings.Index(block, "- id: list")
	if i < 0 {
		t.Fatal("test-packages has no `- id: list` step; the darwin deal moved and the gate test is reading the wrong place")
	}
	return block[i:]
}

// TestDarwinShardsRunOnlyForIntegrationBranches pins the gate: the branch list
// equals the integration branches, each event reads its own target branch, and
// the deal gives a darwin shard nothing while the gate is off.
func TestDarwinShardsRunOnlyForIntegrationBranches(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	step := listStepText(t, src)

	// (1) The gate's branch list is the integration list, in short form.
	m := darwinBranchesRe.FindStringSubmatch(step)
	if m == nil {
		t.Fatal(`the list step carries no DARWIN_BRANCHES="..." line: the darwin shards run for every target branch, so a working branch PR waits on (and is cancelled with) the darwin legs`)
	}
	defs := integrationListRe.FindAllStringSubmatch(src, -1)
	if len(defs) != 1 {
		t.Fatalf("found %d integration lists in ci.yml, want 1", len(defs))
	}
	var refs []string
	if err := json.Unmarshal([]byte(defs[0][1]), &refs); err != nil {
		t.Fatalf("the integration list %q is not JSON: %v", defs[0][1], err)
	}
	var want []string
	for _, r := range refs {
		want = append(want, strings.TrimPrefix(r, "refs/heads/"))
	}
	got := strings.Fields(m[1])
	if !sameSet(got, want) {
		t.Errorf("DARWIN_BRANCHES is %v, the integration branches are %v: the darwin shards run for the integration branches and no others; change the two together", got, want)
	}

	// (2) Each event reads its own target branch: a pull_request its base, the
	// merge queue its base_ref, a push its ref name.
	for _, need := range []string{
		`pull_request) darwin_target="${{ github.base_ref }}"`,
		`merge_group) darwin_target="${{ github.event.merge_group.base_ref }}"`,
		`push) darwin_target="${{ github.ref_name }}"`,
		`schedule|workflow_dispatch) darwin_on=true`,
		`*" $darwin_target "*) darwin_on=true`,
	} {
		if !strings.Contains(step, need) {
			t.Errorf("the list step does not carry %q: each event must read its own target branch, and schedule and workflow_dispatch keep the darwin shards", need)
		}
	}
	if !strings.Contains(step, "darwin_on=false") {
		t.Error("the list step never starts darwin_on at false: the gate must default to off")
	}

	// (3) The deal honours the gate: before any package is appended to the darwin
	// group, the loop sends everything to the Linux shards while the gate is off.
	loop := dealLoop(step)
	if loop == "" {
		t.Fatal("no `for pkg in \"${all[@]}\"` deal loop in the list step")
	}
	guard := strings.Index(loop, `[ "$darwin_on" != true ]`)
	darwin := strings.Index(loop, "studio_grp[")
	if darwin < 0 {
		t.Fatal("the deal loop never appends to the darwin group; the test is reading the wrong loop")
	}
	if guard < 0 || guard > darwin {
		t.Error(`the deal loop appends to the darwin group with no earlier [ "$darwin_on" != true ] guard: a pull request to a working branch gets darwin shards`)
	} else if between := loop[guard:darwin]; !strings.Contains(between, "space_grp[") || !strings.Contains(between, "continue") {
		t.Error("with the gate off the deal must put the package on a Linux shard and continue before the darwin append")
	}

	// (4) With the gate off the darwin-only packages are dropped from the
	// selection before anything is dealt, so a change that selected only those
	// takes the existing empty-change leg and not the "no packages" refusal.
	if !strings.Contains(step, `if [ "$darwin_on" != true ]; then
            kept=()`) {
		t.Error("with the gate off the darwin-only packages must be dropped from the selection before the deal, so a change that selected only them falls to the nothing-to-test leg")
	}

	// (5) No other job puts a change for a working branch on a darwin runner:
	// the only ci.yml matrix entries for macOS self-hosted runners are the ones
	// the list step deals, and the hosted macOS leg runs on push, schedule and
	// workflow_dispatch only.
	for name, body := range jobBlocks(src) {
		if name == "test-packages" {
			continue
		}
		if namesMacOSRunner(body) {
			t.Errorf("job %s names a self-hosted macOS runner outside the gated deal; route it through test-packages' DARWIN gate", name)
		}
		if strings.Contains(body, "macos-latest") && (jobRunsOnEvent(body, "pull_request") || jobRunsOnEvent(body, "merge_group")) {
			t.Errorf("job %s runs macos-latest on a pull_request or merge_group; the darwin legs run on pushes to dev and main, on schedule and on workflow_dispatch only", name)
		}
	}
}

// dealLoop returns the deal loop of the list step: from the loop's `for` line
// to its closing `done`.
func dealLoop(step string) string {
	const head = `for pkg in "${all[@]}"; do`
	i := strings.Index(step, head)
	if i < 0 {
		return ""
	}
	rest := step[i:]
	j := strings.Index(rest, "\n          done\n")
	if j < 0 {
		return rest
	}
	return rest[:j]
}

// macOSLabelRe finds a macOS label as one element of a label list: flow or block
// form, in any position, bare or quoted, in any case.
var macOSLabelRe = regexp.MustCompile(`(?i)(^|[\[,\s'"-])macos($|[\],\s'"])`)

// macOSOSKeyRe finds an `os` key whose value is macOS, in a flow map or a
// matrix entry, bare or quoted (the dealt entries carry `"os":"macOS"`).
var macOSOSKeyRe = regexp.MustCompile(`(?i)["']?\bos["']?\s*:\s*["']?macos(["'\s,}\]]|$)`)

// namesMacOSRunner reports whether a job body puts a macOS label on a runs-on
// list, in any position and any spelling, or carries an os key of macOS.
func namesMacOSRunner(body string) bool {
	if macOSOSKeyRe.MatchString(body) {
		return true
	}
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || !strings.HasPrefix(trimmed, "runs-on:") {
			continue
		}
		spec := strings.TrimPrefix(trimmed, "runs-on:")
		// A block list continues on the `- item` lines below the key.
		for j := i + 1; j < len(lines); j++ {
			next := strings.TrimSpace(lines[j])
			if !strings.HasPrefix(next, "- ") {
				break
			}
			spec += " " + next
		}
		if macOSLabelRe.MatchString(spec) {
			return true
		}
	}
	return false
}

// TestMacOSRunnerMatchCatchesEveryPosition probes namesMacOSRunner: the label in
// any position, quoted or in a block list, is caught; a Linux list is not.
func TestMacOSRunnerMatchCatchesEveryPosition(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		body string
		want bool
	}{
		{"    runs-on: [self-hosted, macOS, ARM64]", true},
		{"    runs-on: [self-hosted, ARM64, macOS]", true},
		{`    runs-on: [self-hosted, "macOS", ARM64]`, true},
		{"    runs-on: [macos]", true},
		{"    runs-on:\n      - self-hosted\n      - macOS\n    steps:", true},
		{`    strategy: {matrix: {entry: [{"os":"macOS"}]}}`, true},
		{"    runs-on: [self-hosted, linux, x64, space]", false},
		{`    runs-on: [self-hosted, "${{ matrix.entry.os }}", "${{ matrix.entry.arch }}"]`, false},
		{"    # runs-on: [self-hosted, macOS]", false},
	} {
		if got := namesMacOSRunner(tc.body); got != tc.want {
			t.Errorf("namesMacOSRunner(%q) = %v, want %v", tc.body, got, tc.want)
		}
	}
}
