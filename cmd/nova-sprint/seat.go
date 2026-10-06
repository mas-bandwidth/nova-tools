package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The coordinator's seat (docs/SPEC-SPRINT.md, "Handing over the seat"; the owner,
// 2026-10-02: "We need to make handover MORE SMOOTH."; nova-tools#5096 item 28).
// coordinator <name> gives the seat (its holder, or the owner) or takes it (the
// one taking it, --take --approved-by <owner>); handover prints what the next
// seat needs, from the store, in one screen; inbox --wait --push seat writes to
// the holder's inbox, and follows the seat.

// OwnerEnv names the sprint's owner when init named none: the name a take
// carries in its record.
const OwnerEnv = "NOVA_SPRINT_OWNER"

// pushSeat is the --push value that follows the seat: the holder's inbox,
// ~/<holder>-working/inbox/sprint-judgments (a directory named seat is ./seat).
const pushSeat = "seat"

// owner is the sprint's owner: init --owner, else NOVA_SPRINT_OWNER.
func (a *app) owner(ctx context.Context, st *store.Store) (string, error) {
	o, err := st.Owner(ctx)
	if err != nil || o != "" {
		return o, err
	}
	return a.getenv(OwnerEnv), nil
}

func (a *app) cmdCoordinator(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("coordinator")
	reason := fs.String("reason", "", "why the seat moves, recorded in the log with who moved it (required)")
	take := fs.Bool("take", false, "take the seat as <name>, who runs this, when the holder is away (asleep, out of credits): wants --approved-by")
	approved := fs.String("approved-by", "", "with --take, the sprint's owner who approved it (init --owner, else "+OwnerEnv+"): the take is refused without the owner's name")
	dry := fs.Bool("dry-run", false, "say whether the seat would move, and how, and write nothing")
	gen := fs.Uint64("generation", 0, "the seat's generation the move was decided at, as handover prints it: refused when the seat has moved since (default: the generation read now)")
	pos, err := parse(fs, args)
	if err != nil || len(pos) != 1 {
		return refuse(stderr, "coordinator", argErr("wants one name, the seat's next holder, ", err, pos...))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "coordinator", err.Error())
	}
	ctx := context.Background()
	owner, err := a.owner(ctx, st)
	if err != nil {
		return a.readFailed("coordinator", err, stderr)
	}
	seat, err := st.SeatCheck(ctx)
	if err != nil {
		return a.readFailed("coordinator", err, stderr)
	}
	// the record names the seat, not a key a configuration refresh wrote over it
	holder := orDashStr(seat.Record, seat.Holder)
	req := sprint.SeatReq{To: pos[0], Who: c.actor, Reason: *reason, Take: *take, ApprovedBy: *approved, Owner: owner, Generation: *gen}
	if req.Generation == 0 {
		req.Generation = seat.Generation // the step refuses the move when the seat moves before it commits
	}
	why := sprint.StaleSeat(seat.Generation, req)
	if why == "" {
		why = sprint.NotSeat(holder, req)
	}
	if why != "" {
		return refuse(stderr, "coordinator", why)
	}
	// the seat goes only to a session the push loop has reached (pushproof.go)
	if why, err := pushGate(ctx, st, req.To, a.now()); err != nil {
		return a.readFailed("coordinator", err, stderr)
	} else if why != "" {
		return refuse(stderr, "coordinator", why)
	}
	how := "given"
	if req.Take {
		how = "taken approved_by=" + oneline.Field(req.ApprovedBy)
	}
	said := fmt.Sprintf("holder=%s from=%s by=%s %s", oneline.Field(req.To), oneline.Field(holder), oneline.Field(c.actor), how)
	if push, err := pushSaid(ctx, st, req.To, a.now()); err != nil {
		return a.readFailed("coordinator", err, stderr)
	} else if push != "" {
		said += " " + push
	}
	if *dry {
		fmt.Fprintf(stdout, "COORDINATOR DRY-RUN %s; nothing was changed\n", said)
		return 0
	}
	step := store.SeatStep(req)
	step.CallerOp = c.op
	res, err := st.Run(ctx, step)
	if err != nil {
		fmt.Fprintf(stderr, "%s coordinator: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	if len(res.Refused) > 0 {
		// the seat moved since the check above: the step read it again
		return refuse(stderr, "coordinator", res.Refused[0].Why)
	}
	h, text, err := a.handover(ctx, st)
	if err != nil {
		return a.readFailed("coordinator", err, stderr)
	}
	if c.json {
		b, _ := json.Marshal(map[string]any{"holder": req.To, "from": holder, "by": c.actor, "taken": req.Take, "approved_by": req.ApprovedBy, "op": res.Op, "handover": h}) // ignored: strings, a bool and a view of strings always encode
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprintf(stdout, "COORDINATOR OK %s\n", said)
	fmt.Fprint(stdout, text)
	return 0
}

func (a *app) cmdHandover(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("handover")
	pg := fs.String("pg", "", "the address of nova-config's store, whose sprint row and revisions the handover holds to the seat and the store: host:port, or a postgres:// URI; else NOVA_PG_DSN; with neither the config side is shown unread")
	if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
		return refuse(stderr, "handover", argErr("takes no words ", err, pos...))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "handover", err.Error())
	}
	h, text, err := a.handoverFrom(context.Background(), st, a.handoverSources(*pg))
	if err != nil {
		return a.readFailed("handover", err, stderr)
	}
	if c.json {
		b, _ := json.Marshal(h) // ignored: a view of strings, numbers and times always encodes
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprint(stdout, text)
	return 0
}

