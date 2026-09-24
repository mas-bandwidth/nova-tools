package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/benchreset"
)

func runBenchReset(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs, addr := lifeFlags("bench reset")
	bench := fs.String("bench", "", "bench name")
	keep := fs.Bool("keep-queue", false, "leave dealt cards dealt")
	grace := fs.Duration("grace", benchreset.DefaultGrace, "TERM grace before KILL")
	actor := fs.String("actor", os.Getenv(seatEnv), "operator seat")
	idem := fs.String("idem", "", "idempotency key")
	clear := fs.Bool("clear", false, "clear a held reset without a stop pass")
	why := fs.String("why", "", "receipted reason for --clear")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "bench reset", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "bench reset", "takes flags, not positional arguments")
	}
	if *bench == "" || *actor == "" {
		return refuse(errOut, "bench reset", "--bench and --actor (or NOVA_FRIEND) are required")
	}
	if *clear {
		if *keep || *idem != "" || *grace != benchreset.DefaultGrace {
			return refuse(errOut, "bench reset", "--clear accepts only --bench, --why, --actor and --redis")
		}
		if strings.TrimSpace(*why) == "" {
			return refuse(errOut, "bench reset", "--clear requires --why")
		}
	} else if strings.TrimSpace(*why) != "" {
		return refuse(errOut, "bench reset", "--why is only for --clear")
	}
	if *grace <= 0 {
		return refuse(errOut, "bench reset", "--grace must be positive")
	}
	st, err := openLifeStore(ctx, lifeAddr(*addr))
	if err != nil {
		return refuse(errOut, "bench reset", err.Error())
	}
	defer st.Close()
	if *clear {
		res, err := benchreset.Clear(ctx, st.Client(), *bench, *actor, *why)
		if err != nil {
			return refuse(errOut, "bench reset", err.Error())
		}
		if res.Code != 0 {
			fmt.Fprintf(errOut, "BENCH RESET bench=%s clear=refused why=%s\n", *bench, res.Why)
			return res.Code
		}
		fmt.Fprintf(out, "BENCH RESET bench=%s id=%s cleared why=%s\n", *bench, res.ID, *why)
		return 0
	}
	res, err := benchreset.Reset(ctx, st.Client(), benchreset.Request{Bench: *bench, Actor: *actor, Idem: *idem, KeepQueue: *keep, Grace: *grace, Stopper: benchreset.RemoteStopper{}})
	if err != nil {
		return refuse(errOut, "bench reset", err.Error())
	}
	for _, card := range res.Cards {
		fmt.Fprintf(out, "BENCH RESET CARD %s from=%s to=%s\n", card.Card.Identity(), card.From, card.To)
	}
	line := fmt.Sprintf("BENCH RESET bench=%s id=%s stopped=%d requeued=%d kept=%d alive=%d took=%s", res.Bench, res.ID, res.Stopped, res.Requeued, res.Kept, res.Alive, res.Took.Round(time.Millisecond))
	if res.Why != "" {
		line += " held why=" + res.Why
	}
	fmt.Fprintln(out, line)
	return res.Code
}
