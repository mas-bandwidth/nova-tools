package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The read verbs: queue, where, inbox, card, check. Each has --json, one
// object for a program; the driver reads the sprint through them.

// summary is the sprint's line: landed / all primaries on the table, percent,
// ETA (the table layer's view line, which has no rate to give an ETA from).
// Every primary landed, it has no ETA: it is done (errata 3 amendment 6).
func summary(t ntable.Table) string {
	if landed, all := counts(t); all > 0 && landed == all {
		return progress(t) + " done"
	}
	return progress(t) + " -> ETA"
}

// progress is landed / all primaries and the percent: "3/10 30.0%".
func progress(t ntable.Table) string {
	landed, all := counts(t)
	pct := "0.0%"
	if all > 0 {
		pct = strconv.FormatFloat(100*float64(landed)/float64(all), 'f', 1, 64) + "%"
	}
	return fmt.Sprintf("%d/%d %s", landed, all, pct)
}

func counts(t ntable.Table) (landed, all int64) {
	j := t.Column(sprint.Landed)
	for _, r := range t.Rows {
		for k, c := range t.Columns {
			if c.Projection != ntable.Count || k >= len(r.Cells) {
				continue
			}
			all += r.Cells[k].Count
			if k == j {
				landed += r.Cells[k].Count
			}
		}
	}
	return landed, all
}

// queueCard is one card of a queue, for a program.
type queueCard struct {
	ID      string  `json:"id"`
	Table   string  `json:"table"`
	Row     string  `json:"row"`
	Col     string  `json:"col"`
	Primary string  `json:"primary,omitempty"`
	Stream  string  `json:"stream,omitempty"`
	Attempt int     `json:"attempt,omitempty"`
	Gen     int     `json:"gen,omitempty"`
	Head    string  `json:"head,omitempty"`
	Score   float64 `json:"score"`
	// The stamps: a work card's dealt and taken, a read card's asked and begun.
	Dealt string `json:"dealt,omitempty"`
	Taken string `json:"taken,omitempty"`
	Asked string `json:"asked,omitempty"`
	Begun string `json:"begun,omitempty"`
	// WaitsFor is, for a waiting primary, the needs it still waits for.
	WaitsFor []string `json:"waits_for,omitempty"`
	// Packet is what the member or reader is handed with the card.
	Packet *sprint.Packet `json:"packet,omitempty"`
}

