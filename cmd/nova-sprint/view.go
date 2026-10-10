package main

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// ROLE VIEWS (the owner, 2026-10-04: "i'd rather you hit this vs. hitting my dashboard which
// is for human eyes", and "it will save $$$ if the data is served to you better";
// ideas#852; docs/SPEC-SPRINT.md section 11, "Role views"). A view is one
// document of what one role needs to act on now, for a model to read every few minutes:
//
//   - view coordinator: everything that needs the seat, ranked by the cards behind it (open
//     judgments, notes addressed to the coordinator, alarms, sentinels reached, friends and
//     machines that need a look), each item with next, the exact command that acts on it;
//   - view worker --as <member|friend>: my cards in order with their briefs, bases, paths and
//     deadlines, what is mine to do next, my results not landed, the notes on my cards.
//
// Both are reads: they write nothing and need no actor. They are compact for the tokens a
// model pays to read them: short keys, the items that need action only (every friend's and
// machine's row with --all), counts where a count is enough, a one-line summary first, and
// --since <cursor>, which leaves out every item the read that printed the cursor showed
// unchanged. The server serves them read-only as GET /api/view/coordinator and
// /api/view/worker?as=<name> (serve.go).

func init() {
	verbClasses["view coordinator"] = classRead
	verbClasses["view worker"] = classRead
}

// viewSchema is the version of both views' JSON: a change that renames or removes a field, or
// changes what one means, is a new schema.
const viewSchema = 1

const (
	// viewWindow is the recent window the views count in: landed and finished in the last 30m.
	viewWindow = 30 * time.Minute
	// viewBacklogAge is how long the oldest result waits in review or merging before the
	// backlog is an alarm.
	viewBacklogAge = 30 * time.Minute
	// viewStaleReport is how long a friend holding cards goes without a beat before her row
	// needs a look.
	viewStaleReport = 15 * time.Minute
	// viewTextLines is the most lines the text form prints: the summary, then the items.
	viewTextLines = 20
	// viewNextMax is the longest next an item carries whole; a judgment's first command
	// longer than this (a group's --answers of hundreds of notes) is given as the inbox
	// read of its group, which prints every decision whole.
	viewNextMax = 300
	// viewWhatMax bounds an item's text.
	viewWhatMax = 160
	// viewWaitMax bounds a worker's results not landed listed one by one.
	viewWaitMax = 20
)

// The item types, one letter each.
const (
	itemJudgment = "j" // an open judgment of the inbox
	itemRequest  = "r" // a note addressed to the coordinator
	itemAlarm    = "a" // an effect on the cards the coordinator acts on
	itemSentinel = "s" // a sentinel whose needs have landed, no judgment open on it
	itemFriend   = "f" // a friend whose row needs a look
	itemMachine  = "m" // a machine whose row needs a look
)

// itemRank orders items of equal weight: judgments first, then what the seat is asked, the
// alarms, the sentinels, the friends and the machines.
var itemRank = map[string]int{itemJudgment: 0, itemRequest: 1, itemAlarm: 2, itemSentinel: 3, itemFriend: 4, itemMachine: 5}

// viewItem is one thing that needs the seat now.
type viewItem struct {
	K    string `json:"k"`             // its key, stable while it stands: what --since compares
	T    string `json:"t"`             // its type, one letter (itemJudgment ...)
	W    string `json:"w,omitempty"`   // what kind: the judgment's type, the alarm's name
	B    int    `json:"b,omitempty"`   // the cards behind it: what the items are ranked by
	N    int    `json:"n,omitempty"`   // the cards it names (a judgment's size)
	Age  string `json:"age,omitempty"` // how long it has stood (never part of --since's compare)
	OD   bool   `json:"od,omitempty"`  // an overdue judgment
	D    string `json:"d,omitempty"`   // a judgment's decisions, | separated
	S    string `json:"s,omitempty"`   // what it is, one line
	Next string `json:"next"`          // the exact command that acts on it
	age  time.Duration
}

// viewRow is a friend's or a machine's row, every row with --all.
type viewRow struct {
	K   string `json:"k"`             // f:<friend> or m:<machine>
	St  string `json:"st"`            // up, down, held
	R   int    `json:"r"`             // ready on the row
	W   int    `json:"w"`             // working on the row
	Wd  int    `json:"wd"`            // the row's width
	F30 int    `json:"f30"`           // finished in the last 30m
	Rep string `json:"rep,omitempty"` // how long since its last beat; "never"
	Run string `json:"run,omitempty"` // a friend's live lane that kept a judgment quiet: "running 32m of 90m"
}

// coordCounts are the sprint's counts, always carried: the work table's primaries by state,
// landed in the last 30m, the held cards, and the machines' width up and cards working.
type coordCounts struct {
	Landed  int `json:"landed"`
	L30     int `json:"l30"`
	All     int `json:"all"`
	Waiting int `json:"wait"`
	Ready   int `json:"ready"`
	Working int `json:"work"`
	Review  int `json:"review"`
	Merging int `json:"merge"`
	Held    int `json:"held,omitempty"`
	Width   int `json:"width"` // the up machines' width
	Busy    int `json:"busy"`  // the cards working on up machines
	J       int `json:"j"`     // the open judgments
	// Rules is the cards a rule answered in the last hour (sprint.RuleAnsweredWithin): the
	// judgments the coordinator did not have to answer.
	Rules int `json:"rules"`
	// Suppressed is the judgments the tick's lane check kept from rising since the epoch
	// began (store.Heartbeat.Suppressed), and By its three causes beside it: the coordinator
	// turns the checks saved.
	Suppressed int           `json:"suppressed"`
	By         suppressedWhy `json:"by"`
}

// suppressedWhy is the suppressed count by cause: a friend's lane live inside its cap (a
// lateness, a stall, finishes none), readers busy (readers behind), a read tier question the
// rule answered.
type suppressedWhy struct {
	Lane    int `json:"lane"`
	Readers int `json:"readers"`
	Tier    int `json:"tier"`
}

