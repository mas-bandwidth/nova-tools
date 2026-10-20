package release

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// staleSHA is the commit every canned forge answer below is about.
const staleSHA = "abc123abc123def"

// checkJSON is one check run as the forge projects it for checkRuns: the check
// suite it belongs to and the exact commit, with no job name standing in for
// provenance (docs/SPEC-RELEASE.md section 19).
func checkJSON(name string, suiteID int64, status, conclusion string) string {
	return fmt.Sprintf(`{"Name":%q,"Status":%q,"Conclusion":%q,"SuiteID":%d,"HeadSHA":%q}`,
		name, status, conclusion, suiteID, staleSHA)
}

// workflowJSON is one workflow run as the forge projects it for checkRuns: the
// suite it answered, its run id and attempt, the workflow path, and when it was
// created and last updated (nova-sprint tla/ReleaseTrain.tla StaleVerdict and
// CutOnlyGreen).
func workflowJSON(suiteID, id int64, path, branch, event, conclusion string, attempt int, created, updated string) string {
	return fmt.Sprintf(`{"SuiteID":%d,"ID":%d,"HeadSHA":%q,"WorkflowPath":%q,"HeadBranch":%q,"Event":%q,"Conclusion":%q,"RunAttempt":%d,"CreatedAt":%q,"UpdatedAt":%q}`,
		suiteID, id, staleSHA, path, branch, event, conclusion, attempt, created, updated)
}

// staleCut reads the canned forge JSON through the production checkRuns join and
// then cuts the commit, so both the provenance read and the set-aside gate are
// exercised with no network, no process and no clock.
func staleCut(t *testing.T, checks, actions string) (code int, out, errs string, f *fakeForge, version string) {
	t.Helper()
	api := &checkProvenanceAPI{checks: checks, actions: actions}
	runs, err := checkRuns(context.Background(), "o/n", staleSHA, api.read)
	require.NoError(t, err)
	return cutCancelledSuite(t, runs)
}

// The 2026-10-20 shape: revert-on-red failed on the commit, and ci's latest
// attempt (attempt 2, same workflow run as the cancelled first) completed green
// after the revert run was created. The stale verdict is set aside and named
// with the ci run id and the attempt that replaced it (docs/SPEC-RELEASE.md
// section 19).
func TestAStaleRevertVerdictIsSetAsideWhenCIsLatestAttemptIsGreen(t *testing.T) {
	t.Parallel()
	checks := strings.Join([]string{
		checkJSON("revert-on-red", 300, "completed", "failure"),
		checkJSON("test", 200, "completed", "success"),
		checkJSON("lint", 200, "completed", "success"),
	}, "\n")
	actions := strings.Join([]string{
		workflowJSON(300, 4000, revertOnRedWorkflow, "main", "push", "failure", 1,
			"2026-10-20T03:00:00Z", "2026-10-20T03:01:00Z"),
		workflowJSON(200, 3000, ciWorkflow, "main", "push", "success", 2,
			"2026-10-20T02:00:00Z", "2026-10-20T04:00:00Z"),
	}, "\n")
	code, out, errs, f, version := staleCut(t, checks, actions)
	assert.Equal(t, 0, code, "out=%s errs=%s", out, errs)
	require.Len(t, f.tagged, 1, "the cut must tag: %v", f.tagged)
	assert.Contains(t, out, "ci-set-aside=4000:stale-verdict-of\\x3d3000@2")
	assert.Contains(t, f.messages[version], "CI set aside: 4000:stale-verdict-of=3000@2")
}

// A revert-on-red verdict stays live when ci's latest attempt is red, and when
// ci is green but completed before the revert run was created: only a later
// green attempt replaces the verdict (docs/SPEC-RELEASE.md section 19).
func TestALiveRevertVerdictIsNotStaleAndStillRefuses(t *testing.T) {
	t.Parallel()
	checks := strings.Join([]string{
		checkJSON("revert-on-red", 300, "completed", "failure"),
		checkJSON("test", 200, "completed", "success"),
	}, "\n")
	for _, tc := range []struct {
		name, conclusion, updated string
	}{
		{name: "ci latest attempt red", conclusion: "failure", updated: "2026-10-20T04:00:00Z"},
		{name: "ci green but older than the verdict", conclusion: "success", updated: "2026-10-20T02:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			actions := strings.Join([]string{
				workflowJSON(300, 4000, revertOnRedWorkflow, "main", "push", "failure", 1,
					"2026-10-20T03:00:00Z", "2026-10-20T03:01:00Z"),
				workflowJSON(200, 3000, ciWorkflow, "main", "push", tc.conclusion, 2,
					"2026-10-20T01:00:00Z", tc.updated),
			}, "\n")
			code, out, errs, f, _ := staleCut(t, checks, actions)
			assert.Equal(t, 2, code, "out=%s errs=%s", out, errs)
			assert.Contains(t, errs, "revert-on-red=failure")
			assert.NotContains(t, out, "ci-set-aside")
			assert.Empty(t, f.tagged, "a live verdict must not be tagged")
		})
	}
}

