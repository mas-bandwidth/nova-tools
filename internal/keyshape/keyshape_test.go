package keyshape

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSecretNameIsTheArgvLogsPredicate(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"DEEPSEEK_API_KEY", "GH_TOKEN", "SOPS_AGE_SECRET", "lower_case_key", "MiXeD_ToKeN"} {
		require.True(t, SecretName(name), "%s is not recognised as a secret name", name)
	}
	// The predicate is deliberately blunt -- it is a substring test, so MONKEY carries
	// KEY -- and blunt in the safe direction: it over-scrubs and never under-scrubs.
	for _, name := range []string{"PATH", "HOME", "LANG", "TERM", "NOVA_SWARM_JOB", "XDG_DATA_HOME"} {
		require.False(t, SecretName(name), "%s was called a secret name", name)
	}
	require.True(t, SecretName("MONKEY"), "the predicate stopped being a substring test; cmd/nova-worker's argv log and shell shim assume it is one")
}