func (a *app) cmdQueue(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("queue")
	as := fs.String("as", "", "a reader (its read cards, asked then reading) or a fleet member (its work cards, ready then working)")
	stream := fs.String("stream", "", "a stream: its merge queue, then its stuck cards")
	col := fs.String("col", "", "with --stream: waiting lists the stream's waiting primaries, each with what it still waits for")
	packets := fs.String("packets", "", "with --as: the packets the worker wants, so the answer carries only those (every other card is listed with its id, column, attempt and gen, and the answer's epoch, with no packet): the first n cards it may start (asked, ready) and every in-flight card (reading, working), each not named by --have; without it every card carries its packet")
	have := fs.String("have", "", "with --packets: the cards, comma separated, the worker wants no packet for (it runs them, or will not start them yet)")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "queue", err.Error())
	}
	want, err := packetsWanted(*packets, *have)
	if err != nil {
		return refuse(stderr, "queue", err.Error())
	}
	if want.n >= 0 && *as == "" {
		return refuse(stderr, "queue", "--packets is a worker's: it takes --as <reader|member>")
	}
	if len(pos) > 0 || (*as == "") == (*stream == "") {
		return refuse(stderr, "queue", "wants one of --as <reader|member>, --stream <s>")
	}
	if *col != "" && (*col != sprint.Waiting || *stream == "") {
		return refuse(stderr, "queue", "--col takes waiting, with --stream <s>")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "queue", err.Error())
	}
	ctx := context.Background()
	if st, err = st.Pinned(ctx); err != nil {
		return a.readFailed("queue", err, stderr)
	}
	epoch := st.PinnedEpoch()
	if *as != "" {
		// the reader's own queue is its beat (docs/SPEC-SPRINT.md section 6):
		// a name that is no reader's row writes none
		if _, err := st.ReaderBeat(ctx, *as); err != nil {
			return a.readFailed("queue", err, stderr)
		}
	}
	var cards []queueCard
	width := 0 // a fleet member's width, from its row (0: not a member, or none read)
	add := func(table string, cs []*sprint.Card) {
		for _, x := range cs {
			cards = append(cards, queueCard{ID: x.ID, Table: table, Row: x.Row, Col: x.Col, Primary: x.F("primary"), Stream: x.F("stream"),
				Attempt: x.Int("attempt"), Gen: x.Int("gen"), Head: x.F("head"), Score: x.Score,
				Dealt: x.F("dealt"), Taken: x.F("taken"), Asked: x.F("asked"), Begun: x.F("begun")})
		}
	}
	if *col == sprint.Waiting {
		s, err := st.Load(ctx, []string{sprint.Work}, func(s *sprint.Snapshot) map[string][]string {
			return map[string][]string{sprint.Work: sprint.ResolveExtras(s)}
		})
		if err != nil {
			return a.readFailed("queue", err, stderr)
		}
		cs := s.Work.Cell(*stream, sprint.Waiting)
		add(sprint.Work, cs)
		for i, x := range cs {
			cards[i].WaitsFor = sprint.WaitsFor(s, x, nil)
		}
	} else if *stream != "" {
		cs, err := st.ReadCells(ctx, sprint.Merge, *stream, sprint.Queued, sprint.Stuck)
		if err != nil {
			return a.readFailed("queue", err, stderr)
		}
		add(sprint.Merge, cs)
	} else {
		var mine []*sprint.Card
		for _, t := range []struct{ table, a, b string }{{sprint.Readers, sprint.Asked, sprint.Reading}, {sprint.Fleet, sprint.Ready, sprint.Working}} {
			cols := []string{t.a, t.b}
			if t.table == sprint.Fleet {
				cols = append(cols, sprint.Ctl) // the member's control card: its width, read with its cards
			}
			cs, err := st.ReadCells(ctx, t.table, *as, cols...)
			if err != nil {
				return a.readFailed("queue", err, stderr)
			}
			var own []*sprint.Card
			for _, x := range cs {
				if x.Col == sprint.Ctl {
					width = sprint.MemberWidth(x)
					continue
				}
				own = append(own, x)
			}
			add(t.table, own)
			mine = append(mine, own...)
		}
		at := want.of(mine)
		need := make([]*sprint.Card, len(at))
		for k, i := range at {
			need[k] = mine[i]
		}
		ps, err := st.Packets(ctx, need)
		if err != nil {
			return a.readFailed("queue", err, stderr)
		}
		for k, i := range at {
			cards[i].Packet = &ps[k]
		}
	}
	if c.json {
		if cards == nil {
			cards = []queueCard{}
		}
		out := map[string]any{"as": *as, "stream": *stream, "epoch": epoch, "cards": cards}
		if width > 0 {
			out["width"] = width // the member runs this many: the fleet row is the truth
		}
		b, _ := json.Marshal(out)
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	var lines []string
	for _, x := range cards {
		l := fmt.Sprintf("%s %s:%s:%s", x.ID, x.Table, x.Row, x.Col)
		if x.Gen > 0 {
			next := "take"
			if x.Col == sprint.Working {
				next = "finish"
			}
			l += " gen=" + strconv.Itoa(x.Gen) + " " + next + ": " + x.ID + "@" + strconv.Itoa(x.Gen) + " --epoch " + strconv.FormatUint(epoch, 10)
		}
		if len(x.WaitsFor) > 0 {
			l += " waits for: " + sprint.Preview(x.WaitsFor, ",")
		}
		for _, st := range [][2]string{{"dealt", x.Dealt}, {"taken", x.Taken}, {"asked", x.Asked}, {"begun", x.Begun}} {
			if st[1] != "" {
				l += " " + st[0] + "=" + st[1]
			}
		}
		lines = append(lines, l)
	}
	if *as != "" && len(lines) > 0 {
		// a member's or a reader's queue: each card with its packet, when the answer carries it
		for i, l := range lines {
			if c.max > 0 && i >= c.max {
				fmt.Fprintf(stdout, "CARD ... and %d more; --max 0 lists all\n", len(lines)-i)
				break
			}
			fmt.Fprintln(stdout, "CARD "+oneline.Escape(l))
			if cards[i].Packet != nil {
				printPacket(stdout, *cards[i].Packet)
			}
		}
		fmt.Fprintf(stdout, "QUEUE OK cards=%d epoch=%d\n", len(cards), epoch)
		return 0
	}
	listed(stdout, "CARD", lines, c.max, "queue")
	fmt.Fprintf(stdout, "QUEUE OK cards=%d epoch=%d\n", len(cards), epoch)
	return 0
}

// wanted is the packets a worker's queue asks for (--packets, --have): n < 0 is every
// card's, the answer as it was before the flags.
type wanted struct {
	n    int
	have map[string]bool
}

// maxPacketsAsked bounds --packets and the cards --have names: a worker's lanes are far
// fewer, and a value past it is no worker's.
const maxPacketsAsked = 1024

// packetsWanted reads --packets and --have, refusing a value no worker sends: --packets a
// count from 0 to maxPacketsAsked, --have card ids (dot-joined words, sprint.ValidCardID),
// and --have only with --packets. The server's fleet listener holds a worker's queue to the
// same (workerVerb).
func packetsWanted(packets, have string) (wanted, error) {
	w := wanted{n: -1}
	if packets == "" {
		if have != "" {
			return w, errors.New("--have names the cards a --packets answer leaves without a packet: give --packets <n> with it")
		}
		return w, nil
	}
	n, err := strconv.Atoi(packets)
	if err != nil || n < 0 || n > maxPacketsAsked {
		return w, fmt.Errorf("--packets is a count of cards from 0 to %d, found %q", maxPacketsAsked, packets)
	}
	w.n, w.have = n, map[string]bool{}
	if have == "" {
		return w, nil
	}
	ids := strings.Split(have, ",")
	if len(ids) > maxPacketsAsked {
		return w, fmt.Errorf("--have names at most %d cards, found %d", maxPacketsAsked, len(ids))
	}
	for _, id := range ids {
		if !sprint.ValidCardID(id) {
			return w, fmt.Errorf("--have is card ids, comma separated, found %q", id)
		}
		w.have[id] = true
	}
	return w, nil
}

