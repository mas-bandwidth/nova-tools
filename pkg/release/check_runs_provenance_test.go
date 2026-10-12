package release

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// checkProvenanceAPI supplies the projected gh JSON without a process or network.
type checkProvenanceAPI struct {
	checks, actions, config        string
	checkErr, actionErr, configErr error
	calls                          [][]string
}

func (f *checkProvenanceAPI) read(_ context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	if len(f.calls) == 1 {
		return f.checks, f.checkErr
	}
	if len(f.calls) == 2 {
		return f.actions, f.actionErr
	}
	if len(f.calls) == 3 {
		return f.config, f.configErr
	}
	return "", errors.New("unexpected extra forge read")
}

// A branch and a tag can share a name; only the immutable workflow's trigger
// configuration proves that a push to the release workflow is a tag push.
func TestCheckRunsProvesTagPushFromTheWorkflowAtTheCheckedCommit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, config string
		proved       bool
	}{
		{name: "tag only", config: "on:\n  push:\n    tags: ['v*']\n  workflow_dispatch:\n", proved: true},
		{name: "same named branch allowed", config: "on:\n  push:\n    tags: ['v*']\n    branches: ['v1.2.3']\n"},
		{name: "branch ignore present", config: "on:\n  push:\n    tags: ['v*']\n    branches-ignore: []\n"},
		{name: "scalar push", config: "on: push\n"},
		{name: "sequence push", config: "on: [push, workflow_dispatch]\n"},
		{name: "empty tags", config: "on:\n  push:\n    tags: []\n"},
		{name: "scalar tag filter", config: "on:\n  push:\n    tags: 'v*'\n"},
		{name: "nonstring tag filter", config: "on:\n  push:\n    tags: [17]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &checkProvenanceAPI{
				checks: `{"Name":"validate","Status":"completed","Conclusion":"cancelled","SuiteID":17,"HeadSHA":"abc123"}
{"Name":"publish","Status":"completed","Conclusion":"cancelled","SuiteID":17,"HeadSHA":"abc123"}`,
				actions: `{"SuiteID":17,"HeadSHA":"abc123","WorkflowPath":".github/workflows/release.yml","HeadBranch":"v1.2.3","Event":"push"}`,
				config:  base64.StdEncoding.EncodeToString([]byte(tc.config)),
			}
			runs, err := checkRuns(context.Background(), "o/n", "abc123", f.read)
			require.NoError(t, err)
			require.Len(t, runs, 2)
			for _, run := range runs {
				assert.Equal(t, tc.proved, run.TagPush, "the ref name alone cannot distinguish a branch from a tag")
			}
			require.Len(t, f.calls, 3, "all checks share one immutable workflow read")
			assert.Equal(t, []string{"api", "repos/o/n/contents/.github/workflows/release.yml?ref=abc123", "--jq", ".content"}, f.calls[2])
		})
	}
}

func TestCheckRunsDoesNotReadWorkflowConfigurationForUnprovedReleaseRuns(t *testing.T) {
	t.Parallel()
	const action = `{"SuiteID":17,"HeadSHA":"abc123","WorkflowPath":".github/workflows/release.yml","HeadBranch":"v1.2.3","Event":"push"}`
	for _, tc := range []struct{ name, actions string }{
		{name: "forged metadata for another commit", actions: strings.Replace(action, "abc123", "another", 1)},
		{name: "other workflow", actions: strings.Replace(action, "release.yml", "required.yml", 1)},
		{name: "manual release run", actions: strings.Replace(action, `"push"`, `"workflow_dispatch"`, 1)},
		{name: "conflicting workflow", actions: action + "\n" + strings.Replace(action, "release.yml", "required.yml", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &checkProvenanceAPI{
				checks:    `{"Name":"validate","Status":"completed","Conclusion":"cancelled","SuiteID":17,"HeadSHA":"abc123"}`,
				actions:   tc.actions,
				configErr: errors.New("configuration must not be queried"),
			}
			runs, err := checkRuns(context.Background(), "o/n", "abc123", f.read)
			require.NoError(t, err)
			require.Len(t, runs, 1)
			assert.False(t, runs[0].TagPush)
			assert.Len(t, f.calls, 2)
		})
	}
}

