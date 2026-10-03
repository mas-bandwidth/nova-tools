package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

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

// A polled balance at zero excludes the provider until a later poll shows a balance (the
// owner, 2026-10-03, 8:18 AM ET: "you'll need to detect when a provider runs out of
// credits, and exclude that provider moving forward, and let me know."): every route of it
// rests "out of credit" with no time, hours do not end it, the coordinator is told in one
// judgment naming it (a payment is the owner's), and the poll that reads a balance over
// zero ends the rest.
func TestAPolledBalanceAtZeroExcludesTheProviderUntilABalanceReturns(t *testing.T) {
	t.Parallel()
	ta, fake, poll := balanceApp(t,
		sprint.Route{Name: "flash-or", Tier: "flash", Provider: "openrouter", Model: "m", Enabled: true},
		sprint.Route{Name: "pro-or", Tier: "pro", Provider: "openrouter", Model: "m", Enabled: true},
		sprint.Route{Name: "flash-oc", Tier: "flash", Provider: "opencode", Model: "m", Enabled: true})
	ta.ok("start")
	poll()
	routes := ta.ok("routes")
	for _, r := range []string{"flash-or", "pro-or"} {
		assert.Contains(t, routes, "ROUTE "+r+" ")
	}
	assert.Equal(t, 2, strings.Count(routes, " rested_until=open balance=-$0.51\n"), "every route of the provider, until paid:\n%s", routes)
	assert.Contains(t, routes, " rested_until=- balance=unknown\n", "the other provider serves")
	ta.ok("tick")
	inbox := ta.ok("inbox")
	assert.Contains(t, inbox, "provider openrouter is out of funds (balance -$0.51 at ")
	assert.Contains(t, inbox, "a payment is the owner's")
	assert.Contains(t, inbox, "nova-sprint funded openrouter --reason")
	assert.Equal(t, 1, strings.Count(inbox, "a provider is out of funds"), "one judgment of the provider:\n%s", inbox)

	ta.a.sleep(3 * time.Hour)
	poll() // still out
	ta.ok("tick")
	assert.Equal(t, 2, strings.Count(ta.ok("routes"), " rested_until=open "), "hours end nothing")

	fake.body = `{"data":{"total_credits":2250,"total_usage":1251}}` // a payment
	poll()
	routes = ta.ok("routes")
	assert.Equal(t, 3, strings.Count(routes, " rested_until=- "), "the poll that reads a balance ends the rests:\n%s", routes)
	assert.Contains(t, routes, " balance=$999.00\n")
	ta.ok("tick")
	assert.NotContains(t, ta.ok("inbox"), "provider openrouter is out of funds", "the judgment closes")
}

// Every provider out of credit stops the sprint (the owner, 2026-10-03, 8:18 AM ET: "then
// if all providers are out, then you stop the sprint."): the tick STOPS the machine with the
// cause, the machine line says it, one judgment is raised; start is refused with the same
// line while they stay out; a poll with a balance restored ends the rests and the
// coordinator starts it.
func TestEveryProviderOutOfCreditStopsTheSprint(t *testing.T) {
	t.Parallel()
	ta, fake, poll := balanceApp(t,
		sprint.Route{Name: "flash-or", Tier: "flash", Provider: "openrouter", Model: "m", Enabled: true},
		sprint.Route{Name: "pro-or", Tier: "pro", Provider: "openrouter", Model: "m", Enabled: true})
	ta.ok("start")
	poll()
	ta.ok("tick")
	var v whereView
	require.NoError(t, json.Unmarshal([]byte(ta.ok("where --json")), &v))
	assert.Equal(t, "machine: STOPPED (every provider is out of credit)", v.Machine)
	inbox := ta.ok("inbox")
	assert.Equal(t, 1, strings.Count(inbox, "every provider is out of credit (openrouter): a payment is the owner's"), "one judgment:\n%s", inbox)

	code, _, errs := ta.do("start")
	assert.Equal(t, 1, code, "start is refused while every provider is out")
	assert.Contains(t, errs, "every provider is out of credit (openrouter): a payment is the owner's; the sprint is STOPPED until a provider is paid")

	fake.body = `{"data":{"total_credits":2250,"total_usage":1251}}`
	poll()
	ta.ok("start")
	ta.ok("tick")
	require.NoError(t, json.Unmarshal([]byte(ta.ok("where --json")), &v))
	assert.NotContains(t, v.Machine, "STOPPED")
	assert.NotContains(t, ta.ok("inbox"), "every provider is out of credit (openrouter)", "the judgment closes once the machine runs")
}

// The run loop's balance poll (nova-tools#5199) reads each provider through the seat's key in
// its own environment and writes the reads: where --json carries the providers table (name,
// balance, spend an hour, state), routes carries each route's provider balance, the poll's
// line names the balances and never the key, and a provider at -$0.51 rests.
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
	assert.Contains(t, out.String(), " BALANCE opencode=unknown openrouter=-$0.51 notes=1\n")
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
	assert.Contains(t, v.Providers[1].State, "resting until ")
	assert.NotContains(t, ta.ok("where"), "openrouter", "the text frame draws no providers table")

	routes := ta.ok("routes")
	assert.Contains(t, routes, "ROUTE flash-or ")
	assert.Contains(t, routes, " balance=-$0.51\n")
	assert.Contains(t, routes, " balance=unknown\n")
}
