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
// (config.Widths: slots less the friend slots charged to the machine); this
// verb reads them through the config package, by the config tool's own address
// rules (config.ResolveDSN), and types no machine name and no width.
//
// It is one step (sprint.FleetReq, Op sync): the members missing come up at
// their width, the widths that differ are set, and the members the inventory
// no longer names are held, never deleted, so their cards are dealt again. A
// sync after a sync writes nothing and says so. There is no new state, so no
// model is owed: every move is one fleet up or fleet down already makes
// (tla/SprintEvents.tla, PlanDown), and the sync's plan is checked against
// the drift it reports (FleetDrift).

// exitCannotRead is fleet sync's exit when the inventory cannot be read: the
// config is unreachable, unmigrated, or holds no machine row at all (a store
// that is not the fleet's would hold every member down). It is the family's
// 3, beside 2 for drift under --check.
const (
	exitCannotRead = 3
	exitDrift      = 2
)

// inventoryFn reads every machine row's width from nova-config: the address
// of the config store (its --pg, else NOVA_PG_DSN) and the Redis the friends'
// beats are read from when a friend carries slots.
type inventoryFn func(ctx context.Context, pg, redisAddr string) ([]config.MachineWidth, error)

// readInventory is the real inventoryFn: Postgres by config.ResolveDSN, Redis
// by the sprint's own address, both bounded.
func (a *app) readInventory(ctx context.Context, pg, redisAddr string) ([]config.MachineWidth, error) {
	dsn, err := config.ResolveDSN(pg, a.getenv)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	st, err := config.OpenPG(ctx, dsn)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	have, err := st.Version(ctx)
	if err != nil {
		return nil, err
	}
	if all, err := config.Migrations(); err == nil && have < len(all) {
		return nil, fmt.Errorf("schema config is at version %d and this binary carries %d; run: nova-config migrate", have, len(all))
	}
	return config.Widths(ctx, st, lazyHosts{a: a, addr: redisAddr})
}

// lazyHosts reads the friends' beats from Redis, dialled only when a friend
// carries slots.
type lazyHosts struct {
	a    *app
	addr string
}

func (l lazyHosts) FriendHosts(ctx context.Context, names []string) (map[string]string, error) {
	if l.addr == "" {
		return nil, errors.New("--redis <addr> is needed to read the friends' beats (or NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR)")
	}
	conn, ok := l.a.conns[l.addr]
	if !ok {
		var err error
		if conn, err = l.a.openConn(ctx, l.addr); err != nil {
			return nil, err
		}
		l.a.conns[l.addr] = conn
	}
	return (&config.RedisApplier{Client: conn.Client()}).FriendHosts(ctx, names)
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
	Held    []string `json:"held"`
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
	ws, err := a.inventory(ctx, *pg, c.redis)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: the config cannot be read: %s; nothing was changed\n", prog, name, oneline.WithRemedy(err.Error(), "nova-config machine list"))
		return exitCannotRead
	}
	if len(ws) == 0 {
		fmt.Fprintf(stderr, "%s %s: the config holds no machine row, and syncing to none would hold every member down; is this the fleet's config? run: nova-config machine list; nothing was changed\n", prog, name)
		return exitCannotRead
	}
	var want []sprint.SyncMember
	for _, w := range ws {
		if w.Member() {
			want = append(want, sprint.SyncMember{Name: w.Machine, Width: w.Width})
		}
	}
	sort.Slice(want, func(i, j int) bool { return want[i].Name < want[j].Name })
	if problems := sprint.SyncProblems(want); len(problems) > 0 {
		if c.json {
			rep := syncReport{Verb: name, Check: *check, Members: len(want), Drift: []syncDrift{}, Held: []string{}, Moved: []string{}, Refused: []string{}}
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
	snap, err := st.Load(ctx, []string{sprint.Fleet, sprint.Work}, nil)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %s\n", prog, name, oneline.WithRemedy(nothingChanged(err), prog+" "+name+" -h"))
		if *check {
			return exitCannotRead
		}
		return 2
	}
	drift := sprint.FleetDrift(snap, want)
	held := sprint.HeldInInventory(snap, want)
	if held == nil {
		held = []string{}
	}
	rep := syncReport{Verb: name, Check: *check, Members: len(want), Drift: []syncDrift{}, Held: held, Moved: []string{}, Refused: []string{}}
	for _, d := range drift {
		rep.Drift = append(rep.Drift, syncDrift{Member: d.Member, Kind: d.Kind, From: d.From, To: d.To})
	}
	if *check {
		return a.syncCheck(c.json, rep, drift, stdout)
	}
	if len(drift) == 0 {
		return a.syncNothing(c.json, rep, stdout)
	}
	step := store.FleetStep(sprint.FleetReq{Op: "sync", Sync: want, Who: c.actor})
	if c.json {
		return a.syncWriteJSON(ctx, c, st, step, rep, stdout)
	}
	code := a.runStep(name, *c, st, step, stdout, stderr)
	if code == 0 {
		for _, m := range held {
			fmt.Fprintf(stdout, "NOTE %s is held by the coordinator and stays held; run: nova-sprint fleet up %s\n", oneline.Escape(m), oneline.Escape(m))
		}
	}
	return code
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
	if len(drift) == 0 {
		fmt.Fprintf(stdout, "FLEET-SYNC CHECK OK drift=0 members=%d: the fleet table matches the inventory\n", rep.Members)
		return 0
	}
	fmt.Fprintf(stdout, "FLEET-SYNC CHECK DRIFT drift=%d members=%d: run: nova-sprint fleet sync\n", len(drift), rep.Members)
	return code
}

// syncWriteJSON writes the sync and prints the one JSON shape: the exit code
// is the step's, as the lines' is (1 refused, 2 the store did not answer).
func (a *app) syncWriteJSON(ctx context.Context, c *common, st *store.Store, step store.Step, rep syncReport, stdout io.Writer) int {
	step.CallerOp = c.op
	res, err := st.Run(ctx, step)
	code := 0
	if res.Moved != nil {
		rep.Moved = res.Moved
	}
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
	return 0
}
