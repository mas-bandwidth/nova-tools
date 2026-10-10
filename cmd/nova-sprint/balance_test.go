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

// creditsAnswer is a transport that answers openrouter's credits, as it did the morning of
// 2026-10-03 until body is set, and counts the requests and the keys they carried: no
// socket is opened.
type creditsAnswer struct {
	keys []string
	body string
}

func (c *creditsAnswer) RoundTrip(r *http.Request) (*http.Response, error) {
	c.keys = append(c.keys, r.Header.Get("Authorization"))
	body := c.body
	if body == "" {
		body = `{"data":{"total_credits":1250,"total_usage":1250.51}}`
	}
	return &http.Response{StatusCode: 200, Header: http.Header{}, Request: r, Body: io.NopCloser(strings.NewReader(body))}, nil
}

// balanceApp is the test app with the store's routes, the fake credits answer and the seat's
// key in its environment, and the store the run loop's poll writes to.
func balanceApp(t *testing.T, routes ...sprint.Route) (*testApp, *creditsAnswer, func() string) {
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	ta.m.SetRoutes(routes)
	fake := &creditsAnswer{}
	ta.a.transport = fake
	base := ta.a.getenv
	ta.a.getenv = func(k string) string {
		if k == "OPENROUTER_API_KEY" {
			return "sk-or-v1-fakefakefakefakefakefakefake0123"
		}
		return base(k)
	}
	st, err := ta.a.store(common{redis: "mem:0", actor: sprint.MachineActor})
	require.NoError(t, err)
	poll := func() string {
		var out bytes.Buffer
		ta.a.pollBalances(context.Background(), st, &out)
		return out.String()
	}
	return ta, fake, poll
}

// A polled balance at zero is the coordinator's judgment and rests nothing (the owner,
// 2026-10-10: "it should raise it to you as a thing to do, but not do it automatically."):
// every route of the provider serves, the inbox holds one judgment, a provider is low on
// funds, naming the verbs; the coordinator's routes rest rests its routes until woken, the
// judgment answered, no poll ends that rest, and routes wake ends it with a reason that is
// not a payment.
func TestAPolledBalanceAtZeroIsAJudgmentAndRestsNothing(t *testing.T) {
	t.Parallel()
	ta, fake, poll := balanceApp(t,
		sprint.Route{Name: "flash-or", Tier: "flash", Provider: "openrouter", Model: "m", Enabled: true},
		sprint.Route{Name: "pro-or", Tier: "pro", Provider: "openrouter", Model: "m", Enabled: true},
		sprint.Route{Name: "flash-oc", Tier: "flash", Provider: "opencode", Model: "m", Enabled: true})
	ta.ok("start")
	poll()
	routes := ta.ok("routes")
	assert.Equal(t, 2, strings.Count(routes, " rested_until=- balance=-$0.51\n"), "every route of the provider serves:\n%s", routes)
	ta.ok("tick")
	inbox := ta.ok("inbox")
	assert.Contains(t, inbox, "provider openrouter is low on funds: balance -$0.51 at ")
	assert.Contains(t, inbox, "STILL SERVE")
	assert.Contains(t, inbox, "nova-sprint routes rest openrouter --reason")
	assert.Equal(t, 1, strings.Count(inbox, "! a provider is low on funds"), "one judgment of the provider:\n%s", inbox)
	assert.NotContains(t, inbox, "a provider is out of funds")

	ta.ok("routes rest openrouter --reason 'out of funds: the owner pays in the morning'")
	assert.Equal(t, 2, strings.Count(ta.ok("routes"), " rested_until=open "), "the coordinator's rest, until woken")
	ta.ok("tick")
	assert.NotContains(t, ta.ok("inbox"), "provider openrouter is low on funds", "answered: the provider rests")

	fake.body = `{"data":{"total_credits":2250,"total_usage":1251}}`
	poll()
	assert.Equal(t, 2, strings.Count(ta.ok("routes"), " rested_until=open "), "no poll ends the coordinator's rest")

	ta.ok("routes wake openrouter --reason 'paid $1000 in the console'")
	routes = ta.ok("routes")
	assert.Equal(t, 3, strings.Count(routes, " rested_until=- "), "woken:\n%s", routes)
	assert.Contains(t, routes, " balance=$999.00\n")
	code, _, errs := ta.do("routes wake openrouter --reason again")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "openrouter does not rest: nothing to wake")
	code, _, errs = ta.do("routes rest openrouter")
	assert.Equal(t, 2, code, "a usage refusal")
	assert.Contains(t, errs, "--reason <text> is required")
}

// A balance at zero never stops the sprint (the owner, 2026-10-10): with every provider
// at -$0.51 the machine runs, dealing on its routes, and the inbox holds the low-on-funds
// judgment, never every provider is out of credit, which only refused takes raise
// (store TestARefusedProviderAloneStopsTheMachineOnceUntilAPayment); start is not refused.
func TestABalanceAtZeroNeverStopsTheSprint(t *testing.T) {
	t.Parallel()
	ta, _, poll := balanceApp(t,
		sprint.Route{Name: "flash-or", Tier: "flash", Provider: "openrouter", Model: "m", Enabled: true},
		sprint.Route{Name: "pro-or", Tier: "pro", Provider: "openrouter", Model: "m", Enabled: true})
	ta.ok("start")
	poll()
	ta.ok("tick")
	var v whereView
	require.NoError(t, json.Unmarshal([]byte(ta.ok("where --json")), &v))
	assert.NotContains(t, v.Machine, "STOPPED")
	inbox := ta.ok("inbox")
	assert.NotContains(t, inbox, "every provider is out of credit")
	assert.Contains(t, inbox, "provider openrouter is low on funds")
	ta.ok("start")
}

// The run loop's balance poll (nova-tools#5199) reads each provider through the seat's key in
// its own environment and writes the reads: where --json carries the providers table (name,
// balance, spend an hour, state), routes carries each route's provider balance, the poll's
// line names the balances and never the key, and a provider at -$0.51 serves on.
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
	assert.Contains(t, out.String(), " BALANCE opencode=unknown openrouter=-$0.51 notes=0\n", "a balance writes no rest and no note")
	assert.NotContains(t, out.String(), key)

	var v whereView
	require.NoError(t, json.Unmarshal([]byte(ta.ok("where --json")), &v))
	require.Len(t, v.Providers, 2)
	assert.Equal(t, "opencode", v.Providers[0].Name)
	assert.Equal(t, "unknown", v.Providers[0].Balance)
	assert.Contains(t, v.Providers[0].Note, "anomalyco/opencode#44189")
	assert.Equal(t, "serving", v.Providers[0].State)
	assert.Equal(t, "openrouter", v.Providers[1].Name)
	assert.Equal(t, "-$0.51", v.Providers[1].Balance)
	assert.NotEmpty(t, v.Providers[1].BalanceAt)
	assert.Equal(t, "serving", v.Providers[1].State)
	assert.NotContains(t, ta.ok("where"), "openrouter", "the text frame draws no providers table")

	routes := ta.ok("routes")
	assert.Contains(t, routes, "ROUTE flash-or ")
	assert.Contains(t, routes, " balance=-$0.51\n")
	assert.Contains(t, routes, " balance=unknown\n")
}
