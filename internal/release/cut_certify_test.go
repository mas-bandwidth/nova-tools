package release

// cut_certify_test.go: nova-update release cut refuses uncertified commits,
// accepts certified ones, reads the newest certification evidence the way
// release.yml does, and never lets a waiver cover certification. The dispatch
// loop is exercised by its own function, because the flag that reaches it is
// cut's to wire and this is the loop under it.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// fakeCertifyForge answers the certification questions with data a test hands
// it and records the dispatches it is asked for. It implements Forge, certReader
// and workflowDispatcher; no socket, no clock.
type fakeCertifyForge struct {
	mu         sync.Mutex
	headSHA    string
	nonCert    []CheckRun
	certs      []CertRun
	pollCount  int
	pollCerts  func(count int) []CertRun
	dispatches int
	gotRef     string
	tags       []string
	tagged     []string
}

func (f *fakeCertifyForge) HeadSHA(_ context.Context, _, _ string) (string, error) {
	if f.headSHA != "" {
		return f.headSHA, nil
	}
	return "abc123def456", nil
}

func (f *fakeCertifyForge) CheckRuns(_ context.Context, _, _ string) ([]CheckRun, error) {
	return f.nonCert, nil
}

func (f *fakeCertifyForge) CertificationRuns(_ context.Context, _, _ string) ([]CertRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pollCerts != nil {
		res := f.pollCerts(f.pollCount)
		f.pollCount++
		return res, nil
	}
	return f.certs, nil
}

func (f *fakeCertifyForge) Tags(_ context.Context, _ string) ([]string, error) {
	if f.tags != nil {
		return f.tags, nil
	}
	return []string{"v0.1.0"}, nil
}

func (f *fakeCertifyForge) Compare(_ context.Context, _, _, _ string) ([]Commit, error) {
	return nil, nil
}

func (f *fakeCertifyForge) Files(_ context.Context, _, _, _ string) ([]string, error) {
	return nil, nil
}

func (f *fakeCertifyForge) Tag(_ context.Context, _, name, sha, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tagged = append(f.tagged, name+"@"+sha)
	return nil
}

func (f *fakeCertifyForge) TagMessage(_ context.Context, _, _ string) (string, error) {
	return "", nil
}

func (f *fakeCertifyForge) DispatchWorkflow(_ context.Context, _, workflow, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dispatches++
	f.gotRef = ref
	if f.pollCerts == nil && f.certs == nil {
		f.certs = []CertRun{{Name: "certification", Status: "completed", Conclusion: "success"}}
	}
	return nil
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	return root
}

func TestCutRefusesUncertifiedCommit(t *testing.T) {
	t.Parallel()

	forge := &fakeCertifyForge{
		headSHA: "abc123def456",
		nonCert: []CheckRun{{Name: "ci", Status: "completed", Conclusion: "success"}},
	}

	var out, errs bytes.Buffer
	code := Run("nova-update", []string{
		"cut", "--repo", "mas-bandwidth/nova-tools", "--from", "main",
		"--version", "v1.0.0", "--changelog", "/dev/null",
		"--no-dogfood-gate", "--reason", "test",
		"--no-journey-gate", "--no-spend-gate",
		"--spend-since", "2026-10-01T00:00:00Z",
	}, &out, &errs, Deps{Forge: forge})

	require.Equal(t, 2, code, "cut should refuse uncertified commit")
	require.Contains(t, errs.String(), "gh workflow run certification.yml --ref abc123def456",
		"refusal must name the dispatch command on the certified sha: %s", errs.String())
	require.NotContains(t, errs.String(), "--ref main",
		"refusal must dispatch on the sha, not the branch ref: %s", errs.String())
}