// handoverView is what the next seat needs, as handover --json carries it: a
// restart checkpoint (handover-is-a-restart-checkpoint.w4; the nova-sprint
// review, item 5), so the next seat recovers its next safe actions from it alone
// and keeps no ledger of its own beside it.
type handoverView struct {
	At        time.Time       `json:"at"`
	Seat      seatView        `json:"seat"`
	Owner     string          `json:"owner,omitempty"`
	Server    *serverView     `json:"server,omitempty"`
	Revisions revisionsView   `json:"revisions"`
	Ownership []ownerView     `json:"ownership"`
	Machine   string          `json:"machine"`
	Summary   string          `json:"summary"`
	Streams   []streamCounts  `json:"streams"`
	Sentinels []sentinelView  `json:"sentinels"`
	Judgments []inboxJudgment `json:"judgments"`
	Cards     []cardEvidence  `json:"cards"`
	Members   []memberView    `json:"members"`
	Routes    routesView      `json:"routes"`
	Installs  installsView    `json:"installs"`
	Decisions []decisionView  `json:"decisions"`
	Rules     []string        `json:"rules"`
	Next      []string        `json:"next"`
	First     []string        `json:"first"`
	groups    []sprint.Group  // the open judgments, as inbox prints them
}

// seatView is the holder, the seat's generation and the sprint's epoch, and the
// last change of the seat; Since is nil while the seat has not moved since init.
type seatView struct {
	Holder     string             `json:"holder"`
	Generation uint64             `json:"generation"`
	Epoch      uint64             `json:"epoch"`
	Since      *time.Time         `json:"since,omitempty"`
	Last       *sprint.SeatChange `json:"last,omitempty"`
}

// serverView is the actor the server runs as, by its record, and when it is
// not the seat's holder, the line of its unit to change (sprint.SeatDrift).
type serverView struct {
	Actor  string `json:"actor"`
	Change string `json:"change,omitempty"`
}

// revisionsView is what the seat runs on: Source, the revision of the binary
// that wrote the handover (buildinfo); Config, nova-config's revision of each
// kind in its store, and Applied, the revision of each kind applied to the
// sprint's store (config:decl); Pending, the kinds whose revision in nova-config
// is ahead of the one applied. ConfigUnread and AppliedUnread say why a side was
// not read ("" when it was).
type revisionsView struct {
	Source        string           `json:"source"`
	Config        map[string]int64 `json:"config"`
	Applied       map[string]int64 `json:"applied"`
	Pending       []string         `json:"pending"`
	ConfigUnread  string           `json:"config_unread,omitempty"`
	AppliedUnread string           `json:"applied_unread,omitempty"`
}

// The states of a place that names the seat's holder (ownerView).
const (
	ownReconciled = "reconciled"
	ownPending    = "pending"
	ownUnread     = "unread"
)

// ownerView is one place that names the seat's holder, and whether it follows
// the accepted handover: the seat's record (the handover itself), the
// coordinator key in the store, the server's actor, nova-config's sprint row,
// and the wake path (the holder's push proof). Pending names the line that
// reconciles it; a pending place never moves the seat: the record does.
type ownerView struct {
	Place string `json:"place"`
	Names string `json:"names"`
	State string `json:"state"`
	Why   string `json:"why,omitempty"`
	Fix   string `json:"fix,omitempty"`
}

// cardEvidence is an open primary as the next seat needs it: where it is, the
// branch and head its work finished at, the reads given (each reader's verdict
// and the head it read), the gate (the last CI result, the head and run it was
// for), its row in the merge table, and Lineage, each attempt with the remedy it
// was given and how it ended, so a remedy already tried is not tried again.
type cardEvidence struct {
	ID      string         `json:"id"`
	Stream  string         `json:"stream"`
	State   string         `json:"state"`
	Attempt int            `json:"attempt"`
	Branch  string         `json:"branch,omitempty"`
	Head    string         `json:"head,omitempty"`
	Reads   []readEvidence `json:"reads"`
	Gate    *gateEvidence  `json:"gate,omitempty"`
	Merge   string         `json:"merge,omitempty"`
	Lineage []attemptView  `json:"lineage"`
}

type readEvidence struct {
	Reader  string `json:"reader"`
	Attempt int    `json:"attempt"`
	Verdict string `json:"verdict"`
	Head    string `json:"head,omitempty"`
}

type gateEvidence struct {
	Result string `json:"result"`
	Head   string `json:"head,omitempty"`
	Run    string `json:"run,omitempty"`
	Source string `json:"source,omitempty"`
}

// attemptView is one attempt of a card: the fix it was dealt with ("" for the
// first, or a redeal with none), its result ("" while it runs), the first line of
// its report, and the head it finished at.
type attemptView struct {
	Attempt int    `json:"attempt"`
	Fix     string `json:"fix,omitempty"`
	Result  string `json:"result,omitempty"`
	Report  string `json:"report,omitempty"`
	Head    string `json:"head,omitempty"`
}

// installsView is the install receipts of the machine handover runs on: each
// unit a running sprint needs, installed, missing or different, from the unit
// files in Dir (units --check's reading); Unread says why they were not read.
type installsView struct {
	Dir    string             `json:"dir,omitempty"`
	Units  []sprint.UnitState `json:"units"`
	Unread string             `json:"unread,omitempty"`
}

type streamCounts struct {
	Stream string         `json:"stream"`
	Counts map[string]int `json:"counts"`
}

type sentinelView struct {
	ID      string   `json:"id"`
	Stream  string   `json:"stream"`
	Reached bool     `json:"reached"`
	Behind  []string `json:"behind"`
}

type memberView struct {
	Member string `json:"member"`
	Status string `json:"status"`
	By     string `json:"by,omitempty"`
}

type routesView struct {
	Enabled  int      `json:"enabled"`
	Disabled []string `json:"disabled"`
}

type decisionView struct {
	At     time.Time `json:"at"`
	Verb   string    `json:"verb"`
	What   string    `json:"what"`
	By     string    `json:"by"`
	Reason string    `json:"reason,omitempty"`
}