// of is which of a worker's cards, in queue order, get their packets: every one when no
// --packets was given; else the first n it may start (asked, ready) and every one in flight
// (reading, working), skipping the cards --have names. A worker starts a card from its
// packet, or recovers an in-flight card it holds no launch for; a card it already runs, or
// a read past its free lanes, needs none (the fleet load test of 2026-10-01: one reader's
// answer, its 150 asked reads each with its brief, was 579,181 bytes every pass).
func (w wanted) of(cards []*sprint.Card) []int {
	var at []int
	left := w.n
	for i, x := range cards {
		switch {
		case w.n < 0:
		case w.have[x.ID]:
			continue
		case x.Col == sprint.Asked || x.Col == sprint.Ready:
			if left == 0 {
				continue
			}
			left--
		}
		at = append(at, i)
	}
	return at
}

func (a *app) readFailed(verbName string, err error, stderr io.Writer) int {
	fmt.Fprintf(stderr, "%s %s: %s\n", prog, verbName, oneline.WithRemedy(err.Error(), prog+" "+verbName+" -h"))
	if ntable.IsRefusal(err) {
		return 1
	}
	return 2
}

// whereView is the view, for a program.
type whereView struct {
	At          time.Time                               `json:"at"`
	Landed      int64                                   `json:"landed"`
	All         int64                                   `json:"all"`
	Summary     string                                  `json:"summary"`
	Tables      map[string]map[string]map[string]string `json:"tables"` // table -> row -> column -> cell as printed
	Streams     []sprint.StreamClock                    `json:"streams"`
	Stalled     []string                                `json:"stalled,omitempty"`
	Coordinator string                                  `json:"coordinator,omitempty"`
	Pending     string                                  `json:"pending,omitempty"`
	Epoch       uint64                                  `json:"epoch"`
	Cleared     time.Time                               `json:"cleared,omitempty"` // when the epoch began
	Machine     string                                  `json:"machine,omitempty"`
	Goals       []goalView                              `json:"goals,omitempty"`
}

// whereRun is what one where was asked, its flags read.
type whereRun struct {
	c       common
	watch   bool
	every   time.Duration
	stale   time.Duration
	atEpoch int64
}

func (a *app) cmdWhere(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("where")
	watch := fs.Bool("watch", false, "redraw in place every --every until interrupted")
	every := fs.Duration("every", time.Second, "the redraw interval with --watch, above 0")
	stale := fs.Duration("stale", defaultStale, "a stream with no progress for longer is shown stalled (--json)")
	atEpoch := fs.Int64("at-epoch", -1, "the sprint as it was at an earlier epoch (before a clear)")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "where", argErr("takes no words ", err))
	}
	ctx := context.Background()
	if *watch && isTwin(c.redis) {
		// before the watch draws anything: a twin has no machine to watch
		if _, err := a.backend(ctx, c.redis, sprint.Names{}); err == nil && a.twinOpen(c.redis) {
			return refuse(stderr, "where", twinMachine)
		}
	}
	if *watch {
		if *every <= 0 {
			return refuse(stderr, "where", "--every wants a duration above 0, got "+every.String())
		}
		// an interrupt ends the watch, and the cursor comes back with it
		var stop context.CancelFunc
		ctx, stop = a.notify(ctx)
		defer stop()
	}
	return a.whereLoop(ctx, whereRun{c: *c, watch: *watch, every: *every, stale: *stale, atEpoch: *atEpoch}, stdout, stderr)
}

// whereLoop shows the view: once, or with --watch every --every until ctx is
// done. A watch of the text redraws in place (watchWriter); --json prints
// one object a frame.
func (a *app) whereLoop(ctx context.Context, r whereRun, stdout, stderr io.Writer) int {
	var w *watchWriter
	if r.watch && !r.c.json {
		w = newWatchWriter(stdout, func() (int, int) { return a.screen(stdout) })
		w.hideCursor()
		defer w.showCursor()
	}
	for {
		// every frame reads the sprint's epoch again: a clear while it
		// watches shows the new epoch
		st, err := a.storeAtCtx(ctx, r.c, r.atEpoch)
		if err != nil {
			if ctx.Err() != nil {
				return 0 // an interrupt cut the read short: the watch is over, not failed
			}
			return refuse(stderr, "where", err.Error())
		}
		v, frame, err := a.where(ctx, st, r.stale)
		if err != nil {
			if ctx.Err() != nil {
				return 0 // an interrupt cut the read short: the watch is over, not failed
			}
			return a.readFailed("where", err, stderr)
		}
		switch {
		case r.c.json:
			b, _ := json.Marshal(v)
			fmt.Fprintln(stdout, string(b))
		case w != nil:
			if err := w.frame(frame); err != nil {
				fmt.Fprintf(stderr, "%s where: stdout: %s\n", prog, oneline.Escape(err.Error()))
				return 1
			}
		default:
			fmt.Fprint(stdout, frame)
		}
		if !r.watch || !a.pause(ctx, r.every) {
			return 0
		}
	}
}

