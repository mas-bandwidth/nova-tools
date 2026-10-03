package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// fleet sync: the fleet table made to match nova-config's inventory
// (docs/SPEC-SPRINT.md, section 5, "The fleet from the inventory"). The
// inventory is the machine rows of nova-config and their widths
// (config.Widths: each row's width field, set directly); this verb reads them
// through the config package, by the config tool's own address rules
// (config.ResolveDSN), and types no machine name and no width.
//
// It is one step (sprint.FleetReq, Op sync): the members missing come up at
// their width, the widths that differ are set, the members whose machine has
// width 0 are held, so their cards are dealt again, and a member with no machine
// row is held the same way and, once no card stays on it, removed: the step takes
// its control card off and the verb then deletes its row (store.DropMembers). A
// member removed in this epoch whose machine row comes back is placed again before
// the step (store.RejoinMembers) and released by it. A sync after a sync writes
// nothing and says so. The sync's plan is checked against the drift it reports
// (FleetDrift). The removal is the one move fleet down does not make; no model
// holds it: the sprint's models are not the design (tla/README.md, the owner's
// ruling of 2026-10-01), and the twin-store tests here and in internal/sprint hold it.

// exitCannotRead is fleet sync's exit when the inventory cannot be read: the
// config is unreachable, unmigrated, or holds no machine row at all (a store
// that is not the fleet's would hold every member down). It is the family's
// 3, beside 2 for drift under --check.
const (
	exitCannotRead = 3
	exitDrift      = 2
)

// inventoryFn reads every machine row's width from nova-config, given the
// address of the config store (its --pg, else NOVA_PG_DSN).
type inventoryFn func(ctx context.Context, pg string) ([]config.MachineWidth, error)

// readInventory is the real inventoryFn: Postgres by config.ResolveDSN,
// bounded.
func (a *app) readInventory(ctx context.Context, pg string) ([]config.MachineWidth, error) {
	var ws []config.MachineWidth
	err := a.withConfig(ctx, pg, func(ctx context.Context, st config.Store) (err error) {
		ws, err = config.Widths(ctx, st)
		return err
	})
	return ws, err
}

// withConfig runs read on nova-config's Postgres store, found by the config
// tool's own address rules (config.ResolveDSN), within 30 s; a schema behind
// this binary's is refused before read runs.
func (a *app) withConfig(ctx context.Context, pg string, read func(context.Context, config.Store) error) error {
	dsn, err := config.ResolveDSN(pg, a.getenv)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	st, err := config.OpenPG(ctx, dsn)
	if err != nil {
		return err
	}
	defer st.Close()
	have, err := st.Version(ctx)
	if err != nil {
		return err
	}
	if all, err := config.Migrations(); err == nil && have < len(all) {
		return fmt.Errorf("schema config is at version %d and this binary carries %d; run: nova-config migrate", have, len(all))
	}
	return read(ctx, st)
}

// syncReport is fleet sync's --json output, one shape whether the verb
// checked, had nothing to write, or wrote: what differed (Drift), what the
// write moved and refused, and the error when the step could not finish.
type syncReport struct {
	Verb    string      `json:"verb"`
	Check   bool        `json:"check"`
	Members int         `json:"members"`
	Drift   []syncDrift `json:"drift"`
	// Held are the members the inventory names that the coordinator holds:
	// the sync leaves their hold.
	Held []string `json:"held"`
	// Holding is a line for each member with no machine row that cards keep on
	// the fleet: it stays held until none does.
	Holding []string `json:"holding"`
	// Waiting is a line for each machine with the default width whose cores no
	// beat has reported: not a member until one does.
	Waiting []string `json:"waiting"`
	Moved   []string `json:"moved"`
	Refused []string `json:"refused"`
	Error   string   `json:"error,omitempty"`
}

type syncDrift struct {
	Member string `json:"member"`
	Kind   string `json:"kind"`
	From   int    `json:"from,omitempty"`
	To     int    `json:"to,omitempty"`
}

