package main

import "testing"

// #3151: the card run's OpenRouter pin config names its key by variable, so
// the harness inherits it from the environment and no auth file is needed.
func TestNativeConfigEnvKeyIsNotMissingAuth(t *testing.T) {
	cfg := []byte(`{"provider":{"openrouter":{"options":{"apiKey":"{env:NOVA_TEST_PIN_KEY}"},"models":{"q/m":{"options":{"provider":{"order":["alibaba"],"allow_fallbacks":false}}}}}}}`)
	t.Setenv("NOVA_TEST_PIN_KEY", "")
	if !modelProviderMissingAuth(cfg, "", "openrouter") {
		t.Fatal("an {env:NAME} key whose variable is empty is missing")
	}
	t.Setenv("NOVA_TEST_PIN_KEY", "set")
	if modelProviderMissingAuth(cfg, "", "openrouter") {
		t.Fatal("an {env:NAME} key whose variable is set was refused as missing")
	}
}