// coordinatorView is view coordinator's document, schema 1.
type coordinatorView struct {
	View   string    `json:"view"`
	Schema int       `json:"schema"`
	Sum    string    `json:"sum"`
	At     time.Time `json:"at"`
	Epoch  uint64    `json:"epoch"`
	Seat   string    `json:"seat,omitempty"`
	Push   string    `json:"push,omitempty"` // the holder's push: adapter=<a> proven=<RFC3339|->
	// Fleet and Friends are the work switches, carried only when off (nova-sprint set
	// --fleet off, --friends off): the deal hands that side no work card.
	Fleet   string `json:"fleet,omitempty"`
	Friends string `json:"friends,omitempty"`
	// FleetTiers and FriendsTiers are the tiers each side may take, carried only when set
	// (nova-sprint set --fleet-tiers, --friends-tiers); absent is all.
	FleetTiers   []string    `json:"fleet_tiers,omitempty"`
	FriendsTiers []string    `json:"friends_tiers,omitempty"`
	Cursor       string      `json:"cursor"`
	N            coordCounts `json:"n"`
	Items        []viewItem  `json:"items"`
	Rows         []viewRow   `json:"rows,omitempty"`
	Same         int         `json:"same,omitempty"` // with --since: items left out, unchanged
	Gone         int         `json:"gone,omitempty"` // with --since: items the cursor's read showed that stand no more
}

// workerCard is one of a worker's cards.
type workerCard struct {
	ID    string    `json:"id"`
	P     string    `json:"p"`               // its primary
	St    string    `json:"st"`              // ready or working
	Brief string    `json:"brief"`           // where its brief is: a friend's BRIEF.md, a member's card command
	Base  string    `json:"base,omitempty"`  // BASE:
	Paths []string  `json:"paths,omitempty"` // PATHS:
	DL    time.Time `json:"dl,omitzero"`     // when its deadline falls by the clock
	Att   int       `json:"att"`
	Gen   int       `json:"gen"`
	Br    string    `json:"br,omitempty"`    // the branch its work is pushed to
	Notes []string  `json:"notes,omitempty"` // the coordinator's words on it: why, the finding, the fix, its notes
	J     []string  `json:"j,omitempty"`     // the kinds of the judgments open on it
}

// waitCard is one of a worker's results that has not landed: the work card it finished ok,
// its primary's state now, and how long since it finished.
type waitCard struct {
	ID  string `json:"id"`
	P   string `json:"p"`
	St  string `json:"st"`
	Age string `json:"age"`
}

// workerView is view worker's document, schema 1.
type workerView struct {
	View     string       `json:"view"`
	Schema   int          `json:"schema"`
	Sum      string       `json:"sum"`
	At       time.Time    `json:"at"`
	Epoch    uint64       `json:"epoch"`
	As       string       `json:"as"`
	Kind     string       `json:"kind"` // member or friend
	Dir      string       `json:"-"`    // a friend's working directory, her row's (store.FriendDirs); "" is ~/<name>-working
	NovaRoot string       `json:"-"`    // the machine row's nova_root, so a friend's fallback working directory and brief path sit under it
	Cursor   string       `json:"cursor"`
	Next     string       `json:"next,omitempty"`
	Quiet    []string     `json:"quiet,omitempty"` // a QUIET line per machine quiet now (fleet quiet)
	Cards    []workerCard `json:"cards"`
	Wait     []waitCard   `json:"wait,omitempty"`
	NWait    int          `json:"nwait,omitempty"` // every result not landed, when more than are listed
	Same     int          `json:"same,omitempty"`
	Gone     int          `json:"gone,omitempty"`
}

func (a *app) cmdViewCoordinator(args []string, stdout, stderr io.Writer) int {
	const name = "view coordinator"
	fs, c := a.verbSetup(name)
	all := fs.Bool("all", false, "every friend's and machine's row too, as rows (by default only the rows that need a look, as items)")
	since := fs.String("since", "", "the cursor an earlier view printed: leave out every item it showed that has not changed, and count them (same) and the ones that stand no more (gone)")
	needs := fs.Bool("needs", false, "instead, list every decision waiting on the coordinator (open judgments, held sentinels, stopped streams, held cards), ranked by the cards blocked behind each, ties by age, each with its evidence; takes no --all or --since")
	if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
		return refuse(stderr, name, argErr("takes no words ", err, pos...))
	}
	if *needs && (*all || *since != "") {
		return refuse(stderr, name, "--needs lists the decisions only and takes neither --all nor --since; run: nova-sprint view coordinator --needs [--json]")
	}
	seen, err := parseCursor(*since)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if *needs {
		v, err := a.readCoordNeeds(context.Background(), st)
		if err != nil {
			return a.readFailed(name, err, stderr)
		}
		if c.json {
			viewJSON(stdout, v)
			return 0
		}
		fmt.Fprint(stdout, coordNeedsText(v))
		return 0
	}
	v, err := a.coordinatorView(context.Background(), st, *all)
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	v.Items, v.Same, v.Gone = sinceItems(v.Items, seen)
	v.Rows, _, _ = sinceRows(v.Rows, seen)
	if c.json {
		viewJSON(stdout, v)
		return 0
	}
	fmt.Fprint(stdout, coordinatorText(v))
	return 0
}

func (a *app) cmdViewWorker(args []string, stdout, stderr io.Writer) int {
	const name = "view worker"
	fs, c := a.verbSetup(name)
	as := fs.String("as", "", "the fleet member or the friend whose view it is")
	since := fs.String("since", "", "the cursor an earlier view printed: leave out every card it showed that has not changed")
	if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
		return refuse(stderr, name, argErr("takes no words ", err, pos...))
	}
	if !sprint.ValidID(*as) {
		return refuse(stderr, name, "--as <member|friend> names the worker whose view it is (letters, digits, _ and -)")
	}
	seen, err := parseCursor(*since)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	v, ok, err := a.workerView(context.Background(), st, *as)
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	if !ok {
		fmt.Fprintf(stderr, "%s %s: %s is no fleet member and no friend of the sprint; a reader's cards are its queue: nova-sprint queue --as %s\n", prog, name, oneline.Escape(*as), oneline.Escape(*as))
		return 1
	}
	sinceWorker(&v, seen)
	if c.json {
		viewJSON(stdout, v)
		return 0
	}
	fmt.Fprint(stdout, workerText(v))
	return 0
}

