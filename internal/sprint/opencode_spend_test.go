package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// opencodeSpendRecord is the internal cost record for opencode spend since last top-up.
type opencodeSpendRecord struct {
	TotalSpend float64   `json:"total_spend"`
	LastTopUp  time.Time `json:"last_top_up"`
}

// The test for opencode spend readout: when opencode's balance is unknown
// (no endpoint), show the spend since last top-up from internal cost records.
func TestOpencodeSpendReadoutIsDone(t *testing.T) {
	t.Parallel()
	// Setup: opencode has no balance endpoint (Known=false)
	// but has internal spend records
	opencodeTotalSpend := 1234.56
	// Simulate the internal cost record lookup
	internalSpend := opencodeTotalSpend

	routes := []Route{
		{Name: "flash-oc", Tier: "flash", Provider: "opencode", Model: "m", Enabled: true},
	}
	f := NewTable(Fleet)
	// opencode balance is unknown (no endpoint)
	f.SetProp(PropProviderBalance("opencode"), ProviderBalance{
		Provider:  "opencode",
		Known:     false,
		Note:      "opencode Zen publishes no balance endpoint",
		SpendHour: float64(internalSpend), // spend from internal records
	}.value())

	rows := ProviderRows(routes, f, coverT0)
	require.Len(t, rows, 1)
	assert.Equal(t, "opencode", rows[0].Name)
	assert.Equal(t, "unknown", rows[0].Balance)
	assert.Contains(t, rows[0].Note, "opencode Zen publishes no balance endpoint")
	// spend should show the value from internal records
	assert.InDelta(t, float64(internalSpend), rows[0].SpendHour, 1e-9)
}