// handoverDecisions is how many of the coordinator's last decisions handover
// shows.
const handoverDecisions = 10

// decisionVerbs are the coordinator's decisions handover shows from the log,
// beside the seat's own changes and the holds (their notes), by the verb of the
// step that wrote the line: rework only with a fix.
var decisionVerbs = []string{"release", "drop", "rework", "redo"}

// configSide is nova-config's side of the seat: its sprint row's coordinator
// and the revision of each kind in its store.
type configSide struct {
	Coordinator string
	Revs        map[string]int64
}

// handoverSources is what handover reads beside the sprint's store: the
// binary's revision, nova-config's side (nil: no config store named), the
// revisions applied to the store (nil: the store keeps none) and the units of
// this machine (nil: not read). A test gives fakes.
type handoverSources struct {
	Source  string
	Config  func(context.Context) (configSide, error)
	Applied func(context.Context, *store.Store) (map[string]int64, error)
	Units   func() (string, []sprint.UnitState, error)
}

// handoverSources is the sources of the binary: nova-config's store at pg (else
// NOVA_PG_DSN) when either names one, the store's config:decl, and the unit
// directory of this machine.
func (a *app) handoverSources(pg string) handoverSources {
	src := handoverSources{Source: buildinfo.Version(version), Applied: appliedRevs}
	if pg != "" || a.getenv(config.EnvPG) != "" {
		src.Config = func(ctx context.Context) (configSide, error) {
			side := configSide{Revs: map[string]int64{}}
			err := a.withConfig(ctx, pg, func(ctx context.Context, st config.Store) error {
				row, found, err := st.Get(ctx, config.KindSprint, config.KindSprint)
				if err != nil {
					return err
				}
				if found {
					side.Coordinator = row.Fields["coordinator"]
				}
				for _, k := range config.Kinds {
					if side.Revs[k.Name], err = st.Rev(ctx, k.Name); err != nil {
						return err
					}
				}
				return nil
			})
			return side, err
		}
	}
	src.Units = func() (string, []sprint.UnitState, error) {
		goos := a.seatOS()
		dir, err := a.seatDir(goos)
		if err != nil {
			return "", nil, err
		}
		states, err := sprint.CheckUnits(dir, goos, sprint.UnitKinds)
		return dir, states, err
	}
	return src
}

// appliedRevs is the revision of each kind nova-config applied to the store, as
// its stamp (config:decl, rev:<kind>) holds it; nil for a store that keeps none.
func appliedRevs(ctx context.Context, st *store.Store) (map[string]int64, error) {
	r, ok := st.B.(*store.Redis)
	if !ok {
		return nil, nil
	}
	h, err := r.C.HGetAll(ctx, config.DeclKey).Result()
	if err != nil {
		return nil, err
	}
	revs := map[string]int64{}
	for f, v := range h {
		if kind, ok := strings.CutPrefix(f, "rev:"); ok {
			var n int64
			_, _ = fmt.Sscan(v, &n) // ignored: a stamp that is no number reads 0, never applied
			revs[kind] = n
		}
	}
	return revs, nil
}

// handover is the handover from the binary's sources (handoverSources), with no
// config store named but by NOVA_PG_DSN: coordinator's receipt.
func (a *app) handover(ctx context.Context, st *store.Store) (handoverView, string, error) {
	return a.handoverFrom(ctx, st, a.handoverSources(""))
}

