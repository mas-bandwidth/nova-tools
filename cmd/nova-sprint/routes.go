package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/route"
)

// nova-sprint routes prints the router's allowed_routes table (#2895) with
// the ranking numbers, or answers one card: may this route run this rung and
// work type. The table is internal/nsprint/route/routes.yaml, embedded.
//
//	nova-sprint routes
//	nova-sprint routes --rung flash [--type recut]
//	nova-sprint routes --check orgptnano --rung flash [--type read3]
//
// Exit 0 printed or allowed, 1 the card is REFUSED, 2 could not run.
func init() {
	register(Verb{
		Name:    "routes",
		Summary: "print allowed_routes per rung and work type with the ranking numbers; --check <route> --rung <r> [--type <t>] answers one card (exit 1 REFUSED)",
		Run:     cmdRoutes,
	})
}

func cmdRoutes(_ context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("routes", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	rung := fs.String("rung", "", "")
	typ := fs.String("type", "", "")
	check := fs.String("check", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, "routes", err.Error()+"; it takes --rung, --type and --check <route>")
	}
	if fs.NArg() > 0 {
		return refuse(stderr, "routes", "takes flags, not positional arguments")
	}
	if (*check != "" || *typ != "") && *rung == "" {
		return refuse(stderr, "routes", "--check and --type need --rung flash or --rung pro")
	}
	tab, err := route.Load()
	if err != nil {
		return refuse(stderr, "routes", err.Error())
	}
	switch {
	case *check != "":
		if err := tab.Check(route.Card{Rung: *rung, Type: *typ, Route: *check}); err != nil {
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

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