func TestCutAcceptsCertifiedCommit(t *testing.T) {
	t.Parallel()

	forge := &fakeCertifyForge{
		headSHA: "abc123def456",
		nonCert: []CheckRun{{Name: "ci", Status: "completed", Conclusion: "success"}},
		certs:   []CertRun{{Name: "certification", Status: "completed", Conclusion: "success"}},
	}

	var out, errs bytes.Buffer
	code := Run("nova-update", []string{
		"cut", "--repo", "mas-bandwidth/nova-tools", "--from", "main",
		"--version", "v1.0.0", "--changelog", "/dev/null",
		"--no-dogfood-gate", "--reason", "test",
		"--no-journey-gate", "--no-spend-gate",
		"--spend-since", "2026-10-01T00:00:00Z",
		"--dry-run",
	}, &out, &errs, Deps{Forge: forge, Now: time.Now})

	require.Equal(t, 0, code, "cut should accept certified commit: %s", errs.String())
	require.Contains(t, out.String(), "RELEASE CUT version=v1.0.0")
	require.Contains(t, out.String(), "publish=workflow")
}

// TestCutDispatchCertification drives the dispatch-and-wait loop directly: one
// dispatch, on the certified sha, and at least one wait before the green
// evidence is read back.
func TestCutDispatchCertification(t *testing.T) {
	t.Parallel()

	var sleeps int
	forge := &fakeCertifyForge{
		headSHA: "abc123def456",
		nonCert: []CheckRun{{Name: "ci", Status: "completed", Conclusion: "success"}},
		certs:   nil,
	}
	forge.pollCerts = func(count int) []CertRun {
		if count == 0 {
			return nil
		}
		return []CertRun{{Name: "certification", Status: "completed", Conclusion: "success"}}
	}

	err := dispatchCertification(context.Background(), forge, "mas-bandwidth/nova-tools", "abc123def456",
		func(time.Duration) { sleeps++ })
	require.NoError(t, err)
	require.Equal(t, 1, forge.dispatches, "should dispatch certification exactly once")
	require.Equal(t, "abc123def456", forge.gotRef,
		"--dispatch-certification must dispatch on the certified sha, not the branch ref")
	require.GreaterOrEqual(t, sleeps, 1, "should wait on the fake")
}

