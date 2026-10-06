package main

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// runRoutePrices implements `nova-config route prices --refresh [--provider <name>] [--dry-run]`.
func runRoutePrices(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	return routePricesTool(d).RunContext(ctx, append([]string{"route", "prices"}, args...), os.Stdin, stdout, stderr)
}

func routePricesTool(d deps) *tool.Tool {
	return &tool.Tool{
		Name:      toolName,
		What:      "a fleet's machines and AI friends as rows in PostgreSQL, applied into Redis",
		ExitTable: "0 done, 1 refused (the verb ran and the store said no), 2 could not run (usage, or a store that did not answer)",
		Verbs: []tool.Verb{
			routePricesVerb(d),
		},
	}
}

func routePricesVerb(d deps) tool.Verb {
	return tool.Verb{
		Name:   "route prices",
		Usage:  "route prices --refresh [--provider openrouter] [--dry-run] [--as <friend>] [--file <file> | --pg <dsn>]",
		Detail: "reads the provider's published models list and sets price fields for each route",
		Effect: tool.Effect("shared write: reads the provider's published API and updates route prices in PostgreSQL and Redis"),
		Flags: func(f *tool.Flags) {
			f.Prints()
			f.Bool("refresh", false, "refresh route prices from the provider's published models list")
			f.String("provider", "", "provider to refresh: openrouter, opencode; default all supported providers")
			f.Bool("dry-run", false, "print the changes that would be recorded and write nothing")
			f.Bool("json", false, "print one JSON object on stdout")
			f.String("as", "", "the actor a write is recorded under")
			f.String("file", "", "a local JSON file standing in for PostgreSQL, at path: the same rows, refusals and history, to try the tool with no database; never the fleet's store")
			f.String("pg", "", "the PostgreSQL dsn, postgres://user@host:port/db with no password")
			f.String("seat", "", "the seat profile in seats.tsv supplying the PostgreSQL DSN and password variable name")
		},
		Run: func(c *tool.Call) *tool.Out {
			return runRoutePricesCall(c, d)
		},
	}
}

