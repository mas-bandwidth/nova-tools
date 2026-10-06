// runRoutePrices is route prices --refresh: each enabled route's price fields set
// from its provider's published list (config.PlanPriceRefresh), dated today, with
// the list's URL as their source (docs/SPEC-CONFIG.md, "route prices").

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// listTimeout bounds the read of a provider's list: a daily loop that hangs on
// it would never say so.
const listTimeout = 60 * time.Second

// maxListBytes bounds the list read: OpenRouter's is a few megabytes.
const maxListBytes = 64 << 20

func runRoutePrices(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	const verb = "route prices"
	fs := verbflag.New(verb)
	c := writeStoreFlags(fs)
	as := actorFlag(fs)
	refresh := fs.Bool("refresh", false, "required: read the provider's published list and set each enabled route's price fields from it, price_as_of today and price_source the list's URL; a price that moved past 2x is a JUDGMENT line, left as it is, and the verb exits 1")
	provider := fs.String("provider", "", "the `provider` word whose routes are refreshed: one of "+config.PriceListProviders()+"; empty (the default) is every provider with a list")
	from := fs.String("from", "", "read the list from a `path` saved from its URL instead of fetching it; the rows still name the URL as their source")
	dry := fs.Bool("dry-run", false, "print each change (PRICES DRY-RUN) and the judgments, and write nothing; it still reads the list and the store")
	asJSON := jsonFlag(fs)
	if code, ok := parse(fs, args, stderr, verb); !ok {
		return code
	}
	var problems []string
	if fs.NArg() > 0 {
		problems = append(problems, "route prices takes no name: it refreshes every enabled route of a provider with a list")
	}
	if !*refresh {
		problems = append(problems, "want route prices --refresh [--provider <provider>] [--dry-run]")
	}
	list := config.PriceLists[0]
	if *provider != "" {
		l, ok := config.PriceListOf(*provider)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s publishes no list this verb reads; the lists: %s", *provider, config.PriceListProviders()))
		}
		list = l
	}
	seatVal := ""
	if c.seat != nil {
		seatVal = *c.seat
	}
	actor, err := actorName(*as, d.getenv, seatVal)
	if err != nil && !*dry {
		problems = append(problems, err.Error())
	}
	dsn, err := c.dsn(d.getenv)
	if err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return refuse(stderr, verb, strings.Join(problems, "; "))
	}
	body, err := readList(ctx, list.URL, *from)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	prices, err := config.ParseOpenRouterList(body)
	if err != nil {
		return refuse(stderr, verb, fmt.Sprintf("%s: %v", list.URL, err))
	}
	st, err := d.openStore(ctx, dsn)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	defer func() { _ = st.Close() }() // ignored: every reply the verb needs is already read, so a close error changes nothing
	if code, stale := behindSchema(ctx, st, stderr, verb, c); stale {
		return code
	}
	rows, err := st.List(ctx, config.KindRoute)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	today := d.now().UTC().Format(time.DateOnly)
	plan := config.PlanPriceRefresh(rows, prices, *provider, list.URL, today)

	o := tool.Done().Fact("source", list.URL).Fact("as_of", today).Fact("dry_run", *dry)
	o.Verb = verb
	var lines []string
	set, same, missing, judgments := 0, 0, 0, 0
	for _, pr := range plan {
		switch {
		case pr.Missing:
			missing++
			lines = append(lines, fmt.Sprintf("PRICES MISSING route=%s model=%s: not on the list; nothing set", config.Value(pr.Route), config.Value(pr.Model)))
			o.Item("route", "name", pr.Route, "model", pr.Model, "missing", true)
		case pr.Changes == nil:
			same++
			lines = append(lines, fmt.Sprintf("PRICES SAME route=%s list=%s", config.Value(pr.Route), config.Value(pr.ListID)))
			o.Item("route", "name", pr.Route, "list", pr.ListID, "changes", map[string]string{})
		default:
			set++
			before := map[string]string{}
			for _, r := range rows {
				if r.Name == pr.Route {
					before = r.Fields
				}
			}
			changed := movedWords(pr, before)
			if *dry {
				lines = append(lines, fmt.Sprintf("PRICES DRY-RUN route=%s list=%s changed=%s", config.Value(pr.Route), config.Value(pr.ListID), changed))
				o.Item("route", "name", pr.Route, "list", pr.ListID, "changes", pr.Changes)
				break
			}
			_, id, err := st.Update(ctx, config.KindRoute, pr.Route, pr.Changes, actor)
			if err != nil {
				return storeErr(stderr, verb, err, toolName+" route show "+pr.Route+c.again())
			}
			lines = append(lines, fmt.Sprintf("PRICES SET route=%s list=%s changed=%s rev=%d", config.Value(pr.Route), config.Value(pr.ListID), changed, id))
			o.Item("route", "name", pr.Route, "list", pr.ListID, "changes", pr.Changes, "rev", id)
		}
		for _, j := range pr.Judgments {
			judgments++
			// judgment-shaped: what moved, why it was not set, and the one command that decides it
			lines = append(lines, fmt.Sprintf("JUDGMENT route=%s %s moved past %dx: have %s, the list says %s; a price that moves that far is never set silently; decide: %s route set %s --%s %s --%s %s --%s %s",
				config.Value(pr.Route), j.Field, config.PriceJumpFactor, orDash(j.Have), j.List, toolName, pr.Route, j.Field, j.List, cardcost.FieldSource, shq(list.URL), cardcost.FieldAsOf, today))
			o.Item("judgment", "route", pr.Route, "field", j.Field, "have", j.Have, "list", j.List)
		}
		if pr.Assumed != "" {
			note := fmt.Sprintf("route=%s provider=%s: priced from %s's list, assumed until %s publishes its own", config.Value(pr.Route), pr.Provider, pr.Assumed, pr.Provider)
			lines = append(lines, "NOTE "+note)
			o.Notes = append(o.Notes, note)
		}
	}
	code := 0
	if judgments > 0 {
		code = 1
	}
	if *asJSON {
		o.Fact("routes", len(plan)).Fact("set", set).Fact("same", same).Fact("missing", missing).Fact("judgments", judgments)
		o.Exit = code
		emit(stdout, o)
		return code
	}
	for _, l := range lines {
		fmt.Fprintln(stdout, l)
	}
	fmt.Fprintf(stdout, "CONFIG PRICES source=%s as_of=%s routes=%d set=%d same=%d missing=%d judgments=%d\n", list.URL, today, len(plan), set, same, missing, judgments)
	return code
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
