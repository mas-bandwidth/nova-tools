package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNativeDeclaresTheRouteModelInTheJobConfig: the config the harness starts with names
// the launch's model under its provider, so a harness whose catalog does not know the
// model (a fresh data home whose catalog fetch lost the race to the built-in snapshot)
// still finds it. The provider here is a three-part model id, as an openrouter route is.
func TestNativeDeclaresTheRouteModelInTheJobConfig(t *testing.T) {
	t.Parallel()
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/x-ai/grok-4.7", label: "catalog",
		card: []byte("FAKE-PWD\n"), slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, "%s", errOut.String())
	raw, err := os.ReadFile(filepath.Join(slot, "data", ".config", "opencode", "opencode.json"))
	require.NoError(t, err, "the job's harness config")
	var cfg struct {
		Provider map[string]struct {
			Models map[string]json.RawMessage `json:"models"`
		} `json:"provider"`
	}
	require.NoError(t, json.Unmarshal(raw, &cfg), "%s", raw)
	_, declared := cfg.Provider["fake"].Models["x-ai/grok-4.7"]
	assert.True(t, declared, "the route's model is not declared under its provider:\n%s", raw)
}
