package main

import (
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/provbalance"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// THE BALANCE POLL (nova-tools#5199; the owner, 2026-10-03: "provider out of funds should
// never be a mystery failure."). run reads each provider's balance when it begins and every
// sprint.BalancePollEvery after, through the seat's key in its own environment
// (internal/provbalance), and writes the reads with the rests they call for in one step
// (sprint.Balance), held to the server's one line of control as a tick is. The HTTP is
// here, outside every tick: no tick waits on a provider.

// balanceLoop polls until ctx is done, waiting on a.after between polls (its own clock: the
// loop's sleep is the tick's pace).
func (a *app) balanceLoop(ctx context.Context, st *store.Store, stdout io.Writer) {
	fmt.Fprintf(stdout, "BALANCE every %s: each provider's balance is read through the seat's key and written to the fleet table\n", sprint.BalancePollEvery)
	for ctx.Err() == nil {
		a.pollBalances(ctx, st, stdout)
		select {
		case <-ctx.Done():
		case <-a.after(sprint.BalancePollEvery):
		}
	}
}

// pollBalances reads the balance of every provider the store's routes name and writes the
// reads (store.BalanceStep), and prints one line of what it read, or why it wrote nothing.
func (a *app) pollBalances(ctx context.Context, st *store.Store, stdout io.Writer) {
	at := a.now().Format("15:04:05")
	routes, _, err := st.Routes(ctx)
	if err != nil {
		fmt.Fprintf(stdout, "%s BALANCE FAIL the routes could not be read: %s\n", at, oneline.Escape(err.Error()))
		return
	}
	names := map[string]bool{}
	for _, r := range routes {
		if r.Provider != "" {
			names[r.Provider] = true
		}
	}
	if len(names) == 0 {
		return
	}
	var reads []sprint.ProviderRead
	var said []string
	for _, p := range slices.Sorted(maps.Keys(names)) {
		rd := provbalance.Read(ctx, a.transport, p, a.getenv)
		reads = append(reads, rd)
		if rd.Known {
			said = append(said, p+"="+sprint.Dollars(rd.Balance))
		} else {
			said = append(said, p+"=unknown")
		}
	}
	a.serial.Lock()
	res, err := st.Run(ctx, store.BalanceStep(sprint.BalanceReq{Reads: reads, Who: sprint.MachineActor}))
	a.serial.Unlock()
	if err != nil {
		fmt.Fprintf(stdout, "%s BALANCE FAIL %s: not written: %s\n", at, strings.Join(said, " "), oneline.Escape(err.Error()))
		return
	}
	fmt.Fprintf(stdout, "%s BALANCE %s notes=%d\n", at, strings.Join(said, " "), res.Notes)
}