func runRoutePricesCall(c *tool.Call, d deps) *tool.Out {
	const verb = "route prices"
	refresh := c.Bool("refresh")
	providerFlag := c.Str("provider")
	dry := c.DryRun() || c.Bool("dry-run")
	asJSON := c.Bool("json")
	as := c.Str("as")
	file := c.Str("file")
	pg := c.Str("pg")
	seat := c.Str("seat")

	if !refresh {
		refuse(c.Stderr, verb, "want --refresh [--provider <name>] [--dry-run]")
		return tool.Exit(2)
	}
	if providerFlag != "" && providerFlag != "openrouter" && providerFlag != "opencode" {
		refuse(c.Stderr, verb, fmt.Sprintf("--provider %s: want openrouter or opencode", providerFlag))
		return tool.Exit(2)
	}

	connVal := conn{
		file: &file,
		pg:   &pg,
		seat: &seat,
	}

	var actor string
	if !dry {
		var err error
		actor, err = actorName(as, d.getenv, seat)
		if err != nil {
			refuse(c.Stderr, verb, err.Error())
			return tool.Exit(2)
		}
	} else if as != "" {
		actor = as
	} else if v := d.getenv(envActor); v != "" {
		actor = v
	} else if seat != "" {
		actor = seat
	} else if getenv := d.getenv; getenv != nil && getenv(seatcred.SeatEnv) != "" {
		actor = getenv(seatcred.SeatEnv)
	}

	dsn, err := connVal.dsn(d.getenv)
	if err != nil {
		refuse(c.Stderr, verb, err.Error())
		return tool.Exit(2)
	}
	st, err := d.openStore(c.Ctx, dsn)
	if err != nil {
		refuse(c.Stderr, verb, err.Error())
		return tool.Exit(2)
	}
	defer func() { _ = st.Close() }() // ignored: every reply the verb needs is already read, so a close error changes nothing

	k, _ := config.Lookup(config.KindRoute)
	if laterKind(k) {
		if code, stale := behindSchema(c.Ctx, st, c.Stderr, verb, connVal); stale {
			return tool.Exit(code)
		}
	}

	catalog, err := config.FetchOpenRouterPrices(c.Ctx, d.httpTransport)
	if err != nil {
		refuse(c.Stderr, verb, fmt.Sprintf("the provider price list could not be read: %v", err))
		return tool.Exit(2)
	}

	routes, err := st.List(c.Ctx, config.KindRoute)
	if err != nil {
		refuse(c.Stderr, verb, err.Error())
		return tool.Exit(2)
	}

	today := d.now().UTC().Format(time.DateOnly)

	type plannedUpdate struct {
		route   config.Row
		changes map[string]string
		prices  config.RoutePrices
	}

	var planned []plannedUpdate
	for _, r := range routes {
		if r.Fields["enabled"] == "false" {
			continue
		}
		p := r.Fields["provider"]
		if providerFlag != "" && p != providerFlag {
			continue
		}
		if p != "openrouter" && p != "opencode" {
			continue
		}
		prices, matched := config.MatchPrices(catalog, p, r.Fields["model"])
		if !matched {
			continue
		}
		// Refusing silently-changed prices over 2x with a judgment-shaped line
		if field, oldVal, newVal, over := config.CheckOver2x(r.Fields, prices); over {
			fmt.Fprintf(c.Stderr, "JUDGMENT route-prices route=%s price changed over 2x: %s %s -> %s\n", r.Name, field, oldVal, newVal)
			refused(c.Stderr, verb, fmt.Sprintf("route %s price changed over 2x: %s %s -> %s", r.Name, field, oldVal, newVal), fmt.Sprintf("%s route set %s --%s %s%s", toolName, r.Name, field, newVal, connVal.again()))
			return tool.Exit(1)
		}

		changes := map[string]string{
			cardcost.FieldInput:  prices.Input,
			cardcost.FieldOutput: prices.Output,
			cardcost.FieldSource: prices.Source,
			cardcost.FieldAsOf:   today,
		}
		if prices.CacheRead != "" {
			changes[cardcost.FieldCacheRead] = prices.CacheRead
		}
		if p == "opencode" {
			if r.Fields["note"] == "" || !strings.Contains(r.Fields["note"], "assumed from OpenRouter") {
				changes["note"] = "assumed from OpenRouter"
			}
		}
		planned = append(planned, plannedUpdate{route: r, changes: changes, prices: prices})
	}

	for _, u := range planned {
		changed := slices.Sorted(maps.Keys(u.changes))
		if dry {
			if asJSON {
				continue
			}
			fmt.Fprintf(c.Stdout, "CONFIG DRY-RUN kind=route name=%s op=set changed=%s\n", config.Value(u.route.Name), config.Value(strings.Join(changed, ",")))
			if u.route.Fields["provider"] == "opencode" {
				fmt.Fprintf(c.Stdout, "route %s: assumed from OpenRouter until OpenCode publishes a list\n", u.route.Name)
			}
			continue
		}
		_, id, err := st.Update(c.Ctx, config.KindRoute, u.route.Name, u.changes, actor)
		if err != nil {
			storeErr(c.Stderr, verb, err, "nova-config route show "+u.route.Name)
			return tool.Exit(1)
		}
		if asJSON {
			continue
		}
		fmt.Fprintf(c.Stdout, "CONFIG SET kind=route name=%s rev=%d changed=%s\n", config.Value(u.route.Name), id, config.Value(strings.Join(changed, ",")))
		if u.route.Fields["provider"] == "opencode" {
			fmt.Fprintf(c.Stdout, "route %s: assumed from OpenRouter until OpenCode publishes a list\n", u.route.Name)
		}
	}

	if asJSON {
		o := tool.Done().Fact("dry_run", dry).Fact("updated", len(planned))
		o.Verb = verb
		return tool.Exit(emit(c.Stdout, o))
	}

	return tool.Exit(0)
}
