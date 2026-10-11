// route_prices.go holds `nova-config route prices --refresh`: each enabled
// route's price fields set from its provider's published list (config's
// PlanPriceRefresh), dated today, with the list's URL as their source
// (docs/SPEC-CONFIG.md, "route prices"). The verb is declared on pkg/tool,
// as login and logout are, so the tool package adds no verbflag.New site and the
// no-hand-printing ledger for cmd/nova-config stays where it is.

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// listTimeout bounds the read of a provider's list: a daily loop that hangs on
// it would never say so.
const listTimeout = 60 * time.Second

// maxListBytes bounds the list read: OpenRouter's is a few megabytes.
const maxListBytes = 64 << 20

// runRoutePricesTool dispatches route prices through pkg/tool with the two
// words runKind stripped, so the verb's name is "route prices" on every line and
// in its help.
func runRoutePricesTool(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	return routePricesTool(d).RunContext(ctx, append([]string{"route", "prices"}, args...), os.Stdin, stdout, stderr)
}

// routePricesTool is the verb's tool: one verb on the skeleton, as login's.
func routePricesTool(d deps) *tool.Tool {
	return &tool.Tool{
		Name:      toolName,
		What:      "a fleet's machines and AI friends as rows in PostgreSQL, applied into Redis",
		ExitTable: "0 done, 1 a price moved past 2x and was left as it is, 2 could not run (usage, or a list or store that did not answer)",
		How: "each kind is a table in PostgreSQL schema config, applied into Redis.\n" +
			"route prices reads its provider's published list and sets the enabled routes' price fields.\n" +
			"A price, the date it was read and the list's URL live in the route row.",
		Verbs: []tool.Verb{routePricesVerb(d)},
	}
}

// routePricesVerb declares the verb's flags through tool.Flags and prints its own
// lines (Flags.Prints), so the skeleton owns the parsing, the refusals and the
// exit while the PRICES/JUDGMENT/STALE grammar stays one shape.
func routePricesVerb(d deps) tool.Verb {
	return tool.Verb{
		Name:      "route prices",
		Usage:     "route prices --refresh [--provider <provider>] [--from <path>] [--pg <dsn> | --file <path> | --seat <seat>] [--dry-run] --actor <name>",
		Effect:    tool.LocalWrite + ": writes the enabled routes' price fields in the config store",
		Detail:    "reads the provider's published list and sets each enabled route's price fields, with price_as_of today and price_source the list's URL; a price that moved past 2x is a JUDGMENT line, left as it is, and the verb exits 1; a row more than 10 percent off the list is named STALE.",
		ExitTable: "0 done, 1 a price moved past 2x and was left as it is, 2 could not run (usage, or a list or store that did not answer)",
		DryRun:    true,
		Flags:     routePricesFlags,
		Run:       func(c *tool.Call) *tool.Out { return runRoutePrices(c, d) },
	}
}

// routePricesFlags declares the verb's flags. The store flags are the tool's own
// (seatStoreFlags, actorFlag); --dry-run is the skeleton's (Verb.DryRun).
func routePricesFlags(f *tool.Flags) {
	f.Prints()
	seatStoreFlags(f.FlagSet)
	actorFlag(f.FlagSet)
	f.Bool("refresh", false, "read the provider's published list and set each enabled route's price fields from it, price_as_of today and price_source the list's URL; a price that moved past 2x is a JUDGMENT line, left as it is, and the verb exits 1")
	f.String("provider", "", "the `provider` word whose routes are refreshed: one of "+config.PriceListProviders()+"; empty (the default) is every provider with a list")
	f.String("from", "", "read the list from a `path` saved from its URL instead of fetching it; the rows still name the URL as their source")
	f.Check(func(c *tool.Call) {
		if !c.Bool("refresh") {
			c.Problem("want route prices --refresh [--provider <provider>] [--from <path>] [--dry-run] --actor <name>")
		}
		if p := c.Str("provider"); p != "" {
			if _, ok := config.PriceListOf(p); !ok {
				c.Problem(fmt.Sprintf("%s publishes no list this verb reads; the lists: %s", p, config.PriceListProviders()))
			}
		}
	})
}