func (a *app) where(ctx context.Context, st *store.Store, stale time.Duration) (whereView, string, error) {
	st, err := st.Pinned(ctx)
	if err != nil {
		return whereView{}, "", err
	}
	es, err := st.EpochNow(ctx)
	if err != nil {
		return whereView{}, "", err
	}
	names := make([]string, len(sprint.ViewOrder))
	for i, t := range sprint.ViewOrder {
		names[i] = st.Names.Table(t)
	}
	shapes, err := st.B.Shapes(ctx, names)
	if err != nil {
		return whereView{}, "", err
	}
	f, err := st.B.ReadFence(ctx)
	if err != nil {
		return whereView{}, "", err
	}
	clocks, err := st.StreamClocks(ctx)
	if err != nil {
		return whereView{}, "", err
	}
	coordinator, err := st.B.Coordinator(ctx)
	if err != nil {
		return whereView{}, "", err
	}
	now := a.now()
	v := whereView{At: now, Tables: map[string]map[string]map[string]string{}, Streams: clocks, Epoch: st.PinnedEpoch(), Coordinator: coordinator}
	if v.Epoch == es.N {
		v.Cleared = es.Cleared
	}
	v.Landed, v.All = counts(shapes[0])
	v.Summary = summary(shapes[0])
	if f.Pending != nil {
		v.Pending = f.Pending.ID
	}
	v.Machine = st.MachineLine(ctx)
	var b strings.Builder
	b.WriteString("SPRINT TABLE\n\n" + whereHeader(v.Summary, v.Machine) + "\n\n")
	var parts []string
	for i, t := range shapes {
		logical := sprint.ViewOrder[i]
		rows := map[string]map[string]string{}
		for _, r := range t.Rows {
			cells := map[string]string{}
			for j, col := range t.Columns {
				cells[col.Name] = ntable.CellText(t.Columns, r, j)
			}
			rows[r.Key] = cells
		}
		v.Tables[logical] = rows
		if logical == sprint.Merge && !slices.Contains(t.Hidden, sprint.Since) {
			// the machine keeps a stream's since; the view does not show it
			t.Hidden = append(append([]string(nil), t.Hidden...), sprint.Since)
		}
		// every table shows, every stream row in it, empty or not
		parts = append(parts, ntable.Render(t, ntable.RenderOpts{Title: logical}))
	}
	b.WriteString(strings.Join(parts, "\n"))
	a.goalsView(ctx, st, &v)
	for _, c := range clocks {
		if c.Stalled(now, stale) {
			v.Stalled = append(v.Stalled, c.Stream)
		}
	}
	return v, b.String(), nil
}

// whereHeader is the one line under the title of the where view: STOPPED when
// the machine is stopped (or a RUNNING machine has not ticked), DONE when it
// stopped because the sprint is done (the view's state text, errata 3
// amendment 6), and the progress line, with no machine text, when it is
// running. Nothing follows any of them.
func whereHeader(summary, machine string) string {
	state := strings.TrimPrefix(machine, "machine: ")
	if strings.HasPrefix(state, "STOPPED") || state == store.DoneState {
		return state // DONE, as the view says, when the sprint is done
	}
	return strings.TrimSpace(summary + strings.TrimPrefix(state, "running"))
}

