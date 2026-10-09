package release

// cut_certify_test.go: nova-update release cut refuses uncertified commits, accepts certified ones,
// and offers --dispatch-certification to dispatch and wait.

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

// fakeCertifyForge implements Forge for certification checks and dispatch.
type fakeCertifyForge struct {
	mu         sync.Mutex
	dispatches int
	dispatched []string
	headSHA    string
	runs       []CheckRun
	pollCount  int
	pollRuns   func(count int) []CheckRun
	onDispatch func(repo, workflow, ref string)
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
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pollRuns != nil {
		res := f.pollRuns(f.pollCount)
		f.pollCount++
		return res, nil
	}
	return f.runs, nil
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

func (f *fakeCertifyForge) DispatchWorkflow(_ context.Context, repo, workflow, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dispatches++
	f.dispatched = append(f.dispatched, workflow)
	if f.onDispatch != nil {
		f.onDispatch(repo, workflow, ref)
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
		runs: []CheckRun{
			{Name: "ci", Status: "completed", Conclusion: "success"},
		},
	}

	var out, errs bytes.Buffer
	code := Run("nova-update", []string{
		"cut", "--repo", "mas-bandwidth/nova-tools", "--from", "main",
		"--version", "v1.0.0", "--changelog", "/dev/null",
		"--no-dogfood-gate", "--reason", "test",
		"--no-journey-gate", "--no-spend-gate",
	}, &out, &errs, Deps{Forge: forge})

	require.Equal(t, 2, code, "cut should refuse uncertified commit")
	require.Contains(t, errs.String(), "gh workflow run certification.yml --ref main",
		"refusal must name the dispatch command: %s", errs.String())
	require.Contains(t, errs.String(), "--dispatch-certification",
		"refusal must offer --dispatch-certification: %s", errs.String())
}

func TestCutAcceptsCertifiedCommit(t *testing.T) {
	t.Parallel()

	forge := &fakeCertifyForge{
		headSHA: "abc123def456",
		runs: []CheckRun{
			{Name: "ci", Status: "completed", Conclusion: "success"},
			{Name: "certification", Status: "completed", Conclusion: "success"},
		},
	}

	var out, errs bytes.Buffer
	code := Run("nova-update", []string{
		"cut", "--repo", "mas-bandwidth/nova-tools", "--from", "main",
		"--version", "v1.0.0", "--changelog", "/dev/null",
		"--no-dogfood-gate", "--reason", "test",
		"--no-journey-gate", "--no-spend-gate",
		"--dry-run",
	}, &out, &errs, Deps{Forge: forge, Now: time.Now})

	require.Equal(t, 0, code, "cut should accept certified commit: %s", errs.String())
	require.Contains(t, out.String(), "RELEASE CUT version=v1.0.0")
	require.Contains(t, out.String(), "publish=workflow")
}

func TestCutDispatchCertification(t *testing.T) {
	t.Parallel()

	var sleeps int
	sleepSeam := func(d time.Duration) {
		sleeps++
	}

	forge := &fakeCertifyForge{
		headSHA: "abc123def456",
		runs: []CheckRun{
			{Name: "ci", Status: "completed", Conclusion: "success"},
		},
	}
	forge.onDispatch = func(repo, workflow, ref string) {
		forge.mu.Lock()
		defer forge.mu.Unlock()
		forge.runs = []CheckRun{
			{Name: "ci", Status: "completed", Conclusion: "success"},
			{Name: "certification", Status: "completed", Conclusion: "success"},
		}
	}

	var out, errs bytes.Buffer
	code := Run("nova-update", []string{
		"cut", "--repo", "mas-bandwidth/nova-tools", "--from", "main",
		"--version", "v1.0.0", "--changelog", "/dev/null",
		"--no-dogfood-gate", "--reason", "test",
		"--no-journey-gate", "--no-spend-gate",
		"--dispatch-certification",
		"--dry-run",
	}, &out, &errs, Deps{Forge: forge, Sleep: sleepSeam, Now: time.Now})

	require.Equal(t, 0, code, "cut should succeed after dispatching certification: %s", errs.String())
	require.Equal(t, 1, forge.dispatches, "should dispatch certification exactly once")
	require.Equal(t, []string{"certification.yml"}, forge.dispatched)
	require.GreaterOrEqual(t, sleeps, 1, "should wait on the fake")
	require.Contains(t, out.String(), "RELEASE CUT version=v1.0.0")
	require.Contains(t, out.String(), "publish=workflow")
}

func TestCertificationNotCoveredByWaivers(t *testing.T) {
	t.Parallel()

	forge := &fakeCertifyForge{
		headSHA: "xyz789",
		runs: []CheckRun{
			{Name: "ci", Status: "completed", Conclusion: "success"},
			{Name: "certification", Status: "completed", Conclusion: "failure"},
		},
	}

	var out, errs bytes.Buffer
	code := Run("nova-update", []string{
		"cut", "--repo", "mas-bandwidth/nova-tools", "--from", "xyz789",
		"--version", "v1.0.0", "--changelog", "/dev/null",
		"--no-dogfood-gate", "--reason", "waived for test",
		"--no-journey-gate",
		"--no-spend-gate",
		"--dry-run",
	}, &out, &errs, Deps{Forge: forge, Now: time.Now})

	require.Equal(t, 2, code, "cut must refuse even when all waivers are provided")
	require.Contains(t, errs.String(), "certification.yml failed",
		"refusal must be about certification, not waivers: %s", errs.String())
}

func TestWorkflowLintPRCIIncludesEveryFunctionalShard(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	ciPath := filepath.Join(root, ".github", "workflows", "ci.yml")
	certPath := filepath.Join(root, ".github", "workflows", "certification.yml")
	relPath := filepath.Join(root, ".github", "workflows", "release.yml")

	ciContent, err := os.ReadFile(ciPath)
	require.NoError(t, err, "reading ci.yml")
	certContent, err := os.ReadFile(certPath)
	require.NoError(t, err, "reading certification.yml")
	relContent, err := os.ReadFile(relPath)
	require.NoError(t, err, "reading release.yml")

	require.NotEmpty(t, certContent)
	require.NotEmpty(t, relContent)

	var ciWorkflow struct {
		Jobs map[string]struct {
			If       string `yaml:"if"`
			Strategy struct {
				Matrix struct {
					Entry string `yaml:"entry"`
				} `yaml:"matrix"`
			} `yaml:"strategy"`
		} `yaml:"jobs"`
	}
	err = yaml.Unmarshal(ciContent, &ciWorkflow)
	require.NoError(t, err, "parsing ci.yml")

	functionalJob, ok := ciWorkflow.Jobs["functional"]
	require.True(t, ok, "ci.yml must define a functional job")

	// Assert PR CI runs functional shards
	require.Contains(t, functionalJob.If, "github.event_name == 'pull_request'",
		"functional job must run on pull_request: %s", functionalJob.If)
	require.Contains(t, functionalJob.If, "github.event_name == 'merge_group'",
		"functional job must run on merge_group: %s", functionalJob.If)

	// Assert it uses the functional shards matrix
	require.Contains(t, functionalJob.Strategy.Matrix.Entry, "needs.test-packages.outputs.functional",
		"functional matrix must evaluate functional shards output")
}