// handoverFrom reads what the next seat needs and renders it: the holder and
// since when, the seat's generation and the epoch, the revisions it runs on,
// every place that names the holder and whether it follows the handover, the
// machine and the progress, each stream's counts, the sentinels held with what
// waits behind each, every open judgment with its answer lines, every open card
// with its evidence and the remedies already tried, the members held or down and
// by whom, the routes disabled, the install receipts, the last decisions from
// the log with their reasons, the next safe lines, and the lines the next seat
// runs first.
func (a *app) handoverFrom(ctx context.Context, st *store.Store, src handoverSources) (handoverView, string, error) {
	now := a.now()
	h := handoverView{At: now, Streams: []streamCounts{}, Sentinels: []sentinelView{}, Judgments: []inboxJudgment{}, Members: []memberView{},
		Decisions: []decisionView{}, Routes: routesView{Disabled: []string{}}, Ownership: []ownerView{}, Cards: []cardEvidence{}, Next: []string{},
		Installs: installsView{Units: []sprint.UnitState{}}}
	v, _, err := a.where(ctx, st, defaultStale, false) // the view carries every table; the frame is not drawn here
	if err != nil {
		return h, "", err
	}
	h.Seat.Holder, h.Seat.Generation, h.Machine, h.Summary = v.Coordinator, sprint.FirstSeatGeneration, v.Machine, v.Summary
	if v.Seat != nil {
		h.Seat.Since, h.Seat.Last, h.Seat.Generation = &v.Seat.At, v.Seat, max(v.Seat.Generation, sprint.FirstSeatGeneration)
	}
	if h.Owner, err = a.owner(ctx, st); err != nil {
		return h, "", err
	}
	seat, err := st.SeatCheck(ctx)
	if err != nil {
		return h, "", err
	}
	// the record is the accepted handover: the seat is its holder's, whatever a key says
	h.Seat.Holder, h.Seat.Epoch = orDashStr(seat.Record, seat.Holder), seat.Epoch
	if seat.Server != "" {
		h.Server = &serverView{Actor: seat.Server, Change: sprint.SeatDrift(h.Seat.Holder, h.Seat.Holder, seat.Server)}
	}
	if err := a.handoverOwnership(ctx, st, &h, seat, src); err != nil {
		return h, "", err
	}
	for _, s := range sortedKeys(v.Tables[sprint.Work]) {
		sc := streamCounts{Stream: s, Counts: map[string]int{}}
		for _, col := range sprint.States {
			var n int
			_, _ = fmt.Sscan(cellText(v.Tables[sprint.Work][s][string(col)]), &n) // ignored: a cell that is no number counts 0
			sc.Counts[string(col)] = n
		}
		h.Streams = append(h.Streams, sc)
	}
	snap, err := st.Load(ctx, []string{sprint.Work}, nil)
	if err != nil {
		return h, "", err
	}
	for _, s := range snap.Streams() {
		for _, c := range snap.Work.Cell(s, string(sprint.Waiting)) {
			if !sprint.IsSentinel(c) {
				continue
			}
			sv := sentinelView{ID: c.ID, Stream: s, Reached: c.F("reached") != "", Behind: []string{}}
			for _, b := range sprint.Behind(snap, c) {
				sv.Behind = append(sv.Behind, b.ID)
			}
			h.Sentinels = append(h.Sentinels, sv)
		}
	}
	if h.Cards, err = openCards(ctx, st, snap); err != nil {
		return h, "", err
	}
	in, err := st.Inbox(ctx, defaultDeadline, defaultStale, 10000)
	if err != nil {
		return h, "", err
	}
	for _, g := range in.Groups {
		if g.Kind == sprint.Judgment {
			h.groups = append(h.groups, g)
		}
	}
	h.Judgments, _ = inboxActs(h.groups, now)
	rs, _, err := st.Routes(ctx)
	if err != nil {
		return h, "", err
	}
	for _, r := range rs {
		if r.Enabled {
			h.Routes.Enabled++
		} else {
			h.Routes.Disabled = append(h.Routes.Disabled, r.Name)
		}
	}
	lines, err := st.Log(ctx)
	if err != nil {
		return h, "", err
	}
	heldBy := map[string]string{}
	seen := map[string]bool{}
	for _, l := range lines {
		d, ok := decisionOf(l)
		if !ok || (l.Op != "" && seen[l.Op]) {
			continue
		}
		seen[l.Op] = true
		h.Decisions = append(h.Decisions, d)
		if m, ok := strings.CutPrefix(d.What, sprint.HoldMember+" "); ok && d.Verb == "hold" {
			heldBy[m] = d.By
		}
	}
	h.Decisions = h.Decisions[max(0, len(h.Decisions)-handoverDecisions):]
	for _, m := range sortedKeys(v.Tables[sprint.Fleet]) {
		status := cellText(v.Tables[sprint.Fleet][m][sprint.Status])
		switch status {
		case sprint.Held:
			h.Members = append(h.Members, memberView{Member: m, Status: status, By: heldBy[m]})
		case sprint.Down:
			h.Members = append(h.Members, memberView{Member: m, Status: status})
		}
	}
	if src.Units == nil {
		h.Installs.Unread = "not read"
	} else if dir, states, err := src.Units(); err != nil {
		h.Installs.Unread = err.Error()
	} else {
		h.Installs.Dir, h.Installs.Units = dir, append(h.Installs.Units, states...)
	}
	h.Rules = []string{handoverWaves}
	h.Next = handoverNext(h)
	h.First = []string{"nova-sprint where", "nova-sprint inbox --wait --push " + pushSeat, `read docs/SPEC-SPRINT.md, "Handing over the seat"`}
	return h, a.handoverText(h), nil
}

// handoverOwnership fills the revisions and the places that name the holder:
// the record, the key, the server, nova-config's sprint row and the wake path.
func (a *app) handoverOwnership(ctx context.Context, st *store.Store, h *handoverView, seat store.SeatCheck, src handoverSources) error {
	holder := h.Seat.Holder
	h.Revisions = revisionsView{Source: src.Source, Config: map[string]int64{}, Applied: map[string]int64{}, Pending: []string{}}
	var side configSide
	if src.Config == nil {
		h.Revisions.ConfigUnread = "no config store named: give --pg <host:port> or NOVA_PG_DSN"
	} else if c, err := src.Config(ctx); err != nil {
		h.Revisions.ConfigUnread = err.Error()
	} else {
		side, h.Revisions.Config = c, c.Revs
	}
	if src.Applied == nil {
		h.Revisions.AppliedUnread = "the store keeps no config stamp"
	} else if revs, err := src.Applied(ctx, st); err != nil {
		h.Revisions.AppliedUnread = err.Error()
	} else if revs == nil {
		h.Revisions.AppliedUnread = "the store keeps no config stamp"
	} else {
		h.Revisions.Applied = revs
	}
	if h.Revisions.ConfigUnread == "" && h.Revisions.AppliedUnread == "" {
		for _, k := range sortedKeys(h.Revisions.Config) {
			if h.Revisions.Config[k] > h.Revisions.Applied[k] {
				h.Revisions.Pending = append(h.Revisions.Pending, k)
			}
		}
	}

	record := ownerView{Place: "record", Names: orDashStr(seat.Record, seat.Holder), State: ownReconciled,
		Why: fmt.Sprintf("the accepted handover, generation %d", h.Seat.Generation)}
	if seat.Record == "" {
		record.Why = "init's key: the seat has not moved since init"
	}
	key := ownerView{Place: "key", Names: orDashStr(seat.Holder, "-"), State: ownReconciled}
	if seat.Holder != holder {
		key.State, key.Why, key.Fix = ownPending, "the key names "+orDashStr(seat.Holder, "no one")+" and the record "+holder+" (a configuration apply writes the key; it does not move the seat)",
			"nova-sprint seat --repair --reason <text>"
	}
	server := ownerView{Place: "server", Names: orDashStr(seat.Server, "-"), State: ownReconciled}
	switch {
	case seat.Server == "":
		server.State, server.Why = ownUnread, "no server's record is fresh: the server is not running, or has not said so in "+store.ServerTTL.String()
	case seat.Server != sprint.MachineActor && seat.Server != holder:
		server.State, server.Why, server.Fix = ownPending, "the server runs as "+seat.Server, sprint.SeatDrift(holder, holder, seat.Server)
	}
	cfg := ownerView{Place: "config", Names: orDashStr(side.Coordinator, "-"), State: ownReconciled}
	switch {
	case h.Revisions.ConfigUnread != "":
		cfg.State, cfg.Why = ownUnread, h.Revisions.ConfigUnread
	case side.Coordinator != holder:
		cfg.State, cfg.Why, cfg.Fix = ownPending,
			"nova-config's sprint row names "+orDashStr(side.Coordinator, "no one")+": an apply of it is held (APPLY HELD) and does not move the seat, and the deal's coordinator role follows the row",
			"nova-config sprint set --coordinator "+holder
	}
	wake := ownerView{Place: "wake", State: ownReconciled}
	rec, ok, err := readPush(ctx, st, holder)
	if err != nil {
		return err
	}
	wake.Names = "-"
	if ok {
		wake.Names = rec.Name + " adapter=" + rec.AdapterName()
	}
	if why := sprint.PushWhy(holder, rec, ok, h.At); why != "" {
		wake.State, wake.Why, wake.Fix = ownPending, why, sprint.PushSetup(holder, rec, ok)
	}
	h.Ownership = []ownerView{record, key, server, cfg, wake}
	return nil
}

