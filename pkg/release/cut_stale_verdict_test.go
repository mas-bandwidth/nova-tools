package release

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// staleAPI answers checkRuns with canned JSON: the check runs first, then the
// workflow runs, and nothing else. No process, socket or network is involved.
type staleAPI struct {
	checks, actions string
	calls           int
}

func (f *staleAPI) read(_ context.Context, _ ...string) (string, error) {
	f.calls++
	switch f.calls {
	case 1:
		return f.checks, nil
	case 2:
		return f.actions, nil
	}
	return "", errors.New("unexpected extra forge read")
}

const staleSHA = "abc123abc123def"

// cannedRuns reads the 2026-10-20 shape from canned JSON exactly as the cut
// reads it: check runs joined to their workflow run's id, attempt and times.
func cannedRuns(t *testing.T, checks, actions string) []checkRun {
	t.Helper()
	api := &staleAPI{checks: checks, actions: actions}
	runs, err := checkRuns(context.Background(), "o/n", staleSHA, api.read)
	require.NoError(t, err)
	require.Len(t, runs, 2)
	return runs
}

// staleVerdictJSON is the 2026-10-20 shape: revert-on-red read attempt 1 of
// main's ci run 39346166100, filed a failed check, and the latest attempt of
// that same run completed green after the revert check was created.
func staleVerdictJSON() (string, string) {
	checks := `{"Name":"revert-on-red","Status":"completed","Conclusion":"failure","SuiteID":11,"HeadSHA":"` + staleSHA + `"}
{"Name":"test (ubuntu-latest, 1)","Status":"completed","Conclusion":"success","SuiteID":22,"HeadSHA":"` + staleSHA + `"}`
	actions := `{"SuiteID":11,"ID":39346150000,"HeadSHA":"` + staleSHA + `","WorkflowPath":".github/workflows/revert-on-red.yml","HeadBranch":"main","Event":"push","Conclusion":"failure","UpdatedAt":"2026-10-20T03:52:00Z","RunAttempt":1,"CreatedAt":"2026-10-20T03:50:00Z"}
{"SuiteID":22,"ID":39346166100,"HeadSHA":"` + staleSHA + `","WorkflowPath":".github/workflows/ci.yml","HeadBranch":"main","Event":"push","Conclusion":"success","UpdatedAt":"2026-10-20T04:10:00Z","RunAttempt":2,"CreatedAt":"2026-10-20T03:55:00Z"}`
	return checks, actions
}

// A completed failed revert-on-red check is set aside as a stale verdict when
// the latest attempt of ci.yml at the same commit completed green after the
// revert check was created (the 2026-10-20 shape; nova-sprint
// tla/ReleaseTrain.tla StaleVerdict, docs/SPEC-RELEASE.md section 19).
func TestAStaleRevertVerdictIsSetAsideWhenCIsLatestAttemptIsGreen(t *testing.T) {
	t.Parallel()
	checks, actions := staleVerdictJSON()
	kept, setAside := sourceChecks(cannedRuns(t, checks, actions), nil, "v1.6.10")
	require.Len(t, kept, 1)
	assert.Equal(t, "test (ubuntu-latest, 1)", kept[0].Name)
	require.Len(t, setAside, 1)
	assert.Equal(t, "revert-on-red", setAside[0].Name)
	assert.Equal(t, "39346150000:stale-verdict-of=39346166100@2", ciSetAside(setAside))
}

