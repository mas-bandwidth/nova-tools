package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
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
	holder, err := st.B.Coordinator(ctx)
	if err != nil {
		return a.readFailed("coordinator", err, stderr)
	}
	req := sprint.SeatReq{To: pos[0], Who: c.actor, Reason: *reason, Take: *take, ApprovedBy: *approved, Owner: owner}
	if why := sprint.NotSeat(holder, req); why != "" {
		return refuse(stderr, "coordinator", why)
	}
	// Every seat-changing operation wants the current seat's complete proof set.
	// A take's whole purpose is an away holder whose pushes are necessarily stale
	// (down or out of credits): only the next holder's judgment proof gates it.
	if holder != "" && !req.Take {
		if why, err := pushGate(ctx, st, holder, a.now()); err != nil {
			return a.readFailed("coordinator", err, stderr)
		} else if why != "" {
			return refuse(stderr, "coordinator", why)
		}
	}
	// The next holder proves its session before handover; its native observers
	// bind to the new seat generation after handover (SPEC-SPRINT section 8).
	if why, err := targetJudgmentGate(ctx, st, req.To, a.now()); err != nil {
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
	if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
		return refuse(stderr, "handover", argErr("takes no words ", err, pos...))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "handover", err.Error())
	}
	h, text, err := a.handover(context.Background(), st)
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

// handoverView is what the next seat needs, as handover --json carries it.
type handoverView struct {
	At        time.Time       `json:"at"`
	Seat      seatView        `json:"seat"`
	Owner     string          `json:"owner,omitempty"`
	Server    *serverView     `json:"server,omitempty"`
	Machine   string          `json:"machine"`
	Summary   string          `json:"summary"`
	Streams   []streamCounts  `json:"streams"`
	Sentinels []sentinelView  `json:"sentinels"`
	Judgments []inboxJudgment `json:"judgments"`
	Members   []memberView    `json:"members"`
	Routes    routesView      `json:"routes"`
	Decisions []decisionView  `json:"decisions"`
	Rules     []string        `json:"rules"`
	First     []string        `json:"first"`
	groups    []sprint.Group  // the open judgments, as inbox prints them
}

// seatView is the holder, the seat's generation and the last change of the
// seat; Since is nil while the seat has not moved since init.
type seatView struct {
	Holder     string             `json:"holder"`
	Generation uint64             `json:"generation"`
	Since      *time.Time         `json:"since,omitempty"`
	Last       *sprint.SeatChange `json:"last,omitempty"`
}

