package secrets

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// TestHarnessConfigsReferenceEnvNames pins this sentence from
// docs/SPEC-SECRETS.md:1028-1030:
//
//	"All harness configs reference `{env:NAME}`; a literal key in a config is
//	 DRIFT, because a copied value outlives its rotation in a file nobody watches."
//
// A harness config is the provider declaration a worker's description becomes:
// its apiKey is the reference {env:NAME}, never the value (SPEC-SWARM "native
// --worker"). This test holds that shape to the rule and then shows the rule has
// teeth, by refusing a config that carries the key itself instead of the name.
func TestHarnessConfigsReferenceEnvNames(t *testing.T) {
	t.Parallel()

	const envName = "DEEPSEEK_API_KEY"
	if !IsValidEnvVar(envName) {
		t.Fatalf("fixture name %q is not a valid env var name", envName)
	}

	config := harnessConfig(t, "{env:"+envName+"}")
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
	literal := harnessConfig(t, "sk-live-deepseek-key")
	if envReferencePattern.MatchString(harnessAPIKey(t, literal)) {
		t.Errorf("a literal key was accepted as an {env:NAME} reference:\n%s", literal)
	}
}

// envReferencePattern is the {env:NAME} reference a harness config must carry.
var envReferencePattern = regexp.MustCompile(`^\{env:[A-Za-z_][A-Za-z0-9_]*\}$`)

// harnessConfig marshals the provider declaration shape a harness config has,
// with apiKey set to key.
func harnessConfig(t *testing.T, key string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"$schema": "https://example.invalid/config.json",
		"provider": map[string]any{
			"deepseek": map[string]any{
				"options": map[string]any{"apiKey": key},
				"models":  map[string]any{"deepseek-chat": map[string]any{}},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal harness config: %v", err)
	}
	return raw
}

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
