package card

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card/harvestcopy"
)

// TestPushTokenNeverReachesTheHarnessOrTheRunner (#4227): the bench's push
// credential is the wrapper's, for its end; harnessEnv (the harness
// program or the in-process Go harness) and runnerEnv (the sandboxed
// runner, nova-swarm native) both drop it and the askpass switch by name,
// whatever else the environment holds.
func TestPushTokenNeverReachesTheHarnessOrTheRunner(t *testing.T) {
	t.Parallel()
	base := []string{"PATH=/usr/bin", "HOME=/h", harvestcopy.TokenEnv + "=ghp-bench", harvestcopy.AskpassEnv + "=1",
		"OPENROUTER_API_KEY=or-val", "NOVA_CARD_BENCH=b"}
	cfg := WrapperConfig{Sprint: "copies", Label: "quack-1-c1", Attempt: 1, Bench: "b"}
	refuse := func(what string, env []string) {
		t.Helper()
		for _, kv := range env {
			k, v, _ := strings.Cut(kv, "=")
			if k == harvestcopy.TokenEnv || k == harvestcopy.AskpassEnv || v == "ghp-bench" {
				t.Fatalf("%s carries the push credential: %q in %v", what, kv, env)
			}
		}
	}
	harness := harnessEnv(base, cfg, "/jobs/copies/quack-1-c1/1", "/results/x")
	refuse("harnessEnv", harness)
	if !contains(harness, "HOME=/h") || !contains(harness, "NOVA_CARD=copies/quack-1-c1/1") {
		t.Fatalf("harnessEnv dropped more than the credential: %v", harness)
	}
	runner, have := runnerEnv(base, "OPENROUTER_API_KEY", RunConfig{JobDir: "/jobs/x", OutDir: "/jobs/x/out"}, "copies/quack-1-c1/1")
	refuse("runnerEnv", runner)
	if !have || !contains(runner, "OPENROUTER_API_KEY=or-val") || !contains(runner, "HOME=/h") {
		t.Fatalf("runnerEnv dropped more than the credential: %v have=%v", runner, have)
	}
}

func contains(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}
