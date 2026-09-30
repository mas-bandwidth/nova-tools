package ci

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestCIOKRunsNoForkCodeAndIsRedOnAFork holds ci-ok to two rules now that its
// verdict is Go (tools/ci aggregate) rather than workflow text. ci-ok runs on a
// self-hosted runner and has no head-repo guard of its own (a skipped ci-ok is a
// passing required check), so:
//   - no step of ci-ok that runs this tree's Go (`go run`, `go build`, ./tools/ci)
//     can fire on a fork's pull_request: its if either names the head-repo
//     equality or cannot fire on pull_request at all;
//   - one step fires on exactly a fork's pull_request and exits 1, so a fork's
//     ci-ok is red without anything of the fork's being run.
func TestCIOKRunsNoForkCodeAndIsRedOnAFork(t *testing.T) {
	t.Parallel()
	raw := readFile(t, filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	var wf ciokWorkflow
	if err := yaml.Unmarshal([]byte(raw), &wf); err != nil {
		t.Fatal(err)
	}
	job, ok := wf.Jobs["ci-ok"]
	if !ok {
		t.Fatal("ci.yml has no ci-ok job")
	}
	const sameRepo = "github.event.pull_request.head.repo.full_name == github.repository"
	const forkRepo = "github.event.pull_request.head.repo.full_name != github.repository"
	forkRed := 0
	for _, s := range job.Steps {
		runsGo := strings.Contains(s.Run, "go run ") || strings.Contains(s.Run, "go build ") || strings.Contains(s.Run, "./tools/ci")
		canFireOnPR := canFireOnPullRequest(s.If)
		if runsGo && canFireOnPR && !strings.Contains(s.If, sameRepo) {
			t.Errorf("ci-ok step %q runs this tree's Go and can fire on a fork's pull_request (if: %s); guard it with %s", s.Name, s.If, sameRepo)
		}
		if strings.Contains(s.If, forkRepo) && strings.Contains(s.If, "github.event_name == 'pull_request'") {
			lines := runLines(s.Run)
			if len(lines) == 0 || lines[len(lines)-1] != "exit 1" || runsGo {
				t.Errorf("ci-ok step %q fires on a fork's pull_request but does not simply exit 1: %q", s.Name, s.Run)
				continue
			}
			forkRed++
		}
	}
	if forkRed != 1 {
		t.Errorf("ci-ok has %d steps that state a fork's pull_request red, want exactly one", forkRed)
	}
}

// canFireOnPullRequest says whether a step's `if:` can be true on a
// pull_request. It answers yes unless the expression states, by `==` on
// github.event_name and by nothing looser, that it fires on other events only:
//   - no github.event_name in it at all: it does not look at the event, so yes;
//   - any `!=` on github.event_name: `!= 'schedule'` and `!= 'push'` are true on
//     a pull_request, so yes (the conservative answer for every `!=`);
//   - no `==` on github.event_name: yes;
//   - otherwise yes exactly when one of its `==` names pull_request (or
//     pull_request_target).
func canFireOnPullRequest(ifExpr string) bool {
	const ev = "github.event_name"
	if !strings.Contains(ifExpr, ev) {
		return true
	}
	if eventNameNotEquals.MatchString(ifExpr) {
		return true
	}
	equals := eventNameEquals.FindAllStringSubmatch(ifExpr, -1)
	if len(equals) == 0 {
		return true
	}
	for _, m := range equals {
		if m[1] == "pull_request" || m[1] == "pull_request_target" {
			return true
		}
	}
	return false
}

var (
	eventNameNotEquals = regexp.MustCompile(`github\.event_name\s*!=`)
	eventNameEquals    = regexp.MustCompile(`github\.event_name\s*==\s*'([a-z_]+)'`)
)

// TestCanFireOnPullRequestIsConservative is the mutation check of the rule
// above, over a table of `if:` texts of its own (never the repository's
// workflow): each row is a step condition that a careless rule would read the
// wrong way, and the answer the ci-ok rule needs.
func TestCanFireOnPullRequestIsConservative(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		cond string
		want bool
	}{
		{"no event name", "always()", true},
		{"empty", "", true},
		{"not schedule fires on a pull request", "always() && github.event_name != 'schedule'", true},
		{"not push fires on a pull request", "github.event_name != 'push'", true},
		{"not pull_request still counts as loose", "github.event_name != 'pull_request'", true},
		{"not pull_request or the fork test", "always() && (github.event_name != 'pull_request' || github.event.pull_request.head.repo.full_name == github.repository)", true},
		{"a pull_request mentioned by another context is no event test", "github.event.pull_request.number == 4", true},
		{"only push", "always() && github.event_name == 'push'", false},
		{"push or dispatch", "github.event_name == 'push' || github.event_name == 'workflow_dispatch'", false},
		{"pull_request", "github.event_name == 'pull_request'", true},
		{"pull_request among others", "github.event_name == 'merge_group' || github.event_name == 'pull_request'", true},
		{"pull_request_target", "github.event_name == 'pull_request_target'", true},
		{"a != hiding behind an == on another event", "github.event_name == 'push' || github.event_name != 'schedule'", true},
		{"spaces around the operator", "github.event_name   !=   'push'", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, canFireOnPullRequest(tc.cond), tc.cond)
		})
	}
}

// TestCIOKForkRuleCatchesAnUnguardedStepUnderAnyNotEqualsIf runs the ci-ok rule
// over workflow text held in this test, edited the way a mistake would edit it:
// an unguarded `./tools/ci` step whose if is `!= 'schedule'` must be reported,
// and the same step guarded by the same-repo test, or fired on push only, must
// not.
func TestCIOKForkRuleCatchesAnUnguardedStepUnderAnyNotEqualsIf(t *testing.T) {
	t.Parallel()
	const sameRepo = "github.event.pull_request.head.repo.full_name == github.repository"
	for _, tc := range []struct {
		name     string
		cond     string
		reported bool
	}{
		{"not schedule, unguarded", "always() && github.event_name != 'schedule'", true},
		{"no event test, unguarded", "always()", true},
		{"not schedule, guarded", "always() && github.event_name != 'schedule' && " + sameRepo, false},
		{"push only", "always() && github.event_name == 'push'", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			text := "jobs:\n  ci-ok:\n    steps:\n      - name: the verdict\n        if: " + tc.cond + "\n        run: ./tools/ci aggregate\n"
			var wf ciokWorkflow
			require.NoError(t, yaml.Unmarshal([]byte(text), &wf))
			step := wf.Jobs["ci-ok"].Steps[0]
			runsGo := strings.Contains(step.Run, "./tools/ci")
			reported := runsGo && canFireOnPullRequest(step.If) && !strings.Contains(step.If, sameRepo)
			assert.Equal(t, tc.reported, reported, tc.cond)
		})
	}
}
