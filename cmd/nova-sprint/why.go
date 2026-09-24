// The why and land verbs answer "why is this approved PR not landed" and
// "what is the lander doing" from the PR record in Redis alone (#2756 v6
// 4.9; nova-tools #3106). They read, never write, and never call GitHub.
// The record fields they read are the contract in internal/nsprint/land.
//
//	nova-sprint why <repo>#<n> --redis <addr> --sprint <S> [--now <unix>]
//	nova-sprint land status --redis <addr> --sprint <S> [--now <unix>]
//
// Exit 0 printed, 1 no record for that PR, 2 could not run, 6 no Redis.
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
		Summary: "why <repo>#<n> --redis <addr> --sprint <S>: one line per landing gate from the PR record (exit 1 no record)",
		Run:     runWhy,
	})
	register(Verb{
		Name:    "land",
		Summary: "land status --redis <addr> --sprint <S>: lanes in flight, landable, dropped by reason, held by holder",
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

func runWhy(ctx context.Context, args []string, out, errOut io.Writer) int {
	addr, sprint, now, pos, err := readFlags("why", args)
	if err != nil {
		return refuse(errOut, "why", err.Error()+"; usage: why <repo>#<n> --redis <addr> --sprint <S>")
	}
	if len(pos) != 1 {
		return refuse(errOut, "why", "takes exactly one PR as <repo>#<n>")
	}
	id, err := land.ParseID(pos[0])
	if err != nil {
		return refuse(errOut, "why", err.Error())
	}
	st, err := store.Open(ctx, addr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint why: %v\n", err)
		return 6
	}
	defer st.Close()
	p, err := land.LoadPR(ctx, st.Client(), sprint, id)
	if errors.Is(err, land.ErrNoRecord) {
		fmt.Fprintf(out, "why %s: %v\n", id, err)
		return 1
	}
	if err != nil {
		return refuse(errOut, "why", err.Error())
	}
	for _, line := range land.Why(p, now) {
		fmt.Fprintln(out, line)
	}
	return 0
}

func runLand(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) > 0 && args[0] == "worker" {
		return runLandWorker(ctx, args[1:], out, errOut)
	}
	if len(args) == 0 || args[0] != "status" {
		return refuse(errOut, "land", "want status (sprint land is the lander's verb, #2942)")
	}
	addr, sprint, now, pos, err := readFlags("land status", args[1:])
	if err != nil {
		return refuse(errOut, "land status", err.Error())
	}
	if len(pos) > 0 {
		return refuse(errOut, "land status", "takes flags, not positional arguments: "+strconv.Quote(pos[0]))
	}
	st, err := store.Open(ctx, addr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint land status: %v\n", err)
		return 6
	}
	defer st.Close()
	snap, err := land.LoadStatus(ctx, st.Client(), sprint)
	if err != nil {
		return refuse(errOut, "land status", err.Error())
	}
	for _, line := range land.Status(snap, now) {
		fmt.Fprintln(out, line)
	}
	return 0
}
