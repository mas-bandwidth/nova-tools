package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

func init() {
	verbs = append(verbs, verb{"fsck", "[--repair] [--pg <dsn>]", "fsck", (*app).cmdFsck})
	notServed = append(notServed, "fsck")
}

// fsckConfigFn is the injected reader for nova-config sprint row's coordinator.
type fsckConfigFn func(ctx context.Context, pg string) (string, error)

var (
	fsckConfigMu   sync.Mutex
	fsckConfigHook fsckConfigFn
)

func setFsckConfigCoordinator(fn fsckConfigFn) func() {
	fsckConfigMu.Lock()
	prev := fsckConfigHook
	fsckConfigHook = fn
	fsckConfigMu.Unlock()
	return func() {
		fsckConfigMu.Lock()
		fsckConfigHook = prev
		fsckConfigMu.Unlock()
	}
}

func (a *app) fsckConfigCoordinator(ctx context.Context, pg string) (string, error) {
	fsckConfigMu.Lock()
	hook := fsckConfigHook
	fsckConfigMu.Unlock()
	if hook != nil {
		return hook(ctx, pg)
	}
	var coord string
	err := a.withConfig(ctx, pg, func(ctx context.Context, st config.Store) error {
		row, found, err := st.Get(ctx, config.KindSprint, config.KindSprint)
		if found && row.Fields != nil {
			coord = row.Fields["coordinator"]
		}
		return err
	})
	return coord, err
}

// cmdFsck runs the fourth fsck check, seat-agreement: four values name the same
// coordinator: the store key sprint:coordinator, the seat record's holder, the
// actor the running server was started with, and the nova-config sprint row's
// coordinator.
func (a *app) cmdFsck(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("fsck")
	repair := fs.Bool("repair", false, "name the repair commands")
	pg := fs.String("pg", "", "the address of the config store: host:port, or a postgres:// URI; else NOVA_PG_DSN")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "fsck", argErr("takes no words ", err, pos...))
	}
	ctx := context.Background()
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "fsck", err.Error())
	}

	key, err := st.B.Coordinator(ctx)
	if err != nil {
		return a.readFailed("fsck", err, stderr)
	}
	rec, ok, err := st.Seat(ctx)
	if err != nil {
		return a.readFailed("fsck", err, stderr)
	}
	record := key
	if ok {
		record = rec.Holder
	}
	server, err := st.ServerActor(ctx)
	if err != nil {
		return a.readFailed("fsck", err, stderr)
	}

	configCoord, _ := a.fsckConfigCoordinator(ctx, *pg)

	finding := sprint.CheckSeatAgreement(key, record, server, configCoord)

	if c.json {
		b, _ := json.Marshal(finding)
		fmt.Fprintln(stdout, string(b))
		if !finding.Clean {
			return 1
		}
		return 0
	}

	fmt.Fprintln(stdout, finding.String())
	if !finding.Clean {
		if *repair && finding.Fix != "" {
			fmt.Fprintf(stderr, "remedy=%s\n", oneline.Escape(finding.Fix))
		}
		return 1
	}
	return 0
}
