package release

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A cut judges source checks, not a cancelled publication of an older tag on
// the same commit. Provenance establishes the exception, never a display name
// or cancellation alone (SPEC-RELEASE, the cut gate).
func TestACutIgnoresThePreviousTagsCancelledReleaseRun(t *testing.T) {
	t.Parallel()

	// This proposed forge evidence shape also compiles before CheckRun preserves
	// provenance. The baseline loses these extra fields and refuses the positive
	// cases. This is domain evidence, not a fixture of the raw GitHub response.
	type evidence struct {
		Name, Status, Conclusion        string
		WorkflowPath, HeadBranch, Event string
		TagPush                        bool
	}
	const releasePath = ".github/workflows/release.yml"
	ci := evidence{Name: "ci", Status: "completed", Conclusion: "success", WorkflowPath: ".github/workflows/ci.yml", HeadBranch: "main", Event: "push"}
	old := evidence{Name: "publish", Status: "completed", Conclusion: "cancelled", WorkflowPath: releasePath, HeadBranch: "v1.2.11", Event: "push", TagPush: true}
	earlier := old
	earlier.HeadBranch = "v1.2.10"
	ambiguous := old
	ambiguous.TagPush = false
	changed := func(field, value string) evidence {
		e := old
		switch field {
		case "name":
			e.Name = value
		case "workflow":
			e.WorkflowPath = value
		case "branch":
			e.HeadBranch = value
		case "event":
			e.Event = value
		case "status":
			e.Status, e.Conclusion = value, ""
		case "conclusion":
			e.Conclusion = value
		}
		return e
	}
	for _, tc := range []struct {
		name    string
		extra   []evidence
		only    bool
		tags    []string
		pass    bool
		refusal string
	}{
		{name: "source CI alone", pass: true},
		{name: "previous tag publication cancelled", extra: []evidence{old}, pass: true},
		{name: "multiple older tag publications cancelled", extra: []evidence{earlier, old}, pass: true},
		{name: "older tag is ordered semantically", extra: []evidence{changed("branch", "v1.2.9")}, tags: []string{"v1.2.9", "v1.2.10", "v1.2.11"}, pass: true},
		{name: "job name is not workflow identity", extra: []evidence{changed("name", "ci")}, pass: true},
		{name: "branch and tag share a name without tag push proof", extra: []evidence{ambiguous}, refusal: "publish=cancelled"},
		{name: "CI cancellation remains blocking", extra: []evidence{changed("workflow", ".github/workflows/ci.yml")}, refusal: "publish=cancelled"},
		{name: "certification cancellation remains blocking", extra: []evidence{changed("workflow", ".github/workflows/certification.yml")}, refusal: "publish=cancelled"},
		{name: "release display name proves nothing", extra: []evidence{{Name: "release", Status: "completed", Conclusion: "cancelled"}}, refusal: "release=cancelled"},
		{name: "unknown workflow", extra: []evidence{changed("workflow", "")}, refusal: "publish=cancelled"},
		{name: "workflow suffix is not exact provenance", extra: []evidence{changed("workflow", ".github/workflows/other-release.yml")}, refusal: "publish=cancelled"},
		{name: "unknown branch", extra: []evidence{changed("branch", "")}, refusal: "publish=cancelled"},
		{name: "unknown event", extra: []evidence{changed("event", "")}, refusal: "publish=cancelled"},
		{name: "branch release", extra: []evidence{changed("branch", "main")}, refusal: "publish=cancelled"},
		{name: "old looking tag absent from forge", extra: []evidence{changed("branch", "v1.2.9")}, refusal: "publish=cancelled"},
		{name: "nonversion tag", extra: []evidence{changed("branch", "nightly")}, tags: []string{"v1.2.10", "v1.2.11", "nightly"}, refusal: "publish=cancelled"},
		{name: "requested version is not older", extra: []evidence{changed("branch", "v1.2.12")}, tags: []string{"v1.2.10", "v1.2.11", "v1.2.12"}, refusal: "publish=cancelled"},
		{name: "future existing tag is not older", extra: []evidence{changed("branch", "v1.2.13")}, tags: []string{"v1.2.10", "v1.2.11", "v1.2.13"}, refusal: "publish=cancelled"},
		{name: "manual release event", extra: []evidence{changed("event", "workflow_dispatch")}, refusal: "publish=cancelled"},
		{name: "pull request release event", extra: []evidence{changed("event", "pull_request")}, refusal: "publish=cancelled"},
		{name: "failed publication is not cancellation", extra: []evidence{changed("conclusion", "failure")}, refusal: "publish=failure"},
		{name: "timed out publication is not cancellation", extra: []evidence{changed("conclusion", "timed_out")}, refusal: "publish=timed_out"},
		{name: "pending publication", extra: []evidence{changed("status", "in_progress")}, refusal: "CI has not finished"},
		{name: "queued publication", extra: []evidence{changed("status", "queued")}, refusal: "CI has not finished"},
		{name: "failure beside ignored publication", extra: []evidence{old, {Name: "ci", Status: "completed", Conclusion: "failure", WorkflowPath: ".github/workflows/ci.yml", HeadBranch: "main", Event: "push"}}, refusal: "ci=failure"},
		{name: "only ignored publications leaves no source evidence", extra: []evidence{earlier, old}, only: true, refusal: "no check run has judged this commit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checks := []evidence{ci}
			if tc.only {
				checks = nil
			}
			checks = append(checks, tc.extra...)
			raw, err := json.Marshal(checks)
			require.NoError(t, err)
			var runs []CheckRun
			require.NoError(t, json.Unmarshal(raw, &runs))
			const sha = "abc123abc123def"
			tags := tc.tags
			if tags == nil {
				tags = []string{"v1.2.10", "v1.2.11"}
			}
			f := &fakeForge{head: map[string]string{"main": sha}, checks: map[string][]CheckRun{sha: runs}, tags: tags}
			changelog := filepath.Join(t.TempDir(), "CHANGELOG.md")
			const before = "# Changelog\n\n## v1.2.11\n\n- Previous release.\n"
			require.NoError(t, os.WriteFile(changelog, []byte(before), 0o644))
			var out, errs bytes.Buffer
			code := Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main", "--version", "v1.2.12", "--changelog", changelog}, &out, &errs, cutDeps(t, f))
			assert.NotContains(t, out.String(), "CI WAIVED")
			if tc.pass {
				assert.Equal(t, 0, code, "out=%s errs=%s", out.String(), errs.String())
				assert.Equal(t, []string{"v1.2.12 " + sha}, f.tagged)
				assert.NotContains(t, f.messages["v1.2.12"], "CI waived")
			} else {
				assert.Equal(t, 2, code, "out=%s errs=%s", out.String(), errs.String())
				assert.Contains(t, errs.String(), tc.refusal)
				assert.Empty(t, f.tagged, "a blocked cut must not write a tag")
				body, err := os.ReadFile(changelog)
				require.NoError(t, err)
				assert.Equal(t, before, string(body), "a blocked cut must not change its changelog")
			}
		})
	}
}