func (a *app) cmdInbox(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("inbox")
	open := fs.String("open", "", "list every member, need and notification of the group of this id")
	read := fs.Bool("read", false, "move the cursor past what is shown: happened notifications before it are not shown again (open judgments always are)")
	deadline := fs.Duration("deadline", defaultDeadline, "a judgment open longer is overdue")
	stale := fs.Duration("stale", defaultStale, "a stream with no progress for longer is shown stalled")
	atEpoch := fs.Int64("at-epoch", -1, "the inbox as it was at an earlier epoch (before a clear)")
	wait := fs.Bool("wait", false, "block until the next tick-end note (the tick addressed the coordinator something), then show the inbox")
	timeout := fs.Duration("timeout", 5*time.Minute, "with --wait, the longest wait; the inbox is shown when it passes")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "inbox", argErr("takes no words ", err))
	}
	if *read && *atEpoch >= 0 {
		return refuse(stderr, "inbox", "--read moves the cursor of the sprint's epoch, and --at-epoch reads an earlier one as it was: give one of them")
	}
	if *read && *wait && a.getenv(ServerEnv) != "" {
		// the cursor is the server's to move, and a wait never runs on the server (waits)
		return refuse(stderr, "inbox", "--read moves the cursor, which the sprint's server (NOVA_SPRINT_SERVER) moves, and --wait waits where it is typed, never on the server: run nova-sprint inbox --wait, then nova-sprint inbox --read; nothing was changed")
	}
	st, err := a.storeAt(*c, *atEpoch)
	if err != nil {
		return refuse(stderr, "inbox", err.Error())
	}
	ctx := context.Background()
	if *read {
		// the cursor is the coordinator's: anyone reads the inbox, and only
		// the coordinator moves what it shows
		rc := *c
		rc.verb = "inbox --read"
		if rc.actor == "" {
			return refuse(stderr, "inbox", "--read moves the coordinator's cursor: --actor <name> is required (or NOVA_SPRINT_ACTOR); nothing was changed")
		}
		if why, err := coordinatorsAlone(ctx, st, rc); err != nil || why != "" {
			if err != nil {
				return a.readFailed("inbox", err, stderr)
			}
			return refuse(stderr, "inbox", why)
		}
	}
	if isNumber(*open) {
		return refuse(stderr, "inbox", "group numbers are not accepted: --open wants a group's id, as inbox prints it")
	}
	if *wait && a.twinOpen(c.redis) {
		return refuse(stderr, "inbox", twinMachine)
	}
	woke := false
	if *wait {
		// the coordinator's one wake a tick (errata 3 amendment 8): the next
		// tick-end note after the notes as they stand now
		if *atEpoch >= 0 || *timeout <= 0 {
			return refuse(stderr, "inbox", "--wait waits on the sprint's epoch for at most a --timeout above zero")
		}
		_, from, err := st.B.Tails(ctx)
		if err != nil {
			return a.readFailed("inbox", err, stderr)
		}
		woke, err = st.WaitTickEnd(ctx, from, *timeout)
		if err != nil {
			return a.readFailed("inbox", err, stderr)
		}
		if !woke {
			// the timeout is said to the person on the stream that is theirs:
			// stdout in the plain rendering, stderr under --json (stdout stays
			// one JSON object, which carries woke=false as well)
			w := stdout
			if c.json {
				w = stderr
			}
			fmt.Fprintf(w, "inbox --wait: no tick end in %s\n", *timeout)
		}
	}
	v, err := st.Inbox(ctx, *deadline, *stale, 10000)
	if err != nil {
		return a.readFailed("inbox", err, stderr)
	}
	var opened *sprint.Group
	if *open != "" {
		g, ok := sprint.FindGroup(v.Groups, *open)
		if !ok {
			fmt.Fprintf(stderr, "%s inbox: no group %s now; %s\n", prog, oneline.Escape(*open), oneline.Escape(groupList(v.Groups)))
			return 1
		}
		opened = &g
	}
	if *read && v.Last != "" {
		if err := st.B.SetCursor(ctx, v.Last); err != nil {
			return a.readFailed("inbox", err, stderr)
		}
	}
	if c.json {
		groups := v.Groups
		if groups == nil {
			groups = []sprint.Group{}
		}
		mach, _, _ := st.Machine(ctx)
		machine := st.MachineLine(ctx)
		judgments, happened := inboxActs(groups, a.now())
		out := map[string]any{"groups": groups, "judgments": judgments, "happened": happened, "done": mach.Done(),
			"last": v.Last, "cursor": v.Cursor, "at": a.now(), "machine": machine}
		if *wait {
			// the timeout is in both renderings: the line above, and woke=false
			// here (the one-value rule)
			out["woke"] = woke
		}
		if opened != nil {
			out["open"] = nonNil(opened.Members)
			if len(opened.Needs) > 0 {
				out["needs"] = opened.Needs
			}
		}
		b, _ := json.Marshal(out)
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	now := a.now()
	judg, other := 0, 0
	for _, g := range v.Groups {
		if g.Kind == sprint.Judgment {
			judg++
		} else {
			other++
		}
		fmt.Fprintln(stdout, groupLine(g, now))
		if g.Hint != "" {
			fmt.Fprintf(stdout, "  %s\n", oneline.Escape(g.Hint))
		}
		for _, cmd := range g.Commands {
			fmt.Fprintf(stdout, "  %s:\n", oneline.Escape(cmd.Decision))
			for _, l := range cmd.Lines {
				fmt.Fprintf(stdout, "    %s\n", oneline.Escape(l))
			}
		}
		if opened != nil && g.ID == opened.ID {
			for _, m := range g.Members {
				fmt.Fprintf(stdout, "  %s\n", oneline.Escape(m))
			}
			for _, n := range g.Needs {
				fmt.Fprintf(stdout, "  NEEDS %s\n", oneline.Escape(n))
			}
			fmt.Fprintf(stdout, "  notes: %s\n", oneline.Escape(strings.Join(g.Notes, " ")))
		}
	}
	fmt.Fprintf(stdout, "INBOX OK judgments=%d happened=%d cursor=%s\n", judg, other, dashed(v.Cursor))
	if line := st.MachineLine(ctx); line != "" {
		fmt.Fprintln(stdout, line)
	}
	return 0
}

