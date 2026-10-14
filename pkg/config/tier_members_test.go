package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTierMembersComeFromTheRouteRows pins row 11: a tier's membership is the
// enabled routes whose tier field names it, in name order, so the route rows
// are the one record and the tier's stored routes array is only a copy
// (pkg/config/kind.go, TierMembers; docs/SPEC-CONFIG.md, "tier").
func TestTierMembersComeFromTheRouteRows(t *testing.T) {
	t.Parallel()

	routes := []Row{
		{Name: "b", Fields: map[string]string{"enabled": "true", "tier": "flash"}},
		{Name: "a", Fields: map[string]string{"enabled": "true", "tier": "flash"}},
		{Name: "c", Fields: map[string]string{"enabled": "false", "tier": "flash"}},
		{Name: "d", Fields: map[string]string{"enabled": "true", "tier": "slow"}},
	}
	assert.Equal(t, []string{"a", "b"}, TierMembers(routes, "flash"), "only the tier's enabled routes, in name order")
	assert.Empty(t, TierMembers(routes, "none"), "a tier no route names is empty")
}
