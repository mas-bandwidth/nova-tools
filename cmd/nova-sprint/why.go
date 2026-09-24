// The why and land status verbs answer "why is this unit not landed" and
// "what is the lander doing" from the unit records in Redis alone
// (nova-tools#3139 rev 7, 7.7, build B14). They read, never write, and never
// call GitHub (no client is built). The keys they read are the unit contract
// in internal/nsprint/land (doc.go, 2.2); the PR form resolves through
// s:<S>:prunit:<repo>:<n>, never the retired s:<S>:pr:<repo>:<n>.
//
//	nova-sprint why <unit>|<repo>#<n> --redis <addr> --sprint <S> [--now <unix>]
//	nova-sprint land status [<unit>] --redis <addr> --sprint <S> [--now <unix>]
//
// Exit (7.7): 0 printed, 1 no record for that unit or PR, 2 refused or
// usage (a REFUSED <reason> remedy=<cmd> line), 6 no Redis.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func init() {
	register(Verb{
		Name:    "why",
		Summary: "why <unit>|<repo>#<n> --redis <addr> --sprint <S>: one line per landable condition from the unit records (exit 1 no record)",
		Run:     runWhy,
	})
	register(Verb{
		Name:    "land",
		Summary: "land status|flaky ...: lander status and flaky observation store",
		Run:     runLand,
	})
}

// readFlags parses --redis, --sprint and --now around at most one
// positional argument, which may come before or after the flags.
func readFlags(name string, args []string) (redisAddr, sprint string, now time.Time, pos []string, err error) {
	fs := taskFlags(name)
	r := fs.String("redis", "", "")
	s := fs.String("sprint", "", "")
	n := fs.Int64("now", 0, "")
	for len(args) > 0 {
		if !strings.HasPrefix(args[0], "-") {
			pos = append(pos, args[0])
			args = args[1:]
			continue
		}
		if err = fs.Parse(args); err != nil {
			return
		}
		args = fs.Args()
	}
	if *r == "" || *s == "" {
		err = errors.New("needs --redis <addr> and --sprint <S>")
		return
	}
	now = time.Now()
	if *n > 0 {
		now = time.Unix(*n, 0)
	}
	return *r, *s, now, pos, nil
}

// refuse77 is a 7.7 refusal: exit 2 with its named REFUSED line.
func refuse77(errOut io.Writer, verb, reason, remedy string) int {
	return refuse(errOut, verb, "REFUSED "+reason+" remedy="+remedy)
}

// whyTarget is the one positional argument of why and land status <unit>:
// <repo>#<n> is the PR form, anything with a slash is a unit id (2.1).
func whyTarget(verb, arg string) (unit string, id land.ID, isPR bool, err error) {
	if strings.Contains(arg, "#") {
		id, err = land.ParseID(arg)
		return "", id, true, err
	}
	if !strings.Contains(arg, "/") || strings.ContainsAny(arg, " \t\n") {
		return "", id, false, fmt.Errorf("%s takes a unit id (gh/<owner>/<repo>/<n>) or <repo>#<n>, not %s", verb, strconv.Quote(arg))
	}
	return arg, id, false, nil
}

// loadTarget loads the unit named by whyTarget's result.
func loadTarget(ctx context.Context, st *store.Store, sprint, unit string, id land.ID, isPR bool) (*land.Unit, error) {
	if isPR {
		return land.LoadPR(ctx, st.Client(), sprint, id)
	}
	return land.LoadUnit(ctx, st.Client(), sprint, unit)
}

func runWhy(ctx context.Context, args []string, out, errOut io.Writer) int {
	const usage = "why <unit>|<repo>#<n> --redis <addr> --sprint <S>"
	addr, sprint, now, pos, err := readFlags("why", args)
	if err != nil {
		return refuse77(errOut, "why", "usage: "+err.Error(), usage)
	}
	if len(pos) != 1 {
		return refuse77(errOut, "why", "usage: takes exactly one unit or <repo>#<n>", usage)
	}
	unit, id, isPR, err := whyTarget("why", pos[0])
	if err != nil {
		return refuse77(errOut, "why", "usage: "+err.Error(), usage)
	}
	st, err := store.Open(ctx, addr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint why: %v\n", err)
		return 6
	}
	defer st.Close()
	u, err := loadTarget(ctx, st, sprint, unit, id, isPR)
	if errors.Is(err, land.ErrNoRecord) {
		fmt.Fprintf(out, "why %s: %v\n", pos[0], err)
		return 1
	}
	if err != nil {
		return refuse77(errOut, "why", "read: "+err.Error(), usage)
	}
	fmt.Fprintln(out, unitHeader(u))
	for _, line := range land.Why(u, now) {
		fmt.Fprintln(out, line)
	}
	return 0
}

// unitHeader names the unit and, when its card names one, the PR.
func unitHeader(u *land.Unit) string {
	h := "unit " + u.Unit
	if u.ID.N > 0 {
		h += " " + u.ID.String()
	}
	return h
}

func runLand(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "land", "want status or flaky or writer (sprint land is the lander's verb, #2942)")
	}
	if args[0] == "writer" {
		return runLandWriter(ctx, args[1:], out, errOut)
	}
	if args[0] == "worker" {
		return runLandWorker(ctx, args[1:], out, errOut)
	}
	if args[0] == "flaky" {
		return runLandFlaky(ctx, args[1:], out, errOut)
	}
	if args[0] != "status" {
		return refuse(errOut, "land", "want status or flaky or writer (sprint land is the lander's verb, #2942)")
	}
	const usage = "land status [<unit>] --redis <addr> --sprint <S>"
	addr, sprint, now, pos, err := readFlags("land status", args[1:])
	if err != nil {
		return refuse77(errOut, "land status", "usage: "+err.Error(), usage)
	}
	if len(pos) > 1 {
		return refuse77(errOut, "land status", "usage: takes at most one unit, not "+strconv.Quote(strings.Join(pos, " ")), usage)
	}
	var unit string
	var id land.ID
	var isPR bool
	if len(pos) == 1 {
		if unit, id, isPR, err = whyTarget("land status", pos[0]); err != nil {
			return refuse77(errOut, "land status", "usage: "+err.Error(), usage)
		}
	}
	st, err := store.Open(ctx, addr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint land status: %v\n", err)
		return 6
	}
	defer st.Close()
	if len(pos) == 1 {
		u, err := loadTarget(ctx, st, sprint, unit, id, isPR)
		if errors.Is(err, land.ErrNoRecord) {
			fmt.Fprintf(out, "land status %s: %v\n", pos[0], err)
			return 1
		}
		if err != nil {
			return refuse77(errOut, "land status", "read: "+err.Error(), usage)
		}
		fmt.Fprintln(out, unitHeader(u))
		for _, line := range land.Why(u, now) {
			fmt.Fprintln(out, line)
		}
		return 0
	}
	snap, err := land.LoadStatus(ctx, st.Client(), sprint)
	if err != nil {
		return refuse77(errOut, "land status", "read: "+err.Error(), usage)
	}
	for _, line := range land.Status(snap, now) {
		fmt.Fprintln(out, line)
	}
	return 0
}