func (a *app) cmdFleetSync(args []string, stdout, stderr io.Writer) int {
	const name = "fleet sync"
	fs, c := a.verbSetup(name)
	check := fs.Bool("check", false, "print the drift between the fleet table and the inventory and write nothing: exit 0 when there is none, 2 when there is")
	pg := fs.String("pg", "", "the config store, Postgres postgres://user@host:port/db with no password (else NOVA_PG_DSN; the password from the variable NOVA_PG_PASSWORD_ENV names), as nova-config takes it")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if len(pos) > 0 {
		return refuse(stderr, name, "takes no words, found "+oneline.Escape(pos[0]))
	}
	// who acts is the verb's usage, before any store is read
	if why := needsActor(*c); why != "" {
		return refuse(stderr, name, why)
	}
	st, err := a.store(*c)
	if err != nil {
		if *check {
			// under --check exit 2 is drift alone: a sprint store that is
			// missing or does not answer is the family's 3
			fmt.Fprintf(stderr, "%s %s: %s\n", prog, name, nothingChanged(err))
			return exitCannotRead
		}
		return refuse(stderr, name, err.Error())
	}
	ctx := context.Background()
	ws, err := a.inventory(ctx, *pg)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: the config cannot be read: %s; nothing was changed\n", prog, name, oneline.WithRemedy(err.Error(), "nova-config machine list"))
		return exitCannotRead
	}
	if len(ws) == 0 {
		fmt.Fprintf(stderr, "%s %s: the config holds no machine row, and syncing to none would hold every member down; is this the fleet's config? run: nova-config machine list; nothing was changed\n", prog, name)
		return exitCannotRead
	}
	// a row with no width has the default, half the cores its machine's beat
	// reports (sprint.WidthOfCores); the sync writes the number it resolves
	var defaults []string
	for _, w := range ws {
		if w.Default {
			defaults = append(defaults, w.Machine)
		}
	}
	beats := map[string]sprint.Beat{}
	if len(defaults) > 0 {
		if beats, err = st.Beats(ctx, defaults); err != nil {
			fmt.Fprintf(stderr, "%s %s: the beats of the machines with the default width cannot be read: %s; nothing was changed; run: %s %s --check\n", prog, name, oneline.Escape(err.Error()), prog, name)
			return exitCannotRead
		}
	}
	var want []sprint.SyncMember
	var machines, names []string
	waiting := []string{}
	for _, w := range ws {
		machines = append(machines, w.Machine)
		width := w.Width
		if w.Default {
			if width = sprint.WidthOfCores(beats[w.Machine].Cores); width == 0 {
				waiting = append(waiting, fmt.Sprintf("%s has the default width, half its cores, and no beat has reported its cores yet; it joins the fleet at the sync after it beats (nova-sprint fleet beat %s on the machine)", w.Machine, w.Machine))
				continue
			}
		}
		if w.Member() {
			want = append(want, sprint.SyncMember{Name: w.Machine, Width: width})
			names = append(names, w.Machine)
		}
	}
	for _, l := range waiting {
		if !c.json {
			fmt.Fprintf(stdout, "NOTE %s\n", oneline.Escape(l))
		}
	}
	sort.Slice(want, func(i, j int) bool { return want[i].Name < want[j].Name })
	if problems := sprint.SyncProblems(want); len(problems) > 0 {
		if c.json {
			rep := syncReport{Verb: name, Check: *check, Members: len(want), Drift: []syncDrift{}, Held: []string{}, Holding: []string{}, Waiting: waiting, Moved: []string{}, Refused: []string{}}
			for _, p := range problems {
				rep.Refused = append(rep.Refused, p.Key+": "+p.Why)
			}
			b, _ := json.Marshal(rep)
			fmt.Fprintln(stdout, string(b))
			return 1
		}
		fmt.Fprintf(stderr, "%s %s: %s; fix the machine row in nova-config; nothing was changed\n", prog, name, oneline.Escape(problems[0].Why))
		return 1
	}
	var rejoined []string
	if !*check {
		// a member removed in this epoch whose machine row is back: its control
		// card placed again, held by the sync, which this sync releases
		if rejoined, err = st.RejoinMembers(ctx, names); err != nil {
			fmt.Fprintf(stderr, "%s %s: placing a removed member's control card again: %s\n", prog, name, oneline.WithRemedy(oneline.Escape(err.Error()), prog+" "+name))
			return 2
		}
	}
	snap, err := st.Load(ctx, []string{sprint.Fleet, sprint.Work}, nil)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %s\n", prog, name, oneline.WithRemedy(nothingChanged(err), prog+" "+name+" -h"))
		if *check {
			return exitCannotRead
		}
		return 2
	}
	drift := sprint.FleetDrift(snap, want, machines)
	held := sprint.HeldInInventory(snap, want)
	if held == nil {
		held = []string{}
	}
	holding := sprint.GoneHolding(snap, want, machines)
	if holding == nil {
		holding = []string{}
	}
	rep := syncReport{Verb: name, Check: *check, Members: len(want), Drift: []syncDrift{}, Held: held, Holding: holding, Waiting: waiting, Moved: []string{}, Refused: []string{}}
	for _, m := range rejoined {
		rep.Moved = append(rep.Moved, m+" placed again: its machine row is back")
		if !c.json {
			fmt.Fprintf(stdout, "NOTE %s rejoins the fleet: its control card is placed again, held by the sync, which releases it now\n", oneline.Escape(m))
		}
	}
	for _, d := range drift {
		rep.Drift = append(rep.Drift, syncDrift{Member: d.Member, Kind: d.Kind, From: d.From, To: d.To})
	}
	if *check {
		return a.syncCheck(c.json, rep, drift, stdout)
	}
	if len(drift) == 0 {
		// the beat records a cleanup cut short still owes, whose rows are gone
		if err := st.FinishDrops(ctx); err != nil {
			fmt.Fprintf(stderr, "%s %s: the fleet table matches the inventory; deleting the beat records of members removed before: %s\n", prog, name, oneline.WithRemedy(oneline.Escape(err.Error()), prog+" "+name))
			return 2
		}
		return a.syncNothing(c.json, rep, stdout)
	}
	step := store.FleetStep(sprint.FleetReq{Op: "sync", Sync: want, Machines: machines, Who: c.actor})
	if c.json {
		return a.syncWriteJSON(ctx, c, st, step, rep, want, machines, stdout)
	}
	code := a.runStep(name, *c, st, step, stdout, stderr)
	if code == 0 {
		gone, holding, err := afterSync(ctx, st, want, machines)
		if err != nil {
			fmt.Fprintf(stderr, "%s %s: the step is written; deleting the rows of the members it removed (%s): %s\n", prog, name, strings.Join(gone, ","), oneline.WithRemedy(oneline.Escape(err.Error()), prog+" "+name))
			return 2
		}
		for _, m := range held {
			fmt.Fprintf(stdout, "NOTE %s is held by the coordinator and stays held; run: nova-sprint fleet up %s\n", oneline.Escape(m), oneline.Escape(m))
		}
		for _, l := range holding {
			fmt.Fprintf(stdout, "NOTE %s\n", oneline.Escape(l))
		}
	}
	return code
}