// A revert-on-red failure still refuses when ci's latest attempt is red, or
// when it is green but completed before the revert check was created: only a
// later green clears the stale verdict (tla/ReleaseTrain.tla CutOnlyGreen,
// docs/SPEC-RELEASE.md section 19).
func TestALiveRevertVerdictStillRefuses(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, conclusion, updated string
	}{
		{name: "ci latest attempt red", conclusion: "failure", updated: "2026-10-20T04:10:00Z"},
		{name: "ci green but older than the revert run", conclusion: "success", updated: "2026-10-20T03:40:00Z"},
		{name: "ci latest attempt pending", conclusion: "", updated: "2026-10-20T04:10:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checks := `{"Name":"revert-on-red","Status":"completed","Conclusion":"failure","SuiteID":11,"HeadSHA":"` + staleSHA + `"}
{"Name":"test (ubuntu-latest, 1)","Status":"completed","Conclusion":"success","SuiteID":22,"HeadSHA":"` + staleSHA + `"}`
			actions := `{"SuiteID":11,"ID":39346150000,"HeadSHA":"` + staleSHA + `","WorkflowPath":".github/workflows/revert-on-red.yml","HeadBranch":"main","Event":"push","Conclusion":"failure","UpdatedAt":"2026-10-20T03:52:00Z","RunAttempt":1,"CreatedAt":"2026-10-20T03:50:00Z"}
{"SuiteID":22,"ID":39346166100,"HeadSHA":"` + staleSHA + `","WorkflowPath":".github/workflows/ci.yml","HeadBranch":"main","Event":"push","Conclusion":"` + tc.conclusion + `","UpdatedAt":"` + tc.updated + `","RunAttempt":2,"CreatedAt":"2026-10-20T03:55:00Z"}`
			kept, setAside := sourceChecks(cannedRuns(t, checks, actions), nil, "v1.6.10")
			assert.Empty(t, setAside)
			require.Len(t, kept, 2)
			assert.Contains(t, ciNotGreenNames(kept), "revert-on-red=failure")
		})
	}
}

// ciNotGreenNames flattens the gate's failing checks for the live-verdict
// assertions.
func ciNotGreenNames(runs []checkRun) []string {
	failing, pending := ciNotGreen(asCheckRuns(runs))
	return append(failing, pending...)
}

// An earlier attempt of one workflow run is set aside when the latest attempt
// (same run id, higher run_attempt) completed green with every job name of the
// earlier one (tla/ReleaseTrain.tla StaleVerdict, docs/SPEC-RELEASE.md section
// 19).
func TestAnEarlierAttemptIsSupersededByTheLatestGreenAttempt(t *testing.T) {
	t.Parallel()
	checks := `{"Name":"test (ubuntu-latest, 1)","Status":"completed","Conclusion":"failure","SuiteID":31,"HeadSHA":"` + staleSHA + `"}
{"Name":"test (ubuntu-latest, 1)","Status":"completed","Conclusion":"success","SuiteID":32,"HeadSHA":"` + staleSHA + `"}`
	actions := `{"SuiteID":31,"ID":7000,"HeadSHA":"` + staleSHA + `","WorkflowPath":".github/workflows/ci.yml","HeadBranch":"main","Event":"push","Conclusion":"failure","UpdatedAt":"2026-10-20T04:00:00Z","RunAttempt":1,"CreatedAt":"2026-10-20T03:50:00Z"}
{"SuiteID":32,"ID":7000,"HeadSHA":"` + staleSHA + `","WorkflowPath":".github/workflows/ci.yml","HeadBranch":"main","Event":"push","Conclusion":"success","UpdatedAt":"2026-10-20T04:10:00Z","RunAttempt":2,"CreatedAt":"2026-10-20T04:05:00Z"}`
	kept, setAside := sourceChecks(cannedRuns(t, checks, actions), nil, "v1.6.10")
	require.Len(t, kept, 1)
	assert.Equal(t, "success", kept[0].Conclusion)
	require.Len(t, setAside, 1)
	assert.Equal(t, "7000:superseded-attempt=7000@2", ciSetAside(setAside))
}

// A failed check of any other workflow is never set aside, even when ci is
// green later: only revert-on-red's stale verdict and a superseded attempt are
// cleared (tla/ReleaseTrain.tla CutOnlyGreen, docs/SPEC-RELEASE.md section 19).
func TestAnotherWorkflowsFailureIsNeverSetAside(t *testing.T) {
	t.Parallel()
	checks := `{"Name":"lint","Status":"completed","Conclusion":"failure","SuiteID":41,"HeadSHA":"` + staleSHA + `"}
{"Name":"test (ubuntu-latest, 1)","Status":"completed","Conclusion":"success","SuiteID":42,"HeadSHA":"` + staleSHA + `"}`
	actions := `{"SuiteID":41,"ID":8000,"HeadSHA":"` + staleSHA + `","WorkflowPath":".github/workflows/required.yml","HeadBranch":"main","Event":"push","Conclusion":"failure","UpdatedAt":"2026-10-20T03:52:00Z","RunAttempt":1,"CreatedAt":"2026-10-20T03:50:00Z"}
{"SuiteID":42,"ID":8001,"HeadSHA":"` + staleSHA + `","WorkflowPath":".github/workflows/ci.yml","HeadBranch":"main","Event":"push","Conclusion":"success","UpdatedAt":"2026-10-20T04:10:00Z","RunAttempt":1,"CreatedAt":"2026-10-20T03:55:00Z"}`
	kept, setAside := sourceChecks(cannedRuns(t, checks, actions), nil, "v1.6.10")
	assert.Empty(t, setAside)
	require.Len(t, kept, 2)
	assert.Contains(t, ciNotGreenNames(kept), "lint=failure")
}

// The stale set-aside travels into the tag annotation and the CHANGELOG as the
// cancelled suite's set-aside already does (docs/SPEC-RELEASE.md section 19).
func TestTheStaleSetAsideIsInTheAnnotation(t *testing.T) {
	t.Parallel()
	checks, actions := staleVerdictJSON()
	code, out, errs, f, version := cutCancelledSuite(t, cannedRuns(t, checks, actions))
	assert.Equal(t, 0, code, "out=%s errs=%s", out, errs)
	require.Len(t, f.tagged, 1, "the cut must tag: %v", f.tagged)
	assert.Contains(t, out, "ci-set-aside=39346150000:stale-verdict-of\\x3d39346166100@2")
	assert.Contains(t, f.messages[version], "CI set aside: 39346150000:stale-verdict-of=39346166100@2")
}
