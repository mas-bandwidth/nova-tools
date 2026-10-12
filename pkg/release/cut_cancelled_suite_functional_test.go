//go:build functional

package release

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jsonAPI stands in for `gh api ... --jq ...`: it returns the projected JSON of
// the check-runs read, then the projected JSON of the actions-runs read, so the
// production checkRuns join is exercised against the real payload shape with no
// network.
type jsonAPI struct {
	payloads []string
	calls    int
}

func (j *jsonAPI) read(_ context.Context, _ ...string) (string, error) {
	if j.calls >= len(j.payloads) {
		return "", errors.New("unexpected extra forge read")
	}
	out := j.payloads[j.calls]
	j.calls++
	return out, nil
}

// The 2026-10-12 shape: check suite 103502831803 is the dev-push certification
// run, cancelled at 04:25Z by the later landing's concurrency, with its
// aggregate certification-ok reporting failure only because its needs were
// cancelled; the dispatched suite 103502831804 completed green later. The cut
// sets the cancelled suite aside because the green suite supersedes it.
func TestCutSetsAsideACancelledCertificationSuiteFunctional(t *testing.T) {
	t.Parallel()
	const sha = "7e2ce59be0000000000000000000000000000000"
	checks := func(dispatchedOK string) string {
		return `{"Name":"test (ubuntu-latest, 1)","Status":"completed","Conclusion":"cancelled","SuiteID":103502831803,"HeadSHA":"` + sha + `"}
{"Name":"test (macos-latest, 1)","Status":"completed","Conclusion":"cancelled","SuiteID":103502831803,"HeadSHA":"` + sha + `"}
{"Name":"certification-ok","Status":"completed","Conclusion":"failure","SuiteID":103502831803,"HeadSHA":"` + sha + `"}
{"Name":"test (ubuntu-latest, 1)","Status":"completed","Conclusion":"success","SuiteID":103502831804,"HeadSHA":"` + sha + `"}
{"Name":"test (macos-latest, 1)","Status":"completed","Conclusion":"` + dispatchedOK + `","SuiteID":103502831804,"HeadSHA":"` + sha + `"}
{"Name":"certification-ok","Status":"completed","Conclusion":"success","SuiteID":103502831804,"HeadSHA":"` + sha + `"}`
	}
	const actions = `{"SuiteID":103502831803,"ID":38203837801,"HeadSHA":"` + sha + `","WorkflowPath":".github/workflows/certification.yml","HeadBranch":"dev","Event":"push","Conclusion":"cancelled","UpdatedAt":"2026-10-12T04:25:00Z"}
{"SuiteID":103502831804,"ID":38203839518,"HeadSHA":"` + sha + `","WorkflowPath":".github/workflows/certification.yml","HeadBranch":"release/v1.2.14","Event":"workflow_dispatch","Conclusion":"success","UpdatedAt":"2026-10-12T05:00:00Z"}`

	readRuns := func(t *testing.T, dispatchedOK string) []checkRun {
		t.Helper()
		api := &jsonAPI{payloads: []string{checks(dispatchedOK), actions}}
		runs, err := checkRuns(context.Background(), "o/n", sha, api.read)
		require.NoError(t, err)
		require.Equal(t, 2, api.calls, "both the check-runs and the actions-runs reads are made")
		return runs
	}

	runCut := func(t *testing.T, runs []checkRun) (int, string, string) {
		t.Helper()
		f := &fakeForge{head: map[string]string{"main": sha}, checksSuites: map[string][]checkRun{sha: runs},
			tags: []string{"v1.2.13"}}
		changelog := filepath.Join(t.TempDir(), "CHANGELOG.md")
		require.NoError(t, os.WriteFile(changelog, []byte("# Changelog\n\n## v1.2.13\n\n- previous\n"), 0o644))
		var out, errs bytes.Buffer
		code := Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main",
			"--version", "v1.2.14", "--changelog", changelog, "--dry-run"}, &out, &errs, cutDeps(t, f))
		return code, out.String(), errs.String()
	}

	t.Run("the cancelled suite is set aside", func(t *testing.T) {
		t.Parallel()
		code, out, errs := runCut(t, readRuns(t, "success"))
		assert.Equal(t, 0, code, "out=%s errs=%s", out, errs)
		assert.Contains(t, out, "dry-run=yes")
		assert.Contains(t, out, "ci-set-aside=38203837801:superseded-by\\x3d38203839518")
	})

	t.Run("a failure in the dispatched suite still refuses", func(t *testing.T) {
		t.Parallel()
		code, out, errs := runCut(t, readRuns(t, "failure"))
		assert.Equal(t, 2, code, "out=%s errs=%s", out, errs)
		assert.Contains(t, errs, "CUT REFUSED")
		assert.Contains(t, errs, "test (macos-latest, 1)=failure")
	})
}
