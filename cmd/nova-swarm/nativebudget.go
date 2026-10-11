package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// nativeBudget is a limited route's global rate budget on its provider proxy
// (pkg/swarm/providerbudget.go): every model request takes it from the sprint's server,
// `budget take <model> --as <worker> --holder <id>`, renews its slot while it runs and
// releases it after. nil when the run names no budget server: an unlimited route, and the
// proxy is as it was. Every line it logs goes to the run's notes, the card's native log.
func nativeBudget(cfg nativeRunConfig, notes io.Writer) *swarm.ProxyBudget {
	if cfg.budgetServer == "" || cfg.model == "" {
		return nil
	}
	client := sprintwire.Client{Addr: cfg.budgetServer}
	as, model := cfg.budgetAs, cfg.model
	holder := cfg.budgetHolder
	if holder == "" {
		holder = cfg.label
	}
	send := func(ctx context.Context, verb, h string) (map[string]any, error) {
		res, err := client.Do(ctx, []string{"budget", verb, model, "--as", as, "--holder", h, "--json"})
		if err != nil {
			return nil, err
		}
		if len(res) != 1 || res[0].Code != 0 {
			why := "no answer"
			if len(res) == 1 {
				why = fmt.Sprintf("exit %d: %s", res[0].Code, oneline.Escape(strings.TrimSpace(res[0].Stderr)))
			}
			return nil, fmt.Errorf("budget %s: %s", verb, why)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(res[0].Stdout)), &got); err != nil {
			return nil, fmt.Errorf("budget %s answered %q: %w", verb, oneline.Escape(res[0].Stdout), err)
		}
		return got, nil
	}
	take := cfg.budgetTake
	if take == nil {
		take = func(ctx context.Context, h string) (bool, time.Duration, error) {
			got, err := send(ctx, "take", h)
			if err != nil {
				return false, 0, err
			}
			if open, _ := got["open"].(bool); open {
				// the server could not read the budget: granted open, and said so once a request
				why, _ := got["why"].(string)
				return false, 0, fmt.Errorf("the server granted open: %s", why)
			}
			granted, _ := got["granted"].(bool)
			ms, _ := got["wait_ms"].(float64)
			return granted, time.Duration(ms) * time.Millisecond, nil
		}
	}
	logf := func(line string) {
		if notes != nil {
			fmt.Fprintf(notes, "NATIVE NOTE: %s model=%s\n", oneline.Escape(line), oneline.Field(model))
		}
	}
	b := &swarm.ProxyBudget{Take: take, Holder: holder, Log: logf}
	if cfg.budgetSlots {
		// the provider has a concurrency budget: each request's slot is renewed while it
		// runs and freed when it ends; with none, no call is made for either
		b.Renew = func(ctx context.Context, h string) {
			// a renewal not written is a lease that runs out, never an error the request
			// could fail on: it is said, and the request goes on
			if _, err := send(ctx, "renew", h); err != nil {
				logf("RATE BUDGET RENEW " + h + " not written (" + err.Error() + "): the slot's lease runs out by itself")
			}
		}
		b.Release = func(h string) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			// a slot not freed expires by itself (its lease): said, never a failure
			if _, err := send(ctx, "release", h); err != nil {
				logf("RATE BUDGET RELEASE " + h + " not written (" + err.Error() + "): the slot's lease runs out by itself")
			}
		}
	}
	return b
}
