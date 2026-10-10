package main

import (
	"io"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
)

// TestApplyDryRunTakesTheRealRunsChecks pins that `apply --dry-run` is the
// real run's plan from the same code path: on the refusing case (no actor) the
// dry run refuses with the real run's exit and status word, and writes nothing
// (docs/STANDARD.md, "A verb that writes has a dry run").
func TestApplyDryRunTakesTheRealRunsChecks(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.env["NOVA_SPRINT_REDIS"] = "bench:6380"
	m := testkit.Main(func(args []string, _ io.Reader, stdout, stderr io.Writer) int {
		return run(args, stdout, stderr, h.deps())
	})
	testkit.DryRunAgrees(t, m.Run, []testkit.DryCase{
		{
			Name: "no actor",
			Setup: func(t *testing.T, root string) ([]string, []string) {
				t.Helper()
				h.dir = root
				delete(h.env, "NOVA_FRIEND")
				return []string{"apply"}, []string{"--file", "try.json"}
			},
		},
	})
}