// serverView is the actor the server runs as, by its record, and when it is
// not the seat's holder, the line of its unit to change (sprint.SeatDrift).
type serverView struct {
	Actor  string `json:"actor"`
	Change string `json:"change,omitempty"`
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

// handover reads what the next seat needs and renders it: the holder and since
// when, the machine and the progress, each stream's counts, the sentinels held
// with what waits behind each, every open judgment with its answer lines, the
// members held or down and by whom, the routes disabled, the last decisions
// from the log with their reasons, and the lines the next seat runs first.
func (a *app) handover(ctx context.Context, st *store.Store) (handoverView, string, error) {
	now := a.now()
	h := handoverView{At: now, Streams: []streamCounts{}, Sentinels: []sentinelView{}, Judgments: []inboxJudgment{}, Members: []memberView{},
		Decisions: []decisionView{}, Routes: routesView{Disabled: []string{}}}
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
	server, err := st.ServerActor(ctx)
	if err != nil {
		return h, "", err
	}
	if server != "" {
		holder := h.Seat.Holder
		if v.Seat != nil {
			holder = v.Seat.Holder // the record's: the key follows it
		}
		h.Server = &serverView{Actor: server, Change: sprint.SeatDrift(holder, holder, server)}
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
	h.Rules = []string{handoverWaves}
	h.First = []string{"nova-sprint where", "nova-sprint inbox --wait --push " + pushSeat, `read docs/SPEC-SPRINT.md, "Handing over the seat"`}
	return h, a.handoverText(h), nil
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
	if h.Server != nil && h.Server.Change != "" {
		line("SERVER %s", h.Server.Change)
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
	for _, f := range h.First {
		line("FIRST %s", f)
	}
	line("HANDOVER OK judgments=%d sentinels=%d members=%d decisions=%d", len(h.Judgments), len(h.Sentinels), len(h.Members), len(h.Decisions))
	return b.String()
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

// seatInbox is a name's inbox for pushed judgments, inbox/sprint-judgments in
// its working directory (rowDir, its nova-config friend row's dir; else
// ~/<name>-working, said once on note: frienddir.go), and the inbox it is
// under; ok false when that inbox is not there.
func (a *app) seatInbox(name, rowDir string, note io.Writer) (dir, parent string, ok bool) {
	if name == "" {
		return "", "", false
	}
	parent = filepath.Join(a.friendDir(name, rowDir, note), "inbox")
	info, err := os.Stat(parent)
	return filepath.Join(parent, "sprint-judgments"), parent, err == nil && info.IsDir()
}

// pushTarget is where inbox --wait --push writes each note: one directory, or,
// following the seat (--push seat), the holder's inbox, and a note addressed to
// someone with an inbox there, theirs. Each directory's files are its cursor.
type pushTarget struct {
	a         *app
	fixed     string                     // --push <dir>; "" follows the seat
	seen      map[string]map[string]bool // each directory's keys, read once
	holder    string                     // the holder last pushed for
	dirs      map[string]string          // the friend rows' dirs, read again at every look (friendRowDirs)
	note      io.Writer                  // where a fallback to ~/<name>-working is said once
	followErr error                      // a friend-row read failure, surfaced by unseen in the wait callback
}

// inbox is seatInbox for name, from the friend rows' dirs as read at the last look.
func (p *pushTarget) inbox(name string) (dir, parent string, ok bool) {
	return p.a.seatInbox(name, p.dirs[name], p.note)
}

// dirOf is the directory the group is written to with the seat held by holder,
// "" when there is none to write it to now.
func (p *pushTarget) dirOf(holder string, g sprint.Group) string {
	if p.fixed != "" {
		return p.fixed
	}
	if g.To != "" && g.To != holder {
		if dir, _, ok := p.inbox(g.To); ok {
			return dir
		}
	}
	if dir, _, ok := p.inbox(holder); ok {
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
	if p.followErr != nil {
		return nil, p.followErr
	}
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

// follow says where a loop following the seat writes, from the friend rows read
// at this look: a line when the holder or her directory moved, and whether the
// holder has an inbox. A loop started for a holder with none is refused (exit 2,
// naming it).
func (p *pushTarget) follow(holder string, first bool, stdout, stderr io.Writer) int {
	if p.fixed != "" {
		return 0
	}
	dirs, err := p.a.friendRowDirs(context.Background())
	if err != nil {
		code := p.a.readFailed("inbox --push", fmt.Errorf("nova-config friend rows cannot be read: %w", err), stderr)
		p.followErr = &exitErr{code: code}
		return code
	}
	moved := holder != p.holder
	if !moved {
		moved = dirs[holder] != p.dirs[holder]
	}
	p.holder, p.dirs, p.note, p.followErr = holder, dirs, stderr, nil
	dir, parent, ok := p.inbox(holder)
	switch {
	case first && !ok:
		return refuse(stderr, "inbox", fmt.Sprintf("--push seat writes to the holder's inbox, %s, and %s is not there: make it, or give --push <dir>", dir, parent))
	case !ok:
		fmt.Fprintf(stdout, "NOTE the seat is %s's, and %s is not there: nothing is pushed until it is\n", oneline.Field(holder), oneline.Field(parent))
	case !first && moved:
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
		case "deliver":
			return a.cmdSeatDeliver(args[1:], stdout, stderr)
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
	now := time.Now()
	if st.Now != nil {
		now = st.Now()
	}
	friends, err := seatFriendLines(ctx, st, now)
	if err != nil {
		return a.readFailed("seat", err, stderr)
	}
	if friends.text != "" {
		facts["friends"] = friends.rows
		if len(friends.alarms) > 0 {
			facts["alarms"] = friends.alarms
		}
	}
	if s.Drift == "" {
		if friends.text != "" {
			line += "\n" + friends.text
		}
		sayOK(stdout, c.json, "seat", line, facts)
		return 0
	}
	if c.json {
		facts["drift"], facts["status"], facts["exit"] = s.Drift, "drift", 1
		b, _ := json.Marshal(facts) // ignored: strings and numbers always encode
		fmt.Fprintln(stdout, string(b))
		return 1
	}
	line += " DRIFT " + oneline.Escape(s.Drift)
	if friends.text != "" {
		line += "\n" + friends.text
	}
	fmt.Fprintln(stdout, line)
	return 1
}

// seatFriendReport is one friend's daemon as nova-sprint seat prints it
// (docs/SPEC-SPRINT.md, daemon-supervised-r-b.w7).
type seatFriendReport struct {
	text   string
	rows   []map[string]any
	alarms []string
}

// seatFriendLines is one FRIEND line per friend of the roster, and one ALARM
// line per condition: her daemon stamp differs from the newest stamp any
// friend beats, or her last beat is older than the beat's down bound
// (Beat.Alive). The newest stamp is the one on the most recent beat, never the
// greatest stamp on the roster: a friend who beats last runs the newest binary,
// whatever its stamp sorts as (a reader's finding, daemon-supervised-r-b.w7).
// An empty stamp is unknown, not a drift, so a friend whose beat carries none
// is never alarmed for one, and neither is anyone while the most recent beat
// carries none. No friends: empty, so the seat line stays the one line it was.
func seatFriendLines(ctx context.Context, st *store.Store, now time.Time) (seatFriendReport, error) {
	beats, err := st.FriendBeats(ctx)
	if err != nil || len(beats) == 0 {
		return seatFriendReport{}, err
	}
	names := slices.Sorted(maps.Keys(beats))
	newest := ""
	var newestAt time.Time
	for _, name := range names {
		beat := beats[name]
		if beat.Beaten() && (newestAt.IsZero() || beat.At.After(newestAt)) {
			newestAt, newest = beat.At, beatDaemonVersion(beat)
		}
	}
	bound := (sprint.MissedBeatsDown * sprint.BeatDeadline).String()
	var b strings.Builder
	var rows []map[string]any
	var alarms []string
	for _, name := range names {
		beat := beats[name]
		version := beatDaemonVersion(beat)
		age := "-"
		if beat.Beaten() {
			age = now.Sub(beat.At).Round(time.Second).String()
		}
		shown := "-"
		if version != "" {
			shown = oneline.Field(version)
		}
		fmt.Fprintf(&b, "FRIEND friend=%s daemon_version=%s last_beat_age=%s\n", oneline.Field(name), shown, age)
		row := map[string]any{"friend": name, "daemon_version": version, "last_beat_age": age}
		if version != "" && newest != "" && version != newest {
			line := fmt.Sprintf("ALARM friend=%s version drift: daemon_version=%s newest=%s", oneline.Field(name), shown, oneline.Field(newest))
			fmt.Fprintln(&b, line)
			alarms = append(alarms, line)
			row["drift"] = true
		}
		if !beat.Alive(now) {
			why := "no beat"
			if beat.Beaten() {
				why = "last beat older than " + bound
			}
			line := fmt.Sprintf("ALARM friend=%s daemon down: %s", oneline.Field(name), why)
			fmt.Fprintln(&b, line)
			alarms = append(alarms, line)
			row["down"] = true
		}
		rows = append(rows, row)
	}
	text := strings.TrimSuffix(b.String(), "\n")
	return seatFriendReport{text: text, rows: rows, alarms: alarms}, nil
}

// beatDaemonVersion is the build stamp a friend's last beat carried
// (sprint.FriendReport.DaemonVersion, friend beat --daemon-version); empty when
// the beat carried none (her daemon never said, or she never beat).
func beatDaemonVersion(beat sprint.Beat) string {
	if beat.Friend == nil {
		return ""
	}
	return beat.Friend.DaemonVersion
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

// The seat's record (coordinator-config-through-the-seat; the owner, 2026-10-05: every
// step the coordinator did by hand is a missing instruction): what seat install learned
// of this machine's seat that a cold coordinator's environment does not hold, beside the
// store login (storelogin.go). It names the sprint's server, which seat check measures
// when NOVA_SPRINT_SERVER is not set, and the nova-config seat whose row seat install
// wrote into nova-config's seats.tsv, which nova-config --seat and seat check read. It
// holds no secret: the row names the variable of the password, and nova-config reads
// that password through the store login's nova-secrets seat.

// seatRecordFile is the record's name, in the store login's directory.
const seatRecordFile = "seat.json"

// configTool is the tool whose seats.tsv seat install writes the config seat's row into.
const configTool = "nova-config"

type seatRecord struct {
	Server     string `json:"server,omitempty"`
	ConfigSeat string `json:"config_seat,omitempty"`
}

// seatRecordPath is the record's file, beside the store login's.
func (a *app) seatRecordPath() (string, error) {
	loginFile := a.loginFile
	if loginFile == nil {
		loginFile = a.defaultLoginFile
	}
	p, err := loginFile()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(p), seatRecordFile), nil
}

// recordedSeat is the record seat install wrote; ok false when there is none, or the app
// has the store login off (a test's app, as recordedLogin).
func (a *app) recordedSeat() (seatRecord, bool, error) {
	if a.loginFile == nil {
		return seatRecord{}, false, nil
	}
	path, err := a.seatRecordPath()
	if err != nil {
		return seatRecord{}, false, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return seatRecord{}, false, nil
	}
	if err != nil {
		return seatRecord{}, false, fmt.Errorf("the seat record %s is unreadable: %v; run: nova-sprint seat install", path, err)
	}
	var r seatRecord
	if err := json.Unmarshal(b, &r); err != nil {
		return seatRecord{}, false, fmt.Errorf("the seat record %s is not a seat record: %v; run: nova-sprint seat install", path, err)
	}
	return r, true, nil
}

// seatServer is the sprint's server the seat's record names, "" when none.
func (a *app) seatServer() string {
	r, ok, err := a.recordedSeat()
	if err != nil || !ok { // ignored: an unreadable record is the seat check's config line, DOWN with its remedy (withConfigSeat)
		return ""
	}
	return r.Server
}

// configSeatFlags are seat install's flags for the nova-config seat profile.
type configSeatFlags struct{ seat, dsn, passwordEnv *string }

func addConfigSeatFlags(fs flagSet) configSeatFlags {
	return configSeatFlags{
		seat:        fs.String("config-seat", "", "the `name` of the nova-config seat profile written into "+configTool+"'s "+seatcred.ProfileFile+" (nova-config --seat <name> reads it); wants --config-dsn and --config-password-env"),
		dsn:         fs.String("config-dsn", "", "the config store's PostgreSQL `dsn`, postgres://user@host:port/db with no password"),
		passwordEnv: fs.String("config-password-env", "", "the `NAME` of the config store's password: the variable nova-config reads it from, else the key of the store login's nova-secrets seat (nova-sprint seat login) it is read from in process"),
	}
}

// profile is the row the flags name, ok false when they name none; a half-named or bad
// row is refused, quoting no password.
func (f configSeatFlags) profile() (seatcred.ConfigProfile, bool, error) {
	p := seatcred.ConfigProfile{Name: strings.TrimSpace(*f.seat), DSN: strings.TrimSpace(*f.dsn), PasswordEnv: strings.TrimSpace(*f.passwordEnv)}
	if p.Name+p.DSN+p.PasswordEnv == "" {
		return p, false, nil
	}
	var missing []string
	for _, m := range []struct{ v, flag string }{{p.Name, "--config-seat <name>"}, {p.DSN, "--config-dsn <dsn>"}, {p.PasswordEnv, "--config-password-env <NAME>"}} {
		if m.v == "" {
			missing = append(missing, m.flag)
		}
	}
	if len(missing) > 0 {
		return p, false, errors.New("the nova-config seat profile wants " + strings.Join(missing, ", ") + "; nothing was written")
	}
	if !secrets.IsValidAsName(p.Name) {
		return p, false, errors.New("--config-seat " + oneline.Field(p.Name) + " must match [A-Za-z0-9_-]+; nothing was written")
	}
	if !envName.MatchString(p.PasswordEnv) {
		return p, false, errors.New("--config-password-env must name a variable, [A-Z_][A-Z0-9_]*; nothing was written")
	}
	if strings.ContainsAny(p.DSN, "\t\n") {
		return p, false, errors.New("--config-dsn holds a tab or a newline; nothing was written")
	}
	if _, err := config.ResolveDSN(p.DSN, func(string) string { return "" }); err != nil {
		return p, false, fmt.Errorf("--config-dsn: %v; nothing was written", err) // ResolveDSN's refusals quote no password
	}
	return p, true, nil
}

var envName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// configProfilePath is nova-config's seats.tsv, where nova-config --seat reads it.
func (a *app) configProfilePath() (string, error) {
	return seatcred.ProfilePath(configTool, func(k string) string {
		if k == "HOME" {
			if h, err := a.home(); err == nil {
				return h
			}
		}
		return a.getenv(k)
	})
}

// writeConfigProfile writes p's row into the seats.tsv at path in place of the seat's row
// there, keeping every other line, for its user alone and whole or not at all; the file
// is read back as nova-config reads it before it replaces the old one.
func writeConfigProfile(path string, p seatcred.ConfigProfile) error {
	row := p.Name + "\t" + p.DSN + "\t" + p.PasswordEnv
	var lines []string
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	placed := false
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		name, _, _ := strings.Cut(strings.TrimSpace(l), "\t")
		switch {
		case l == "" && len(b) == 0:
			continue
		case name == p.Name && !strings.HasPrefix(strings.TrimSpace(l), "#"):
			if !placed {
				lines, placed = append(lines, row), true
			}
		default:
			lines = append(lines, l)
		}
	}
	if !placed {
		lines = append(lines, row)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".seats-*.tsv")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // ignored: gone after the rename; a failed write leaves nothing behind
	_, werr := f.WriteString(strings.Join(lines, "\n") + "\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return werr
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	if got, err := seatcred.LoadConfigProfile(tmp, p.Name); err != nil || got != p {
		return fmt.Errorf("the row written does not read back as nova-config reads it: %v", err)
	}
	return os.Rename(tmp, path)
}

// installSeat is seat install's part beyond the unit: the nova-config seat profile and
// the seat's record. server is the server the unit was given, "" for none.
func (a *app) installSeat(f configSeatFlags, server string, dry bool, stdout io.Writer) error {
	p, named, err := f.profile()
	if err != nil {
		return err
	}
	if !named && server == "" {
		return nil
	}
	recPath, err := a.seatRecordPath()
	if err != nil {
		return fmt.Errorf("the seat record has no place: %v", err)
	}
	rec, _, err := a.recordedSeat()
	if err != nil && !dry {
		return err
	}
	if server != "" {
		rec.Server = server
	}
	var profPath string
	if named {
		if profPath, err = a.configProfilePath(); err != nil {
			return err
		}
		rec.ConfigSeat = p.Name
	}
	said := fmt.Sprintf("seat=%s dsn=%s password-env=%s profile=%s", oneline.Field(rec.ConfigSeat), oneline.Field(p.DSN), oneline.Field(p.PasswordEnv), oneline.Field(profPath))
	if dry {
		if named {
			fmt.Fprintf(stdout, "SEAT CONFIG DRY-RUN %s; nothing was written\n", said)
		}
		fmt.Fprintf(stdout, "SEAT RECORD DRY-RUN file=%s server=%s; nothing was written\n", oneline.Field(recPath), oneline.Field(orDashStr(rec.Server, "-")))
		return nil
	}
	if named {
		if err := writeConfigProfile(profPath, p); err != nil {
			return fmt.Errorf("the nova-config seat profile %s was not written: %v", profPath, err)
		}
	}
	b, _ := json.MarshalIndent(rec, "", "  ") // ignored: a struct of strings always encodes
	if err := writePrivate(recPath, append(b, '\n')); err != nil {
		return fmt.Errorf("the seat record %s was not written: %v", recPath, err)
	}
	if named {
		fmt.Fprintf(stdout, "SEAT CONFIG OK %s server=%s%s\n", said, oneline.Field(orDashStr(rec.Server, "-")), a.configPasswordNote(p))
	}
	fmt.Fprintf(stdout, "SEAT RECORD OK file=%s server=%s config-seat=%s\n", oneline.Field(recPath), oneline.Field(orDashStr(rec.Server, "-")), oneline.Field(orDashStr(rec.ConfigSeat, "-")))
	return nil
}

// configPasswordNote says where nova-config --seat will find the config seat's
// password: the environment, the store login's nova-secrets seat (read here and
// dropped), or nowhere yet, with the remedy.
func (a *app) configPasswordNote(p seatcred.ConfigProfile) string {
	if a.getenv(p.PasswordEnv) != "" {
		return " password=env"
	}
	l, ok, err := a.recordedLogin()
	if err != nil || !ok {
		return " password=unresolved NOTE no store login names a nova-secrets seat to read " + p.PasswordEnv + " from; run: nova-sprint seat login"
	}
	sl := l.secrets()
	sl.Name = p.PasswordEnv
	if s, err := a.secretReader()(sl); err != nil || !s.Loaded() || s.Empty() {
		return " password=unresolved NOTE seat " + oneline.Field(l.As) + " of " + oneline.Field(l.Store) + " holds no " + p.PasswordEnv + "; seal it with nova-secrets seal --as " + oneline.Field(l.As) + " --name " + p.PasswordEnv
	}
	return " password=secrets"
}

// withConfigSeat adds the config seat's line to the seat check when the seat's record
// names one: OK with the row nova-config --seat reads, else DOWN with the remedy.
func (a *app) withConfigSeat(r sprint.SeatCheckReport) sprint.SeatCheckReport {
	rec, ok, err := a.recordedSeat()
	if (!ok && err == nil) || (ok && rec.ConfigSeat == "") {
		return r
	}
	l := sprint.SeatCheckLine{Thing: "config"}
	if err != nil {
		l.Facts, l.Remedy = []string{"why=" + strconv.Quote(err.Error())}, "nova-sprint seat install"
	} else if path, perr := a.configProfilePath(); perr != nil {
		l.Facts, l.Remedy = []string{"seat=" + oneline.Field(rec.ConfigSeat), "why=" + strconv.Quote(perr.Error())}, "nova-sprint seat install"
	} else if p, perr := seatcred.LoadConfigProfile(path, rec.ConfigSeat); perr != nil {
		l.Facts = []string{"seat=" + oneline.Field(rec.ConfigSeat), "profile=" + oneline.Field(path), "why=" + strconv.Quote(perr.Error())}
		l.Remedy = "nova-sprint seat install --config-seat " + rec.ConfigSeat + " --config-dsn <dsn> --config-password-env <NAME>"
	} else {
		l.Up = true
		l.Facts = []string{"seat=" + oneline.Field(p.Name), "dsn=" + oneline.Field(p.DSN), "password-env=" + oneline.Field(orDashStr(p.PasswordEnv, "-")), "profile=" + oneline.Field(path)}
	}
	r.Lines = append(r.Lines, l)
	if !l.Up {
		r.Down++
		r.ExitCode = 1
	}
	return r
}
