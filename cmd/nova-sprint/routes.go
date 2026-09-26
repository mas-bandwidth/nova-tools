package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/route"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

// nova-sprint routes prints the router's allowed_routes table (#2895) with
// the ranking numbers, or answers one card: may this route run this rung and
// work type. The table is internal/nsprint/route/routes.yaml, embedded.
//
//	nova-sprint routes
//	nova-sprint routes --rung flash [--type recut]
//	nova-sprint routes --check orgptnano --rung flash [--type read3 [--redis <host:port>]]
//	nova-sprint routes --preamble ordspro
//	nova-sprint routes --tier flash|pro
//	nova-sprint routes --tier flash|pro --ids <card label>
//	nova-sprint routes --spread
//
// --tier prints the allowed routes of the tier a card names with ROUTE:
// pro|flash, one "<route> <via>/<model>" line each in the table's efficiency
// order (best first); a held or dropped route is never printed. The bench
// harness (rowan-tools' nova-card-harness) runs the card on the
// first line's model.
//
// --tier with --ids prints the one route the swarm spread picks for that
// card: "<route> <launch>", where the provider is the spread row owning slot
// fnv32a(label) mod the sum of the tier's shares (Glenn 2026-09-24: all four
// providers, DeepSeek direct, OpenCode, OpenRouter and Mercury; OpenRouter
// is flash only and OpenCode owns two pro slots since #3949) and the route
// is that provider's first. The launch string is <via>/<model>, except
// Mercury's, whose model already names its provider (inception/mercury-2.5).
// The route may be held: the spread's providers are chosen, not ranked.
// --spread prints the whole spread table.
//
// --preamble prints the route's one-paragraph preamble (#2498 S8), built from
// the attribution's fault classes the table carries for it, for a card front
// to inline; a route that carries none is REFUSED (exit 1, nothing on stdout).
//
// With --check and --type, the answer is the fold's (#3178): the command reads
// routes:<type> (one HGETALL; `nova-sprint fold` is its only writer) from
// --redis, default $NOVA_SPRINT_REDIS, and checks the card with CheckFold, so
// a route the fold benched for the type is refused with the fold's reason. An
// absent key is no fold: the static table answers alone. No address, an
// unreachable store or a record ParseFold refuses exits 2 with nothing on
// stdout; it never falls back to the static answer (no evidence is not
// negative evidence). Every other form (the table, --rung [--type], and
// --check with no --type) is the static table and never opens the store.
//
// Exit 0 printed or allowed, 1 the card is REFUSED, 2 could not run.
func init() {
	register(Verb{
		Name:    "routes",
		Summary: "print allowed_routes per rung and work type with the ranking numbers; --tier flash|pro prints that tier's allowed routes and launch models, best first, and with --ids <card> the one spread route that card runs on; --spread prints the per-tier provider spread; --check <route> --rung <r> [--type <t>] answers one card (exit 1 REFUSED); --preamble <route> prints its preamble",
		Run:     cmdRoutes,
	})
}

func cmdRoutes(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := verbflag.New("routes")
	rung := fs.String("rung", "", "the ladder rung")
	typ := fs.String("type", "", "the work type")
	check := fs.String("check", "", "the route to check")
	preamble := fs.String("preamble", "", "the route whose preamble is printed")
	tier := fs.String("tier", "", "the model type: flash or pro")
	label := fs.String("ids", "", verbflag.HelpIDs)
	spread := fs.Bool("spread", false, "spread the tiers over the providers round-robin")
	addr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, "routes", err.Error()+"; it takes --rung, --type, --check <route>, --preamble <route>, --tier flash|pro [--ids <card>], --spread and --redis <host:port>")
	}
	if fs.NArg() > 0 {
		return refuse(stderr, "routes", "takes flags, not positional arguments")
	}
	if *tier != "" && (*check != "" || *typ != "" || *rung != "" || *preamble != "" || *spread) {
		return refuse(stderr, "routes", "--tier takes no other flag but --ids")
	}
	if *label != "" && *tier == "" {
		return refuse(stderr, "routes", "--ids needs --tier flash or --tier pro")
	}
	if *spread && (*check != "" || *typ != "" || *rung != "" || *preamble != "") {
		return refuse(stderr, "routes", "--spread takes no other flag")
	}
	if *preamble != "" && (*check != "" || *typ != "" || *rung != "") {
		return refuse(stderr, "routes", "--preamble takes no other flag")
	}
	if (*check != "" || *typ != "") && *rung == "" {
		return refuse(stderr, "routes", "--check and --type need --rung flash or --rung pro")
	}
	tab, err := route.Load()
	if err != nil {
		return refuse(stderr, "routes", err.Error())
	}
	switch {
	case *preamble != "":
		p, err := tab.Preamble(*preamble)
		if err != nil {
			fmt.Fprintf(stderr, "REFUSED %s\n", err)
			return 1
		}
		fmt.Fprintln(stdout, p)
		return 0
	case *check != "":
		var fold *route.Fold
		if *typ != "" {
			if *addr == "" {
				return refuse(stderr, "routes", "--check needs --redis <host:port> (or NOVA_SPRINT_REDIS)")
			}
			if fold, err = readFold(ctx, *addr, *typ); err != nil {
				return refuse(stderr, "routes", err.Error())
			}
		}
		if err := tab.CheckFold(route.Card{Rung: *rung, Type: *typ, Route: *check}, fold); err != nil {
			fmt.Fprintln(stdout, err.Error())
			return 1
		}
		fmt.Fprintf(stdout, "OK route=%s rung=%s type=%s\n", *check, *rung, dash(*typ))
		return 0
	case *spread:
		_, err = io.WriteString(stdout, tab.RenderSpread())
		if err != nil {
			return refuse(stderr, "routes", err.Error())
		}
		return 0
	case *tier != "" && *label != "":
		r, _, err := tab.Pick(*tier, *label)
		if err != nil {
			return refuse(stderr, "routes", err.Error())
		}
		fmt.Fprintf(stdout, "%s %s\n", r.Route, r.Launch())
		return 0
	case *tier != "":
		rows := tab.Tier(*tier)
		if len(rows) == 0 {
			return refuse(stderr, "routes", "tier "+*tier+" has no allowed routes; the tiers are flash and pro")
		}
		for _, r := range rows {
			fmt.Fprintf(stdout, "%s %s\n", r.Route, r.Launch())
		}
		return 0
	case *rung != "":
		allowed := tab.Allowed(*rung, *typ)
		if len(allowed) == 0 {
			return refuse(stderr, "routes", "rung "+*rung+" has no allowed routes; the rungs are the table's (run: nova-sprint routes)")
		}
		fmt.Fprintf(stdout, "ALLOWED %s %s %s\n", *rung, dash(*typ), strings.Join(allowed, " "))
		return 0
	}
	_, err = io.WriteString(stdout, tab.Render())
	if err != nil {
		return refuse(stderr, "routes", err.Error())
	}
	return 0
}

// readFold opens the store at addr, reads routes:<workType> and closes the
// store. (nil, nil) is an absent key; any other failure is an error.
func readFold(ctx context.Context, addr, workType string) (*route.Fold, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	st, err := store.Open(ctx, addr)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	return route.ReadFold(ctx, st.Client(), workType)
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
