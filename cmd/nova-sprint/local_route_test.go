package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A local route is shown like any other (docs/SPEC-LOCAL.md, "Fleet"): routes names its
// serving machine and the lanes in use, and where's providers table carries the local
// provider beside the metered ones.
func TestRoutesAndWhereShowALocalRoute(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	ta.m.SetRoutes([]sprint.Route{
		{Name: "local-gemma4-32k-g1", Tier: "flash", Provider: "local", Model: "gemma4-32k", Machine: "g1", Deadline: 600, Enabled: true},
		{Name: "flash-or", Tier: "flash", Provider: "openrouter", Model: "m", Deadline: 600, Enabled: true},
	})
	ta.m.SetLanes(map[string]int{"g1": 2})
	routes := ta.ok("routes")
	assert.Contains(t, routes, "ROUTE local-gemma4-32k-g1 model=local/gemma4-32k tier=flash enabled=true ")
	assert.Contains(t, routes, " serve=g1 lanes=0/2\n")
	assert.NotContains(t, routes, "ROUTE flash-or model=openrouter/m tier=flash enabled=true attempts=0 ok=0 failed=0 provider_failures=0 mean_wall=- rested_until=- balance=- serve=")

	var v whereView
	require.NoError(t, json.Unmarshal([]byte(ta.ok("where --json")), &v))
	var names []string
	for _, p := range v.Providers {
		names = append(names, p.Name)
	}
	assert.Contains(t, names, "local")
}
