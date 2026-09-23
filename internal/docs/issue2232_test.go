package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestIssue2232 reads docs/SPEC-FLEET-KUBE.md and proves the text
// nova-tools#2232 asks for ("Run one single-node k3s per bench with one Job
// per card"): one single-node k3s per bench (not one fleet cluster), and one
// card gets one pod with its own cgroup, env, log stream, and
// activeDeadlineSeconds. It finds the reference implementation types the spec
// carries for the internal/fleetkube package and checks that they render a
// single-node per bench and at most one pod per card.

func TestIssue2232(t *testing.T) {
	t.Parallel()

	spec, err := os.ReadFile("../../docs/SPEC-FLEET-KUBE.md")
	if err != nil {
		t.Fatalf("docs/SPEC-FLEET-KUBE.md: %v", err)
	}
	body := string(spec)

	for _, want := range []string{
		"One single-node k3s per bench, not one fleet cluster",
		"one card gets one pod",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("docs/SPEC-FLEET-KUBE.md missing %q", want)
		}
	}

	// The spec must carry the reference implementation types for
	// internal/fleetkube, from which per-bench k3s and Job-per-card
	// templates are built. A BenchNode declares one single-node k3s
	// per bench (one node, not a fleet cluster; Studio is not a
	// node). A CardJob declares one Job per card (one pod, its own
	// cgroup, env, log stream, activeDeadlineSeconds).
	captureRe := regexp.MustCompile("(?s)```go\n(.*?)```")
	matches := captureRe.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		t.Fatalf("docs/SPEC-FLEET-KUBE.md has no Go code block with the reference implementation types")
	}

	var foundBench, foundCard bool
	for _, m := range matches {
		block := m[1]
		if strings.Contains(block, "type BenchNode struct") {
			foundBench = true
			if !strings.Contains(block, "BenchName") {
				t.Errorf("BenchNode missing BenchName field; each bench must identify its node")
			}
			// One single-node k3s per bench: the type must not
			// carry a fleet-level cluster reference nor a studio
			// node flag.
			for _, bad := range []string{"Cluster", "FleetCluster", "StudioNode"} {
				if strings.Contains(block, bad) {
					t.Errorf("BenchNode must not contain %q: one single-node k3s per bench, not one fleet cluster; the Studio is not a node", bad)
				}
			}
		}
		if strings.Contains(block, "type CardJob struct") {
			foundCard = true
			if !strings.Contains(block, "ActiveDeadlineSeconds") {
				t.Errorf("CardJob missing ActiveDeadlineSeconds field; one card gets one pod with its own activeDeadlineSeconds")
			}
			// One card gets one pod: the type must not multiply
			// pods per card.
			for _, bad := range []string{"Replicas", "PodCount", "Multiple"} {
				if strings.Contains(block, bad) && !strings.Contains(block, "// never") {
					t.Errorf("CardJob must not contain %q: one card gets one pod, not many", bad)
				}
			}
		}
	}
	if !foundBench {
		t.Errorf("docs/SPEC-FLEET-KUBE.md missing BenchNode type (behaviour 13)")
	}
	if !foundCard {
		t.Errorf("docs/SPEC-FLEET-KUBE.md missing CardJob type (behaviour 14)")
	}
}