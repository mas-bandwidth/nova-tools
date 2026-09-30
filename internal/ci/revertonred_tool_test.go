package ci

import (
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestRevertOnRedRunsAToolBuiltFromDevNotFromTheRedTree holds revert-on-red.yml
// to one rule: the program that reverts a red push to main is never built from
// the tree being reverted. That push may be the one that broke tools/ci, and a
// revert that cannot build cannot revert it. The workflow checks the red tree
// out at the workspace root (it is what `git revert` acts on) and checks dev's
// tip, the last green source of main, out beside it; setup-go reads that
// checkout's go.mod, and the step builds tools/ci from that checkout alone and
// runs the binary. No step may `go run` or `go build` tools/ci from the root.
func TestRevertOnRedRunsAToolBuiltFromDevNotFromTheRedTree(t *testing.T) {
	t.Parallel()
	type step struct {
		Name string            `yaml:"name"`
		Uses string            `yaml:"uses"`
		Run  string            `yaml:"run"`
		With map[string]string `yaml:"with"`
	}
	var wf struct {
		Jobs map[string]struct {
			Steps []step `yaml:"steps"`
		} `yaml:"jobs"`
	}
	raw := readFile(t, filepath.Join(repoRoot(t), ".github", "workflows", "revert-on-red.yml"))
	if err := yaml.Unmarshal([]byte(raw), &wf); err != nil {
		t.Fatalf("revert-on-red.yml: %v", err)
	}
	job, ok := wf.Jobs["revert-on-red"]
	if !ok {
		t.Fatal("revert-on-red.yml has no revert-on-red job")
	}
	toolDir, goSetup, built := "", false, false
	for i, s := range job.Steps {
		switch {
		case strings.HasPrefix(s.Uses, "actions/checkout@") && s.With["path"] != "":
			if s.With["ref"] != "dev" {
				t.Errorf("step %d checks the tool out at ref %q, want dev (the last green source of main)", i, s.With["ref"])
			}
			toolDir = s.With["path"]
		case strings.HasPrefix(s.Uses, "actions/checkout@"):
			if !strings.Contains(s.With["ref"], "workflow_run.head_sha") {
				t.Errorf("step %d checks out %q at the root; the root is the red tree, workflow_run.head_sha", i, s.With["ref"])
			}
		case strings.HasPrefix(s.Uses, "actions/setup-go@"):
			if toolDir == "" || s.With["go-version-file"] != toolDir+"/go.mod" {
				t.Errorf("step %d sets up Go from %q; want the tool checkout's go.mod, after that checkout", i, s.With["go-version-file"])
			}
			goSetup = true
		}
		for _, line := range strings.Split(s.Run, "\n") {
			l := strings.TrimSpace(line)
			if !strings.Contains(l, "./tools/ci") {
				continue
			}
			if toolDir == "" || !strings.HasPrefix(l, "go -C "+toolDir+" build ") {
				t.Errorf("step %d builds or runs tools/ci from the red tree: %q; build it with go -C %s", i, l, toolDir)
				continue
			}
			built = true
		}
	}
	if toolDir == "" || !goSetup || !built {
		t.Fatalf("revert-on-red.yml: tool checkout %q, Go set up %t, tool built from it %t; all three are required", toolDir, goSetup, built)
	}
}