// coordinatorView reads what needs the seat: one read of the work, merge and fleet tables,
// the inbox, the friends' rows, the machines' beats and the machine's record, all at one
// epoch. With all, every friend's and machine's row is carried too.
func (a *app) coordinatorView(ctx context.Context, st *store.Store, all bool) (coordinatorView, error) {
	now := a.now()
	v := coordinatorView{View: "coordinator", Schema: viewSchema, At: now.UTC().Truncate(time.Second), Items: []viewItem{}}
	// the holder's push, read where seat push writes it: the store as given, never an epoch's
	holder, err := st.B.Coordinator(ctx)
	if err != nil {
		return v, err
	}
	if v.Push, err = pushSaid(ctx, st, holder, now); err != nil {
		return v, err
	}
	st, err = st.Pinned(ctx)
	if err != nil {
		return v, err
	}
	v.Epoch = st.PinnedEpoch()
	s, err := st.Load(ctx, []string{sprint.Work, sprint.Merge, sprint.Fleet}, nil)
	if err != nil {
		return v, err
	}
	in, err := st.Inbox(ctx, defaultDeadline, defaultStale, 10000)
	if err != nil {
		return v, err
	}
	friends, err := st.FriendRows(ctx, now)
	if err != nil {
		return v, err
	}
	members := s.Members()
	beats, err := st.Beats(ctx, members)
	if err != nil {
		return v, err
	}
	if v.Seat, err = st.B.Coordinator(ctx); err != nil {
		return v, err
	}

	machine, hb, merr := st.Machine(ctx) // a store that keeps no machine record: the machine's alarm is not drawn
	within := func(stamp string) bool {
		t, err := time.Parse(time.RFC3339, stamp)
		return err == nil && !t.Before(now.Add(-viewWindow)) && !t.After(now)
	}

	// the counts: the work table's primaries by state (sentinels aside), and the recent landings
	n := &v.N
	for _, stream := range s.Streams() {
		for _, col := range sprint.States {
			for _, c := range s.Work.Cell(stream, col) {
				if sprint.IsSentinel(c) {
					continue
				}
				n.All++
				switch col {
				case sprint.Waiting:
					n.Waiting++
				case sprint.Ready:
					n.Ready++
				case sprint.Working:
					n.Working++
				case sprint.Review:
					n.Review++
				case sprint.Merging:
					n.Merging++
				case sprint.Landed:
					n.Landed++
					if within(c.F("landed")) {
						n.L30++
					}
				}
			}
		}
	}
	n.Held = sprint.HeldBack(s)
	for _, k := range sprint.RuleAnsweredWithin(append(s.Work.Cards(), s.Fleet.Cards()...), now, time.Hour) {
		n.Rules += k
	}
	runs := map[string]string{} // friend: her live lane as the last tick's lane check read it
	if sup := hb.Suppressed; merr == nil && sup.Epoch == v.Epoch {
		// a count of an earlier epoch is not this one's: the first tick after a clear starts it again
		n.Suppressed, n.By = sup.N, suppressedWhy{Lane: sup.Lane, Readers: sup.Readers, Tier: sup.Tier}
		for _, q := range hb.Quiet {
			if q.Friend != "" && runs[q.Friend] == "" {
				runs[q.Friend] = q.Run
			}
		}
	}
	finished := func(row string) int {
		f := 0
		for _, c := range append(s.Fleet.Cell(row, sprint.DoneOK), s.Fleet.Cell(row, sprint.DoneFailed)...) {
			if within(c.F("finished")) {
				f++
			}
		}
		return f
	}

	// the inbox: open judgments, and the notes addressed to the coordinator
	behindOf := map[string]bool{} // cards an open judgment names: a sentinel among them has its item there
	asks, newest := map[string]int{}, map[string]time.Time{}
	for _, g := range in.Groups {
		switch {
		case g.Kind == sprint.Judgment:
			n.J++
			for _, m := range g.Members {
				behindOf[m] = true
			}
			v.Items = append(v.Items, judgmentItem(g, now))
		case g.To != "":
			// one item a type of note, however many streams it came from: a count where a
			// count is enough, its newest words, its oldest age
			at, ok := asks[g.Type]
			if !ok {
				at = len(v.Items)
				asks[g.Type] = at
				v.Items = append(v.Items, viewItem{K: "r:" + g.Type, T: itemRequest, W: g.Type, Next: "nova-sprint inbox --read"})
			}
			it := &v.Items[at]
			it.N++
			if age := now.Sub(g.Oldest); age > it.age {
				it.age = age
			}
			if it.N == 1 || g.Oldest.After(newest[g.Type]) {
				newest[g.Type] = g.Oldest
				it.S = viewClip(cmp.Or(g.What, g.Hint))
				if h := strings.TrimSpace(g.Hint); strings.HasPrefix(h, "nova-sprint ") {
					it.Next = h
				}
			}
		}
	}

	// the sentinels reached, no judgment open on them
	sentinels := sprintSentinels(s, "")
	for _, x := range sentinels {
		if !x.Reached || behindOf[x.ID] {
			continue
		}
		v.Items = append(v.Items, viewItem{K: "s:" + x.ID, T: itemSentinel, W: "sentinel reached", B: x.Behind, S: "stream " + x.Stream + ": its needs have landed; " + strconv.Itoa(x.Behind) + " cards wait behind it",
			Next: "nova-sprint release " + x.ID + " --reason '<what you looked at and found>'"})
	}

	// the machines: width and work, a row down that is not held
	var rows []viewRow
	for _, m := range members {
		ctl := s.MemberCtl(m)
		status, width := cmp.Or(s.Fleet.Texts[m][sprint.Status], ctl.F("status")), s.Width(m) // as the fleet table shows it: up, down, held
		r, w := s.Fleet.Count(m, sprint.Ready), s.Fleet.Count(m, sprint.Working)
		if status == sprint.Up {
			n.Width += width
			n.Busy += w
		}
		rep := "never"
		var since time.Duration
		if b, ok := beats[m]; ok && b.Beaten() {
			since = now.Sub(b.At)
			rep = ageWord(since)
		}
		rows = append(rows, viewRow{K: "m:" + m, St: cmp.Or(status, sprint.Down), R: r, W: w, Wd: width, F30: finished(m), Rep: rep})
		if status != sprint.Up && status != sprint.Held && width > 0 {
			v.Items = append(v.Items, viewItem{K: "m:" + m, T: itemMachine, W: "machine down", B: r + w, S: m + " is down and not held (width " + strconv.Itoa(width) + "); last beat " + rep,
				Next: "nova-sprint log --member " + m + " --since 1h", age: since})
		}
	}

	// the friends: holding cards and down, or holding cards with no report for a while
	for _, f := range friends {
		row := sprint.FriendRow(f.Name)
		r, w := s.Fleet.Count(row, sprint.Ready), s.Fleet.Count(row, sprint.Working)
		rep, since := "never", time.Duration(0)
		if !f.Beat.IsZero() {
			since = now.Sub(f.Beat)
			rep = ageWord(since)
		}
		rows = append(rows, viewRow{K: "f:" + f.Name, St: f.Status, R: r, W: w, Wd: f.Width, F30: finished(row), Rep: rep, Run: runs[f.Name]})
		if r+w == 0 {
			continue
		}
		why, kind := "", "friend stale"
		switch {
		case f.Status == sprint.Down:
			why = "is down"
			if f.Reason != "" {
				why += " (" + f.Reason + ")"
			}
		case f.Beat.IsZero():
			why = "has never reported"
		case since > viewStaleReport:
			why = "has not reported for " + rep
		case f.Status == sprint.Up && !f.Active.IsZero() && now.Sub(friendLastWork(s, f)) > s.FriendIdleAfter():
			// her daemon answers (she beats) and nothing of hers moves: the finding of
			// 2026-10-04, a friend idle for two hours while the table said up with 8 working.
			// It reads the newest evidence of her work (sprint.FriendWorked, and her cards'
			// moves), never her daemon's walk alone: on 2026-10-06 that walk read 3d while
			// she reported hourly
			kind = "friend idle"
			why = "has a daemon that answers and no evidence of work for " + ageWord(now.Sub(friendLastWork(s, f))) + " (no session write, session proof or answer, finish, report or card move)"
		}
		if why == "" {
			continue
		}
		v.Items = append(v.Items, viewItem{K: "f:" + f.Name, T: itemFriend, W: kind, B: r + w,
			S:    fmt.Sprintf("%s %s and holds %d ready, %d working", f.Name, why, r, w),
			Next: "nova-sprint friend take " + f.Name + " --all-unstarted --reason '" + f.Name + " " + strings.ReplaceAll(why, "'", "") + "'", age: since})
	}
	if all {
		v.Rows = rows
	}

	// the alarms: effects on the cards, never a load number
	running := merr == nil && machine.Running()
	firstSentinel := func() string {
		for _, x := range sentinels {
			if x.Reached {
				return x.ID
			}
		}
		if len(sentinels) > 0 {
			return sentinels[0].ID
		}
		return ""
	}
	release := func(fallback string) string {
		if id := firstSentinel(); id != "" {
			return "nova-sprint release " + id + " --reason '<why the wave goes now>'"
		}
		return fallback
	}
	if merr == nil && !running && !machine.Done() && n.Landed < n.All {
		who := cmp.Or(machine.Who, "no one")
		what := "the machine is STOPPED (by " + who
		if machine.Cause != "" {
			what += ", " + machine.Cause
		}
		v.Items = append(v.Items, viewItem{K: "a:stopped", T: itemAlarm, W: "machine stopped", B: n.All - n.Landed, S: what + ") with " + strconv.Itoa(n.All-n.Landed) + " cards not landed",
			Next: "nova-sprint start", age: now.Sub(machine.Since)})
	}
	if s.FleetOff() {
		v.Fleet = sprint.SwitchOff
	}
	if s.FriendsOff() {
		v.Friends = sprint.SwitchOff
	}
	if s.Work != nil {
		props := s.Work.Props()
		v.FleetTiers, v.FriendsTiers = sprint.SideTiers(props, sprint.PropFleetTiers), sprint.SideTiers(props, sprint.PropFriendsTiers)
	}
	if running && n.Width > 0 && 2*n.Busy < n.Width && !s.FleetOff() { // off: the machines are dealt nothing
		next := "nova-sprint where --all"
		if n.Ready < n.Width {
			next = release("nova-sprint needs --roots")
		}
		v.Items = append(v.Items, viewItem{K: "a:idle", T: itemAlarm, W: "fleet idle", B: n.Width - n.Busy,
			S:    fmt.Sprintf("the machines work %d of their width %d; ready %d, waiting %d", n.Busy, n.Width, n.Ready, n.Waiting),
			Next: next})
	}
	if n.Ready == 0 && n.Waiting > 0 {
		v.Items = append(v.Items, viewItem{K: "a:dry", T: itemAlarm, W: "ready empty", B: n.Waiting,
			S:    fmt.Sprintf("nothing is ready and %d cards wait (%d held)", n.Waiting, n.Held),
			Next: release("nova-sprint needs --roots")})
	}
	for _, col := range []sprint.State{sprint.Review, sprint.Merging} {
		oldest, stream, count := time.Duration(0), "", 0
		for _, c := range s.Work.Column(col) {
			count++
			if age := resultAge(s, c, now); age > oldest {
				oldest, stream = age, c.Row
			}
		}
		if oldest < viewBacklogAge {
			continue
		}
		next := "nova-sprint ask --stream " + stream
		if col == sprint.Merging {
			next = "nova-sprint land --stream " + stream
		}
		v.Items = append(v.Items, viewItem{K: "a:" + col, T: itemAlarm, W: col + " backlog", B: count,
			S: fmt.Sprintf("%d in %s, the oldest result waiting %s (stream %s)", count, col, ageWord(oldest), stream), Next: next, age: oldest})
	}
	for _, stream := range s.Merge.Rows() {
		ctl := s.StreamCtl(stream)
		if ctl.F("state") != sprint.StreamStopped || slices.ContainsFunc(in.Open, func(o sprint.Open) bool { return o.Subject() == sprint.StreamSubject(stream) }) {
			continue
		}
		next := "nova-sprint resume --stream " + stream
		if ctl.F("cause") == "red" {
			next += " --did '<what was done>'"
		}
		var age time.Duration
		if t, err := time.Parse(time.RFC3339, ctl.F("since")); err == nil {
			age = now.Sub(t)
		}
		left := 0
		for _, col := range sprint.States {
			if col != sprint.Landed {
				left += s.Work.Count(stream, col)
			}
		}
		v.Items = append(v.Items, viewItem{K: "a:stopped:" + stream, T: itemAlarm, W: "stream stopped", B: left,
			S: "stream " + stream + " stopped (" + cmp.Or(ctl.F("cause"), "-") + "), no judgment open on it", Next: next, age: age})
	}

	for i := range v.Items {
		if v.Items[i].age > 0 {
			v.Items[i].Age = ageWord(v.Items[i].age)
		}
	}
	slices.SortStableFunc(v.Items, func(x, y viewItem) int {
		return cmp.Or(cmp.Compare(y.B, x.B), cmp.Compare(itemRank[x.T], itemRank[y.T]), cmp.Compare(y.age, x.age), cmp.Compare(x.K, y.K))
	})
	v.Cursor = cursorOf(itemDigests(v.Items), rowDigests(v.Rows))
	v.Sum = coordinatorSum(v, merr == nil, machine)
	return v, nil
}