// openCards is every primary of the work table not landed, with its evidence and
// lineage, in the table's order; a sentinel is no card.
func openCards(ctx context.Context, st *store.Store, snap *sprint.Snapshot) ([]cardEvidence, error) {
	out := []cardEvidence{}
	for _, s := range snap.Streams() {
		for _, col := range sprint.States {
			if col == sprint.Landed {
				continue
			}
			for _, c := range snap.Work.Cell(s, string(col)) {
				if sprint.IsSentinel(c) {
					continue
				}
				e := cardEvidence{ID: c.ID, Stream: s, State: string(col), Attempt: c.Int("attempt"), Branch: c.F("branch"), Head: c.F("head"),
					Reads: []readEvidence{}, Lineage: []attemptView{}}
				if r := c.F("ci"); r != "" {
					e.Gate = &gateEvidence{Result: r, Head: c.F("ci_head"), Run: c.F("ci_run"), Source: c.F("ci_source")}
				}
				if e.Attempt > 0 {
					info, err := st.CardOf(ctx, c.ID)
					if err != nil {
						return nil, err
					}
					cardTrail(&e, info)
				}
				out = append(out, e)
			}
		}
	}
	return out, nil
}

// cardTrail is a card's attempts, its reads and its merge row, from its records.
func cardTrail(e *cardEvidence, info store.CardInfo) {
	for _, w := range info.Work {
		n := w.Int("attempt")
		if n == 0 {
			_, _ = fmt.Sscan(strings.TrimPrefix(w.ID[strings.LastIndex(w.ID, ".w")+1:], "w"), &n) // ignored: an id with no attempt reads 0
		}
		report, _, _ := strings.Cut(strings.TrimSpace(w.F("report")), "\n")
		// a finished work card says ok=yes or ok=no (its finish, steps_work.go)
		result := map[string]string{"yes": "ok", "no": "failed"}[w.F("ok")]
		e.Lineage = append(e.Lineage, attemptView{Attempt: n, Fix: w.F("fix"), Result: result, Report: report, Head: w.F("head")})
		if e.Branch == "" {
			e.Branch = w.F("branch")
		}
	}
	slices.SortFunc(e.Lineage, func(x, y attemptView) int { return x.Attempt - y.Attempt })
	for _, r := range info.Reads {
		if r.F("verdict") == "" {
			continue
		}
		e.Reads = append(e.Reads, readEvidence{Reader: r.F("reader"), Attempt: r.Int("attempt"), Verdict: r.F("verdict"), Head: r.F("head")})
	}
	if m := info.Merge; m != nil {
		e.Merge = m.Col
	}
}

// handoverNext is the next safe lines, from the generated state alone: each
// place that does not follow the handover, each kind whose configuration is not
// applied, and the seat's own move, pinned to the generation it was read at (a
// line read before the seat moves again is refused, never applied).
func handoverNext(h handoverView) []string {
	next := []string{}
	for _, o := range h.Ownership {
		if o.State == ownPending && o.Fix != "" {
			next = append(next, o.Fix)
		}
	}
	for _, k := range h.Revisions.Pending {
		next = append(next, fmt.Sprintf("nova-config apply --kind %s (revision %d is not applied; the store has %d)", k, h.Revisions.Config[k], h.Revisions.Applied[k]))
	}
	return append(next, fmt.Sprintf("nova-sprint coordinator <name> --generation %d --reason <text> (the seat moves only from generation %d)", h.Seat.Generation, h.Seat.Generation))
}

