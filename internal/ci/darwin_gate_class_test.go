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

// darwin_gate_class_test.go holds ci.yml's darwin unit shards to the branch
// gate: they run where the target branch is an integration branch (dev, main)
// and on schedule and workflow_dispatch, and never for a change bound for a
// working branch. The Go is the same Go on both OSes, the Linux shards run every
// selected package, and the darwin legs are the slowest and the scarcest: cancelled at
// the two-minute cap whenever their host is loaded, which turned every working
// branch PR red for a reason the change did not cause. certification.yml is
// untouched: it runs on pushes to dev, nightly and on dispatch, and certifies on darwin.
//
// The gate is Go: pkgselect.DarwinOn decides it from the event and the target
// branch, and `ci test-matrix --target-branch` deals the matrix with it (its
// table test is pkg/pkgselect's TestDarwinOn). With the gate off every
// package rides the Linux shards and the darwin-only packages have no leg, as on
// the nightly schedule, so the `test` job has no darwin entry to run and `ci-ok`
// has no darwin job to wait for. This test holds what stays in the workflow:
// the gate's branch list is the integration list, the list step hands each event's
// own target branch to the verb, and no other job puts a change on a darwin runner.
// Read as text, like the other workflow class tests.

// listStepText returns the `list` step of test-packages, from its `- id: list`
// line to the end of the job.
func listStepText(t *testing.T, src string) string {
	t.Helper()
	block := jobBody(src, "test-packages")
	require.NotEmpty(t, block, "no test-packages job in ci.yml; the darwin gate has nothing to read")
	i := strings.Index(block, "- id: list")
	require.NotEqual(t, -1, i, "test-packages has no `- id: list` step; the darwin deal moved and the gate test is reading the wrong place")
	return block[i:]
}

// TestDarwinShardsRunOnlyForIntegrationBranches pins the gate: pkgselect's
// branch list equals the integration branches, the list step reads each event's
// own target branch into the verb, and no other job names a darwin runner.
func TestDarwinShardsRunOnlyForIntegrationBranches(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	step := listStepText(t, src)

	// (1) The gate's branch list is the integration list, in short form.
	defs := integrationListRe.FindAllStringSubmatch(src, -1)
	require.Len(t, defs, 1, "integration lists in ci.yml")
	var refs []string
	require.NoError(t, json.Unmarshal([]byte(defs[0][1]), &refs), "the integration list %q is not JSON", defs[0][1])
	var want []string
	for _, r := range refs {
		want = append(want, strings.TrimPrefix(r, "refs/heads/"))
	}
	assert.True(t, sameSet(pkgselect.DarwinBranches, want), "pkgselect.DarwinBranches is %v, the integration branches are %v: the darwin shards run for the integration branches and no others; change the two together", pkgselect.DarwinBranches, want)

	// (2) The list step hands the verb the event's own target branch: a pull
	// request's base_ref, the merge queue's base_ref, a push's ref name.
	assert.Contains(t, step, "test-matrix", "the list step does not run `ci test-matrix`")
	for _, need := range []string{
		"--target-branch",
		"github.base_ref",
		"github.event.merge_group.base_ref",
		"github.ref_name",
	} {
		assert.Contains(t, step, need, "the list step does not carry %q: each event must hand the verb its own target branch, or the darwin shards run for every branch", need)
	}

	// (3) No other job puts a change for a working branch on a darwin runner:
	// the only ci.yml matrix entries for macOS self-hosted runners are the ones
	// the list step deals, and the hosted macOS leg runs on push, schedule and
	// workflow_dispatch only.
	for name, body := range jobBlocks(src) {
		if name == "test-packages" {
			continue
		}
		assert.False(t, namesMacOSRunner(body), "job %s names a self-hosted macOS runner outside the gated deal; route it through test-packages' --target-branch gate", name)
		if strings.Contains(body, "macos-latest") {
			assert.False(t, jobRunsOnEvent(body, "pull_request") || jobRunsOnEvent(body, "merge_group"), "job %s runs macos-latest on a pull_request or merge_group; the darwin legs run on pushes to dev and main, on schedule and on workflow_dispatch only", name)
		}
	}
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
		assert.Equal(t, tc.want, namesMacOSRunner(tc.body), "namesMacOSRunner(%q) = %v, want %v", tc.body, namesMacOSRunner(tc.body), tc.want)
	}
}
