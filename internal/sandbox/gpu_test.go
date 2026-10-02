package sandbox

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseGPUModeRejectsBlanketAccess(t *testing.T) {
	t.Parallel()

	_, err := ParseGPUMode("all")
	require.NotNil(t, err, "ParseGPUMode(\"all\") accepted a blanket GPU grant; only none|metal are explicit capabilities")
	m, err := ParseGPUMode("metal")
	require.Nil(t, err, "ParseGPUMode(metal) = %q, %v; want metal", m, err)
	require.Equal(t, GPUMetal, m, "ParseGPUMode(metal) = %q, %v; want metal", m, err)
}
