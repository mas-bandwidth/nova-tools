package release

// cut_certify_test.go: `nova-update release cut` refuses a commit
// certification.yml has not vouched for, names the dispatch that fixes it,
// accepts a commit whose latest certification evidence is green, and with
// --dispatch-certification starts the run once and waits on it. The waivers
// never cover certification. Pure: a fake forge, no socket and no real time.

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCutRefusesAnUncertifiedCommitNamingTheDispatch: a commit no
// certification.yml run vouches for is refused, and the refusal names the
// dispatch that would fix it (`gh workflow run certification.yml --ref <sha>`,
// the same remedy release.yml prints) and tags nothing. It is the gate
// release.yml's certified job makes, one step earlier.
func TestCutRefusesAnUncertifiedCommitNamingTheDispatch(t *testing.T) {
	t.Parallel()

	f := cutForge()
	f.head = map[string]string{"main": "abc123abc123def"}
	f.certRuns = map[string][]CertificationRun{} // nothing has vouched
	var out, errs bytes.Buffer
	code := Run("nova-update", cutArgs(changelogIn(t, t.TempDir()), "--dry-run"), &out, &errs, cutDeps(t, f))
	require.Equalf(t, 2, code, "an uncertified commit was cut: code=%d out=%s errs=%s", code, out.String(), errs.String())
	assert.Containsf(t, errs.String(), "gh workflow run certification.yml --ref abc123abc123def",
		"the refusal does not name the dispatch: %s", errs.String())
	assert.Containsf(t, errs.String(), "certification", "the refusal does not say what is missing: %s", errs.String())
	assert.Emptyf(t, f.tagged, "a commit no certification run vouched for was tagged: %v", f.tagged)
	assert.Emptyf(t, f.dispatches, "a dispatch happened without --dispatch-certification: %v", f.dispatches)
}

// TestCutAcceptsACertifiedCommit: a commit whose latest certification evidence
// is a completed success passes the gate and cuts, and the receipt says which
// path publishes it.
func TestCutAcceptsACertifiedCommit(t *testing.T) {
	t.Parallel()

	f := cutForge()
	f.certRuns = map[string][]CertificationRun{
		"abc123abc123def": {{ID: 7, Status: "completed", Conclusion: "success", UpdatedAt: "2026-09-18T09:00:00Z"}},
	}
	var out, errs bytes.Buffer
	code := Run("nova-update", cutArgs(changelogIn(t, t.TempDir()), "--dry-run"), &out, &errs, cutDeps(t, f))
	require.Equalf(t, 0, code, "a certified commit was refused: code=%d errs=%s", code, errs.String())
	assert.Containsf(t, out.String(), "publish="+PublishPath, "the cut line does not say which path publishes: %s", out.String())
}

// TestCutDispatchCertificationDispatchesOnceAndWaits: with
// --dispatch-certification an uncertified commit is dispatched ONCE at the
// commit (never at the branch name, which can move) and the cut waits on the
// run the fake records, then finishes.
func TestCutDispatchCertificationDispatchesOnceAndWaits(t *testing.T) {
	t.Parallel()

	f := cutForge()
	f.certRuns = map[string][]CertificationRun{} // uncertified until the dispatch
	var out, errs bytes.Buffer
	code := Run("nova-update", cutArgs(changelogIn(t, t.TempDir()), "--dispatch-certification"), &out, &errs, cutDeps(t, f))
	require.Equalf(t, 0, code, "the dispatched cut did not finish: code=%d errs=%s", code, errs.String())
	require.Equalf(t, []string{"abc123abc123def"}, f.dispatches, "dispatched refs %v, want exactly the commit once", f.dispatches)
	require.Equalf(t, 1, len(f.tagged), "the dispatched cut tagged %v", f.tagged)
}

// TestCertificationIsNotCoveredByTheWaivers: every waiver a cut takes
// (--no-dogfood-gate, --no-journey-gate, --no-spend-gate) is asked about its
// own evidence, and none of them is evidence about certification.yml. A red
// certification still refuses with all three set.
func TestCertificationIsNotCoveredByTheWaivers(t *testing.T) {
	t.Parallel()

	f := cutForge()
	f.certRuns = map[string][]CertificationRun{
		"abc123abc123def": {{ID: 9, Status: "completed", Conclusion: "failure", UpdatedAt: "2026-09-18T09:00:00Z"}},
	}
	var out, errs bytes.Buffer
	code := Run("nova-update", cutArgs(changelogIn(t, t.TempDir()), "--dry-run",
		"--no-dogfood-gate", "--no-journey-gate", "--no-spend-gate", "--reason", "test"),
		&out, &errs, cutDeps(t, f))
	require.Equalf(t, 2, code, "the waivers covered a red certification: code=%d out=%s errs=%s", code, out.String(), errs.String())
	assert.Containsf(t, errs.String(), "certification", "the refusal does not name certification: %s", errs.String())
	assert.Emptyf(t, f.tagged, "a red certification was tagged: %v", f.tagged)
}

// TestCertificationReasonTakesTheLatestEvidence: the latest updated_at stamp
// group decides, uniformly; a rerun of an older run id is newer evidence than
// a later run that was never rerun; a skipped certification is not a success;
// and a still-running run makes the cut wait rather than pass.
func TestCertificationReasonTakesTheLatestEvidence(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		runs  []CertificationRun
		wants string
	}{
		{"nothing", nil, "no certification.yml run"},
		{"in flight", []CertificationRun{{ID: 1, Status: "in_progress"}}, "still in_progress"},
		{"green", []CertificationRun{{ID: 1, Status: "completed", Conclusion: "success", UpdatedAt: "2026-09-18T09:00:00Z"}}, ""},
		{"latest rerun green over an older red", []CertificationRun{
			{ID: 1, Status: "completed", Conclusion: "failure", UpdatedAt: "2026-09-18T09:00:00Z"},
			{ID: 2, Status: "completed", Conclusion: "success", UpdatedAt: "2026-09-18T09:10:00Z"},
		}, ""},
		{"latest red", []CertificationRun{
			{ID: 1, Status: "completed", Conclusion: "success", UpdatedAt: "2026-09-18T09:00:00Z"},
			{ID: 2, Status: "completed", Conclusion: "failure", UpdatedAt: "2026-09-18T09:10:00Z"},
		}, "not green"},
		{"a skipped certification does not vouch", []CertificationRun{{ID: 1, Status: "completed", Conclusion: "skipped", UpdatedAt: "2026-09-18T09:00:00Z"}}, "not green"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := certificationReason(tc.runs)
			if tc.wants == "" {
				assert.Emptyf(t, got, "%s: reason %q, want none", tc.name, got)
				return
			}
			assert.Containsf(t, got, tc.wants, "%s: reason %q does not name %q", tc.name, got, tc.wants)
		})
	}
}