// TestCertificationIsTheNewestRunNotAnOlderGreen pins the gate to the same
// evidence release.yml reads through `go run ./tools/ghrelease certified`:
// every certification run completed, and the newest updated_at run green. An
// older green must not vouch for a newer red or a newer still-running run.
func TestCertificationIsTheNewestRunNotAnOlderGreen(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		runs    []CertRun
		wantErr string
	}{
		{
			"an older green does not cover a newer red",
			[]CertRun{
				{Name: "certification", Status: "completed", Conclusion: "success", UpdatedAt: "2026-10-01T10:00:00Z"},
				{Name: "certification", Status: "completed", Conclusion: "failure", UpdatedAt: "2026-10-01T11:00:00Z"},
			},
			"certification.yml failed",
		},
		{
			"an older green does not cover a newer run still in flight",
			[]CertRun{
				{Name: "certification", Status: "completed", Conclusion: "success", UpdatedAt: "2026-10-01T10:00:00Z"},
				{Name: "certification", Status: "in_progress", UpdatedAt: "2026-10-01T11:00:00Z"},
			},
			"certification.yml is still running",
		},
		{
			"a rerun green after an older red vouches",
			[]CertRun{
				{Name: "certification", Status: "completed", Conclusion: "failure", UpdatedAt: "2026-10-01T10:00:00Z"},
				{Name: "certification", Status: "completed", Conclusion: "success", UpdatedAt: "2026-10-01T11:00:00Z"},
			},
			"",
		},
		{
			"two runs sharing the latest stamp must both be green",
			[]CertRun{
				{Name: "certification", Status: "completed", Conclusion: "success", UpdatedAt: "2026-10-01T11:00:00Z"},
				{Name: "certification-ok", Status: "completed", Conclusion: "failure", UpdatedAt: "2026-10-01T11:00:00Z"},
			},
			"certification.yml failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := certified(tc.runs, "abc123def456")
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestCertificationNotCoveredByWaivers(t *testing.T) {
	t.Parallel()

	forge := &fakeCertifyForge{
		headSHA: "xyz789",
		nonCert: []CheckRun{{Name: "ci", Status: "completed", Conclusion: "success"}},
		certs:   []CertRun{{Name: "certification", Status: "completed", Conclusion: "failure"}},
	}

	var out, errs bytes.Buffer
	code := Run("nova-update", []string{
		"cut", "--repo", "mas-bandwidth/nova-tools", "--from", "xyz789",
		"--version", "v1.0.0", "--changelog", "/dev/null",
		"--no-dogfood-gate", "--reason", "waived for test",
		"--no-journey-gate",
		"--no-spend-gate",
		"--spend-since", "2026-10-01T00:00:00Z",
		"--dry-run",
	}, &out, &errs, Deps{Forge: forge, Now: time.Now})

	require.Equal(t, 2, code, "cut must refuse even when all waivers are provided")
	require.Contains(t, errs.String(), "certification.yml failed",
		"refusal must be about certification, not waivers: %s", errs.String())
}

// TestWorkflowLintPRCIIncludesEveryFunctionalShard pins the shard invariant: the
// functional shards a pull request runs are the shards the merge group runs. Both
// events share ONE `functional` job whose matrix is test-packages' `functional`
// output, so there is no per-event shard list to drift, and the pull_request arm
// carries the head-repo guard that keeps a fork PR off the self-hosted pool. The
// other two workflows in the chain are read here too: certification.yml is the
// whole-tree tier the release is certified against, and release.yml's certified
// job asks the very check cut now mirrors.
func TestWorkflowLintPRCIIncludesEveryFunctionalShard(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	ciContent, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	require.NoError(t, err, "reading ci.yml")
	certContent, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "certification.yml"))
	require.NoError(t, err, "reading certification.yml")
	relContent, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release.yml"))
	require.NoError(t, err, "reading release.yml")

	var ciWorkflow struct {
		Jobs map[string]struct {
			If       string            `yaml:"if"`
			Outputs  map[string]string `yaml:"outputs"`
			Strategy struct {
				Matrix struct {
					Entry any `yaml:"entry"`
				} `yaml:"matrix"`
			} `yaml:"strategy"`
		} `yaml:"jobs"`
	}
	require.NoError(t, yaml.Unmarshal(ciContent, &ciWorkflow), "parsing ci.yml")

	// test-packages is the one place the shards are computed, for both events.
	testPackages, ok := ciWorkflow.Jobs["test-packages"]
	require.True(t, ok, "ci.yml must define test-packages, the functional shard source")
	require.Equal(t, "${{ steps.list.outputs.functional }}", testPackages.Outputs["functional"],
		"test-packages must expose the functional shard list once: %v", testPackages.Outputs)

	functionalJob, ok := ciWorkflow.Jobs["functional"]
	require.True(t, ok, "ci.yml must define one functional job")
	require.Contains(t, functionalJob.If, "github.event_name == 'pull_request'",
		"functional job must run on pull_request: %s", functionalJob.If)
	require.Contains(t, functionalJob.If, "github.event.pull_request.head.repo.full_name == github.repository",
		"the pull_request arm must carry the head-repo guard every self-hosted job carries: %s", functionalJob.If)
	require.Contains(t, functionalJob.If, "github.event_name == 'merge_group'",
		"functional job must run on merge_group: %s", functionalJob.If)

	// One job, one matrix, both events: the shards cannot differ between them.
	entryStr, ok := functionalJob.Strategy.Matrix.Entry.(string)
	require.True(t, ok, "functional entry must be a string expression")
	require.Contains(t, entryStr, "needs.test-packages.outputs.functional",
		"the functional matrix must evaluate the one test-packages shard list")

	require.Contains(t, string(certContent), "certification-ok",
		"certification.yml must aggregate its tier into certification-ok")
	require.Contains(t, string(relContent), "go run ./tools/ghrelease certified",
		"release.yml's certified job must ask the same check cut mirrors")
}
