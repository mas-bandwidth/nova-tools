// Package provbalance reads a model provider's balance through the seat's key: the
// transport of the sprint's balance poll (internal/sprint balance.go; nova-tools#5199).
// It decides nothing: it answers what the provider said, or that its balance is unknown and
// why, and never says the key.
package provbalance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ProviderRead is one balance the poll read: the provider, the dollars left (Known false
// with Note saying why when there is none to read), and its count of dollars used (HasUsed
// false when it keeps none). The sprint's balance poll names it ProviderRead.
type ProviderRead struct {
	Provider string
	Known    bool
	Balance  float64
	HasUsed  bool
	Used     float64
	Note     string
}

// UsageRead is one provider's own count of a UTC day's usage as the run loop read it: Known
// false with Note saying why when there was none to read. The sprint's cost reconciliation
// names it UsageRead.
type UsageRead struct {
	Provider string
	Known    bool
	Day      string // the UTC day the count is of, 2006-01-02
	Used     float64
	Note     string
}

// OpenRouterURL is openrouter's credits endpoint: GET with the key as a bearer token answers
// {"data": {"total_credits": <dollars bought>, "total_usage": <dollars used>}}.
const OpenRouterURL = "https://openrouter.ai/api/v1/credits"

// KeyEnv is the variable each provider's key is in, as nova-secrets exec delivers it to the
// run loop (`nova-secrets exec --only OPENROUTER_API_KEY -- nova-sprint run ...`).
var KeyEnv = map[string]string{"openrouter": "OPENROUTER_API_KEY"}

// unknownWhy is why a provider with no balance endpoint is recorded unknown.
var unknownWhy = map[string]string{
	// read 2026-10-03: Zen's docs and the harness's CLI (providers, models, stats: local
	// readouts) publish no balance; anomalyco/opencode#44189 asks for one and is open
	"opencode": "opencode Zen publishes no balance endpoint (anomalyco/opencode#44189, open; its CLI reads none): the console is the only readout",
}

// Timeout bounds one read: a provider that does not answer leaves the balance unknown.
const Timeout = 15 * time.Second

// maxBody bounds the answer read.
const maxBody = 64 << 10

// Read is the provider's balance over the transport (nil is http.DefaultTransport), its key
// read from getenv; a provider with no endpoint, no key, or an answer that is not its shape
// is unknown with why.
func Read(ctx context.Context, rt http.RoundTripper, provider string, getenv func(string) string) ProviderRead {
	unknown := func(why string) ProviderRead { return ProviderRead{Provider: provider, Note: why} }
	if provider != "openrouter" {
		if why, ok := unknownWhy[provider]; ok {
			return unknown(why)
		}
		return unknown("no balance endpoint is known for provider " + provider)
	}
	env := KeyEnv[provider]
	key := getenv(env)
	if key == "" {
		return unknown(env + " is not in the run loop's environment: run it under nova-secrets exec --only " + env)
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, OpenRouterURL, nil)
	if err != nil {
		return unknown("the request could not be made: " + err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+key)
	if rt == nil {
		rt = http.DefaultTransport
	}
	resp, err := (&http.Client{Transport: rt}).Do(req)
	if err != nil {
		return unknown("GET " + OpenRouterURL + " failed: " + err.Error())
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return unknown("GET " + OpenRouterURL + ": the answer could not be read: " + err.Error())
	}
	if resp.StatusCode != http.StatusOK {
		return unknown(fmt.Sprintf("GET %s answered %d", OpenRouterURL, resp.StatusCode))
	}
	var wire struct {
		Data struct {
			Credits *float64 `json:"total_credits"`
			Usage   *float64 `json:"total_usage"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil || wire.Data.Credits == nil || wire.Data.Usage == nil {
		return unknown("GET " + OpenRouterURL + " answered no data.total_credits and data.total_usage")
	}
	return ProviderRead{Provider: provider, Known: true, Balance: *wire.Data.Credits - *wire.Data.Usage, HasUsed: true, Used: *wire.Data.Usage}
}

// OpenRouterKeyURL is openrouter's key endpoint: GET with the key as a bearer token answers
// {"data": {"usage_daily": <dollars the key used today, the UTC day>, ...}}, the provider's
// own count the cost reconciliation sets beside the sprint's records (sprint.CostReconcile).
const OpenRouterKeyURL = "https://openrouter.ai/api/v1/key"

// ReadUsage is the provider's own count of the UTC day's usage over the transport (nil is
// http.DefaultTransport), its key read from getenv; day is the UTC day the count is of,
// 2006-01-02. A provider with no usage endpoint, no key, or an answer that is not its shape
// is unknown with why, and the key is never in the why.
func ReadUsage(ctx context.Context, rt http.RoundTripper, provider, day string, getenv func(string) string) UsageRead {
	unknown := func(why string) UsageRead { return UsageRead{Provider: provider, Note: why} }
	if provider != "openrouter" {
		if why, ok := unknownWhy[provider]; ok {
			return unknown(why)
		}
		return unknown("no usage endpoint is known for provider " + provider)
	}
	env := KeyEnv[provider]
	key := getenv(env)
	if key == "" {
		return unknown(env + " is not in this environment: run under nova-secrets exec --only " + env)
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
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
	defer resp.Body.Close() // ignored: the answer is read to its end or bounded above; a close error loses nothing read
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return unknown("GET " + OpenRouterKeyURL + ": the answer could not be read: " + err.Error())
	}
	if resp.StatusCode != http.StatusOK {
		return unknown(fmt.Sprintf("GET %s answered %d", OpenRouterKeyURL, resp.StatusCode))
	}
	var wire struct {
		Data struct {
			Daily *float64 `json:"usage_daily"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil || wire.Data.Daily == nil {
		return unknown("GET " + OpenRouterKeyURL + " answered no data.usage_daily")
	}
	return UsageRead{Provider: provider, Known: true, Day: day, Used: *wire.Data.Daily}
}
