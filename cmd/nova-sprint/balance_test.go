package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// creditsAnswer is a transport that answers openrouter's credits as it did the morning of
// 2026-10-03, and counts the requests and the keys they carried: no socket is opened.
type creditsAnswer struct{ keys []string }

func (c *creditsAnswer) RoundTrip(r *http.Request) (*http.Response, error) {
	c.keys = append(c.keys, r.Header.Get("Authorization"))
	return &http.Response{StatusCode: 200, Header: http.Header{}, Request: r,
		Body: io.NopCloser(strings.NewReader(`{"data":{"total_credits":1250,"total_usage":1250.51}}`))}, nil
}

// The run loop's balance poll (nova-tools#5199) reads each provider through the seat's key in
// its own environment and writes the reads: where --json carries the providers table (name,
// balance, spend an hour, state), routes carries each route's provider balance, the poll's
// line names the balances and never the key, and a provider at $-0.51 rests.
func TestTheRunLoopPollsBalancesAndWhereShowsTheProvidersTable(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	ta.m.SetRoutes([]sprint.Route{
		{Name: "flash-or", Tier: "flash", Provider: "openrouter", Model: "m", Enabled: true},
		{Name: "flash-oc", Tier: "flash", Provider: "opencode", Model: "m", Enabled: true},
	})
	fake := &creditsAnswer{}
	ta.a.transport = fake
	const key = "sk-or-v1-fakefakefakefakefakefakefake0123"
	base := ta.a.getenv
	ta.a.getenv = func(k string) string {
		if k == "OPENROUTER_API_KEY" {
			return key
		}
		return base(k)
	}
	st, err := ta.a.store(common{redis: "mem:0", actor: sprint.MachineActor})
	require.NoError(t, err)
	var out bytes.Buffer
	ta.a.pollBalances(context.Background(), st, &out)
	assert.Equal(t, []string{"Bearer " + key}, fake.keys, "one read, openrouter's, with the seat's key")
	assert.Contains(t, out.String(), " BALANCE opencode=unknown openrouter=$-0.51 notes=1\n")
	assert.NotContains(t, out.String(), key)

	var v whereView
	require.NoError(t, json.Unmarshal([]byte(ta.ok("where --json")), &v))
	require.Len(t, v.Providers, 2)
	assert.Equal(t, "opencode", v.Providers[0].Name)
	assert.Equal(t, "unknown", v.Providers[0].Balance)
	assert.Contains(t, v.Providers[0].Note, "anomalyco/opencode#44189")
	assert.Equal(t, "serving", v.Providers[0].State)
	assert.Equal(t, "openrouter", v.Providers[1].Name)
	assert.Equal(t, "$-0.51", v.Providers[1].Balance)
	assert.NotEmpty(t, v.Providers[1].BalanceAt)
	assert.Contains(t, v.Providers[1].State, "resting until ")
	assert.NotContains(t, ta.ok("where"), "openrouter", "the text frame draws no providers table")

	routes := ta.ok("routes")
	assert.Contains(t, routes, "ROUTE flash-or ")
	assert.Contains(t, routes, " balance=$-0.51\n")
	assert.Contains(t, routes, " balance=unknown\n")
}
