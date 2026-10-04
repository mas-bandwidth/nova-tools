package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A local route is shown like any other (docs/SPEC-LOCAL.md, "Fleet"): routes names its
// endpoint and the slots in use, and where's providers table carries the local
// provider beside the metered ones.
func TestRoutesAndWhereShowALocalRoute(t *testing.T) {
	t.Parallel()
	routesSeconds := 600
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	ta.m.SetRoutes([]sprint.Route{
		{Name: "local-gemma4-32k-g1", Tier: "flash", Provider: "local", Model: "gemma4-32k", Endpoint: "http://g1.test:11434/v1", Concurrency: 2, Deadline: routesSeconds, Enabled: true},
		{Name: "flash-or", Tier: "flash", Provider: "openrouter", Model: "m", Deadline: routesSeconds, Enabled: true},
	})
	routes := ta.ok("routes")
	assert.Contains(t, routes, "ROUTE local-gemma4-32k-g1 model=local/gemma4-32k tier=flash enabled=true ")
	assert.Contains(t, routes, " endpoint=http://g1.test:11434/v1 concurrency=0/2\n")
	assert.NotContains(t, routes, "ROUTE flash-or model=openrouter/m tier=flash enabled=true attempts=0 ok=0 failed=0 provider_failures=0 mean_wall=- rested_until=- balance=- endpoint=")

	var v whereView
	require.NoError(t, json.Unmarshal([]byte(ta.ok("where --json")), &v))
	var names []string
	for _, p := range v.Providers {
		names = append(names, p.Name)
	}
	assert.Contains(t, names, "local")
}