// coordNeedsView is view coordinator --needs' document, schema 1: sprint.NeedsRank over one read of
// the work and merge tables and the open judgments, at one epoch, no git (docs/SPEC-SPRINT.md,
// view-coordinator-needs.w1).
type coordNeedsView struct {
	View   string        `json:"view"`
	Schema int           `json:"schema"`
	At     time.Time     `json:"at"`
	Epoch  uint64        `json:"epoch"`
	Total  int           `json:"total"`
	Needs  []sprint.Need `json:"needs"`
}

func (a *app) readCoordNeeds(ctx context.Context, st *store.Store) (coordNeedsView, error) {
	v := coordNeedsView{View: "coordinator-needs", Schema: viewSchema, Needs: []sprint.Need{}}
	st, err := st.Pinned(ctx)
	if err != nil {
		return v, err
	}
	v.Epoch = st.PinnedEpoch()
	s, err := st.Load(ctx, []string{sprint.Work, sprint.Merge}, nil)
	if err != nil {
		return v, err
	}
	in, err := st.Inbox(ctx, defaultDeadline, defaultStale, 10000)
	if err != nil {
		return v, err
	}
	s.Open = in.Open
	v.At = s.Now.UTC().Truncate(time.Second)
	if n := sprint.NeedsRank(s); n != nil {
		v.Needs = n
	}
	v.Total = len(v.Needs)
	return v, nil
}

