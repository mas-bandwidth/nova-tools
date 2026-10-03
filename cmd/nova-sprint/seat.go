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
	how := "given"
	if req.Take {
		how = "taken approved_by=" + oneline.Field(req.ApprovedBy)
	}
	said := fmt.Sprintf("holder=%s from=%s by=%s %s", oneline.Field(req.To), oneline.Field(holder), oneline.Field(c.actor), how)
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
	Machine   string          `json:"machine"`
	Summary   string          `json:"summary"`
	Streams   []streamCounts  `json:"streams"`
	Sentinels []sentinelView  `json:"sentinels"`
	Judgments []inboxJudgment `json:"judgments"`
	Members   []memberView    `json:"members"`
	Routes    routesView      `json:"routes"`
	Decisions []decisionView  `json:"decisions"`
	First     []string        `json:"first"`
	groups    []sprint.Group  // the open judgments, as inbox prints them
}

// seatView is the holder and the last change of the seat; Since is nil while
// the seat has not moved since init.
type seatView struct {
	Holder string             `json:"holder"`
	Since  *time.Time         `json:"since,omitempty"`
	Last   *sprint.SeatChange `json:"last,omitempty"`
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
// beside the seat's own changes, by the verb of the step that wrote the line
// (fleet down's is fleet hold): rework only with a fix.
var decisionVerbs = []string{"release", "drop", "needs", "fleet hold", "rework"}

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
	h.Seat.Holder, h.Machine, h.Summary = v.Coordinator, v.Machine, v.Summary
	if v.Seat != nil {
		h.Seat.Since, h.Seat.Last = &v.Seat.At, v.Seat
	}
	if h.Owner, err = a.owner(ctx, st); err != nil {
		return h, "", err
	}
	for _, s := range sortedKeys(v.Tables[sprint.Work]) {
		sc := streamCounts{Stream: s, Counts: map[string]int{}}
		for _, col := range sprint.States {
			var n int
			_, _ = fmt.Sscan(v.Tables[sprint.Work][s][string(col)], &n) // ignored: a cell that is no number counts 0
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
		if d.Verb == "fleet down" {
			heldBy[d.What] = d.By
		}
	}
	h.Decisions = h.Decisions[max(0, len(h.Decisions)-handoverDecisions):]
	for _, m := range sortedKeys(v.Tables[sprint.Fleet]) {
		status := v.Tables[sprint.Fleet][m][sprint.Status]
		switch status {
		case sprint.Held:
			h.Members = append(h.Members, memberView{Member: m, Status: status, By: heldBy[m]})
		case sprint.Down:
			h.Members = append(h.Members, memberView{Member: m, Status: status})
		}
	}
	h.First = []string{"nova-sprint where", "nova-sprint inbox --wait --push " + pushSeat, `read docs/SPEC-SPRINT.md, "Handing over the seat"`}
	return h, a.handoverText(h), nil
}

// decisionOf is the log line as one of the coordinator's decisions: a seat
// change, a release, a drop, a fleet down, or a rework with a fix.
func decisionOf(l sprint.Line) (decisionView, bool) {
	if n := l.Note; n != nil {
		if n.Type != sprint.NSeat && n.Type != sprint.NSeatTaken {
			return decisionView{}, false
		}
		return decisionView{At: l.At, Verb: n.Type, What: n.What, By: n.Who}, true
	}
	if l.Kind != sprint.LineMove || !slices.Contains(decisionVerbs, l.Verb) || (l.Verb == "rework" && l.Text["fix"] == "") {
		return decisionView{}, false
	}
	hold := l.Verb == "fleet hold"
	if hold != (l.Table == sprint.Fleet) || (!hold && l.Table != sprint.Work) {
		return decisionView{}, false // one line a decision: the member's, or the primaries'
	}
	what := l.Card
	if len(l.Cards) > 0 {
		what = sprint.Preview(l.Cards, ",")
	}
	if hold {
		return decisionView{At: l.At, Verb: "fleet down", What: strings.TrimPrefix(l.Card, sprint.CtlID("")), By: l.Actor}, true
	}
	reason := l.Text["reason"]
	if l.Verb == "rework" {
		reason = l.Text["fix"]
	}
	return decisionView{At: l.At, Verb: l.Verb, What: what, By: l.Actor, Reason: reason}, true
}

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