// inboxJudgment is one open judgment as a program acts on it: what it is,
// the cards it is about, and each decision open to the coordinator as the
// exact command lines that make it (the ones inbox prints), in order.
type inboxJudgment struct {
	ID        string        `json:"id"` // what --group takes
	Kind      string        `json:"kind"`
	Type      string        `json:"type"`
	What      string        `json:"what"`
	Stream    string        `json:"stream"`
	Size      int           `json:"size"` // what --expect takes
	Cards     []string      `json:"cards"`
	Notes     []string      `json:"notes"` // what --answers takes
	Marked    bool          `json:"marked"`
	Overdue   bool          `json:"overdue"`
	Waited    string        `json:"waited"`
	Due       *time.Time    `json:"due,omitempty"`
	Answers   []inboxAnswer `json:"answers"`
	Decisions []string      `json:"decisions"`
}

// inboxAnswer is one decision and the command lines that make it.
type inboxAnswer struct {
	Decision string   `json:"decision"`
	Commands []string `json:"commands"`
}

// inboxHappened is one group of what happened since the cursor.
type inboxHappened struct {
	ID     string   `json:"id"`
	Kind   string   `json:"kind"`
	Type   string   `json:"type"`
	What   string   `json:"what"`
	Stream string   `json:"stream"`
	Size   int      `json:"size"`
	Count  int      `json:"count"`
	Cards  []string `json:"cards"`
	Notes  []string `json:"notes"`
	To     string   `json:"to,omitempty"`
	Hint   string   `json:"hint,omitempty"`
}

// inboxActs splits the inbox's groups into the open judgments and what
// happened, each with its cards whole, for inbox --json.
func inboxActs(groups []sprint.Group, now time.Time) ([]inboxJudgment, []inboxHappened) {
	judgments, happened := []inboxJudgment{}, []inboxHappened{}
	for _, g := range groups {
		cards := nonNil(g.Members)
		if g.Kind != sprint.Judgment {
			happened = append(happened, inboxHappened{ID: g.ID, Kind: g.Kind, Type: g.Type, What: g.What, Stream: g.Stream, Size: g.Size, Count: g.Count,
				Cards: cards, Notes: nonNil(g.Notes), To: g.To, Hint: g.Hint})
			continue
		}
		j := inboxJudgment{ID: g.ID, Kind: g.Kind, Type: g.Type, What: g.What, Stream: g.Stream, Size: g.Size, Cards: cards, Notes: nonNil(g.Notes),
			Marked: g.Marked, Overdue: g.Overdue, Waited: now.Sub(g.Oldest).Round(time.Second).String(), Answers: []inboxAnswer{}, Decisions: nonNil(g.Decisions)}
		if !g.Due.IsZero() {
			due := g.Due
			j.Due = &due
		}
		for _, c := range g.Commands {
			j.Answers = append(j.Answers, inboxAnswer{Decision: c.Decision, Commands: nonNil(c.Lines)})
		}
		judgments = append(judgments, j)
	}
	return judgments, happened
}

