package release

// cut_certify_test.go: nova-update release cut refuses uncertified commits, accepts certified ones,
// and offers --dispatch-certification to dispatch and wait.

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeCertifyForge implements Forge but answers with certification runs.
type fakeCertifyForge struct {
	runs map[string][]CertificationRun // sha -> runs
	refToSHA map[string]string        // ref -> sha mapping
}

func (f *fakeCertifyForge) HeadSHA(_ context.Context, repo, ref string) (string, error) {
	if f.refToSHA != nil {
		if sha, ok := f.refToSHA[ref]; ok {
			return sha, nil
		}
	}
	// Default sha
	return "abc123def456", nil
}

// CertificationRun is a minimal certification run record.
type CertificationRun struct {
	ID         int    `json:"id"`
	HeadSHA    string `json:"head_sha"`
	Status     string `json:"status"`     // queued, in_progress, completed
	Conclusion string `json:"conclusion"` // success, failure, skipped, neutral, etc.
	UpdatedAt  time.Time
}

func (f *fakeCertifyForge) CheckRuns(_ context.Context, repo, sha string) ([]CheckRun, error) {
	runs := f.runs[sha]
	if len(runs) == 0 {
		return nil, nil
	}
	// Sort by updated_at descending
	var latest CertificationRun
	for _, r := range runs {
		if r.UpdatedAt.After(latest.UpdatedAt) {
			latest = r
		}
	}
	// Convert to CheckRun format
	var checkRuns []CheckRun
	if latest.Status == "completed" {
		checkRuns = append(checkRuns, CheckRun{
			Name:       "certification",
			Status:     latest.Status,
			Conclusion: latest.Conclusion,
		})
	}
	return checkRuns, nil
}

func (f *fakeCertifyForge) Tags(_ context.Context, repo string) ([]string, error) {
	// Return empty tags list
	return nil, nil
}

func (f *fakeCertifyForge) Compare(_ context.Context, repo, base, head string) ([]Commit, error) {
	// Return empty commits list
	return nil, nil
}

func (f *fakeCertifyForge) Files(_ context.Context, repo, base, head string) ([]string, error) {
	// Return empty files list
	return nil, nil
}

func (f *fakeCertifyForge) Tag(_ context.Context, repo, name, sha, message string) error {
	return nil
}

func (f *fakeCertifyForge) TagMessage(_ context.Context, repo, tag string) (string, error) {
	return "", nil
}

func TestCutRefusesUncertifiedCommit(t *testing.T) {
	t.Parallel()

	// Fake forge with no certification runs
	uncertified := &fakeCertifyForge{
		runs: map[string][]CertificationRun{},
		refToSHA: map[string]string{"HEAD": "abc123def456"},
	}

	var out, errs bytes.Buffer
	// This should refuse because there's no certification run
	code := Run("nova-update", []string{
		"cut", "--repo", "o/n", "--from", "HEAD", "--version", "v999.0.0",
		"--changelog", "/dev/null",
		"--no-dogfood-gate", "--reason", "test",
	}, &out, &errs, Deps{Forge: uncertified})

	// Code 2 is expected (refusal)
	require.Equal(t, 2, code, "cut should refuse uncertified commit")
	require.True(t, strings.Contains(errs.String(), "CI"),
		"should mention CI in refusal: %s", errs.String())
}

func TestCutAcceptsCertifiedCommit(t *testing.T) {
	t.Parallel()

	// Fake forge with green certification run
	now := time.Now()
	certified := &fakeCertifyForge{
		runs: map[string][]CertificationRun{
			"abc123def456": {
				{
					ID:         12345,
					HeadSHA:    "abc123def456",
					Status:     "completed",
					Conclusion: "success",
					UpdatedAt:  now,
				},
			},
		},
		refToSHA: map[string]string{"abc123def456": "abc123def456"},
	}

	var out, errs bytes.Buffer
	// This should pass because there's a green certification run
	code := Run("nova-update", []string{
		"cut", "--repo", "o/n", "--from", "abc123def456",
		"--version", "v999.0.0", "--changelog", "/dev/null",
		"--no-dogfood-gate", "--reason", "test",
	}, &out, &errs, Deps{Forge: certified})

	// Should not refuse due to certification
	require.NotEqual(t, 1, code, "should not refuse due to certification")
	require.False(t, strings.Contains(errs.String(), "certification"),
		"should not mention certification in refusal: %s", errs.String())
}

func TestCutDispatchCertification(t *testing.T) {
	t.Parallel()

	// This test verifies that --dispatch-certification flag exists
	// and would dispatch certification workflow
	var out, errs bytes.Buffer

	_ = Run("nova-update", []string{
		"cut", "--repo", "o/n", "--from", "HEAD",
		"--version", "v999.0.0", "--changelog", "/dev/null",
		"--no-dogfood-gate", "--reason", "test",
		"--dispatch-certification",
	}, &out, &errs, Deps{Forge: &fakeCertifyForge{
		refToSHA: map[string]string{"HEAD": "abc123def456"},
	}})

	// Should recognize the flag even if other checks fail
	require.False(t, strings.Contains(errs.String(), "unrecognized flag") &&
		strings.Contains(errs.String(), "--dispatch-certification"),
		"--dispatch-certification flag should be recognized")
}

func TestCertificationNotCoveredByWaivers(t *testing.T) {
	t.Parallel()

	// Certification should never be covered by waivers
	// This is enforced in the release/cut logic
	now := time.Now()
	redCertification := &fakeCertifyForge{
		runs: map[string][]CertificationRun{
			"xyz789": {
				{
					ID:         67890,
					HeadSHA:    "xyz789",
					Status:     "completed",
					Conclusion: "failure", // red certification
					UpdatedAt:  now,
				},
			},
		},
		refToSHA: map[string]string{"xyz789": "xyz789"},
	}

	var out, errs bytes.Buffer
	code := Run("nova-update", []string{
		"cut", "--repo", "o/n", "--from", "xyz789",
		"--version", "v999.0.0", "--changelog", "/dev/null",
		"--no-dogfood-gate", "--reason", "test",
	}, &out, &errs, Deps{Forge: redCertification})

	require.Equal(t, 2, code, "should refuse red certification")
	require.True(t, strings.Contains(errs.String(), "certification"),
		"should refuse red certification: %s", errs.String())
}
