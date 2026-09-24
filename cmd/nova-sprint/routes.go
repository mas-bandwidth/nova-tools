package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/route"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// nova-sprint routes prints the router's allowed_routes table (#2895) with
// the ranking numbers, or answers one card: may this route run this rung and
// work type. The table is internal/nsprint/route/routes.yaml, embedded.
//
//	nova-sprint routes
//	nova-sprint routes --rung flash [--type recut]
//	nova-sprint routes --check orgptnano --rung flash [--type read3 [--redis <host:port>]]
//	nova-sprint routes --preamble ordspro
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
		Summary: "print allowed_routes per rung and work type with the ranking numbers; --check <route> --rung <r> [--type <t>] answers one card (exit 1 REFUSED); --preamble <route> prints its preamble",
		Run:     cmdRoutes,
	})
}

func cmdRoutes(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("routes", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	rung := fs.String("rung", "", "")
	typ := fs.String("type", "", "")
	check := fs.String("check", "", "")
	preamble := fs.String("preamble", "", "")
	addr := fs.String("redis", os.Getenv("NOVA_SPRINT_REDIS"), "")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, "routes", err.Error()+"; it takes --rung, --type, --check <route>, --preamble <route> and --redis <host:port>")
	}
	if fs.NArg() > 0 {
		return refuse(stderr, "routes", "takes flags, not positional arguments")
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