func dashed(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// groupLine is one inbox group: its kind, its id (what --group takes), a mark
// for a repeat or an overdue one, the type, the stream, its size (what --expect
// takes), how long it waited, the primaries (bounded, then the command that
// lists them all), and the decisions open to the coordinator.
func groupLine(g sprint.Group, now time.Time) string {
	mark := " "
	if g.Marked {
		mark = "!"
	}
	kind := strings.ToUpper(g.Kind)
	l := fmt.Sprintf("%s %s %s %s", kind, g.ID, mark, g.Type)
	if g.Stream != "" {
		l += "  stream=" + g.Stream
	}
	if g.Size > 0 || g.Kind == sprint.Judgment {
		l += "  size=" + strconv.Itoa(g.Size)
	} else {
		l += "  x" + strconv.Itoa(g.Count)
	}
	if g.Kind == sprint.Judgment {
		l += "  waited=" + now.Sub(g.Oldest).Round(time.Second).String()
		if g.Overdue {
			l += " OVERDUE"
		} else if !g.Due.IsZero() {
			l += "  due=" + g.Due.Local().Format("15:04:05")
		}
		if g.Before > 0 {
			l += "  before=" + strconv.Itoa(g.Before)
		}
	}
	if g.To != "" {
		l += "  for=" + g.To // addressed to the coordinator: shown first
	}
	if len(g.Primaries) > 0 {
		ps := g.Primaries
		more := ""
		if len(ps) > 8 || g.Size > len(ps) {
			if len(ps) > 8 {
				ps = ps[:8]
			}
			more = ",... all: nova-sprint inbox --open " + g.ID
		}
		l += "  (" + strings.Join(ps, ",") + more + ")"
	}
	if g.What != "" {
		l += "  " + g.What
	}
	if len(g.Commands) > 0 {
		return oneline.Escape(l) // the decisions follow, as commands
	}
	if len(g.Decisions) > 0 {
		l += "  -> " + strings.Join(g.Decisions, " | ")
	}
	if len(g.Notes) > 0 && g.Kind == sprint.Judgment {
		l += "  [answers: " + g.Notes[0]
		if len(g.Notes) > 1 {
			l += " +" + strconv.Itoa(len(g.Notes)-1)
		}
		l += "]"
	}
	return oneline.Escape(l)
}

// cardView is everything about one primary.
type cardView struct {
	Primary  *sprint.Card       `json:"primary"`
	Work     []*sprint.Card     `json:"work_cards"`
	Reads    []*sprint.Card     `json:"read_cards"`
	Merge    *sprint.Card       `json:"merge,omitempty"`
	Open     []sprint.Open      `json:"open,omitempty"`
	Needs    []sprint.NeedState `json:"needs,omitempty"`
	NeededBy []string           `json:"needed_by,omitempty"`
	Held     *sprint.Hold       `json:"held,omitempty"` // what holds it now (check rule 12)
	// What it cost: each consumer (a work card's take, a read) with its record, and
	// the totals, computed from the consumers' records (sprint.CardCost).
	Cost sprint.CardCostView `json:"cost"`
	// The story: its timeline from the log, and the words given (reports,
	// findings, fixes, reasons) whole.
	Timeline []storyLine `json:"timeline"`
	Texts    []storyText `json:"texts"`
}

func (a *app) cmdCard(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("card")
	atEpoch := fs.Int64("at-epoch", -1, "the primary as it was at an earlier epoch (before a clear)")
	fields := fs.Bool("fields", false, "every field of the primary and its cards, one record a line, instead of its story")
	pos, err := parse(fs, args)
	if err != nil || len(pos) != 1 {
		return refuse(stderr, "card", argErr("wants one primary id ", err))
	}
	id := pos[0]
	st, err := a.storeAt(*c, *atEpoch)
	if err != nil {
		return refuse(stderr, "card", err.Error())
	}
	ctx := context.Background()
	v, err := st.CardOf(ctx, id)
	if err != nil {
		return a.readFailed("card", err, stderr)
	}
	if v.Primary == nil {
		fmt.Fprintf(stderr, "%s card: no primary %s; run: nova-sprint where\n", prog, oneline.Escape(id))
		return 1
	}
	// What holds it (the no-stall rule, check rule 12): an outside actor, the
	// next tick, an open judgment, what it waits on, or the machine STOPPED.
	var held *sprint.Hold
	if *atEpoch < 0 {
		if hd, err := st.Held(ctx, id); err == nil {
			held = &hd
		}
	}
	lines, err := st.Log(ctx)
	if err != nil {
		return a.readFailed("card", err, stderr)
	}
	events, texts := a.story(v, lines)
	if c.json {
		if events == nil {
			events = []storyLine{}
		}
		if texts == nil {
			texts = []storyText{}
		}
		b, _ := json.Marshal(cardView{Primary: v.Primary, Work: v.Work, Reads: v.Reads, Merge: v.Merge, Open: v.Open, Needs: v.Needs, NeededBy: v.NeededBy, Held: held,
			Cost: sprint.CardCostOf(v.Primary), Timeline: events, Texts: texts})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	if !*fields {
		place := ""
		if ws, err := st.Load(ctx, []string{sprint.Work}, nil); err == nil && v.Primary.Placed() {
			place = linePlace(v.Primary, ws.Work.Column(sprint.States...))
		}
		a.printStory(stdout, v, events, texts, held, place)
		for _, w := range v.Work {
			for _, line := range sprint.AttemptLines(w) {
				fmt.Fprintln(stdout, oneline.Escape(line))
			}
		}
		if len(v.Work) > 0 {
			fmt.Fprintln(stdout, sprint.NextLine(v.Work))
		}
		// what it cost: a line per consumer that ended, and the totals
		for _, line := range sprint.CardCostOf(v.Primary).CostLines() {
			fmt.Fprintln(stdout, oneline.Escape(line))
		}
		epoch := uint64(0)
		if pinned, err := st.Pinned(ctx); err == nil {
			epoch = pinned.PinnedEpoch()
		}
		fmt.Fprintf(stdout, "CARD OK id=%s epoch=%d work_cards=%d read_cards=%d open=%d\n", oneline.Escape(id), epoch, len(v.Work), len(v.Reads), len(v.Open))
		return 0
	}
	printCard(stdout, "PRIMARY", v.Primary)
	for _, x := range v.Work {
		printCard(stdout, "WORK", x)
	}
	for _, x := range v.Reads {
		printCard(stdout, "READ", x)
	}
	if v.Merge != nil {
		printCard(stdout, "MERGE", v.Merge)
	}
	for _, n := range v.Needs {
		waived := ""
		if n.Waived {
			waived = " waived"
			if n.WaivedBy != "" {
				waived += " by " + oneline.Escape(n.WaivedBy)
			}
			if n.WaivedAt != "" {
				waived += " at " + oneline.Escape(n.WaivedAt)
			}
		}
		fmt.Fprintf(stdout, "NEEDS %s %s%s\n", oneline.Escape(n.ID), oneline.Escape(n.State), waived)
	}
	for _, n := range v.NeededBy {
		fmt.Fprintf(stdout, "NEEDED-BY %s\n", oneline.Escape(n))
	}
	for _, o := range v.Open {
		fmt.Fprintf(stdout, "OPEN %s %s -> %s\n", oneline.Escape(o.Note.ID), oneline.Escape(o.Note.Type), oneline.Escape(strings.Join(o.Note.Decisions, " | ")))
	}
	if held != nil {
		fmt.Fprintf(stdout, "HELD %s\n", oneline.Escape(held.String()))
	}
	epoch := uint64(0)
	if pinned, err := st.Pinned(ctx); err == nil {
		epoch = pinned.PinnedEpoch()
	}
	fmt.Fprintf(stdout, "CARD OK id=%s epoch=%d work_cards=%d read_cards=%d open=%d\n", oneline.Escape(id), epoch, len(v.Work), len(v.Reads), len(v.Open))
	return 0
}

func printCard(w io.Writer, kind string, c *sprint.Card) {
	place := "-"
	if c.Placed() {
		place = c.Row + ":" + c.Col
	}
	keys := make([]string, 0, len(c.Fields))
	for k := range c.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var fields []string
	for _, k := range keys {
		fields = append(fields, k+"="+oneline.Field(c.Fields[k]))
	}
	fmt.Fprintf(w, "%s %s place=%s score=%s rev=%d %s\n", kind, oneline.Escape(c.ID), oneline.Escape(place),
		strconv.FormatFloat(c.Score, 'g', -1, 64), c.Rev, strings.Join(fields, " "))
}

func (a *app) cmdCheck(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("check")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "check", argErr("takes no words ", err))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "check", err.Error())
	}
	rep, _, err := st.Check(context.Background(), 5)
	if err != nil {
		return a.readFailed("check", err, stderr)
	}
	code := 0
	if len(rep.Violations) > 0 {
		code = 1
	}
	if c.json {
		if rep.Violations == nil {
			rep.Violations = []sprint.Violation{}
		}
		b, _ := json.Marshal(rep)
		fmt.Fprintln(stdout, string(b))
		return code
	}
	var lines []string
	for _, v := range rep.Violations {
		lines = append(lines, v.String())
	}
	listed(stderr, "VIOLATION", lines, c.max, "check")
	status := "OK"
	if code != 0 {
		status = "FAIL"
	}
	out := stdout
	if code != 0 {
		out = stderr
	}
	inFlight := ""
	if rep.InFlight {
		inFlight = " in_flight=yes"
	}
	fmt.Fprintf(out, "CHECK %s violations=%d pending=%s%s\n", status, len(rep.Violations), dashed(rep.Pending), inFlight)
	return code
}