// afterSync deletes the row of every member that is not a member of the
// inventory whose control card is off the table (the members the sync's step
// removed, and a row a sync before left when its delete failed), each delete
// conditional at its commit on the control card still off the table at the
// revision read (store.DropMembers, RowsDelIf), so a member placed again in
// between (fleet up) keeps its row and its cards. holding is the members with
// no machine row that cards keep on the fleet, held (sprint.GoneHolding), as
// read after the delete.
func afterSync(ctx context.Context, st *store.Store, want []sprint.SyncMember, machines []string) (gone, holding []string, err error) {
	keep := make([]string, len(want))
	for i, w := range want {
		keep[i] = w.Name
	}
	if gone, err = st.DropMembers(ctx, keep); err != nil {
		return gone, nil, err
	}
	snap, err := st.Load(ctx, []string{sprint.Fleet, sprint.Work}, nil)
	if err != nil {
		return gone, nil, err
	}
	return gone, sprint.GoneHolding(snap, want, machines), nil
}

// nothingChanged is an error as one line ending in "nothing was changed",
// once.
func nothingChanged(err error) string {
	msg := oneline.Escape(err.Error())
	if strings.Contains(msg, "nothing was changed") {
		return msg
	}
	return msg + "; nothing was changed"
}

// syncCheck prints the drift and writes nothing: exit 0 when there is none,
// 2 when there is.
func (a *app) syncCheck(asJSON bool, rep syncReport, drift []sprint.Drift, stdout io.Writer) int {
	code := 0
	if len(drift) > 0 {
		code = exitDrift
	}
	if asJSON {
		b, _ := json.Marshal(rep)
		fmt.Fprintln(stdout, string(b))
		return code
	}
	for _, d := range drift {
		fmt.Fprintf(stdout, "DRIFT %s %s\n", d.Kind, oneline.Escape(d.Line()))
	}
	for _, m := range rep.Held {
		fmt.Fprintf(stdout, "NOTE %s is held by the coordinator and stays held; run: nova-sprint fleet up %s\n", oneline.Escape(m), oneline.Escape(m))
	}
	for _, l := range rep.Holding {
		fmt.Fprintf(stdout, "NOTE %s\n", oneline.Escape(l))
	}
	if len(drift) == 0 {
		fmt.Fprintf(stdout, "FLEET-SYNC CHECK OK drift=0 members=%d: the fleet table matches the inventory\n", rep.Members)
		return 0
	}
	fmt.Fprintf(stdout, "FLEET-SYNC CHECK DRIFT drift=%d members=%d: run: nova-sprint fleet sync\n", len(drift), rep.Members)
	return code
}

