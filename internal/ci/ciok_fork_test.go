package ci

import (
	"path/filepath"
	"strings"
	"testing"

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
		canFireOnPR := strings.Contains(s.If, "pull_request") || !strings.Contains(s.If, "github.event_name")
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