func TestCheckRunsRefusesUnreadableWorkflowConfiguration(t *testing.T) {
	t.Parallel()
	readErr := errors.New("workflow contents unavailable")
	for _, tc := range []struct {
		name, config string
		err          error
	}{
		{name: "API failure after content", config: base64.StdEncoding.EncodeToString([]byte("on:\n  push:\n    tags: ['v*']\n")), err: readErr},
		{name: "invalid base64", config: "not base64!"},
		{name: "malformed YAML", config: base64.StdEncoding.EncodeToString([]byte("on: [push\n"))},
		{name: "duplicate YAML keys", config: base64.StdEncoding.EncodeToString([]byte("on:\n  push:\n    tags: ['v*']\n  push:\n    branches: ['v1.2.3']\n"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &checkProvenanceAPI{
				checks:  `{"Name":"validate","Status":"completed","Conclusion":"cancelled","SuiteID":17,"HeadSHA":"abc123"}`,
				actions: `{"SuiteID":17,"HeadSHA":"abc123","WorkflowPath":".github/workflows/release.yml","HeadBranch":"v1.2.3","Event":"push"}`,
				config:  tc.config, configErr: tc.err,
			}
			runs, err := checkRuns(context.Background(), "o/n", "abc123", f.read)
			require.Error(t, err)
			assert.Empty(t, runs, "unreadable configuration provides no successful provenance result")
			assert.Len(t, f.calls, 3)
			if tc.err != nil {
				assert.ErrorIs(t, err, readErr)
			}
		})
	}
}

// Suite and commit identity, rather than a job's displayed name, establish provenance.
func TestCheckRunsJoinsProvenanceBySuiteAndSHAInOnePaginatedRead(t *testing.T) {
	t.Parallel()
	f := &checkProvenanceAPI{
		checks: `{"Name":"validate","Status":"completed","Conclusion":"cancelled","SuiteID":17,"HeadSHA":"abc123"}
{"Name":"validate","Status":"completed","Conclusion":"cancelled","SuiteID":18,"HeadSHA":"abc123"}
{"Name":"lint","Status":"completed","Conclusion":"cancelled","SuiteID":17,"HeadSHA":"abc123"}
{"Name":"validate","Status":"completed","Conclusion":"cancelled","SuiteID":19,"HeadSHA":"abc123"}`,
		actions: `{"SuiteID":18,"HeadSHA":"abc123","WorkflowPath":".github/workflows/required.yml","HeadBranch":"dev","Event":"push"}
{"SuiteID":17,"HeadSHA":"other","WorkflowPath":".github/workflows/wrong.yml","HeadBranch":"dev","Event":"push"}
{"SuiteID":17,"HeadSHA":"abc123","WorkflowPath":".github/workflows/pr-check.yml","HeadBranch":"dev","Event":"push"}
{"SuiteID":99,"HeadSHA":"abc123","WorkflowPath":".github/workflows/unrelated.yml","HeadBranch":"dev","Event":"push"}`,
	}
	runs, err := checkRuns(context.Background(), "o/n", "abc123", f.read)
	require.NoError(t, err)
	require.Len(t, runs, 4)
	assert.Equal(t, []checkRun{
		{CheckRun: CheckRun{Name: "validate", Status: "completed", Conclusion: "cancelled", WorkflowPath: ".github/workflows/pr-check.yml", HeadBranch: "dev", Event: "push"}, SuiteID: 17},
		{CheckRun: CheckRun{Name: "validate", Status: "completed", Conclusion: "cancelled", WorkflowPath: ".github/workflows/required.yml", HeadBranch: "dev", Event: "push"}, SuiteID: 18},
		{CheckRun: CheckRun{Name: "lint", Status: "completed", Conclusion: "cancelled", WorkflowPath: ".github/workflows/pr-check.yml", HeadBranch: "dev", Event: "push"}, SuiteID: 17},
		{CheckRun: CheckRun{Name: "validate", Status: "completed", Conclusion: "cancelled"}, SuiteID: 19},
	}, runs)
	assert.Equal(t, [][]string{
		{"api", "--paginate", "repos/o/n/commits/abc123/check-runs", "--jq", ".check_runs[] | {Name:.name, Status:.status, Conclusion:.conclusion, SuiteID:.check_suite.id, HeadSHA:.head_sha}"},
		{"api", "--paginate", "repos/o/n/actions/runs?head_sha=abc123&per_page=100", "--jq", ".workflow_runs[] | {SuiteID:.check_suite_id, ID:.id, HeadSHA:.head_sha, WorkflowPath:.path, HeadBranch:.head_branch, Event:.event, Conclusion:.conclusion, UpdatedAt:.updated_at}"},
	}, f.calls, "all checks share one paginated workflow-run query for the exact commit")
}

func TestCheckRunsLeavesUnprovedOrAmbiguousProvenanceBlank(t *testing.T) {
	t.Parallel()
	const first = `{"SuiteID":17,"HeadSHA":"abc123","WorkflowPath":".github/workflows/pr-check.yml","HeadBranch":"dev","Event":"push"}`
	for _, tc := range []struct {
		name, actions string
		proved        bool
	}{
		{name: "no workflow runs"},
		{name: "different suite", actions: strings.Replace(first, `"SuiteID":17`, `"SuiteID":18`, 1)},
		{name: "different commit", actions: strings.Replace(first, "abc123", "another", 1)},
		{name: "missing suite", actions: strings.Replace(first, `"SuiteID":17,`, "", 1)},
		{name: "missing commit", actions: strings.Replace(first, `"HeadSHA":"abc123",`, "", 1)},
		{name: "identical repeat", actions: first + "\n" + first, proved: true},
		{name: "workflow conflict stays ambiguous", actions: first + "\n" + strings.Replace(first, "pr-check.yml", "required.yml", 1) + "\n" + first},
		{name: "branch conflict", actions: first + "\n" + strings.Replace(first, `"dev"`, `"main"`, 1)},
		{name: "event conflict", actions: first + "\n" + strings.Replace(first, `"push"`, `"pull_request"`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &checkProvenanceAPI{
				checks:  `{"Name":"validate","Status":"completed","Conclusion":"cancelled","SuiteID":17,"HeadSHA":"abc123"}`,
				actions: tc.actions,
			}
			runs, err := checkRuns(context.Background(), "o/n", "abc123", f.read)
			require.NoError(t, err)
			require.Len(t, runs, 1)
			want := checkRun{CheckRun: CheckRun{Name: "validate", Status: "completed", Conclusion: "cancelled"}, SuiteID: 17}
			if tc.proved {
				want.WorkflowPath, want.HeadBranch, want.Event = ".github/workflows/pr-check.yml", "dev", "push"
			}
			assert.Equal(t, want, runs[0])
			assert.Len(t, f.calls, 2)
		})
	}
}

func TestCheckRunsDoesNotGuessMissingWorkflowMetadata(t *testing.T) {
	t.Parallel()
	for _, missing := range []string{"WorkflowPath", "HeadBranch", "Event"} {
		t.Run(missing, func(t *testing.T) {
			t.Parallel()
			fields := map[string]string{"WorkflowPath": ".github/workflows/pr-check.yml", "HeadBranch": "dev", "Event": "push"}
			fields[missing] = ""
			f := &checkProvenanceAPI{
				checks:  `{"Name":"validate","Status":"completed","Conclusion":"cancelled","SuiteID":17,"HeadSHA":"abc123"}`,
				actions: `{"SuiteID":17,"HeadSHA":"abc123","WorkflowPath":"` + fields["WorkflowPath"] + `","HeadBranch":"` + fields["HeadBranch"] + `","Event":"` + fields["Event"] + `"}`,
			}
			runs, err := checkRuns(context.Background(), "o/n", "abc123", f.read)
			require.NoError(t, err)
			require.Len(t, runs, 1)
			got := map[string]string{"WorkflowPath": runs[0].WorkflowPath, "HeadBranch": runs[0].HeadBranch, "Event": runs[0].Event}
			assert.Empty(t, got[missing], "missing provenance must not be inferred from a name or another field")
		})
	}
}

func TestCheckRunsDoesNotQueryMetadataWithoutACandidate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, checks string }{
		{name: "no checks"},
		{name: "successful check", checks: `{"Name":"validate","Status":"completed","Conclusion":"success","SuiteID":17,"HeadSHA":"abc123"}`},
		{name: "in progress", checks: `{"Name":"validate","Status":"in_progress","Conclusion":"cancelled","SuiteID":17,"HeadSHA":"abc123"}`},
		{name: "no suite", checks: `{"Name":"validate","Status":"completed","Conclusion":"cancelled","HeadSHA":"abc123"}`},
		{name: "no commit", checks: `{"Name":"validate","Status":"completed","Conclusion":"cancelled","SuiteID":17}`},
		{name: "different commit", checks: `{"Name":"validate","Status":"completed","Conclusion":"cancelled","SuiteID":17,"HeadSHA":"another"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &checkProvenanceAPI{checks: tc.checks, actionErr: errors.New("metadata must not be queried")}
			runs, err := checkRuns(context.Background(), "o/n", "abc123", f.read)
			require.NoError(t, err)
			assert.Len(t, f.calls, 1)
			for _, run := range runs {
				assert.Empty(t, run.WorkflowPath)
				assert.Empty(t, run.HeadBranch)
				assert.Empty(t, run.Event)
			}
		})
	}
}

func TestCheckRunsRefusesFailedOrMalformedReadsWithoutPartialEvidence(t *testing.T) {
	t.Parallel()
	const check = `{"Name":"validate","Status":"completed","Conclusion":"cancelled","SuiteID":17,"HeadSHA":"abc123"}`
	const action = `{"SuiteID":17,"HeadSHA":"abc123","WorkflowPath":".github/workflows/pr-check.yml","HeadBranch":"dev","Event":"push"}`
	readErr := errors.New("forge unavailable")
	for _, tc := range []struct {
		name, checks, actions string
		checkErr, actionErr   error
		calls                 int
	}{
		{name: "check request fails after output", checks: check, checkErr: readErr, calls: 1},
		{name: "check JSON malformed after row", checks: check + "\n{", calls: 1},
		{name: "check suite wrong type", checks: strings.Replace(check, `"SuiteID":17`, `"SuiteID":"17"`, 1), calls: 1},
		{name: "metadata request fails after output", checks: check, actions: action, actionErr: readErr, calls: 2},
		{name: "metadata JSON malformed after row", checks: check, actions: action + "\n{", calls: 2},
		{name: "metadata suite wrong type", checks: check, actions: strings.Replace(action, `"SuiteID":17`, `"SuiteID":"17"`, 1), calls: 2},
		{name: "metadata path wrong type", checks: check, actions: strings.Replace(action, `".github/workflows/pr-check.yml"`, `17`, 1), calls: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &checkProvenanceAPI{checks: tc.checks, actions: tc.actions, checkErr: tc.checkErr, actionErr: tc.actionErr}
			runs, err := checkRuns(context.Background(), "o/n", "abc123", f.read)
			require.Error(t, err)
			assert.Empty(t, runs, "failed reads cannot return a successful-looking prefix")
			assert.Len(t, f.calls, tc.calls)
			if tc.checkErr != nil || tc.actionErr != nil {
				assert.ErrorIs(t, err, readErr)
			}
		})
	}
}