// cmdRoutes is each route of the store with what its attempts did: the work
// cards dealt on it, finished ok, failed, failed by the provider, and the mean
// wall from take to finish, so a bad route shows (docs/SPEC-SPRINT.md, the
// deal's route).
func (a *app) cmdRoutes(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("routes")
	if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
		return refuse(stderr, "routes", argErr("takes no words ", err))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "routes", err.Error())
	}
	ctx := context.Background()
	rs, _, err := st.Routes(ctx)
	if err != nil {
		return a.readFailed("routes", err, stderr)
	}
	s, err := st.Load(ctx, []string{sprint.Fleet}, nil)
	if err != nil {
		return a.readFailed("routes", err, stderr)
	}
	stats := sprint.RouteStats(rs, s.Fleet)
	if c.json {
		b, _ := json.Marshal(map[string]any{"tiers": sprint.TierRoutes(rs), "routes": stats})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	if t := sprint.TierRoutes(rs); t != "" {
		fmt.Fprintln(stdout, "TIERS "+t)
	}
	for _, x := range stats {
		r := x.Route
		model := "-"
		if r.Provider != "" {
			model = r.Provider + "/" + r.Model
		}
		how := fmt.Sprintf("tier=%s enabled=%t", oneline.Field(orDashStr(r.Tier, "-")), r.Enabled)
		if x.Pinned {
			how = "pinned"
		} else if r.Tier == "" {
			how = "gone" // a route the cards name that the store no longer holds
		}
		fmt.Fprintf(stdout, "ROUTE %s model=%s %s attempts=%d ok=%d failed=%d provider_failures=%d mean_wall=%s\n",
			oneline.Field(r.Name), oneline.Field(model), how, x.Attempts, x.OK, x.Failed, x.Provider, x.MeanWall)
	}
	fmt.Fprintf(stdout, "ROUTES OK routes=%d\n", len(stats))
	return 0
}
