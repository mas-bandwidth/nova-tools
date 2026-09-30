package ci

import (
	"path/filepath"
	"strings"
	"testing"
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
		`len(strings.Fields(res.Stdout)) < 3`,
		`e.getenv("GITHUB_EVENT_NAME") != "pull_request"`,
	} {
		if !strings.Contains(verb, want) {
			t.Errorf("tools/ci/sel_ancestry.go does not contain %q: the fetch is not the one %s and %s name", want, devHistoryFetch, foundationHistoryFetch)
		}
	}

	for _, file := range []string{"ci.yml", "certification.yml"} {
		src := readFile(t, filepath.Join(root, ".github", "workflows", file))
		for _, step := range []struct{ name, want string }{
			{"fetch dev's ancestry for a main run", "run: " + ciRunner + " fetch-ancestry dev"},
			{"fetch sprint/foundation's ancestry for a promotion", "run: " + ciRunner + " fetch-ancestry --promotion sprint/foundation"},
		} {
			body := stepBody(src, step.name)
			if body == "" {
				t.Errorf("%s has no step %q", file, step.name)
				continue
			}
			if !strings.Contains(body, step.want) {
				t.Errorf("%s step %q does not run %q:\n%s", file, step.name, step.want, body)
			}
			for _, line := range strings.Split(body, "\n") {
				if code := strings.TrimSpace(line); !strings.HasPrefix(code, "#") && strings.Contains(code, "git fetch") {
					t.Errorf("%s step %q spells a git fetch itself (%q); the fetch is the verb's", file, step.name, code)
				}
			}
		}
	}
}
