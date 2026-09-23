package secrets

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// TestHarnessConfigsReferenceEnvNames pins this sentence from
// docs/SPEC-SECRETS.md:1028-1030:
//
//	"All harness configs reference `{env:NAME}`; a literal key in a config is
//	 DRIFT, because a copied value outlives its rotation in a file nobody watches."
//
// A harness config is the provider declaration a worker's description becomes:
// its apiKey is the reference {env:NAME}, never the value (SPEC-SWARM "native
// --worker"). This test exercises the real production writer in
// internal/swarm/worker.go (Worker.HarnessConfig), failing if the writer emits
// a literal key or raw variable name rather than an {env:NAME} reference.
func TestHarnessConfigsReferenceEnvNames(t *testing.T) {
	t.Parallel()

	const envName = "DEEPSEEK_API_KEY"
	if !IsValidEnvVar(envName) {
		t.Fatalf("fixture name %q is not a valid env var name", envName)
	}

	w := swarm.Worker{
		Provider: "deepseek",
		Model:    "deepseek-chat",
		EnvVar:   envName,
	}

	config := w.HarnessConfig()
	ref := harnessAPIKey(t, config)
	if !envReferencePattern.MatchString(ref) {
		t.Errorf("apiKey = %q, want a {env:NAME} reference and never a literal key", ref)
	}
	if name := strings.TrimSuffix(strings.TrimPrefix(ref, "{env:"), "}"); name != envName {
		t.Errorf("apiKey references %q, want %q", name, envName)
	} else if !IsValidEnvVar(name) {
		t.Errorf("apiKey references %q, which is not a valid environment variable name", name)
	}
	for _, literal := range []string{"sk-", "ghp_", "xoxb-", "age1"} {
		if strings.Contains(string(config), literal) {
			t.Errorf("harness config carries literal key material %q:\n%s", literal, config)
		}
	}

	// The sentence is not vacuous: a config that carries the value instead of
	// the name is DRIFT, and it is the shape above, not this one, that passes.
	for _, badKey := range []string{"sk-live-deepseek-key", "ghp_1234567890abcdef", envName} {
		if envReferencePattern.MatchString(badKey) {
			t.Errorf("a literal key %q was accepted as an {env:NAME} reference", badKey)
		}
	}
}

// envReferencePattern is the {env:NAME} reference a harness config must carry.
var envReferencePattern = regexp.MustCompile(`^\{env:[A-Za-z_][A-Za-z0-9_]*\}$`)

// harnessAPIKey reads the single provider's apiKey back out of a harness config.
func harnessAPIKey(t *testing.T, raw []byte) string {
	t.Helper()
	var doc struct {
		Provider map[string]struct {
			Options struct {
				APIKey string `json:"apiKey"`
			} `json:"options"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal harness config: %v", err)
	}
	for _, p := range doc.Provider {
		return p.Options.APIKey
	}
	t.Fatal("harness config names no provider")
	return ""
}