// An earlier attempt of a workflow run is set aside when the latest attempt of
// the same run completed green and ran every job name the earlier one did
// (nova-sprint tla/ReleaseTrain.tla CutOnlyGreen; docs/SPEC-RELEASE.md section
// 19).
func TestAnEarlierAttemptIsSupersededByTheLatestGreenAttempt(t *testing.T) {
	t.Parallel()
	checks := strings.Join([]string{
		checkJSON("test", 100, "completed", "failure"),
		checkJSON("test", 101, "completed", "success"),
		checkJSON("lint", 101, "completed", "success"),
	}, "\n")
	actions := strings.Join([]string{
		workflowJSON(100, 9000, ciWorkflow, "main", "push", "failure", 1,
			"2026-10-20T02:00:00Z", "2026-10-20T03:00:00Z"),
		workflowJSON(101, 9000, ciWorkflow, "main", "push", "success", 2,
			"2026-10-20T03:30:00Z", "2026-10-20T04:00:00Z"),
	}, "\n")
	code, out, errs, f, version := staleCut(t, checks, actions)
	assert.Equal(t, 0, code, "out=%s errs=%s", out, errs)
	require.Len(t, f.tagged, 1, "the cut must tag: %v", f.tagged)
	assert.Contains(t, out, "ci-set-aside=9000:superseded-attempt\\x3d9000@2")
	assert.Contains(t, f.messages[version], "CI set aside: 9000:superseded-attempt=9000@2")
}

// Another workflow's failure is never set aside by a later green ci run: only a
// revert-on-red verdict is judged beside ci (docs/SPEC-RELEASE.md section 19).
func TestAnotherWorkflowsFailureIsNeverSetAside(t *testing.T) {
	t.Parallel()
	checks := strings.Join([]string{
		checkJSON("validate", 400, "completed", "failure"),
		checkJSON("test", 200, "completed", "success"),
	}, "\n")
	actions := strings.Join([]string{
		workflowJSON(400, 5000, ".github/workflows/required.yml", "main", "push", "failure", 1,
			"2026-10-20T03:00:00Z", "2026-10-20T03:01:00Z"),
		workflowJSON(200, 3000, ciWorkflow, "main", "push", "success", 2,
			"2026-10-20T02:00:00Z", "2026-10-20T04:00:00Z"),
	}, "\n")
	code, out, errs, f, _ := staleCut(t, checks, actions)
	assert.Equal(t, 2, code, "out=%s errs=%s", out, errs)
	assert.Contains(t, errs, "validate=failure")
	assert.NotContains(t, out, "ci-set-aside")
	assert.Empty(t, f.tagged, "another workflow's failure must not be tagged")
}

// The stale set-aside travels into the tag annotation, so the record of what was
// ignored outlives the terminal (docs/SPEC-RELEASE.md section 19).
func TestTheStaleSetAsideIsInTheAnnotation(t *testing.T) {
	t.Parallel()
	checks := strings.Join([]string{
		checkJSON("revert-on-red", 300, "completed", "cancelled"),
		checkJSON("test", 200, "completed", "success"),
	}, "\n")
	actions := strings.Join([]string{
		workflowJSON(300, 4000, revertOnRedWorkflow, "main", "push", "cancelled", 1,
			"2026-10-20T03:00:00Z", "2026-10-20T03:01:00Z"),
		workflowJSON(200, 3000, ciWorkflow, "main", "push", "success", 3,
			"2026-10-20T02:00:00Z", "2026-10-20T04:00:00Z"),
	}, "\n")
	code, out, errs, f, version := staleCut(t, checks, actions)
	assert.Equal(t, 0, code, "out=%s errs=%s", out, errs)
	require.Len(t, f.tagged, 1, "the cut must tag: %v", f.tagged)
	assert.Contains(t, f.messages[version], "CI set aside: 4000:stale-verdict-of=3000@3")
}
