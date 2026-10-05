package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// THE COST RECONCILIATION (docs/SPEC-SPRINT.md, "What a card cost"; internal/sprint
// cost_reconcile.go). run reads each provider's own count of today's usage when it begins
// and every sprint.CostReconcileEvery after, through the seat's key in its own environment,
// and writes the reads beside the sprint's records of the same UTC day in one step
// (sprint.CostReconcile), held to the server's one line of control as a tick is. The HTTP
// is here, outside every tick, as the balance poll's is (balance.go).

// OpenRouterKeyURL is openrouter's key endpoint: GET with the key as a bearer token answers
// {"data": {"usage_daily": <dollars the key used this UTC day>, ...}}.
const OpenRouterKeyURL = "https://openrouter.ai/api/v1/key"

// usageKeyEnv is the variable each provider's key is in (internal/provbalance KeyEnv).
var usageKeyEnv = map[string]string{"openrouter": "OPENROUTER_API_KEY"}

// costReconcileLoop reconciles until ctx is done, waiting on a.after between reads.
func (a *app) costReconcileLoop(ctx context.Context, st *store.Store, stdout io.Writer) {
	fmt.Fprintf(stdout, "COST RECONCILE every %s: each provider's usage today is read through the seat's key and set beside the sprint's cost records\n", sprint.CostReconcileEvery)
	for ctx.Err() == nil {
		a.reconcileCosts(ctx, st, stdout)
		select {
		case <-ctx.Done():
		case <-a.after(sprint.CostReconcileEvery):
		}
	}
}

// reconcileCosts reads the usage of every provider the store's routes name and writes the
// reconciliation (sprint.CostReconcile), and prints one line of what it read, or why it
// wrote nothing.
func (a *app) reconcileCosts(ctx context.Context, st *store.Store, stdout io.Writer) {
	at := a.now()
	routes, _, err := st.Routes(ctx)
	if err != nil {
		fmt.Fprintf(stdout, "%s COST RECONCILE FAIL the routes could not be read: %s\n", at.Format("15:04:05"), oneline.Escape(err.Error()))
		return
	}
	names := map[string]bool{}
	for _, r := range routes {
		if r.Provider != "" {
			names[r.Provider] = true
		}
	}
	if len(names) == 0 {
		return
	}
	var reads []sprint.UsageRead
	var said []string
	for _, p := range slices.Sorted(maps.Keys(names)) {
		rd := readProviderUsage(ctx, a.transport, p, a.getenv, at)
		reads = append(reads, rd)
		if rd.Known {
			said = append(said, p+"="+sprint.Dollars(rd.Used))
		} else {
			said = append(said, p+"=unknown")
		}
	}
	req := sprint.CostReconcileReq{Reads: reads, Who: sprint.MachineActor}
	step := store.Step{Args: store.ArgsOf(req), Verb: "cost reconcile", Load: store.All, Routes: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.CostReconcile(s, req) }}
	a.serial.Lock()
	res, err := st.Run(ctx, step)
	a.serial.Unlock()
	if err != nil {
		fmt.Fprintf(stdout, "%s COST RECONCILE FAIL %s: not written: %s\n", at.Format("15:04:05"), strings.Join(said, " "), oneline.Escape(err.Error()))
		return
	}
	fmt.Fprintf(stdout, "%s COST RECONCILE %s notes=%d\n", at.Format("15:04:05"), strings.Join(said, " "), res.Notes)
}

// readProviderUsage is the provider's own count of the UTC day of now, over the transport
// (nil is http.DefaultTransport), its key read from getenv; a provider with no usage
// endpoint, no key, or an answer not of the shape is unknown with why, and the key is never
// said.
func readProviderUsage(ctx context.Context, rt http.RoundTripper, provider string, getenv func(string) string, now time.Time) sprint.UsageRead {
	day := now.UTC().Format(time.DateOnly)
	unknown := func(why string) sprint.UsageRead { return sprint.UsageRead{Provider: provider, Day: day, Note: why} }
	env, ok := usageKeyEnv[provider]
	if !ok {
		return unknown("no usage endpoint is known for provider " + provider)
	}
	key := getenv(env)
	if key == "" {
		return unknown(env + " is not in the run loop's environment: run it under nova-secrets exec --only " + env)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, OpenRouterKeyURL, nil)
	if err != nil {
		return unknown("the request could not be made: " + err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+key)
	if rt == nil {
		rt = http.DefaultTransport
	}
	resp, err := (&http.Client{Transport: rt}).Do(req)
	if err != nil {
		return unknown("GET " + OpenRouterKeyURL + " failed: " + err.Error())
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return unknown("GET " + OpenRouterKeyURL + ": the answer could not be read: " + err.Error())
	}
	if resp.StatusCode != http.StatusOK {
		return unknown(fmt.Sprintf("GET %s answered %d", OpenRouterKeyURL, resp.StatusCode))
	}
	var wire struct {
		Data struct {
			UsageDaily *float64 `json:"usage_daily"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil || wire.Data.UsageDaily == nil {
		return unknown("GET " + OpenRouterKeyURL + " answered no data.usage_daily")
	}
	return sprint.UsageRead{Provider: provider, Known: true, Day: day, Used: *wire.Data.UsageDaily}
}
