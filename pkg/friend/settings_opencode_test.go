package friend_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/friend/friendtest"
)

// TestInstallUpdatesStaleModelOnMove tests that when a friend AI moves to a
// new machine and is installed with a new provider/model, the install
// detects drift from the old model and writes the new model to opencode.json.
// This ensures the friend uses the correct provider key after a machine move
// (docs/SPEC-FRIEND.md, Harness settings: opencode's model, provider/model).
func TestInstallUpdatesStaleModelOnMove(t *testing.T) {
	t.Parallel()

	m := friendtest.NewMemFS()
	require.NoError(t, m.MkdirAll("/home/zoe/zoe-working", 0o755))

	// an old opencode.json from the previous machine, naming the old provider
	oldConfig := `{
  "model": "old-provider/old-model",
  "permission": {
    "external_directory": {
      "/home/zoe/zoe-working/**": "allow"
    }
  }
}
`
	require.NoError(t, m.WriteFile("/home/zoe/zoe-working/opencode.json", []byte(oldConfig), 0o644))

	h := friend.HarnessSettings{
		Friend:  "zoe",
		Dir:     "/home/zoe/zoe-working",
		Home:    "/home/zoe",
		Harness: "opencode",
		Model:   "new-provider/new-model", // the model after the move
		FS:      m,
	}

	// the old model is drift against what install would write
	before, err := h.Check()
	require.NoError(t, err)
	drift := friend.Drift(before)
	require.NotEmpty(t, drift, "check should find drift when the old model is there")
	assert.Contains(t, drift[0].Name, "model", "drift should name the model setting")

	// install writes the new model
	wrote, err := h.Write()
	require.NoError(t, err)
	assert.NotEmpty(t, wrote, "install should write the new model")

	text, err := m.ReadFile("/home/zoe/zoe-working/opencode.json")
	require.NoError(t, err)
	assert.Contains(t, string(text), `"model": "new-provider/new-model"`)

	after, err := h.Check()
	require.NoError(t, err)
	assert.Empty(t, friend.Drift(after), "no drift after install")
}