// coordNeedsText is the needs a line each, heaviest first, at most viewTextLines lines.
func coordNeedsText(v coordNeedsView) string {
	var b strings.Builder
	fmt.Fprintf(&b, "VIEW coordinator --needs total=%d\n", v.Total)
	for i, n := range v.Needs {
		if i == viewTextLines-2 && len(v.Needs)-i > 1 {
			fmt.Fprintf(&b, "+%d more: nova-sprint view coordinator --needs --json\n", len(v.Needs)-i)
			break
		}
		b.WriteString(oneline.Escape(strconv.Itoa(n.Behind)+" behind "+ageWord(n.Age)+" "+n.Evidence()) + "\n")
	}
	return b.String()
}

// judgmentItem is an open judgment as an item: its first decision's command when it is short
// enough to carry whole, else the inbox read of its group.
func judgmentItem(g sprint.Group, now time.Time) viewItem {
	next := "nova-sprint inbox --open " + g.ID
	if len(g.Commands) > 0 && len(g.Commands[0].Lines) > 0 {
		if lines := strings.Join(g.Commands[0].Lines, " && "); len(lines) <= viewNextMax {
			next = lines
		}
	}
	what := g.What
	if g.Stream != "" && !strings.Contains(what, g.Stream) {
		what = strings.TrimSpace("stream " + g.Stream + ": " + what)
	}
	return viewItem{K: "j:" + g.ID, T: itemJudgment, W: g.Type, B: g.Behind, N: g.Size, OD: g.Overdue, D: strings.Join(g.Decisions, "|"), S: viewClip(what), Next: next, age: now.Sub(g.Oldest)}
}

// resultAge is how long a primary's result has waited: since its newest work card finished.
func resultAge(s *sprint.Snapshot, pr *sprint.Card, now time.Time) time.Duration {
	var newest time.Time
	for _, c := range s.Fleet.Of(pr.ID) {
		if t, err := time.Parse(time.RFC3339, c.F("finished")); err == nil && t.After(newest) {
			newest = t
		}
	}
	if newest.IsZero() {
		return 0
	}
	return now.Sub(newest)
}

// coordinatorSum is the view's first line: the seat, what needs it, and the sprint's counts.
func coordinatorSum(v coordinatorView, known bool, m store.Machine) string {
	types := map[string]int{}
	behind := 0
	for _, it := range v.Items {
		types[it.T]++
		if it.T == itemJudgment {
			behind = max(behind, it.B)
		}
	}
	n := v.N
	state := "unknown"
	switch {
	case !known:
	case m.Done():
		state = "done"
	case m.Running():
		state = "running"
	default:
		state = "STOPPED"
	}
	sum := fmt.Sprintf("seat=%s machine=%s j=%d(max %d behind) alarms=%d asks=%d sentinels=%d friends=%d machines=%d | landed %d/%d +%d/30m | ready %d wait %d work %d review %d merge %d | busy %d/%d | rules %d/h | suppressed %d (lane %d readers %d tier %d)",
		cmp.Or(v.Seat, "-"), state, n.J, behind, types[itemAlarm], types[itemRequest], types[itemSentinel], types[itemFriend], types[itemMachine],
		n.Landed, n.All, n.L30, n.Ready, n.Waiting, n.Working, n.Review, n.Merging, n.Busy, n.Width, n.Rules, n.Suppressed, n.By.Lane, n.By.Readers, n.By.Tier)
	if v.Push != "" {
		sum += " | push " + v.Push
	}
	if line := switchesLine(v.Fleet, v.Friends, v.FleetTiers, v.FriendsTiers); line != "" {
		sum += " | " + line
	}
	return sum
}

