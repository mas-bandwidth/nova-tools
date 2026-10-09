package secrets

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeTokenPlanNamesOnlyAndRenewal(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(store, ".git"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(store, ".sops.yaml"), []byte("creation_rules: []\n"), 0o600))
	for name, body := range map[string]string{
		"source": "CLAUDE_CODE_OAUTH_TOKEN: ENC[AES256_GCM,data:source-secret-sentinel,type:str]\nsops:\n  age: []\n",
		"worker": "OTHER_KEY: ENC[AES256_GCM,data:worker-secret-sentinel,type:str]\nsops:\n  age: []\n",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(store, name+".yaml"), []byte(body), 0o600))
	}
	now := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	plan, err := PlanClaudeToken(store, "source", []string{"worker"}, "2026-10-01T00:00:00Z", now)
	require.NoError(t, err)
	assert.Equal(t, ClaudeTokenSeat{Seat: "source", State: "sealed_name_present_unverified"}, plan.Source)
	assert.Equal(t, []ClaudeTokenSeat{{Seat: "worker", State: "missing"}}, plan.Targets)
	assert.Equal(t, "planned", plan.RenewalState)
	assert.Equal(t, "2027-08-27T00:00:00Z", plan.RenewBy)

	unknown, err := PlanClaudeToken(store, "source", []string{"worker"}, "", now)
	require.NoError(t, err)
	assert.Equal(t, "unknown", unknown.RenewalState)
	assert.Empty(t, unknown.RenewBy)
	require.NoError(t, os.Remove(filepath.Join(store, "worker.yaml")))
	withoutSeat, err := PlanClaudeToken(store, "source", []string{"worker"}, "", now)
	require.NoError(t, err)
	assert.Equal(t, "seat_missing", withoutSeat.Targets[0].State)

	for _, tc := range []struct{ name, issued string }{
		{"future", "2027-10-01T00:00:00Z"},
		{"invalid", "2026-10-01"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := PlanClaudeToken(store, "source", []string{"worker"}, tc.issued, now)
			assert.Error(t, err)
		})
	}
}

func TestClaudeTokenPlanRefusesClearOrAmbiguousSeats(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(store, ".git"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(store, ".sops.yaml"), []byte("creation_rules: []\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(store, "source.yaml"), []byte("CLAUDE_CODE_OAUTH_TOKEN: clear-secret-sentinel\nsops:\n  age: []\n"), 0o600))
	now := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	_, err := PlanClaudeToken(store, "source", []string{"worker"}, "", now)
	assert.ErrorContains(t, err, "duplicate or clear")
	require.NoError(t, os.WriteFile(filepath.Join(store, "source.yaml"), []byte("CLAUDE_CODE_OAUTH_TOKEN: ENC[AES256_GCM,data:a,type:str]\nCLAUDE_CODE_OAUTH_TOKEN: ENC[AES256_GCM,data:b,type:str]\nsops:\n  age: []\n"), 0o600))
	_, err = PlanClaudeToken(store, "source", []string{"worker"}, "", now)
	assert.ErrorContains(t, err, "duplicate or clear")
	for _, targets := range [][]string{{}, {"source"}, {"worker", "worker"}, {"worker "}} {
		_, err := PlanClaudeToken(store, "source", targets, "", now)
		assert.Error(t, err)
	}
}
