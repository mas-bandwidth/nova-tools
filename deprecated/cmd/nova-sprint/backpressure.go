package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
)

func init() {
	register(Verb{
		Name:    "backpressure",
		Summary: "check: refuse a second backpressure key beside s:<S>:backpressure, one round trip",
		Run:     runBackpressure,
	})
}

const backpressureUsage = "usage: nova-sprint backpressure check --sprint <name> [--redis <addr>]"

// runBackpressure is `nova-sprint backpressure check --sprint <S> [--redis
// <addr>]` (#3276). It reads the named backpressure keys in one pipeline and
// prints one receipt line with the round-trip count: exit 0 OK, 1 when a
// legacy key is present (the remedy names it), 2 usage or no store.
func runBackpressure(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 || args[0] != "check" {
		return refuse(errOut, "backpressure", "want the subverb check; "+backpressureUsage)
	}
	fs := taskFlags("backpressure check")
	redisAddr := fs.String("redis", redisDefault(), "")
	sprint := fs.String("sprint", "", "")
	if err := fs.Parse(args[1:]); err != nil {
		return refuse(errOut, "backpressure check", err.Error()+"; "+backpressureUsage)
	}
	if fs.NArg() > 0 || *sprint == "" {
		return refuse(errOut, "backpressure check", "needs --sprint <name> and no positional arguments; "+backpressureUsage)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	st, err := openTaskStore(ctx, taskAddr(*redisAddr))
	if err != nil {
		return refuse(errOut, "backpressure check", err.Error())
	}
	defer st.Close()
	kc, err := deal.CheckOneBackpressureKey(ctx, st, *sprint)
	if errors.Is(err, deal.ErrTwoBackpressureKeys) {
		fmt.Fprintf(out, "%s remedy=delete the legacy key after checking no writer still sets it\n", kc.Line())
		return 1
	}
	if err != nil {
		return refuse(errOut, "backpressure check", err.Error())
	}
	fmt.Fprintln(out, kc.Line())
	return 0
}