// coordinatorText is the view in at most viewTextLines lines: the summary, then an item a
// line, heaviest first.
func coordinatorText(v coordinatorView) string {
	var b strings.Builder
	b.WriteString("VIEW coordinator " + v.Sum + "\n")
	lines := 1
	for i, it := range v.Items {
		if lines == viewTextLines-1 && len(v.Items)-i > 1 {
			fmt.Fprintf(&b, "+%d more: nova-sprint view coordinator --json\n", len(v.Items)-i)
			break
		}
		b.WriteString(oneline.Escape(itemLine(it)) + "\n")
		lines++
	}
	if v.Same+v.Gone > 0 {
		fmt.Fprintf(&b, "since: same=%d gone=%d\n", v.Same, v.Gone)
	}
	b.WriteString("cursor=" + v.Cursor + "\n")
	return b.String()
}

// itemLine is one item as the text prints it.
func itemLine(it viewItem) string {
	l := strings.ToUpper(it.T) + " " + strconv.Itoa(it.B)
	if it.Age != "" {
		l += " " + it.Age
	}
	l += " " + it.K
	if it.OD {
		l += " OVERDUE"
	}
	if it.S != "" {
		l += ": " + it.S
	}
	return l + " -> " + it.Next
}

// workerView reads a worker's cards: the cards dealt to its row and not finished (one read of
// the in-flight cells), their packets (the brief, the base, the notes), and the cards it
// finished ok whose primaries have not landed. ok is false for a name that is no member and no
// friend.
func (a *app) workerView(ctx context.Context, st *store.Store, as string) (workerView, bool, error) {
	now := a.now()
	v := workerView{View: "worker", Schema: viewSchema, At: now.UTC().Truncate(time.Second), As: as, Cards: []workerCard{}}
	st, err := st.Pinned(ctx)
	if err != nil {
		return v, false, err
	}
	v.Epoch = st.PinnedEpoch()
	friends, err := st.FriendNames(ctx)
	if err != nil {
		return v, false, err
	}
	row := as
	if slices.Contains(friends, as) {
		v.Kind, row = "friend", sprint.FriendRow(as)
		dirs, err := st.FriendDirs(ctx)
		if err != nil {
			return v, false, err
		}
		v.Dir = dirs[as]
		v.NovaRoot = a.machineNovaRoot()
	}
	d, err := st.Dealt(ctx)
	if err != nil {
		return v, false, err
	}
	if v.Kind == "" {
		ctl, err := st.ReadCells(ctx, sprint.Fleet, as, sprint.Ctl)
		if err != nil {
			return v, false, err
		}
		if len(ctl) == 0 {
			return v, false, nil
		}
		v.Kind = "member"
	}
	var mine []*sprint.Card
	for _, c := range d.Cards {
		if c.Row == row {
			mine = append(mine, c)
		}
	}
	// in flight first (working), then in the order they are dealt
	slices.SortStableFunc(mine, func(x, y *sprint.Card) int {
		return cmp.Or(cmp.Compare(colRank(x.Col), colRank(y.Col)), cmp.Compare(x.Score, y.Score), cmp.Compare(x.ID, y.ID))
	})
	ps, err := st.Packets(ctx, mine)
	if err != nil {
		return v, false, err
	}
	dealt, _ := dealtView(d, st.Names.Prefix, v.Epoch)
	deadline := map[string]time.Time{}
	for _, c := range dealt {
		deadline[c.ID] = c.Deadline
	}
	open := map[string][]string{}
	for _, o := range d.Open {
		for _, p := range append([]string{o.Subject()}, o.Note.Primaries...) {
			if !slices.Contains(open[p], o.Note.Type) {
				open[p] = append(open[p], o.Note.Type)
			}
		}
	}
	for i, c := range mine {
		p := ps[i]
		wc := workerCard{ID: c.ID, P: p.Primary, St: c.Col, Att: p.Attempt, Gen: p.Gen, DL: deadline[c.ID], Br: p.Branch, J: open[p.Primary],
			Base: cmp.Or(p.Base, swarm.ReadCardBase([]byte(p.Brief)).Ref), Paths: swarm.CardPaths([]byte(p.Brief))}
		if v.Kind == "friend" {
			wc.Brief = friendWorkDir(v.NovaRoot, as, v.Dir) + "/inbox/" + friendJobOf(p) + "/BRIEF.md"
		} else {
			wc.Brief = "nova-sprint card " + p.Primary + " --brief"
		}
		for _, l := range [][2]string{{"why: ", p.Why}, {"finding: ", p.Finding}, {"fix: ", p.Fix}} {
			if l[1] != "" {
				wc.Notes = append(wc.Notes, viewClip(l[0]+l[1]))
			}
		}
		for _, note := range p.Notes {
			wc.Notes = append(wc.Notes, viewClip(note))
		}
		v.Cards = append(v.Cards, wc)
	}
	if len(mine) > 0 {
		v.Next = workerNext(v, mine[0], ps[0])
	}
	// every machine quiet now, for every member and friend: run no go build or test there
	// (docs/SPEC-SPRINT.md section 5, fleet-quiet-machine-b.w7)
	shapes, err := st.B.Shapes(ctx, []string{st.Names.Table(sprint.Fleet)})
	if err != nil {
		return v, false, err
	}
	v.Quiet = sprint.QuietLines(shapes[0].Props, now)

	// my results not landed: the cards finished ok whose primaries wait in review or merging
	done, err := st.ReadCells(ctx, sprint.Fleet, row, sprint.DoneOK)
	if err != nil {
		return v, false, err
	}
	byPrimary := map[string]*sprint.Card{}
	for _, c := range done {
		p := c.F(sprint.PrimaryField)
		if old := byPrimary[p]; old == nil || c.F("finished") > old.F("finished") {
			byPrimary[p] = c
		}
	}
	prims, err := st.Records(ctx, sprint.Work, slices.Sorted(maps.Keys(byPrimary)))
	if err != nil {
		return v, false, err
	}
	for _, pr := range prims {
		if pr == nil || (pr.Col != sprint.Review && pr.Col != sprint.Merging) {
			continue
		}
		c := byPrimary[pr.ID]
		age := "-"
		if t, err := time.Parse(time.RFC3339, c.F("finished")); err == nil {
			age = ageWord(now.Sub(t))
		}
		v.NWait++
		if len(v.Wait) < viewWaitMax {
			v.Wait = append(v.Wait, waitCard{ID: c.ID, P: pr.ID, St: pr.Col, Age: age})
		}
	}
	if v.NWait <= len(v.Wait) {
		v.NWait = 0
	}
	v.Cursor = cursorOf(workerDigests(v))
	ready, working := 0, 0
	for _, c := range v.Cards {
		if c.St == sprint.Working {
			working++
		} else {
			ready++
		}
	}
	v.Sum = fmt.Sprintf("%s %s: working %d ready %d, results not landed %d", v.Kind, as, working, ready, max(v.NWait, len(v.Wait)))
	return v, true, nil
}