// decisionOf is the log line as one of the coordinator's decisions: a seat
// change, a hold or unhold (its note), a release, a drop, or a rework with a fix.
func decisionOf(l sprint.Line) (decisionView, bool) {
	if n := l.Note; n != nil {
		switch n.Type {
		case sprint.NHeld, sprint.NUnheld:
			// a hold or its release (hold.go): "<kind> <name> held[: reason][; ...]", the
			// kind and name what it acted on and the rest its words
			kind, rest, _ := strings.Cut(n.What, " ")
			name, rest, _ := strings.Cut(rest, " ")
			rest = strings.TrimPrefix(strings.TrimPrefix(rest, "released from its hold"), "held")
			return decisionView{At: l.At, Verb: map[string]string{sprint.NHeld: "hold", sprint.NUnheld: "unhold"}[n.Type], What: kind + " " + name, By: n.Who,
				Reason: strings.TrimLeft(rest, ":; ")}, true
		case sprint.NSeat, sprint.NSeatTaken:
		default:
			return decisionView{}, false
		}
		return decisionView{At: l.At, Verb: n.Type, What: n.What, By: n.Who}, true
	}
	if l.Kind != sprint.LineMove || !slices.Contains(decisionVerbs, l.Verb) || ((l.Verb == "rework" || l.Verb == "redo") && l.Text["fix"] == "") {
		return decisionView{}, false
	}
	if l.Table != sprint.Work {
		return decisionView{}, false // one line a decision: the primaries'
	}
	what := l.Card
	if len(l.Cards) > 0 {
		what = sprint.Preview(l.Cards, ",")
	}
	reason := l.Text["reason"]
	if l.Verb == "rework" || l.Verb == "redo" {
		reason = l.Text["fix"]
	}
	return decisionView{At: l.At, Verb: l.Verb, What: what, By: l.Actor, Reason: reason}, true
}

// handoverWaves is the first rule of the seat (the owner, 2026-10-03: "BATCH EVERYTHING"):
// the verbs refuse singles (add --one, rework and drop --one) and the tick raises "the fleet
// is starving" under twice the width.
const handoverWaves = "Cards are admitted and released in waves of at least the fleet's width: add takes a directory, release names a wave, rework and drop answer a group; a single-card verb outside a judgment is the sign of doing it wrong."

// handoverText is the handover in lines a person reads in one screen.
func (a *app) handoverText(h handoverView) string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintln(&b, oneline.Escape(fmt.Sprintf(format, args...))) }
	head := "HANDOVER seat=" + h.Seat.Holder + " since=init"
	if s := h.Seat.Last; s != nil {
		head = "HANDOVER seat=" + h.Seat.Holder + " since=" + a.clock12(s.At, h.At)
		if s.Taken {
			head += ", taken from " + s.From + ", approved by " + s.ApprovedBy + ": " + s.Reason
		} else {
			head += ", given by " + s.By + " (from " + s.From + "): " + s.Reason
		}
	}
	line("%s", head)
	line("SEAT holder=%s generation=%d epoch=%d", h.Seat.Holder, h.Seat.Generation, h.Seat.Epoch)
	if h.Server != nil && h.Server.Change != "" {
		line("SERVER %s", h.Server.Change)
	}
	r := h.Revisions
	rev := "REVISION source=" + orDashStr(r.Source, "-") + " config=" + revsText(r.Config, r.ConfigUnread) + " applied=" + revsText(r.Applied, r.AppliedUnread)
	if r.ConfigUnread == "" && r.AppliedUnread == "" {
		rev += " pending=" + orDashStr(strings.Join(r.Pending, ","), "none")
	}
	line("%s", rev)
	for _, o := range h.Ownership {
		s := "OWNER " + o.Place + " names=" + o.Names + " " + o.State
		if o.Why != "" {
			s += ": " + o.Why
		}
		if o.Fix != "" {
			s += "; run: " + o.Fix
		}
		line("%s", s)
	}
	line("%s", strings.TrimSpace(h.Machine+"  progress "+h.Summary))
	for _, s := range h.Streams {
		var cs []string
		for _, col := range sprint.States {
			cs = append(cs, fmt.Sprintf("%s=%d", col, s.Counts[string(col)]))
		}
		line("STREAM %s %s", s.Stream, strings.Join(cs, " "))
	}
	for _, s := range h.Sentinels {
		state := "held"
		if s.Reached {
			state = "reached, release is the coordinator's"
		}
		line("SENTINEL %s stream=%s %s: %d wait behind it (%s)", s.ID, s.Stream, state, len(s.Behind), sprint.Preview(s.Behind, ","))
	}
	for _, g := range h.groups {
		b.WriteString(groupText(g, h.At, false))
	}
	for _, c := range h.Cards {
		line("%s", cardText(c))
		for _, t := range c.Lineage {
			if t.Fix == "" && t.Result != "failed" {
				continue // the first try, or one that ran with no remedy and did not fail, is no lineage
			}
			s := fmt.Sprintf("TRIED %s attempt=%d", c.ID, t.Attempt)
			if t.Fix != "" {
				s += " fix: " + t.Fix + ";"
			}
			s += " " + orDashStr(t.Result, "running")
			if t.Report != "" && t.Result == "failed" {
				s += ": " + t.Report
			}
			line("%s", s)
		}
	}
	for _, m := range h.Members {
		if m.By != "" {
			line("MEMBER %s %s by %s", m.Member, m.Status, m.By)
		} else {
			line("MEMBER %s %s", m.Member, m.Status)
		}
	}
	switch {
	case h.Routes.Enabled+len(h.Routes.Disabled) == 0:
		line("ROUTES none in the store: see nova-config route list")
	case len(h.Routes.Disabled) == 0:
		line("ROUTES enabled=%d disabled=none", h.Routes.Enabled)
	default:
		line("ROUTES enabled=%d disabled=%s", h.Routes.Enabled, strings.Join(h.Routes.Disabled, ","))
	}
	if h.Installs.Unread != "" {
		line("INSTALLS unread: %s", h.Installs.Unread)
	} else {
		n := 0
		for _, u := range h.Installs.Units {
			if u.State == sprint.UnitInstalled {
				n++
				continue
			}
			s := "INSTALL " + u.Kind + " " + u.State
			if u.Why != "" {
				s += " (" + u.Why + ")"
			}
			if u.Owed != "" {
				s += "; owed: " + u.Install + " (" + u.Owed + ")"
			} else {
				s += "; run: " + u.Install
			}
			line("%s", s)
		}
		line("INSTALLS installed=%d of %d dir=%s", n, len(h.Installs.Units), h.Installs.Dir)
	}
	for _, d := range h.Decisions {
		s := "DECISION " + a.clock12(d.At, h.At) + " " + d.Verb + " " + d.What
		if !strings.HasPrefix(d.Verb, sprint.NSeat) {
			s += " by " + d.By
		}
		if d.Reason != "" {
			s += ": " + d.Reason
		}
		line("%s", s)
	}
	for _, r := range h.Rules {
		line("RULE %s", r)
	}
	for _, n := range h.Next {
		line("NEXT %s", n)
	}
	for _, f := range h.First {
		line("FIRST %s", f)
	}
	line("HANDOVER OK judgments=%d sentinels=%d members=%d decisions=%d", len(h.Judgments), len(h.Sentinels), len(h.Members), len(h.Decisions))
	return b.String()
}

