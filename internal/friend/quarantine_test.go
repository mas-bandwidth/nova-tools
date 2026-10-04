package friend

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The supplied profile cannot attest delivery merely from agentapi's exit status.
func TestProductionFactoryRefusesTransportOnlyAntigravityProfile(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("../../profiles/adapters/antigravity.json")
	require.NoError(t, err)
	var config CommandConfig
	require.NoError(t, json.Unmarshal(body, &config))
	adapter, err := NewAdapter(config, time.Second)
	require.ErrorContains(t, err, "unsupported")
	require.Nil(t, adapter)
}
