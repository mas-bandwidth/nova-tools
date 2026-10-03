package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"slices"

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
// ETA. The ETA is the time until every card on the table has landed (the owner,
// 2026-10-02: "it's the ETA to all cards being done, not the cards that are in
// flight or not blocked"): every primary neither landed nor dropped, held ones
// too, at the landing rate (sprint.LandingRate), in whole minutes rounded up,
// with no seconds ("47m", "1h12m"), and from a day on in days and hours, the
// hours rounded up ("52d2h"); before five have landed, or with no rate, the ETA
// reads a dash. Every primary landed, it has no ETA: it is done (errata 3
// amendment 6).
// eta is the minutes left (etaMinutes, or the view's held value), 0 when there
// is no estimate. held is the cards no tick moves on its own (sprint.HeldBack:
// behind a sentinel not released, or admitted held), shown apart as held=N
// when there are any, and counted in the ETA.
func summary(t ntable.Table, held, eta int64) string {
	landed, all := counts(t)
	line := progress(t)
	if held > 0 {
		line += fmt.Sprintf(" held=%d", held)
	}
	switch {
	case all > 0 && landed == all:
		return progress(t) + " done"
	case eta >= 24*60:
		h := (eta + 59) / 60
		return fmt.Sprintf("%s -> ETA %dd%dh", line, h/24, h%24)
	case eta >= 60:
		return fmt.Sprintf("%s -> ETA %dh%dm", line, eta/60, eta%60)
	case eta > 0:
		return fmt.Sprintf("%s -> ETA %dm", line, eta)
	}
	return line + " -> ETA -"
}

// etaMinutes is the estimate of the minutes left, rounded up: every card not
// landed, held ones too, at rate cards an hour; 0 when there is none (fewer
// than five landed, nothing left, or no rate).
func etaMinutes(t ntable.Table, rate float64) int64 {
	landed, all := counts(t)
	left := all - landed
	if rate <= 0 || landed < 5 || left <= 0 {
		return 0
	}
	return int64(math.Ceil(float64(left) * 60 / rate))
}

// etaHold is how long the view holds an estimate: it shows the largest of the
// last etaHold, so the value is stable while landings arrive in rounds.
const etaHold = 10 * time.Second

// etaSample is one estimate the view computed and when.
type etaSample struct {
	at time.Time
	m  int64
}

// etaKey is what the cards still to land are made of apart from the landings:
// every primary on the table and the held ones. A landing changes neither; add,
// drop and release change one (a brief, a rework and a stream remove change
// neither: none adds, takes off or frees a card), and their change reaches the
// table at the next tick's drain.
type etaKey struct{ all, held int64 }