// revsText is each kind's revision, kind:rev in the kinds' order, "unread" with
// why when the side was not read, and "none" when it holds none.
func revsText(revs map[string]int64, unread string) string {
	if unread != "" {
		return "unread"
	}
	var ks []string
	for _, k := range sortedKeys(revs) {
		ks = append(ks, fmt.Sprintf("%s:%d", k, revs[k]))
	}
	return orDashStr(strings.Join(ks, ","), "none")
}

// cardText is an open card's line: where it is, its branch and head, its reads,
// its gate and its merge row.
func cardText(c cardEvidence) string {
	s := fmt.Sprintf("CARD %s stream=%s state=%s attempt=%d", c.ID, c.Stream, c.State, c.Attempt)
	if c.Branch != "" {
		s += " branch=" + c.Branch
	}
	if c.Head != "" {
		s += " head=" + c.Head
	}
	if len(c.Reads) > 0 {
		var rs []string
		for _, r := range c.Reads {
			w := r.Reader + ":" + r.Verdict
			if r.Head != "" {
				w += "@" + r.Head
			}
			rs = append(rs, w)
		}
		s += " reads=" + strings.Join(rs, ",")
	}
	if g := c.Gate; g != nil {
		s += " gate=" + g.Result
		if g.Head != "" {
			s += "@" + g.Head
		}
		if g.Run != "" {
			s += " run=" + g.Run
		}
	}
	if c.Merge != "" {
		s += " merge=" + c.Merge
	}
	return s
}

// clock12 is a time as the owner reads it: 12-hour, "5:21 PM", in the zone times
// print in; with the day when it is not now's.
func (a *app) clock12(t, now time.Time) string {
	t, now = t.In(a.zone()), now.In(a.zone())
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("3:04 PM")
	}
	return t.Format("Mon Jan 2 3:04 PM")
}