// runRoutePrices reads the list, plans the refresh of the enabled routes that
// name it, and writes each plan through the config store. A price that moved past
// 2x is left as it is and named, and the run exits 1; a row more than 10 percent
// off the list is named stale. --dry-run prints the plan and writes nothing.
func runRoutePrices(c *tool.Call, d deps) *tool.Out {
	const verb = "route prices"
	pg, file, seat := c.Str("pg"), c.Str("file"), c.Str("seat")
	cn := conn{pg: &pg, file: &file, seat: &seat}
	provider, from := c.Str("provider"), c.Str("from")
	dry := c.DryRun()
	actor, err := actorName(c.Str("actor"), d.getenv, seat)
	if err != nil && !dry {
		c.Problem(err.Error())
		return nil
	}
	dsn, err := cn.dsn(d.getenv)
	if err != nil {
		c.Problem(err.Error())
		return nil
	}
	list := config.PriceLists[0]
	if provider != "" {
		if l, ok := config.PriceListOf(provider); ok {
			list = l
		}
	}
	body, err := readList(c.Ctx, list.URL, from)
	if err != nil {
		c.Problem(err.Error())
		return nil
	}
	prices, err := config.ParseOpenRouterList(body)
	if err != nil {
		c.Problem(fmt.Sprintf("%s: %v", list.URL, err))
		return nil
	}
	st, err := d.openStore(c.Ctx, dsn)
	if err != nil {
		c.Problem(err.Error())
		return nil
	}
	defer func() { _ = st.Close() }() // ignored: every reply the verb needs is already read, so a close error changes nothing
	if code, stale := behindSchema(c.Ctx, st, c.Stderr, verb, cn); stale {
		return tool.Exit(code)
	}
	rows, err := st.List(c.Ctx, config.KindRoute)
	if err != nil {
		c.Problem(err.Error())
		return nil
	}
	today := d.now().UTC().Format(time.DateOnly)
	plan := config.PlanPriceRefresh(rows, prices, provider, list.URL, today)

	set, same, missing, judgments, stale := 0, 0, 0, 0, 0
	for _, pr := range plan {
		before := map[string]string{}
		for _, r := range rows {
			if r.Name == pr.Route {
				before = r.Fields
			}
		}
		switch {
		case pr.Missing:
			missing++
			fmt.Fprintf(c.Stdout, "PRICES MISSING route=%s model=%s: not on the list; nothing set\n", config.Value(pr.Route), config.Value(pr.Model))
		case pr.Changes == nil:
			same++
			fmt.Fprintf(c.Stdout, "PRICES SAME route=%s list=%s\n", config.Value(pr.Route), config.Value(pr.ListID))
		case dry:
			set++
			fmt.Fprintf(c.Stdout, "PRICES DRY-RUN route=%s list=%s changed=%s\n", config.Value(pr.Route), config.Value(pr.ListID), movedWords(pr, before))
		default:
			set++
			_, id, err := st.Update(c.Ctx, config.KindRoute, pr.Route, pr.Changes, actor)
			if err != nil {
				c.Problem(err.Error() + "; want: " + toolName + " route show " + pr.Route + cn.again())
				return nil
			}
			fmt.Fprintf(c.Stdout, "PRICES SET route=%s list=%s changed=%s rev=%d\n", config.Value(pr.Route), config.Value(pr.ListID), movedWords(pr, before), id)
		}
		for _, j := range pr.Judgments {
			judgments++
			// judgment-shaped: what moved, why it was not set, and the one command that decides it
			fmt.Fprintf(c.Stdout, "JUDGMENT route=%s %s moved past %dx: have %s, the list says %s; a price that moves that far is never set silently; decide: %s route set %s --%s %s --%s %s --%s %s\n",
				config.Value(pr.Route), j.Field, config.PriceJumpFactor, orDash(j.Have), j.List, toolName, pr.Route, j.Field, j.List, cardcost.FieldSource, shq(list.URL), cardcost.FieldAsOf, today)
		}
		if len(pr.Stale) > 0 {
			stale++
			var parts []string
			for _, s := range pr.Stale {
				parts = append(parts, fmt.Sprintf("%s: have %s, the list says %s", s.Field, s.Have, s.List))
			}
			fmt.Fprintf(c.Stdout, "STALE route=%s %s\n", config.Value(pr.Route), strings.Join(parts, "; "))
		}
		if pr.Assumed != "" {
			fmt.Fprintf(c.Stdout, "NOTE route=%s provider=%s: priced from %s's list, assumed until %s publishes its own\n", config.Value(pr.Route), pr.Provider, pr.Assumed, pr.Provider)
		}
	}
	code := 0
	if judgments > 0 {
		code = 1
	}
	fmt.Fprintf(c.Stdout, "CONFIG PRICES source=%s as_of=%s routes=%d set=%d same=%d missing=%d judgments=%d stale=%d\n",
		list.URL, today, len(plan), set, same, missing, judgments, stale)
	if c.Given("as") {
		fmt.Fprintln(c.Stdout, "NOTE --as is --actor")
	}
	return tool.Exit(code)
}

// movedWords is a refresh's price changes on its line, field:old->new in the
// list's order, a price not set before as -; "-" when only the date and source
// change.
func movedWords(pr config.PriceRefresh, before map[string]string) string {
	var parts []string
	for _, f := range pr.Moved {
		have := before[f]
		if c, err := cardcost.Canonical(have); err == nil && have != "" {
			have = c
		}
		parts = append(parts, f+":"+orDash(have)+"->"+pr.Changes[f])
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}

// orDash is a value on a line, "-" when empty.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// readList is the provider's published list: the file --from names, else a GET
// of its URL, which needs no key.
func readList(ctx context.Context, url, from string) ([]byte, error) {
	if from != "" {
		b, err := os.ReadFile(from)
		if err != nil {
			return nil, fmt.Errorf("--from: %v", err)
		}
		return b, nil
	}
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reading the list: %v", err)
	}
	defer func() { _ = resp.Body.Close() }() // ignored: the body is read in full or the read's error is the answer
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("reading the list: %s answered %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxListBytes))
	if err != nil {
		return nil, fmt.Errorf("reading the list: %v", err)
	}
	return b, nil
}
