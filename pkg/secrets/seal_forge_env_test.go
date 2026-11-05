package secrets

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSealGHPassesTheResolvedSeatForgeEnvironment(t *testing.T) {
	t.Parallel()

	var gotEnv []string
	run := func(_ io.Reader, env []string, _, _ string, _ ...string) ([]byte, error) {
		gotEnv = append([]string(nil), env...)
		return []byte("ok"), nil
	}
	out, err := sealGH(run, "gh", t.TempDir(), []string{"GH_CONFIG_DIR=/seat/gh-config"}, "pr", "view", "7")
	require.NoError(t, err)
	assert.Equal(t, "ok", out)
	assert.Contains(t, gotEnv, "GH_CONFIG_DIR=/seat/gh-config")
	assert.Contains(t, gotEnv, "GH_PROMPT_DISABLED=1")
}