// workerNext is what is the worker's to do next: its first card's next step.
func workerNext(v workerView, c *sprint.Card, p sprint.Packet) string {
	at := c.ID + "@" + strconv.Itoa(p.Gen) + " --epoch " + strconv.FormatUint(v.Epoch, 10)
	switch {
	case v.Kind == "friend" && p.Kind == "read":
		// a read on her row is returned, never finished (sprint.FriendReadOutboxLine)
		return "read " + c.ID + ": write " + friendWorkDir(v.NovaRoot, v.As, v.Dir) + "/outbox/" + friendJobOf(p) + "/REPORT.md with Verdict: LAND, or Verdict: HOLD and a line naming the file:line or rule and what to change"
	case v.Kind == "friend" && c.Col == sprint.Working:
		return "finish " + c.ID + ": push to " + p.Branch + ", then write " + friendWorkDir(v.NovaRoot, v.As, v.Dir) + "/outbox/" + friendJobOf(p) + "/REPORT.md with Verdict: LAND|HOLD|FAIL and Head: <sha>"
	case v.Kind == "friend":
		return "start " + c.ID + ": its brief is " + v.Cards[0].Brief
	case c.Col == sprint.Working:
		return "nova-sprint finish --as " + v.As + " " + at + " (--head <commit> | --failed) --report '<what was done>'"
	}
	return "nova-sprint take --as " + v.As + " " + at
}

// workerText is the worker's view as lines: the summary, what is next, a card a line, a
// result a line.
func workerText(v workerView) string {
	var b strings.Builder
	line := func(s string) { b.WriteString(oneline.Escape(s) + "\n") }
	line("VIEW worker " + v.Sum)
	if v.Next != "" {
		line("NEXT " + v.Next)
	}
	for _, q := range v.Quiet {
		line(q)
	}
	for _, c := range v.Cards {
		l := "CARD " + c.ID + " " + c.St + " att=" + strconv.Itoa(c.Att) + " base=" + cmp.Or(c.Base, "-") + " paths=" + cmp.Or(strings.Join(c.Paths, ","), "-")
		if !c.DL.IsZero() {
			l += " deadline=" + ageWord(c.DL.Sub(v.At))
		}
		l += " brief=" + c.Brief
		if len(c.J) > 0 {
			l += " judgments=" + strings.Join(c.J, ",")
		}
		line(l)
		for _, n := range c.Notes {
			line("  NOTE " + n)
		}
	}
	for _, w := range v.Wait {
		line("WAIT " + w.ID + " " + w.St + " " + w.Age)
	}
	if v.NWait > 0 {
		line(fmt.Sprintf("+%d more results not landed", v.NWait-len(v.Wait)))
	}
	if v.Same+v.Gone > 0 {
		line(fmt.Sprintf("since: same=%d gone=%d", v.Same, v.Gone))
	}
	line("cursor=" + v.Cursor)
	return b.String()
}

// colRank orders a worker's cards: working before ready.
func colRank(col string) int {
	if col == sprint.Working {
		return 0
	}
	return 1
}

// THE CURSOR (--since). A view's cursor is the digest of every item, row and card it showed:
// three bytes each (FNV-1a of the key and of what makes it another thing to act on, its age
// left out: viewItem.digest), sorted, in base64url after "1.". A view given it leaves out
// every item whose digest the cursor holds (same: unchanged since that read) and counts the
// cursor's digests no item has now (gone: answered, landed, cleared, or changed and shown
// again). The server keeps nothing between reads: the cursor is the reader's.

const cursorVersion = "1."

// digestBytes is the bytes of one digest in a cursor: three, the low 24 bits of FNV-1a. A
// cursor of 70 items is 280 characters; a changed item that collides with its old digest is
// one in sixteen million.
const (
	digestBytes = 3
	digestMask  = 1<<(8*digestBytes) - 1
)

// digest is an item's four bytes: its key and the JSON of what it says.
func digest(key string, v any) uint32 {
	h := fnv.New32a()
	b, _ := json.Marshal(v) // ignored: the views' items are strings and numbers
	h.Write([]byte(key))
	h.Write([]byte{0})
	h.Write(b)
	return h.Sum32() & digestMask
}

// digest is the item's digest over what makes it another item to act on, by its type: a
// judgment's weight, size, decisions and command; a note's words; a sentinel's cards behind;
// an alarm's or a row's standing and command. Its age, and the counts in an alarm's words,
// which move every read, are left out, so an alarm that still stands is the same alarm.
func (it viewItem) digest() uint32 {
	kept := viewItem{K: it.K, T: it.T, W: it.W, Next: it.Next}
	switch it.T {
	case itemJudgment:
		kept.B, kept.N, kept.OD, kept.D = it.B, it.N, it.OD, it.D
	case itemRequest:
		kept.S, kept.N = it.S, it.N
	case itemSentinel:
		kept.B = it.B
	}
	return digest(it.K, kept)
}

func (r viewRow) digest() uint32 {
	r.Rep = "" // its age moves every read
	return digest(r.K, r)
}

