package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// fsck's check seat-agreement (docs/SPEC-SPRINT.md, "Handing over the seat",
// fsck-seat-agreement-r.w2): the coordinator key, the seat record's holder and
// the server's actor from the store (store.SeatCheck), and the nova-config
// sprint row's coordinator from the config store, held to one name by
// sprint.CheckSeatAgreement. It names each fix and writes nothing.

// cmdFsckSeat is fsck's seat-agreement: the check's line, exit 0 when the four
// values agree, 1 when they do not, 2 when a value could not be read. The fsck
// verb's row in verbs.go is the fsck verb's card's (fsck-held-without-beat);
// this card adds the check and no verb.
func (a *app) cmdFsckSeat(args []string, stdout, stderr io.Writer) int {
	return a.fsckSeat(args, stdout, stderr, a.configCoordinator)
}

// fsckSeat is cmdFsckSeat with the config row read by row, given --pg.
func (a *app) fsckSeat(args []string, stdout, stderr io.Writer, row func(pg string) sprint.ConfigCoordinator) int {
	fs, c := a.verbSetup("fsck seat")
	pg := fs.String("pg", "", "the address of nova-config's store, whose sprint row's coordinator is checked: host:port, or a postgres:// URI; else NOVA_PG_DSN")
	if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
		return refuse(stderr, "fsck seat", argErr("takes no words ", err, pos...))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "fsck seat", err.Error())
	}
	ctx := context.Background()
	s, err := st.SeatCheck(ctx)
	if err != nil {
		return a.readFailed("fsck", err, stderr)
	}
	f, err := sprint.FsckSeat(ctx, s.Holder, s.Record, s.Server, row(*pg))
	if err != nil {
		fmt.Fprintf(stderr, "%s fsck seat FAILED: %s\n", prog, oneline.WithRemedy(err.Error(), prog+" fsck seat --pg <host:port or postgres:// URI>"))
		return 2
	}
	if f.Drift == "" {
		sayOK(stdout, c.json, "fsck seat", f.Line(), map[string]any{"checks": []sprint.SeatAgreement{f}})
		return 0
	}
	if c.json {
		b, _ := json.Marshal(map[string]any{"verb": "fsck", "status": "drift", "exit": 1, "checks": []sprint.SeatAgreement{f}}) // ignored: strings always encode
		fmt.Fprintln(stdout, string(b))
		return 1
	}
	fmt.Fprintln(stdout, f.Line())
	return 1
}

// configCoordinator is the nova-config sprint row's coordinator, read with
// nova-config's library at pg (withConfig).
func (a *app) configCoordinator(pg string) sprint.ConfigCoordinator {
	return func(ctx context.Context) (string, error) {
		var name string
		err := a.withConfig(ctx, pg, func(ctx context.Context, st config.Store) error {
			row, found, err := st.Get(ctx, config.KindSprint, config.KindSprint)
			if found {
				name = row.Fields["coordinator"]
			}
			return err
		})
		return name, err
	}
}