// syncWriteJSON writes the sync and prints the one JSON shape: the exit code
// is the step's, as the lines' is (1 refused, 2 the store did not answer).
func (a *app) syncWriteJSON(ctx context.Context, c *common, st *store.Store, step store.Step, rep syncReport, want []sprint.SyncMember, machines []string, stdout io.Writer) int {
	step.CallerOp = c.op
	res, err := st.Run(ctx, step)
	code := 0
	rep.Moved = append(rep.Moved, res.Moved...)
	for _, r := range res.Refused {
		rep.Refused = append(rep.Refused, r.Key+": "+r.Why)
		code = 1
	}
	if err != nil {
		rep.Error = err.Error()
		code = 2
		var pe *store.PendingError
		var cut *store.CutError
		var cleared *store.ClearedError
		if errors.As(err, &pe) || errors.As(err, &cut) || errors.As(err, &cleared) {
			code = 1
		}
	}
	if code == 0 {
		gone, holding, err := afterSync(ctx, st, want, machines)
		if err != nil {
			rep.Error, code = "the step is written; deleting the rows of the members it removed ("+strings.Join(gone, ",")+"): "+err.Error()+"; run: "+prog+" fleet sync", 2
		}
		if holding != nil {
			rep.Holding = holding
		}
	}
	b, _ := json.Marshal(rep)
	fmt.Fprintln(stdout, string(b))
	return code
}

// syncNothing says a sync had nothing to write.
func (a *app) syncNothing(asJSON bool, rep syncReport, stdout io.Writer) int {
	if asJSON {
		b, _ := json.Marshal(rep)
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprintf(stdout, "FLEET-SYNC OK moved=0 members=%d: nothing to do, the fleet table already matches the inventory\n", rep.Members)
	for _, m := range rep.Held {
		fmt.Fprintf(stdout, "NOTE %s is held by the coordinator and stays held; run: nova-sprint fleet up %s\n", oneline.Escape(m), oneline.Escape(m))
	}
	for _, l := range rep.Holding {
		fmt.Fprintf(stdout, "NOTE %s\n", oneline.Escape(l))
	}
	return 0
}
