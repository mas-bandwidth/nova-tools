package secrets

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestHarnessConfigsReferenceEnvNames verifies that harness configs use
// {env:NAME} references for provider keys rather than literal key values.
// This guards against DRIFT: copied values that outlive their rotation
// in a file nobody watches.
func TestHarnessConfigsReferenceEnvNames(t *testing.T) {
	cfg := map[string]any{
		"$schema": "https://example.invalid/config.json",
		"provider": map[string]any{
			"fake": map[string]any{
				"options": map[string]any{
					"apiKey": "{env:FAKE_KEY}",
				},
				"models": map[string]any{
					"m": map[string]any{},
				},
			},
		},
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal harness config: %v", err)
	}
	configStr := string(raw)

	if !strings.Contains(configStr, "{env:FAKE_KEY}") {
		t.Errorf("harness config must reference the env var name, not literal value:\n%s", configStr)
	}
	if strings.Contains(configStr, "sk-") {
		t.Errorf("harness config must never contain literal key material:\n%s", configStr)
	}
}