// A waiver records the source failures it actually waived, not the old
// publication runs excluded by the cut gate (SPEC-RELEASE section 19).
func TestACutWaiverNamesOnlyTheRetainedSourceFailure(t *testing.T) {
	t.Parallel()
	var runs []CheckRun
	require.NoError(t, json.Unmarshal([]byte(`[
		{"Name":"publish","Status":"completed","Conclusion":"cancelled","WorkflowPath":".github/workflows/release.yml","HeadBranch":"v1.2.11","Event":"push","TagPush":true},
		{"Name":"ci","Status":"completed","Conclusion":"failure","WorkflowPath":".github/workflows/ci.yml","HeadBranch":"main","Event":"push"}
	]`), &runs))
	const sha = "abc123abc123def"
	f := &fakeForge{head: map[string]string{"main": sha}, checks: map[string][]CheckRun{sha: runs}, tags: []string{"v1.2.10", "v1.2.11"}}
	changelog := filepath.Join(t.TempDir(), "CHANGELOG.md")
	require.NoError(t, os.WriteFile(changelog, []byte("# Changelog\n"), 0o644))
	var out, errs bytes.Buffer
	code := Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main", "--version", "v1.2.12", "--changelog", changelog, "--waive-ci", "the owner, 2026-10-11"}, &out, &errs, cutDeps(t, f))
	require.Equal(t, 0, code, "out=%s errs=%s", out.String(), errs.String())
	assert.Equal(t, []string{"v1.2.12 " + sha}, f.tagged)
	assert.Contains(t, out.String(), "RELEASE CUT CI WAIVED")
	assert.Contains(t, out.String(), `checks=ci\x3dfailure`)
	assert.NotContains(t, out.String(), `publish\x3dcancelled`)
	body, err := os.ReadFile(changelog)
	require.NoError(t, err)
	for _, text := range []string{f.messages["v1.2.12"], string(body)} {
		assert.Contains(t, text, "CI waived checks: ci=failure")
		assert.NotContains(t, text, "publish=cancelled")
	}
}
