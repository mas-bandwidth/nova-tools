package release

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// certRun is one certification check run in a suite: the suite id groups the
// runs, the workflow run id and conclusion are the suite's own provenance, and
// updatedAt is the suite's completion time (docs/SPEC-RELEASE.md section 19).
func certRun(name string, suiteID, runID int64, conclusion, workflowConclusion string, branch, event string, updated time.Time) checkRun {
	return checkRun{
		CheckRun: CheckRun{
			ID: runID, Name: name, Status: "completed", Conclusion: conclusion,
			WorkflowPath: ".github/workflows/certification.yml",
			HeadBranch:   branch,
			Event:        event,
		},
		SuiteID:            suiteID,
		WorkflowConclusion: workflowConclusion,
		UpdatedAt:          updated,
	}
}

// cancelledCertSuite is suite A of the 2026-10-12 shape: a push certification
// run on dev whose workflow concluded cancelled, with every job cancelled
// except the aggregate certification-ok, which reports failure only because its
// needs were cancelled (docs/SPEC-RELEASE.md section 19).
func cancelledCertSuite(suiteID, runID int64, updated time.Time) []checkRun {
	return []checkRun{
		certRun("test (ubuntu-latest, 1)", suiteID, runID, "cancelled", "cancelled", "dev", "push", updated),
		certRun("test (macos-latest, 1)", suiteID, runID, "cancelled", "cancelled", "dev", "push", updated),
		certRun("certification-ok", suiteID, runID, "failure", "cancelled", "dev", "push", updated),
	}
}

// greenCertSuite is suite B: a later dispatched certification run on the
// release branch whose workflow concluded success and whose jobs are all green.
func greenCertSuite(suiteID, runID int64, updated time.Time) []checkRun {
	return []checkRun{
		certRun("test (ubuntu-latest, 1)", suiteID, runID, "success", "success", "release/v1.2.14", "workflow_dispatch", updated),
		certRun("test (macos-latest, 1)", suiteID, runID, "success", "success", "release/v1.2.14", "workflow_dispatch", updated),
		certRun("certification-ok", suiteID, runID, "success", "success", "release/v1.2.14", "workflow_dispatch", updated),
	}
}

func cutCancelledSuite(t *testing.T, runs []checkRun) (code int, out, errs string, f *fakeForge, version string) {
	t.Helper()
	const sha = "abc123abc123def"
	f = &fakeForge{head: map[string]string{"main": sha}, checksSuites: map[string][]checkRun{sha: runs},
		tags: []string{"v1.2.13"}}
	changelog := filepath.Join(t.TempDir(), "CHANGELOG.md")
	require.NoError(t, os.WriteFile(changelog, []byte("# Changelog\n\n## v1.2.13\n\n- previous\n"), 0o644))
	var outB, errsB bytes.Buffer
	version = "v1.2.14"
	code = Run("nova-update", []string{"cut", "--repo", "o/n", "--from", "main",
		"--version", version, "--changelog", changelog}, &outB, &errsB, cutDeps(t, f))
	return code, outB.String(), errsB.String(), f, version
}

// A cancelled certification suite superseded by a later green suite of the same
// workflow at the same sha is set aside: the cancelled runs never judged the
// commit, so the cut proceeds and names what it set aside with the reason
// superseded-by=<the green suite's run id> (docs/SPEC-RELEASE.md section 19).
func TestACancelledCertificationSuiteSupersededByAGreenOneIsSetAside(t *testing.T) {
	t.Parallel()
	early := time.Date(2026, 10, 12, 4, 25, 0, 0, time.UTC)
	late := time.Date(2026, 10, 12, 5, 0, 0, 0, time.UTC)
	runs := append(cancelledCertSuite(100, 1000, early), greenCertSuite(200, 2000, late)...)
	code, out, errs, f, version := cutCancelledSuite(t, runs)
	assert.Equal(t, 0, code, "out=%s errs=%s", out, errs)
	require.Len(t, f.tagged, 1, "the cut must tag: %v", f.tagged)
	// The set-aside runs are named on the cut's line, each with the green
	// suite's run id as the reason (the field renders = as \x3d).
	assert.Contains(t, out, "ci-set-aside=1000:superseded-by\\x3d2000")
	// And written into the tag annotation and the changelog section.
	assert.Contains(t, f.messages[version], "CI set aside: 1000:superseded-by=2000")
}

// A suite whose workflow run concluded failure is never set aside, even when a
// later green suite of the same workflow exists: the failure judged the commit
// (docs/SPEC-RELEASE.md section 19).
func TestAFailedSuiteIsNeverSetAside(t *testing.T) {
	t.Parallel()
	early := time.Date(2026, 10, 12, 4, 25, 0, 0, time.UTC)
	late := time.Date(2026, 10, 12, 5, 0, 0, 0, time.UTC)
	runs := []checkRun{
		certRun("certification", 100, 1000, "failure", "failure", "dev", "push", early),
	}
	runs = append(runs, greenCertSuite(200, 2000, late)...)
	code, _, errs, f, _ := cutCancelledSuite(t, runs)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "certification=failure")
	assert.Empty(t, f.tagged, "a red sha must not be tagged")
}

// A cancelled suite with no later green suite of its workflow still refuses:
// cancellation alone proves nothing about the commit (docs/SPEC-RELEASE.md
// section 19).
func TestACancelledSuiteWithNoGreenSuccessorStillRefuses(t *testing.T) {
	t.Parallel()
	early := time.Date(2026, 10, 12, 4, 25, 0, 0, time.UTC)
	runs := cancelledCertSuite(100, 1000, early)
	code, _, errs, f, _ := cutCancelledSuite(t, runs)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "certification-ok=failure")
	assert.Empty(t, f.tagged, "a red sha must not be tagged")
}

// A green suite that lacks one of the cancelled suite's job names (a matrix
// leg) does not supersede it: the cancelled suite judged jobs the green one
// never ran (docs/SPEC-RELEASE.md section 19).
func TestAGreenSuccessorMissingAJobDoesNotSupersede(t *testing.T) {
	t.Parallel()
	early := time.Date(2026, 10, 12, 4, 25, 0, 0, time.UTC)
	late := time.Date(2026, 10, 12, 5, 0, 0, 0, time.UTC)
	greenMissing := []checkRun{
		certRun("test (ubuntu-latest, 1)", 200, 2000, "success", "success", "release/v1.2.14", "workflow_dispatch", late),
		certRun("certification-ok", 200, 2000, "success", "success", "release/v1.2.14", "workflow_dispatch", late),
	}
	runs := append(cancelledCertSuite(100, 1000, early), greenMissing...)
	code, _, errs, f, _ := cutCancelledSuite(t, runs)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "test (macos-latest, 1)=cancelled")
	assert.Empty(t, f.tagged, "a red sha must not be tagged")
}

// A green suite that completed before the cancelled one does not supersede it:
// the successor must be later (docs/SPEC-RELEASE.md section 19).
func TestAnEarlierGreenSuiteDoesNotSupersedeALaterCancelledOne(t *testing.T) {
	t.Parallel()
	early := time.Date(2026, 10, 12, 4, 25, 0, 0, time.UTC)
	late := time.Date(2026, 10, 12, 5, 0, 0, 0, time.UTC)
	runs := append(greenCertSuite(200, 2000, early), cancelledCertSuite(100, 1000, late)...)
	code, _, errs, f, _ := cutCancelledSuite(t, runs)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "certification-ok=failure")
	assert.Empty(t, f.tagged, "a red sha must not be tagged")
}