// seatTitle is the where view's title line: SPRINT TABLE and the holder, and
// when the seat was taken (not given), since when.
func (a *app) seatTitle(holder string, seat *sprint.SeatChange, now time.Time) string {
	if holder == "" {
		return "SPRINT TABLE"
	}
	t := "SPRINT TABLE  coordinator " + holder
	if seat != nil && seat.Taken && seat.Holder == holder {
		t += " (taken " + a.clock12(seat.At, now) + ")"
	}
	return oneline.Escape(t)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// seatInbox is a name's inbox for pushed judgments,
// ~/<name>-working/inbox/sprint-judgments, and the inbox it is under; ok false
// when that inbox is not there.
func (a *app) seatInbox(name string) (dir, parent string, ok bool) {
	home, err := a.home()
	if err != nil || name == "" {
		return "", "", false
	}
	parent = filepath.Join(home, name+"-working", "inbox")
	info, err := os.Stat(parent)
	return filepath.Join(parent, "sprint-judgments"), parent, err == nil && info.IsDir()
}

// pushTarget is where inbox --wait --push writes each note: one directory, or,
// following the seat (--push seat), the holder's inbox, and a note addressed to
// someone with an inbox there, theirs. Each directory's files are its cursor.
type pushTarget struct {
	a      *app
	fixed  string                     // --push <dir>; "" follows the seat
	seen   map[string]map[string]bool // each directory's keys, read once
	holder string                     // the holder last pushed for
}

// dirOf is the directory the group is written to with the seat held by holder,
// "" when there is none to write it to now.
func (p *pushTarget) dirOf(holder string, g sprint.Group) string {
	if p.fixed != "" {
		return p.fixed
	}
	if g.To != "" && g.To != holder {
		if dir, _, ok := p.a.seatInbox(g.To); ok {
			return dir
		}
	}
	if dir, _, ok := p.a.seatInbox(holder); ok {
		return dir
	}
	return ""
}

// keys is the directory's keys, made and read the first time it is named.
func (p *pushTarget) keys(dir string) (map[string]bool, error) {
	if k, ok := p.seen[dir]; ok {
		return k, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	k, err := pushedKeys(dir)
	if err != nil {
		return nil, err
	}
	p.seen[dir] = k
	return k, nil
}

// unseen is the groups that wake the coordinator and hold a key their
// directory does not, in the inbox's order; a group with no directory now waits.
func (p *pushTarget) unseen(l inboxLook) ([]sprint.Group, error) {
	var out []sprint.Group
	for _, g := range l.groups {
		if !forCoordinator(g) {
			continue
		}
		dir := p.dirOf(l.holder, g)
		if dir == "" {
			continue
		}
		k, err := p.keys(dir)
		if err != nil {
			return nil, err
		}
		if len(unseen(k, []sprint.Group{g})) > 0 {
			out = append(out, g)
		}
	}
	return out, nil
}

// follow says where a loop following the seat writes, once for each holder: a
// line when the seat moved, and whether the holder has an inbox. A loop
// started for a holder with none is refused (exit 2, naming it).
func (p *pushTarget) follow(holder string, first bool, stdout, stderr io.Writer) int {
	if p.fixed != "" || holder == p.holder {
		return 0
	}
	p.holder = holder
	dir, parent, ok := p.a.seatInbox(holder)
	switch {
	case first && !ok:
		return refuse(stderr, "inbox", fmt.Sprintf("--push seat writes to the holder's inbox, %s, and %s is not there: make it, or give --push <dir>", dir, parent))
	case !ok:
		fmt.Fprintf(stdout, "NOTE the seat is %s's, and %s is not there: nothing is pushed until it is\n", oneline.Field(holder), oneline.Field(parent))
	case !first:
		fmt.Fprintf(stdout, "NOTE the seat is %s's: pushing to %s\n", oneline.Field(holder), oneline.Field(dir))
	}
	return 0
}

// cmdSeat is the seat as the friends' daemons read it every second: the
// holder, the epoch and the seat's generation, from the seat's keys and no
// table (store.SeatState), so the keepalive loop never serializes the board.
// The line goes on to name the seat record's holder (record=, once the seat has
// moved since init) and the actor the server runs as (server=, while its
// record is fresh), and exits 1 naming the drift when the key, the record and
// the server's actor disagree; --repair (the record's holder or the owner,
// --reason) writes the key from the record, logged with who and why
// (seat-key-follows-record.w2).
func (a *app) cmdSeat(args []string, stdout, stderr io.Writer) int {
	// seat login and seat logout are the seat's store login, kept on this machine and
	// never in the store (storelogin.go); seat push and seat pong are the seat's push
	// proof (pushproof.go)
	if len(args) > 0 {
		switch args[0] {
		case "login":
			return a.cmdSeatLogin(args[1:], stdout, stderr)
		case "logout":
			return a.cmdSeatLogout(args[1:], stdout, stderr)
		case "push":
			return a.cmdSeatPush(args[1:], stdout, stderr)
		case "pong":
			return a.cmdSeatPong(args[1:], stdout, stderr)
		}
	}
	fs, c := a.verbSetup("seat")
	repair := fs.Bool("repair", false, "write the coordinator key from the seat's record when they differ: the record's holder or the owner, with --reason; logged with who and why")
	reason := fs.String("reason", "", "with --repair, why the key is repaired, recorded in the log (required)")
	if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
		return refuse(stderr, "seat", argErr("takes no words ", err, pos...))
	}
	if *reason != "" && !*repair {
		return refuse(stderr, "seat", "--reason goes with --repair")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "seat", err.Error())
	}
	ctx := context.Background()
	if *repair {
		return a.seatRepair(ctx, st, *c, *reason, stdout, stderr)
	}
	s, err := st.SeatCheck(ctx)
	if err != nil {
		return a.readFailed("seat", err, stderr)
	}
	line := fmt.Sprintf("SEAT holder=%s epoch=%d generation=%d", orDashStr(s.Holder, "-"), s.Epoch, s.Generation)
	if s.Record != "" || s.Server != "" {
		line += fmt.Sprintf(" record=%s server=%s", oneline.Field(orDashStr(s.Record, s.Holder)), oneline.Field(orDashStr(s.Server, "-")))
	}
	facts := map[string]any{"holder": s.Holder, "epoch": s.Epoch, "generation": s.Generation, "record": s.Record, "server": s.Server}
	if s.Drift == "" {
		sayOK(stdout, c.json, "seat", line, facts)
		return 0
	}
	if c.json {
		facts["drift"], facts["status"], facts["exit"] = s.Drift, "drift", 1
		b, _ := json.Marshal(facts) // ignored: strings and numbers always encode
		fmt.Fprintln(stdout, string(b))
		return 1
	}
	fmt.Fprintln(stdout, line+" DRIFT "+oneline.Escape(s.Drift))
	return 1
}

// seatRepair is seat --repair: the coordinator key written from the seat's
// record by its holder or the owner, the log's line saying who and why.
func (a *app) seatRepair(ctx context.Context, st *store.Store, c common, reason string, stdout, stderr io.Writer) int {
	if c.actor == "" {
		return refuse(stderr, "seat", "--repair wants --actor <name> (or NOVA_SPRINT_ACTOR): the record's holder or the owner; nothing was changed")
	}
	owner, err := a.owner(ctx, st)
	if err != nil {
		return a.readFailed("seat", err, stderr)
	}
	was, err := st.B.Coordinator(ctx)
	if err != nil {
		return a.readFailed("seat", err, stderr)
	}
	step, why, err := st.SeatRepairStep(ctx, sprint.SeatRepairReq{Who: c.actor, Reason: reason, Owner: owner})
	if err != nil {
		return a.readFailed("seat", err, stderr)
	}
	if why != "" {
		return refuse(stderr, "seat", why)
	}
	step.CallerOp = c.op
	res, err := st.Run(ctx, step)
	if err != nil {
		fmt.Fprintf(stderr, "%s seat: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	if len(res.Refused) > 0 {
		return refuse(stderr, "seat", res.Refused[0].Why)
	}
	s, err := st.SeatCheck(ctx)
	if err != nil {
		return a.readFailed("seat", err, stderr)
	}
	sayOK(stdout, c.json, "seat", fmt.Sprintf("SEAT REPAIRED key=%s was=%s by=%s", oneline.Field(s.Holder), oneline.Field(orDashStr(was, "-")), oneline.Field(c.actor)),
		map[string]any{"holder": s.Holder, "was": was, "by": c.actor, "op": res.Op, "drift": s.Drift})
	if s.Drift != "" {
		fmt.Fprintln(stdout, "DRIFT "+oneline.Escape(s.Drift))
	}
	return 0
}

// cellText is a where view's cell as the text it was printed as; "" for a row field that
// is no string.
func cellText(cell any) string {
	s, _ := cell.(string)
	return s
}