func itemDigests(items []viewItem) []uint32 {
	out := make([]uint32, len(items))
	for i, it := range items {
		out[i] = it.digest()
	}
	return out
}

func rowDigests(rows []viewRow) []uint32 {
	out := make([]uint32, len(rows))
	for i, r := range rows {
		out[i] = r.digest()
	}
	return out
}

func workerCardDigest(c workerCard) uint32 { return digest("c:"+c.ID, c) }

func waitDigest(w waitCard) uint32 {
	w.Age = ""
	return digest("w:"+w.ID, w)
}

func workerDigests(v workerView) []uint32 {
	var out []uint32
	for _, c := range v.Cards {
		out = append(out, workerCardDigest(c))
	}
	for _, w := range v.Wait {
		out = append(out, waitDigest(w))
	}
	return out
}

// cursorOf is the cursor of the digests.
func cursorOf(sets ...[]uint32) string {
	var all []uint32
	for _, s := range sets {
		all = append(all, s...)
	}
	slices.Sort(all)
	all = slices.Compact(all)
	b := make([]byte, 0, digestBytes*len(all))
	for _, d := range all {
		b = append(b, byte(d>>16), byte(d>>8), byte(d))
	}
	return cursorVersion + base64.RawURLEncoding.EncodeToString(b)
}

// parseCursor is the digests a cursor holds; nil for no cursor.
func parseCursor(c string) (map[uint32]bool, error) {
	if c == "" {
		return nil, nil
	}
	const bad = "--since wants the cursor a view printed (cursor=1.<base64url>), found "
	rest, ok := strings.CutPrefix(c, cursorVersion)
	if !ok {
		return nil, fmt.Errorf("%s%s", bad, viewClip(c))
	}
	b, err := base64.RawURLEncoding.DecodeString(rest)
	if err != nil || len(b)%digestBytes != 0 {
		return nil, fmt.Errorf("%s%s", bad, viewClip(c))
	}
	out := map[uint32]bool{}
	for i := 0; i < len(b); i += digestBytes {
		out[uint32(b[i])<<16|uint32(b[i+1])<<8|uint32(b[i+2])] = true
	}
	return out, nil
}

// sinceItems is the items the cursor's read did not show as they are, how many it did
// (same), and how many of its digests no item has now (gone). No cursor keeps every item.
func sinceItems(items []viewItem, seen map[uint32]bool) (out []viewItem, same, gone int) {
	if seen == nil {
		return items, 0, 0
	}
	out = []viewItem{}
	now := map[uint32]bool{}
	for _, it := range items {
		d := it.digest()
		now[d] = true
		if seen[d] {
			same++
			continue
		}
		out = append(out, it)
	}
	return out, same, goneOf(seen, now)
}

// sinceRows is sinceItems for the rows of --all; the rows' gone are not counted apart.
func sinceRows(rows []viewRow, seen map[uint32]bool) ([]viewRow, int, int) {
	if seen == nil {
		return rows, 0, 0
	}
	var out []viewRow
	same := 0
	for _, r := range rows {
		if seen[r.digest()] {
			same++
			continue
		}
		out = append(out, r)
	}
	return out, same, 0
}

// sinceWorker leaves out of the worker's view every card and result the cursor holds.
func sinceWorker(v *workerView, seen map[uint32]bool) {
	if seen == nil {
		return
	}
	now := map[uint32]bool{}
	cards := []workerCard{}
	for _, c := range v.Cards {
		d := workerCardDigest(c)
		now[d] = true
		if seen[d] {
			v.Same++
			continue
		}
		cards = append(cards, c)
	}
	var wait []waitCard
	for _, w := range v.Wait {
		d := waitDigest(w)
		now[d] = true
		if seen[d] {
			v.Same++
			continue
		}
		wait = append(wait, w)
	}
	v.Cards, v.Wait, v.Gone = cards, wait, goneOf(seen, now)
}

// goneOf counts the digests seen that are not among now. A digest of a row the view did not
// draw this time (a read without --all after one with it) counts as gone.
func goneOf(seen, now map[uint32]bool) int {
	gone := 0
	for d := range seen {
		if !now[d] {
			gone++
		}
	}
	return gone
}

// ageWord is a duration in one unit, the fewest characters: 45s, 25m, 3h, 2d; a negative
// one (a deadline passed) with a minus.
func ageWord(d time.Duration) string {
	sign := ""
	if d < 0 && d > -time.Second {
		d = 0 // a clock a moment ahead: now
	}
	if d < 0 {
		sign, d = "-", -d
	}
	switch {
	case d < 90*time.Second:
		return sign + strconv.Itoa(int(d/time.Second)) + "s"
	case d < 90*time.Minute:
		return sign + strconv.Itoa(int(d/time.Minute)) + "m"
	case d < 48*time.Hour:
		return sign + strconv.Itoa(int(d/time.Hour)) + "h"
	}
	return sign + strconv.Itoa(int(d/(24*time.Hour))) + "d"
}

// viewClip is the text on one line, at most viewWhatMax bytes, cut at a rune.
func viewClip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= viewWhatMax {
		return s
	}
	cut := viewWhatMax
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

// viewJSON prints the view as one line of JSON, <, > and & as they are: a model reads the
// bytes, and an escape is six of them.
func viewJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v) // ignored: a view of strings, numbers and times always encodes, and stdout is the caller's
}

// friendLastWork is the newest evidence of the friend's work (sprint.FriendWorked over her
// row and what her beat and the store's records say) or of a card of hers moving
// (sprint.FriendCardMoved): what the idle item measures.
func friendLastWork(s *sprint.Snapshot, f store.FriendRow) time.Time {
	w := sprint.FriendWork{Active: f.Active, Proof: f.Proof, Finished: f.Finished}
	if f.Health != nil && f.Health.State == sprint.Up {
		w.Answered = f.Health.Seen
	}
	if f.Report != nil && len(f.Report.Running) > 0 {
		w.Running = f.Beat
	}
	at, _ := sprint.FriendWorked(s, f.Name, w)
	if moved := sprint.FriendCardMoved(s, f.Name); moved.After(at) {
		at = moved
	}
	return at
}