// heldETA is the largest estimate of the last etaHold, m at now among them: a
// stable value while landings arrive in rounds. The hold is over the same cards
// to land: an estimate made over another key is dirty and is forgotten, so the
// first read after the tick that drained an add, a drop or a release shows the
// estimate recomputed over the new count at the rate measured (the owner,
// 2026-10-02: "When you add new cards, the ETA needs to be made dirty and
// recalculated."; nova-tools#5171). No estimate (0) is shown as none and
// forgets what was held. One process holds its own: a where run once shows its
// estimate, a watch and the server hold theirs.
func (a *app) heldETA(now time.Time, k etaKey, m int64) int64 {
	a.etaMu.Lock()
	defer a.etaMu.Unlock()
	if m == 0 || k != a.etaKey {
		a.etas, a.etaKey = nil, k
	}
	if m == 0 {
		return 0
	}
	a.etas = append(slices.DeleteFunc(a.etas, func(s etaSample) bool { return now.Sub(s.at) >= etaHold }), etaSample{now, m})
	for _, s := range a.etas {
		m = max(m, s.m)
	}
	return m
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
	isReader := false
	if *as != "" {
		// the reader's own queue is its beat (docs/SPEC-SPRINT.md section 6):
		// a name that is no reader's row writes none, and the answer says so
		// (reader), for the reader loop to say whose verb makes the row
		if isReader, err = st.ReaderBeat(ctx, *as); err != nil {
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
		// a reader runs at its machine's width: reader-<m>'s is m's fleet row's
		// (sprint.ReaderMachine), read here as the member's own is above
		if m, ok := sprint.ReaderMachine(*as); isReader && ok {
			ctl, err := st.ReadCells(ctx, sprint.Fleet, m, sprint.Ctl)
			if err != nil {
				return a.readFailed("queue", err, stderr)
			}
			if len(ctl) > 0 {
				width = sprint.MemberWidth(ctl[0])
			}
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
			out["width"] = width // the worker runs this many: the fleet row is the truth
		}
		if *as != "" {
			out["reader"] = isReader // --as is a row of the readers table
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
	err = noSprintYet(err)
	fmt.Fprintf(stderr, "%s %s: %s\n", prog, verbName, oneline.WithRemedy(err.Error(), prog+" "+verbName+" -h"))
	if ntable.IsRefusal(err) {
		return 1
	}
	return 2
}

// noSprintYet is err as a store with no sprint in it says it: a table the sprint reads is
// not there (NOTABLE) because init never ran, so the line names init, never the table
// layer's words (ONBOARDING point 2); any other err as it is. A refusal stays a refusal.
func noSprintYet(err error) error {
	var r *ntable.Refusal
	if !errors.As(err, &r) || r.Code != "NOTABLE" {
		return err
	}
	return &ntable.Refusal{Code: r.Code, Location: "this store", Sentence: "no sprint here yet: init makes its tables",
		Next: "nova-sprint init --coordinator <name>"}
}

// whereView is the view, for a program.
type whereView struct {
	At          time.Time                               `json:"at"`
	Landed      int64                                   `json:"landed"`
	All         int64                                   `json:"all"`
	Held        int64                                   `json:"held,omitempty"` // behind a sentinel not released, or admitted held: in the ETA
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
	// Seat is the seat's last change (coordinator <name>): who gave or took
	// it, when and why; absent while the seat has not moved since init.
	Seat *sprint.SeatChange `json:"seat,omitempty"`
	// Providers is the providers table (nova-tools#5199): each provider the routes name,
	// its balance as the run loop's poll last read it, the spend an hour measured, and
	// whether its routes serve; absent with no route. The text frame does not draw it.
	Providers []sprint.ProviderRow `json:"providers,omitempty"`
}

// whereRun is what one where was asked, its flags read.
type whereRun struct {
	c       common
	watch   bool
	all     bool
	every   time.Duration
	stale   time.Duration
	atEpoch int64
}

func (a *app) cmdWhere(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("where")
	watch := fs.Bool("watch", false, "redraw in place every --every until interrupted")
	every := fs.Duration("every", time.Second, "the redraw interval with --watch, above 0")
	all := fs.Bool("all", false, "draw the readers and merge tables too, hidden from the default frame (--json always carries them)")
	stale := fs.Duration("stale", defaultStale, "a stream with no progress for longer is shown stalled (--json)")
	atEpoch := fs.Int64("at-epoch", -1, "the sprint as it was at an earlier epoch (before a clear)")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "where", argErr("takes no words ", err, pos...))
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
	r := whereRun{c: *c, watch: *watch, all: *all, every: *every, stale: *stale, atEpoch: *atEpoch}
	if addr := a.server(fs); addr != "" {
		// the sprint's server draws each frame: one plain where a frame, so the watch
		// never holds the server between frames
		plain := without(fs, args, "watch", "every")
		return a.drawLoop(ctx, r, stdout, stderr, func(ctx context.Context) (string, int, bool) {
			res, err := a.ask(ctx, addr, []string{"where"}, plain)
			switch {
			case err != nil && ctx.Err() != nil:
				return "", 0, false // an interrupt cut the read short: the watch is over, not failed
			case err != nil:
				return "", a.unanswered("where", addr, err, stderr), false
			case res.Code != 0:
				a.answer(res, stdout, stderr)
				return "", res.Code, false
			}
			_, _ = io.WriteString(stderr, res.Stderr) // ignored: the caller's own stream
			return res.Stdout, 0, true
		})
	}
	return a.whereLoop(ctx, r, stdout, stderr)
}

// whereLoop shows the view: once, or with --watch every --every until ctx is
// done. A watch of the text redraws in place (watchWriter); --json prints
// one object a frame.
func (a *app) whereLoop(ctx context.Context, r whereRun, stdout, stderr io.Writer) int {
	return a.drawLoop(ctx, r, stdout, stderr, func(ctx context.Context) (string, int, bool) {
		// every frame reads the sprint's epoch again: a clear while it
		// watches shows the new epoch
		st, err := a.storeAtCtx(ctx, r.c, r.atEpoch)
		if err != nil {
			if ctx.Err() != nil {
				return "", 0, false // an interrupt cut the read short: the watch is over, not failed
			}
			return "", refuse(stderr, "where", err.Error()), false
		}
		v, frame, err := a.where(ctx, st, r.stale, r.all)
		if err != nil {
			if ctx.Err() != nil {
				return "", 0, false // an interrupt cut the read short: the watch is over, not failed
			}
			return "", a.readFailed("where", err, stderr), false
		}
		if r.c.json {
			b, _ := json.Marshal(v)
			return string(b) + "\n", 0, true
		}
		return frame, 0, true
	})
}

// drawLoop prints the frames frame gives: once, or with --watch every --every until
// ctx is done, a text frame redrawn in place (watchWriter). frame is the text of one
// frame, or the exit code the view ends with (ok false).
func (a *app) drawLoop(ctx context.Context, r whereRun, stdout, stderr io.Writer, frame func(context.Context) (text string, code int, ok bool)) int {
	var w *watchWriter
	if r.watch && !r.c.json {
		w = newWatchWriter(stdout, func() (int, int) { return a.screen(stdout) })
		w.hideCursor()
		defer w.showCursor()
	}
	for {
		text, code, ok := frame(ctx)
		if !ok {
			return code
		}
		if w != nil {
			if err := w.frame(text); err != nil {
				fmt.Fprintf(stderr, "%s where: stdout: %s\n", prog, oneline.Escape(err.Error()))
				return 1
			}
		} else {
			fmt.Fprint(stdout, text)
		}
		if !r.watch || !a.pause(ctx, r.every) {
			return 0
		}
	}
}

// where is the view and its frame. The frame draws the tables of sprint.ShownOrder,
// or with all every table in sprint.AllOrder: the readers and merge tables are
// hidden from the default frame (the owner, 2026-10-02: "please hide the reader
// and merge tables"); the view for a program carries every table whichever is drawn.
func (a *app) where(ctx context.Context, st *store.Store, stale time.Duration, all bool) (whereView, string, error) {
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
	seat, moved, err := st.Seat(ctx)
	if err != nil {
		return whereView{}, "", err
	}
	now := a.now()
	v := whereView{At: now, Tables: map[string]map[string]map[string]string{}, Streams: clocks, Epoch: st.PinnedEpoch(), Coordinator: coordinator}
	if moved {
		v.Seat = &seat
	}
	if v.Epoch == es.N {
		v.Cleared = es.Cleared
	}
	v.Landed, v.All = counts(shapes[0])
	// the held cards and the landings of the hour from the tick's where
	// record: no card is read (store.WhereFacts)
	facts, err := st.WhereFacts(ctx, shapes[0].Revision)
	if err != nil {
		return whereView{}, "", err
	}
	v.Held = int64(facts.Held)
	rate := sprint.LandingRate(facts.Landed, v.Landed, facts.Machine.Spans, facts.Machine.FirstStart(es.Cleared), now)
	v.Summary = summary(shapes[0], v.Held, a.heldETA(now, etaKey{v.All, v.Held}, etaMinutes(shapes[0], rate)))

	if f.Pending != nil {
		v.Pending = f.Pending.ID
	}
	if facts.Records {
		v.Machine = st.MachineLineOf(facts.Machine, facts.Heartbeat)
	}
	var b strings.Builder
	b.WriteString(a.seatTitle(v.Coordinator, v.Seat, now) + "\n\n" + whereHeader(v.Summary, v.Machine) + "\n\n")
	parts := map[string]string{}
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
		switch logical { // the text only: v.Tables keeps every reader's and stream's row
		case sprint.Readers:
			t = readersAll(t)
		case sprint.Merge:
			t = mergeAll(t)
		}
		if logical == sprint.Fleet {
			parts[logical] = fleetText(t)
			continue
		}
		// every table shows, every stream row in it, empty or not
		parts[logical] = ntable.Render(t, ntable.RenderOpts{Title: logical})
	}
	friends, err := st.FriendRows(ctx, now)
	if err != nil {
		return whereView{}, "", err
	}
	ft := friendsTable(friends)
	v.Tables[sprint.Friends] = map[string]map[string]string{}
	for _, r := range ft.Rows {
		cells := map[string]string{}
		for j, col := range ft.Columns {
			cells[col.Name] = ntable.CellText(ft.Columns, r, j)
		}
		v.Tables[sprint.Friends][r.Key] = cells
	}
	// the table layer draws it as it draws the fleet: header, rule, rows, rule,
	// the folded footer; with no friend the header, its rule and the footer
	parts[sprint.Friends] = ntable.Render(ft, ntable.RenderOpts{Title: sprint.Friends})
	order := sprint.ShownOrder
	if all {
		order = sprint.AllOrder
	}
	var shown []string
	for _, t := range order {
		shown = append(shown, parts[t])
	}
	b.WriteString(strings.Join(shown, "\n"))
	a.goalsView(ctx, st, &v)
	if v.Providers, err = providersView(ctx, st, shapes, now); err != nil {
		return whereView{}, "", err
	}
	for _, c := range clocks {
		if c.Stalled(now, stale) {
			v.Stalled = append(v.Stalled, c.Stream)
		}
	}
	return v, b.String(), nil
}

// providersView is the providers table from the routes and the fleet table's properties as
// the view's shapes read them (no card is read).
func providersView(ctx context.Context, st *store.Store, shapes []ntable.Table, now time.Time) ([]sprint.ProviderRow, error) {
	routes, _, err := st.Routes(ctx)
	if err != nil || len(routes) == 0 {
		return nil, err
	}
	fleet := sprint.NewTable(sprint.Fleet)
	if i := slices.Index(sprint.ViewOrder, sprint.Fleet); i >= 0 && i < len(shapes) {
		fleet.SetProps(shapes[i].Props)
	}
	return sprint.ProviderRows(routes, fleet, now), nil
}

// friendsTable is the friends table (sprint.FriendsDef) with a row per friend
// in the order given: her job cards' counts in ready, working and the hidden
// ok and failed, her width and her status as text; done and ok% are the
// table's own formulas over the counts (ntable.CellText), as the fleet
// table's are.
func friendsTable(friends []store.FriendRow) ntable.Table {
	t := sprint.FriendsDef()
	at := map[string]int{}
	for j, c := range t.Columns {
		at[c.Name] = j
	}
	for _, f := range friends {
		cells := make([]ntable.Cell, len(t.Columns))
		cells[at[string(sprint.Ready)]].Count = int64(f.Ready)
		cells[at[string(sprint.Working)]].Count = int64(f.Working)
		cells[at[sprint.DoneOK]].Count = int64(f.OK)
		cells[at[sprint.DoneFailed]].Count = int64(f.Failed)
		t.Rows = append(t.Rows, ntable.Row{Key: f.Name, Cells: cells,
			Texts: map[string]string{sprint.FieldWidth: strconv.Itoa(f.Width), sprint.Status: f.Status}})
	}
	return t
}

// allRow is the label of the one row the view draws for the readers and the
// merge tables: blank, as the work table's footer is (the owner, 2026-10-02:
// "please remove 'all'").
const allRow = ""

// readersAll is the readers table as the view's text draws it: one row, unlabelled,
// whose cells are the sums over every reader (hidden rows, readers away or down,
// counted as the footer counted them), and no footer, which would say the same
// thing twice (the owner, 2026-10-01: "change the table to just be one row, sum
// of all"; "i just need to see reader *progress* overall"). A cell some reader's
// set did not come back for prints "?", as the footer's sum did. Display only:
// the stored table, its rows and where --json are as they were.
func readersAll(t ntable.Table) ntable.Table { return allOf(t, nil) }

// mergeAll is the merge table as the view's text draws it, the same way (the
// owner, 2026-10-01: "Can we please (for next sprint) do the same for merge"):
// queued, merged and stuck are the sums over every stream; ci and state, which
// do not add up, show the value across the streams that most needs the
// coordinator's eye (worst), and a stopped state the count of streams stopped,
// so one stopped stream of four is not hidden ("stopped 1").
func mergeAll(t ntable.Table) ntable.Table {
	ci, _ := worst(t.Rows, sprint.CI, "red", "green")
	state, n := worst(t.Rows, sprint.StateCol, sprint.StreamStopped, sprint.StreamMerging, sprint.StreamWaiting, sprint.StreamLanded)
	if state == sprint.StreamStopped {
		state += " " + strconv.Itoa(n)
	}
	return allOf(t, map[string]string{sprint.CI: ci, sprint.StateCol: state})
}

// worst is the value of a text column, over the rows, that comes first in
// order (most attention first), and how many rows hold it. A value the order
// does not name comes after every named one, and "-" or blank after that: the
// cell shows "-" when no row has a value.
func worst(rows []ntable.Row, col string, order ...string) (string, int) {
	rank := func(v string) int {
		if i := slices.Index(order, v); i >= 0 {
			return i
		}
		if v == "" || v == "-" {
			return len(order) + 1
		}
		return len(order)
	}
	w, n := "-", 0
	for _, r := range rows {
		v := r.Texts[col]
		switch {
		case n == 0 || rank(v) < rank(w):
			w, n = v, 1
		case v == w:
			n++
		}
	}
	if w == "" {
		w = "-"
	}
	return w, n
}

// allOf is the table as one row, unlabelled, whose count cells are the sums over every
// row (an unread set prints "?", as the footer's sum did) and whose text cells
// are texts, with no footer: the stored table, its rows and where --json are as
// they were.
func allOf(t ntable.Table, texts map[string]string) ntable.Table {
	all := ntable.Row{Key: allRow, Cells: make([]ntable.Cell, len(t.Columns)), Texts: texts}
	for _, r := range t.Rows {
		for j := range t.Columns {
			if j >= len(r.Cells) || r.Cells[j].Unread {
				all.Cells[j].Unread = true
				continue
			}
			all.Cells[j].Count += r.Cells[j].Count
		}
	}
	cols := slices.Clone(t.Columns)
	for j := range cols {
		cols[j].Fold = ntable.None
	}
	t.Columns, t.Rows = cols, []ntable.Row{all}
	return t
}

// fleetText is the fleet table as the view's text draws it (the owner,
// 2026-10-02, at a total of 132 over members of which 52 could take a card:
// "width 132?!"): every member's row as stored, and a footer that sums only the
// members whose status is up, so ready, working, width, done and ok% are the
// fleet that can take a card; a held or down member's numbers show on its own
// row and not in the total. Display only: the stored table, its rows and where
// --json are as they were. The footer is the table layer's own fold over the up
// rows, drawn at the widths of the whole table so the two line up.
func fleetText(t ntable.Table) string {
	up := t
	up.Rows = nil
	for _, r := range t.Rows {
		if r.Texts[sprint.Status] == sprint.Up {
			up.Rows = append(up.Rows, r)
		}
	}
	opts := ntable.RenderOpts{Title: sprint.Fleet}
	// the widths that hold every cell of both renderings, read off each one's rule
	var drawn []string
	for _, c := range t.Columns {
		if !t.IsHidden(c.Name) {
			drawn = append(drawn, c.Name)
		}
	}
	opts.Widths = map[string]int{}
	for _, text := range []string{ntable.Render(t, opts), ntable.Render(up, opts)} {
		lines := strings.Split(text, "\n")
		if len(lines) < 2 {
			continue
		}
		for k, seg := range strings.Split(lines[1], "-+-") {
			switch {
			case k == 0:
				opts.LabelWidth = max(opts.LabelWidth, len(seg))
			case k-1 < len(drawn):
				opts.Widths[drawn[k-1]] = max(opts.Widths[drawn[k-1]], len(seg))
			}
		}
	}
	all := strings.Split(strings.TrimSuffix(ntable.Render(t, opts), "\n"), "\n")
	ups := strings.Split(strings.TrimSuffix(ntable.Render(up, opts), "\n"), "\n")
	all[len(all)-1] = ups[len(ups)-1]
	return strings.Join(all, "\n") + "\n"
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
	wait := fs.Bool("wait", false, "block until a judgment, or a note to the coordinator, that was not in the inbox when the wait began (a held judgment never wakes it), or the machine stops; then show the inbox, saying what is new")
	timeout := fs.Duration("timeout", 5*time.Minute, "with --wait, the longest wait; the inbox is shown when it passes (with --push, how often the loop looks at the machine)")
	push := fs.String("push", "", "with --wait, keep running (until interrupted): each new judgment and note to the coordinator is written once as <dir>/<note id>.md, the group as inbox --open prints it and the clock; the files there are the cursor, so a restart pushes nothing twice; a local write; seat is the holder's inbox, ~/<holder>-working/inbox/sprint-judgments, followed through a seat change (a directory named seat is ./seat)")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "inbox", argErr("takes no words ", err, pos...))
	}
	if *read && *atEpoch >= 0 {
		return refuse(stderr, "inbox", "--read moves the cursor of the sprint's epoch, and --at-epoch reads an earlier one as it was: give one of them")
	}
	if *push != "" && (!*wait || *read || *open != "" || *atEpoch >= 0) {
		return refuse(stderr, "inbox", "--push <dir> runs with --wait alone, a loop that writes each new judgment to the directory: it takes no --read, --open or --at-epoch")
	}
	if *read && *wait && a.server(fs) != "" {
		// the cursor is the server's to move, and a wait never runs on the server (waits)
		return refuse(stderr, "inbox", "--read moves the cursor, which the sprint's server (NOVA_SPRINT_SERVER) moves, and --wait waits where it is typed, never on the server: run nova-sprint inbox --wait, then nova-sprint inbox --read; nothing was changed")
	}
	if addr := a.server(fs); addr != "" && *wait {
		return a.inboxWaitAt(addr, fs, args, *atEpoch, *timeout, *push, c.json, stdout, stderr)
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
	var fresh []sprint.Group
	if *wait {
		// the coordinator's one wake (errata 3 amendment 8): the tick-end
		// notes are slept on, and the first that brings something new for
		// the coordinator ends the wait (inboxwait.go)
		if *atEpoch >= 0 || *timeout <= 0 {
			return refuse(stderr, "inbox", "--wait waits on the sprint's epoch for at most a --timeout above zero")
		}
		src, err := a.storeSource(ctx, st, *deadline, *stale)
		if err != nil {
			return a.readFailed("inbox", err, stderr)
		}
		if *push != "" {
			return a.pushLoop(ctx, src, *push, *timeout, c.json, stdout, stderr)
		}
		first, err := src.inbox(ctx, "")
		if err != nil {
			return a.readFailed("inbox", err, stderr)
		}
		var after inboxLook
		if fresh, after, err = a.waitNew(ctx, src, seenFresh(seenKeys(first.groups)), first.machine == machineRunning, *timeout); err != nil {
			return a.waitFailed(err, stderr)
		}
		stopped := first.machine == machineRunning && after.machine != machineRunning
		woke = len(fresh) > 0 || stopped
		sayWoke(fresh, stopped, *timeout, c.json, stdout, stderr)
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
		holder, err := st.B.Coordinator(ctx)
		if err != nil {
			return a.readFailed("inbox", err, stderr)
		}
		out := map[string]any{"groups": groups, "judgments": judgments, "happened": happened, "done": mach.Done(),
			"last": v.Last, "cursor": v.Cursor, "at": a.now(), "machine": machine, "coordinator": holder}
		if *wait {
			// how the wait ended is in both renderings: the line above, and
			// woke with the new groups' ids here (the one-value rule)
			out["woke"], out["new"] = woke, idsOf(fresh)
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
		fmt.Fprint(stdout, groupText(g, now, opened != nil && g.ID == opened.ID))
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
		} else if g.Quiet {
			l += "  quiet until=" + g.Due.Local().Format("15:04:05") // wait set it
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
	if g.What != "" && g.Kind != sprint.Judgment {
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

// groupText is one inbox group as inbox prints it: its line, a judgment's
// finding in full under it (one line per line of the finding), its hint, each
// decision with the command lines that make it, and, opened (inbox --open),
// every member, every need and its notes.
func groupText(g sprint.Group, now time.Time, opened bool) string {
	var b strings.Builder
	fmt.Fprintln(&b, groupLine(g, now))
	if g.Kind == sprint.Judgment && g.What != "" {
		for _, l := range strings.Split(g.What, "\n") {
			fmt.Fprintf(&b, "  %s\n", oneline.Escape(l))
		}
	}
	if g.Hint != "" {
		fmt.Fprintf(&b, "  %s\n", oneline.Escape(g.Hint))
	}
	for _, cmd := range g.Commands {
		fmt.Fprintf(&b, "  %s:\n", oneline.Escape(cmd.Decision))
		for _, l := range cmd.Lines {
			fmt.Fprintf(&b, "    %s\n", oneline.Escape(l))
		}
	}
	if opened {
		for _, m := range g.Members {
			fmt.Fprintf(&b, "  %s\n", oneline.Escape(m))
		}
		for _, n := range g.Needs {
			fmt.Fprintf(&b, "  NEEDS %s\n", oneline.Escape(n))
		}
		fmt.Fprintf(&b, "  notes: %s\n", oneline.Escape(strings.Join(g.Notes, " ")))
	}
	return b.String()
}

// cardView is everything about one primary.
type cardView struct {
	Primary  *sprint.Card       `json:"primary"`
	Tier     string             `json:"tier"`    // the tier it is on (sprint.CardTiers)
	Ceiling  string             `json:"ceiling"` // the highest the machine escalates it to
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
		return refuse(stderr, "card", argErr("wants one primary id ", err, pos...))
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
		tier, ceiling := sprint.CardTiers(v.Primary)
		b, _ := json.Marshal(cardView{Primary: v.Primary, Tier: tier, Ceiling: ceiling, Work: v.Work, Reads: v.Reads, Merge: v.Merge, Open: v.Open, Needs: v.Needs, NeededBy: v.NeededBy, Held: held,
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
		// the tier it is on and its ceiling: flash first, pro on escalation
		tier, ceiling := sprint.CardTiers(v.Primary)
		fmt.Fprintf(stdout, "CARD OK id=%s epoch=%d work_cards=%d read_cards=%d open=%d tier=%s ceiling=%s\n", oneline.Escape(id), epoch, len(v.Work), len(v.Reads), len(v.Open), tier, ceiling)
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
	var fields []string
	for _, k := range slices.Sorted(maps.Keys(c.Fields)) {
		fields = append(fields, k+"="+oneline.Field(c.Fields[k]))
	}
	fmt.Fprintf(w, "%s %s place=%s score=%s rev=%d %s\n", kind, oneline.Escape(c.ID), oneline.Escape(place),
		strconv.FormatFloat(c.Score, 'g', -1, 64), c.Rev, strings.Join(fields, " "))
}

func (a *app) cmdCheck(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("check")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "check", argErr("takes no words ", err, pos...))
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
// deal's route), and when its rest ends while the machine rests it for children
// that ended with no result (rule 3, sprint.RouteRests).
func (a *app) cmdRoutes(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("routes")
	if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
		return refuse(stderr, "routes", argErr("takes no words ", err, pos...))
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
	rests, balances := sprint.RouteRests(rs, s.Fleet), sprint.ProviderBalances(s.Fleet)
	for i := range stats {
		if r, ok := rests[stats[i].Route.Name]; ok && r.Resting(s.Now) {
			stats[i].RestedUntil = r.Until.UTC().Format(time.RFC3339)
			if r.Open() {
				stats[i].RestedUntil = "open" // until a balance returns
			}
			stats[i].RestedFor = r.Cause + ": " + r.Said()
		}
		if b, ok := balances[stats[i].Route.Provider]; ok {
			stats[i].Balance, stats[i].BalanceAt = "unknown", b.At.UTC().Format(time.RFC3339)
			if b.Known {
				stats[i].Balance = sprint.Dollars(b.Balance)
			}
		}
	}
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
		fmt.Fprintf(stdout, "ROUTE %s model=%s %s attempts=%d ok=%d failed=%d provider_failures=%d mean_wall=%s rested_until=%s balance=%s\n",
			oneline.Field(r.Name), oneline.Field(model), how, x.Attempts, x.OK, x.Failed, x.Provider, x.MeanWall, orDashStr(x.RestedUntil, "-"), oneline.Field(orDashStr(x.Balance, "-")))
	}
	fmt.Fprintf(stdout, "ROUTES OK routes=%d\n", len(stats))
	return 0
}
