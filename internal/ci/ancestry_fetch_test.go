package ci

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestAncestryFetchesAreOneVerb: the classtests rule reads a landing from git,
// and a shallow ancestry is a red run naming the fetch (devHistoryFetch,
// foundationHistoryFetch). Every workflow step that completes an ancestry is one
// call of `ci fetch-ancestry` (no fetch written into the step), the verb runs
// the fetch those constants name (commits and trees only, completed with
// --unshallow when the checkout is shallow), and a dev ancestry is fetched on a
// main run while sprint/foundation's is fetched on the promotion and the landing
// runs, where a one-parent commit reads no promotion.
func TestAncestryFetchesAreOneVerb(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	verb := readFile(t, filepath.Join(root, "tools", "ci", "sel_ancestry.go"))
	for _, want := range []string{
		`"git", "fetch", "--no-tags", "--filter=blob:none"`,
		`argv = append(argv, "--unshallow")`,
		`"+"+branch+":refs/remotes/origin/"+branch`,
		`"--is-shallow-repository"`,
		`a one-parent commit: no promotion to read`,
		`"git", "cat-file", "-p", "HEAD"`,
		`strings.Count("\n"+headers, "\nparent ") < 2`,
		`e.getenv("GITHUB_EVENT_NAME") != "pull_request"`,
	} {
		assert.Contains(t, verb, want, "tools/ci/sel_ancestry.go: the fetch is not the one %s and %s name", devHistoryFetch, foundationHistoryFetch)
	}

	for _, file := range []string{"ci.yml", "certification.yml"} {
		src := readFile(t, filepath.Join(root, ".github", "workflows", file))
		for _, step := range []struct{ name, want string }{
			{"fetch dev's ancestry for a main run", ciRunner + " fetch-ancestry dev"},
			{"fetch sprint/foundation's ancestry for a promotion", ciRunner + " fetch-ancestry --promotion sprint/foundation"},
		} {
			body := stepBody(src, step.name)
			if !assert.NotEmpty(t, body, "%s has no step %q", file, step.name) {
				continue
			}
			assert.Contains(t, body, step.want, "%s step %q", file, step.name)
			for _, line := range strings.Split(body, "\n") {
				code := strings.TrimSpace(line)
				if !strings.HasPrefix(code, "#") {
					assert.NotContains(t, code, "git fetch", "%s step %q spells a git fetch itself; the fetch is the verb's", file, step.name)
				}
			}
		}
	}
}

// Hosted shrink-only guards read the full dev ancestry: the default branch
// fetches before the deal, other refs fetch only when their shard selects CI.
func TestHostedRatchetFetchesDevBeforeTests(t *testing.T) {
	t.Parallel()
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				Name     string `yaml:"name"`
				If       string `yaml:"if"`
				Run      string `yaml:"run"`
				Shell    string `yaml:"shell"`
				Continue any    `yaml:"continue-on-error"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	raw := readFile(t, filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	require.NoError(t, yaml.Unmarshal([]byte(raw), &wf))
	job, ok := wf.Jobs["test-hosted"]
	require.True(t, ok)
	main, deal, fetch, test := -1, -1, -1, -1
	for i, step := range job.Steps {
		if strings.Contains(step.Run, ciRunner+" deal ") {
			deal = i
		}
		if step.Name == "fetch dev's ancestry for a main run" {
			main = i
			assert.Equal(t, "github.ref_name == github.event.repository.default_branch", step.If)
		}
		if step.Name == "fetch dev's ancestry for the hosted ratchet" {
			fetch = i
			assert.Equal(t, "contains(format(' {0} ', env.HOSTED_PKGS), '/internal/ci ') && github.ref_name != github.event.repository.default_branch", step.If)
		}
		if step.Name == "fetch dev's ancestry for a main run" || step.Name == "fetch dev's ancestry for the hosted ratchet" {
			assert.Equal(t, ciRunner+" fetch-ancestry dev", strings.TrimSpace(step.Run))
			assert.Equal(t, "bash", step.Shell)
			assert.True(t, step.Continue == nil || step.Continue == false, "fetch failure must fail the job")
		}
		assert.NotContains(t, step.Run, "--depth=1", "hosted guard must not narrow ancestry after a full fetch")
		if strings.Contains(step.Run, "make test") {
			test = i
		}
	}
	require.GreaterOrEqual(t, main, 0)
	require.GreaterOrEqual(t, deal, 0)
	require.GreaterOrEqual(t, fetch, 0)
	require.GreaterOrEqual(t, test, 0)
	assert.True(t, main < deal && deal < fetch && fetch < test, "main/deal/fetch/test = %d/%d/%d/%d", main, deal, fetch, test)
}
