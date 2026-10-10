package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"math/big"
	"slices"

	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/sprintdash"
)

// The read verbs: queue, where, inbox, card, check. Each has --json, one
// object for a program; the driver reads the sprint through them.

// summary is the sprint's line, the headline: landed / all primaries on the
// table, percent, ETA. On the table is the streams drawn: an archived stream's
// cards (stream archive) leave the headline when it is archived (the owner,
// 2026-10-06, after 69 landed streams were archived and the headline still read
// 1859/2874: "I really don't think we have 2.8k cards post-archive..."), and
// where --json carries them beside it (archived_cards, archived_landed). The ETA is the time until every card on the table has landed (the owner,
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
	line := progress(t)
	if held > 0 {
		line += fmt.Sprintf(" held=%d", held)
	}
	switch {
	case sprintDone(t):
		return doneLine(t)
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
// landed on the table, held ones too, at rate cards an hour; 0 when there is
// none (fewer than five landed in the epoch, an archived stream's counted, as
// the rate counts them; nothing left; or no rate).
func etaMinutes(t ntable.Table, rate float64) int64 {
	landed, all := counts(t)
	gone, _ := archivedCounts(t)
	left := all - landed
	if rate <= 0 || landed+gone < 5 || left <= 0 {
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
// every primary of the epoch (the table's and the archived streams') and the
// held ones. An archive moves landed cards off the table and changes neither,
// so it keeps the held estimate. A landing changes neither; add,
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

// sprintDone is whether every primary has landed: every one on the table, or,
// with none on the table, every stream archived (an archived stream holds only
// landed cards; the tick archives every stream of a sprint done).
func sprintDone(t ntable.Table) bool {
	landed, all := counts(t)
	_, gone := archivedCounts(t)
	return landed == all && (all > 0 || gone > 0)
}

// doneLine is the headline of a sprint done, "N/N 100.0% done", over the
// epoch's cards, the archived streams' too: with nothing left on the table
// there is no progress to count, and the tick archives each stream of a sprint
// done, so the table's alone would read 0/0.
func doneLine(t ntable.Table) string {
	landed, all := counts(t)
	gl, ga := archivedCounts(t)
	return progressOf(landed+gl, all+ga) + " done"
}

// progress is landed / all primaries on the table and the percent: "3/10 30.0%".
func progress(t ntable.Table) string {
	return progressOf(counts(t))
}

func progressOf(landed, all int64) string {
	pct := "0.0%"
	if all > 0 {
		pct = strconv.FormatFloat(100*float64(landed)/float64(all), 'f', 1, 64) + "%"
	}
	return fmt.Sprintf("%d/%d %s", landed, all, pct)
}

// counts is the landed and all primaries of the work table's streams on the
// table: an archived stream's row (archivedRow) is not counted (archivedCounts
// counts those).
func counts(t ntable.Table) (landed, all int64) {
	return countRows(t, false)
}

// archivedCounts is the landed and all primaries of the archived streams.
func archivedCounts(t ntable.Table) (landed, all int64) {
	return countRows(t, true)
}

// archivedRow is whether a work row is an archived stream's: hidden (stream
// archive) and holding only landed cards. A hidden row that holds a card not
// landed again (an add to it) is back on the table, counted in the headline,
// before the next tick draws it again.
func archivedRow(t ntable.Table, r ntable.Row) bool {
	if !r.Hidden {
		return false
	}
	j := t.Column(sprint.Landed)
	for k, c := range t.Columns {
		if c.Projection == ntable.Count && k != j && k < len(r.Cells) && r.Cells[k].Count > 0 {
			return false
		}
	}
	return true
}

func countRows(t ntable.Table, archived bool) (landed, all int64) {
	j := t.Column(sprint.Landed)
	for _, r := range t.Rows {
		if archivedRow(t, r) != archived {
			continue
		}
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

// readyPrimaries is the ready primaries across the work table's streams
// (docs/SPEC-SPRINT.md section 1, the ready buffer a fleet is fed at): the sum
// of the ready column's counts.
func readyPrimaries(t ntable.Table) int64 {
	j := t.Column(string(sprint.Ready))
	var n int64
	for _, r := range t.Rows {
		if j >= 0 && j < len(r.Cells) {
			n += r.Cells[j].Count
		}
	}
	return n
}

// upWidth is the total width of the fleet members whose status is up
// (docs/SPEC-SPRINT.md section 1, the fleet table's width column): the sum of
// each up member's width, the default where its row names none.
func upWidth(t ntable.Table) int {
	var n int
	for _, r := range t.Rows {
		if r.Texts[sprint.Status] != sprint.Up {
			continue
		}
		if w, err := sprint.ParseWidth(r.Texts[sprint.FieldWidth]); err == nil {
			n += w
		} else {
			n += sprint.DefaultWidth
		}
	}
	return n
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
	// Priority is the card's level (sprint.QueuePriority: a read card's reader, a work card's
	// as its deal wrote it, a primary's own), printed beside it.
	Priority string `json:"priority"`
	// The stamps: a work card's dealt and taken, a read card's asked and begun.
	Dealt string `json:"dealt,omitempty"`
	Taken string `json:"taken,omitempty"`
	Asked string `json:"asked,omitempty"`
	Begun string `json:"begun,omitempty"`
	// WaitsFor is, for a waiting primary, the needs it still waits for.
	WaitsFor []string `json:"waits_for,omitempty"`
	// Packet is what the member or reader is handed with the card.
	Packet *sprint.Packet `json:"packet,omitempty"`
	// kind is the card's kind: a read on a friend's fleet row is returned, not finished.
	kind string
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
	width := -1 // a fleet member's width, from its row (-1: not a member, or none read; 0: it drains)
	add := func(table string, cs []*sprint.Card) {
		for _, x := range cs {
			cards = append(cards, queueCard{ID: x.ID, Table: table, Row: x.Row, Col: x.Col, Primary: x.F("primary"), Stream: x.F("stream"),
				Attempt: x.Int("attempt"), Gen: x.Int("gen"), Head: x.F("head"), Score: x.Score, Priority: queuePriority(x),
				Dealt: x.F("dealt"), Taken: x.F("taken"), Asked: x.F("asked"), Begun: x.F("begun"), kind: x.F("kind")})
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
		// a merge card carries no head: it is the primary's, read here with the queue
		mg, err := st.ReadCells(ctx, sprint.Work, *stream, sprint.Merging)
		if err != nil {
			return a.readFailed("queue", err, stderr)
		}
		for _, m := range mg {
			for i := range cards {
				if cards[i].ID == m.ID {
					cards[i].Head, cards[i].Attempt = m.F("head"), m.Int("attempt")
					cards[i].Priority, _ = sprint.CardPriority(m)
				}
			}
		}
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
		if width >= 0 {
			out["width"] = width // the worker runs this many: the fleet row is the truth
		}
		if word := machineWord(ctx, st); word != "" {
			out["machine"] = word // STOPPED: the worker cancels its lanes and takes nothing (internal/member machineStop)
		}
		if *as != "" {
			out["reader"] = isReader // --as is a row of the readers table
		}
		b, err := json.Marshal(out)
		if err != nil {
			// never an empty line at exit 0: a queue that cannot be written as JSON is said
			fmt.Fprintf(stderr, "%s queue: the answer could not be written as JSON (%d cards): %s\n", prog, len(cards), oneline.Escape(err.Error()))
			return 1
		}
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	var lines []string
	for _, x := range cards {
		l := fmt.Sprintf("%s %s:%s:%s", x.ID, x.Table, x.Row, x.Col)
		if x.Table == sprint.Fleet && x.kind == "read" {
			// a read on a friend's row: her outbox or the read verb returns it, never finish
			if x.Gen > 0 {
				l += " gen=" + strconv.Itoa(x.Gen)
			}
			l += " read: --ok|--broken " + x.ID + " --epoch " + strconv.FormatUint(epoch, 10)
		} else if x.Gen > 0 {
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
		if x.Priority != "" {
			l += " priority=" + x.Priority // its level, last (priority.go)
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
// a read past its free lanes, needs none: every packet carries its brief, and
// a reader's full answer already runs to hundreds of kilobytes a pass.
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
	Pushes  []sprint.SeatPushLine `json:"pushes,omitempty"`
	At      time.Time             `json:"at"`
	Landed  int64                 `json:"landed"`
	All     int64                 `json:"all"`
	Held    int64                 `json:"held,omitempty"` // behind a sentinel not released, or admitted held: in the ETA
	Summary string                `json:"summary"`
	// ArchivedCards and ArchivedLanded are the cards of the archived streams (stream
	// archive) and those landed, beside the headline, which counts only the streams on the
	// table: All+ArchivedCards and Landed+ArchivedLanded are the whole epoch's.
	ArchivedCards  int64 `json:"archived_cards"`
	ArchivedLanded int64 `json:"archived_landed"`
	// Done is a sprint done (sprintDone): every card of the epoch landed. Then the headline,
	// Landed, All and the summary's "N/N 100.0% done", is the epoch's, the archived streams'
	// cards included, as the CLI's done line is; the dashboard's hero and cost read it.
	Done bool `json:"done,omitempty"`
	// Tables is table -> row -> column -> cell as printed (a string, every cell of every
	// row, the shape the dashboard's pull reads); a work row carries besides its cells
	// `per_landed` (dollars per landed card, as the cost column shows money).
	// StreamCosts carries what is no string, beside it.
	Tables map[string]map[string]map[string]any `json:"tables"`
	// Tiers counts every card by its brief's tier (flash, pro, heavy, or whatever word the
	// brief carries), from the tick's where record; absent before the first tick.
	Tiers map[string]int `json:"tiers,omitempty"`
	// StreamCosts is each stream's cards by their briefs' tier (`tiers`, counts) and its
	// spend by the tier each attempt and read ran on (`cost_by_tier`, money strings), from
	// the tick's where record (sprint.TierCosts); absent before the first tick of an epoch.
	// A stream's complete cost (`total_cost`, and `per_landed` on its work row) includes
	// its reads. The reads are also on their own: `cost_reads` (the same dollars as
	// `read_cost`) and `reads_unpriced` (finished reads with no dollar figure).
	StreamCosts map[string]sprint.TierCosts `json:"stream_costs,omitempty"`
	// ReadSpend is the day's read spend per route, one line under the summary
	// (sprint.ReadSpendLine), from the tick's where record; absent when no read ended today.
	ReadSpend string               `json:"read_spend,omitempty"`
	Streams   []sprint.StreamClock `json:"streams"`
	// StageTimes is where a card's wall time goes: the median and p90 in seconds of each
	// stage (needs, deal, take, work, rework, read_wait, read, accept, merge) over the
	// cards landed in the last 24 h, overall and per stream, from the tick's where record
	// (sprint.CycleTimes, docs/SPEC-SPRINT.md, cycle-time-breakdownb.w1); absent before
	// the first tick of an epoch or with no landing in the window.
	StageTimes  *sprint.StageTimes    `json:"stage_times,omitempty"`
	Stalled     []string              `json:"stalled,omitempty"`
	Critical    []sprint.CriticalCard `json:"critical,omitempty"` // the five heaviest (weight.go)
	Coordinator string                `json:"coordinator,omitempty"`
	Pending     string                `json:"pending,omitempty"`
	Epoch       uint64                `json:"epoch"`
	Cleared     time.Time             `json:"cleared,omitempty"` // when the epoch began
	Machine     string                `json:"machine,omitempty"`
	// FleetWork and FriendsWork are the work switches (nova-sprint set --fleet, --friends;
	// sprint.PropFleet, sprint.PropFriends): on or off, always carried, so the dashboard greys
	// a side that is off. Off, the deal hands that side no work card; reads flow.
	FleetWork   string `json:"fleet_work"`
	FriendsWork string `json:"friends_work"`
	// FleetTiers and FriendsTiers are the tiers each side may take (nova-sprint set
	// --fleet-tiers, --friends-tiers; sprint.PropFleetTiers, sprint.PropFriendsTiers):
	// "all", the default, or the list, always carried.
	FleetTiers   any `json:"fleet_tiers"`
	FriendsTiers any `json:"friends_tiers"`
	// ReadsNeeded is the reads every card in review needs (nova-sprint set --reads;
	// sprint.PropReadsNeeded): the count while one is set, else "default" (each card's own
	// rule), always carried.
	ReadsNeeded any        `json:"reads_needed"`
	Goals       []goalView `json:"goals,omitempty"`
	// Seat is the seat's last change (coordinator <name>): who gave or took
	// it, when and why; absent while the seat has not moved since init.
	Seat *sprint.SeatChange `json:"seat,omitempty"`
	// Holds is every hold in force (hold <name>... --reason), with --cards (the dashboard's
	// read): what is held, its kind, the reason, by whom and since when; the tables' status
	// cells read held beside it.
	Holds []sprint.HoldView `json:"holds,omitempty"`
	// Providers is the providers table (nova-tools#5199): each provider the routes name,
	// its balance as the run loop's poll last read it, the spend an hour measured, and
	// whether its routes serve; absent with no route. The text frame does not draw it.
	Providers []sprint.ProviderRow `json:"providers,omitempty"`
	// Lanes is where --json --cards's, read for the dashboard: every machine's lanes with a
	// holder or a queue (lane list; docs/SPEC-SPRINT.md section 18); absent when none is, and
	// without --cards. The text frame does not draw it.
	Lanes []sprint.LaneRow `json:"lanes,omitempty"`
	// Cards and Judgments are where --json --cards's, read for the dashboard's pull routes
	// (store.Dealt): every work card dealt to a fleet row and not finished, and the open
	// judgments naming one of their primaries; absent without --cards.
	Cards []dealtCard `json:"cards,omitempty"`
	// Merging is where --json --cards's merge queue with heads: every primary merging, its
	// stream, its head and its attempt, so a program reads the queue and the heads in one call
	// (merge --landed names a card by id@head); absent without --cards.
	Merging   []mergingCard `json:"merging,omitempty"`
	Judgments []judgmentRef `json:"judgments,omitempty"`
	// Rows is where --json --rows's: every primary's row of the work table, in work
	// order, its fields but the brief; absent without --rows.
	Rows []primaryRow `json:"rows,omitempty"`
	// Archived is the archived streams (stream archive): their names, the cards landed
	// in them and what those cost, the work table's cost cells summed to the cent; absent
	// with none. Their rows of the work and merge tables, and their primaries in rows, are
	// in tables and rows only with --archived; the summary and the drawn footers leave
	// them out either way (ArchivedCards, ArchivedLanded carry their counts).
	Archived *archivedView `json:"archived,omitempty"`
	// Ready, Width, Buffer and Low are the ready buffer a program reads off
	// the view (docs/SPEC-SPRINT-DASHBOARD.md): the ready primaries across the
	// work table's streams, the total width of the fleet members that are up,
	// the string "<ready>/<2*width>" and whether ready is under width.
	Ready int64 `json:"ready"`
	// Backup is the pipeline's backup state (sprint.BackupOf over the work table's count
	// cells): reads, merges or none; ReadsWaiting the reads wanted now and not asked, from the
	// tick's where record (sprint.ReadsWaiting).
	Backup       string `json:"backup"`
	ReadsWaiting int    `json:"reads_waiting"`
	// ReadsWindow is the ok and broken verdicts over the last 30 minutes of running time and
	// when that window began (sprint.ReadsWindowOf), from the tick's where record; zero before
	// the first tick of an epoch.
	ReadsWindow sprint.ReadsWindowView `json:"reads_window"`
	// ReadCards is the epoch's read cards ready, working and done, and each fleet and friends
	// row of tables carries its cards by level and its reads (rowCardFields), from the tick's
	// where record (sprint.RowCardCounts).
	ReadCards sprint.ReadCardCounts `json:"read_cards"`
	Fix       int                   `json:"fix,omitempty"`
	// Priorities is every open primary whose level is not normal, by level, and
	// StreamPriorities each stream's default level that is not normal, from the tick's where
	// record (sprint.PriorityCounts, sprint.StreamPriorities); absent when every card is normal.
	Priorities       map[string][]string `json:"priorities,omitempty"`
	StreamPriorities map[string]string   `json:"stream_priorities,omitempty"`
	Width            int                 `json:"width"`
	Buffer           string              `json:"buffer"`
	Low              bool                `json:"low"`
	// MergeRow is the merge state, the dashboard's Merge row (docs/SPEC-SPRINT-DASHBOARD.md,
	// "Merge"): the cards in merging and review, landed per 30 minutes, the oldest merging
	// card's age (with --cards or --rows), the base's gate and its failing test, the drift
	// between the base and the development branch, and the minutes since the last sync and
	// the last promotion (mergeFactsOf).
	MergeRow sprintdash.MergeRow `json:"merge_row"`
	// Friends is the friends table's rows with what each friend's last beat reported (her
	// load and her own counts, friend beat), beside the table's counts, which are the sprint's.
	Friends []store.FriendRow `json:"friends,omitempty"`
	// Releases is the count of cards left per release across the streams (docs/SPEC-SPRINT.md section 11, where --release).
	Releases map[string]int64 `json:"releases,omitempty"`
	// StoreRTTP50MS and StoreRTTP99MS are the store round trip's p50 and p99 over the
	// last minute, as the server measured it (store.StoreRTTRecord, store-latency-row-r.w2).
	StoreRTTP50MS *float64 `json:"store_rtt_p50_ms,omitempty"`
	StoreRTTP99MS *float64 `json:"store_rtt_p99_ms,omitempty"`
}

// archivedView is where --json's archived streams (stream archive).
type archivedView struct {
	Streams []string `json:"streams"`
	Cards   int64    `json:"cards"`
	Landed  int64    `json:"landed"`
	Cost    string   `json:"cost"`
}

// archivedOf is the work table's archived streams, nil with none: the rows the
// table layer hides (row hide), counted in the folds and not drawn.
func archivedOf(t ntable.Table) *archivedView {
	var a archivedView
	var costs []string
	a.Landed, a.Cards = archivedCounts(t)
	for _, r := range t.Rows {
		if !archivedRow(t, r) {
			continue
		}
		a.Streams = append(a.Streams, r.Key)
		if v, ok := strings.CutPrefix(r.Texts[sprint.Cost], "$"); ok {
			costs = append(costs, v)
		}
	}
	if len(a.Streams) == 0 {
		return nil
	}
	a.Cost = "-"
	if sum, ok := cardcost.Sum(costs...); ok && len(costs) > 0 {
		if r, ok := new(big.Rat).SetString(sum); ok {
			a.Cost = cardcost.Cents(r)
		}
	}
	return &a
}

// drawnRows is the table without the archived streams' rows (stream archive),
// so its footer folds only the rows drawn. A hidden row of a stream back on the
// table (an add put a card not landed in it, archivedRow) is drawn at once,
// before the next tick shows it again, so the headline's count of its cards is
// backed by its row.
func drawnRows(t ntable.Table, gone *archivedView) ntable.Table {
	rows := make([]ntable.Row, 0, len(t.Rows))
	for _, r := range t.Rows {
		if gone.has(r.Key) {
			continue
		}
		r.Hidden = false
		rows = append(rows, r)
	}
	t.Rows = rows
	return t
}

// has is whether the stream is archived; a nil view has none.
func (a *archivedView) has(stream string) bool {
	return a != nil && slices.Contains(a.Streams, stream)
}

// archivedLine is the line under the work table of where's frame with streams
// archived: how many, the cards landed in them and what those cost.
func archivedLine(a *archivedView) string {
	if a == nil {
		return ""
	}
	return fmt.Sprintf("%d archived %s, %d %s landed, %s (where --json --archived)", len(a.Streams),
		map[bool]string{true: "stream", false: "streams"}[len(a.Streams) == 1], a.Landed,
		map[bool]string{true: "card", false: "cards"}[a.Landed == 1], a.Cost)
}

// dealtCard is a work card dealt to a fleet row and not finished: the row (a machine, or a
// friend's, friend.<name>), its state there, since its deal (ready) or its take (working),
// when its deadline falls by the clock (sprint.WorkDeadline; the tick counts running time,
// so a stop moves it later), and the branch its work is pushed to.
type dealtCard struct {
	ID       string    `json:"id"`
	Primary  string    `json:"primary"`
	Stream   string    `json:"stream"`
	Member   string    `json:"member"`
	State    string    `json:"state"`
	Since    time.Time `json:"since,omitzero"`
	Deadline time.Time `json:"deadline,omitzero"`
	Branch   string    `json:"branch"`
	Priority string    `json:"priority,omitempty"`
	Tier     string    `json:"tier,omitempty"` // the tier its route was drawn from (sprint.FieldTier)
}

// mergingCard is a primary merging: its stream, the head its merge would land, its attempt.
type mergingCard struct {
	ID      string `json:"id"`
	Stream  string `json:"stream"`
	Head    string `json:"head"`
	Attempt int    `json:"attempt,omitempty"`
}

// mergingView is the merging primaries, by stream then id.
func mergingView(cs []*sprint.Card) []mergingCard {
	out := make([]mergingCard, 0, len(cs))
	for _, c := range cs {
		out = append(out, mergingCard{ID: c.ID, Stream: c.Row, Head: c.F("head"), Attempt: c.Int("attempt")})
	}
	slices.SortFunc(out, func(a, b mergingCard) int { return cmp.Or(cmp.Compare(a.Stream, b.Stream), cmp.Compare(a.ID, b.ID)) })
	return out
}

// mergeFactsOf is the Merge row's facts (docs/SPEC-SPRINT-DASHBOARD.md, "Merge") from what
// where has read, and no card: the work table's merging and review counts, the where
// record's landings, the drift and the last sync the merge table's properties record
// (sprint.DevDriftOf), the last promotion the work table's (sprint.Promotion), and the
// lander's gate at its last landing (a live stream's ci). The open base-red judgments, the
// gate's red, are read only when a stream is stopped: the base-gate rule's stop stops it,
// and a sprint that runs keeps where's exchanges as they were (where_cost_test.go).
func mergeFactsOf(ctx context.Context, st *store.Store, work, merge ntable.Table, clocks []sprint.StreamClock, landed []time.Time, now time.Time) (sprintdash.MergeFacts, error) {
	f := sprintdash.MergeFacts{Now: now, Merging: cellCount(work, string(sprint.Merging)), Review: cellCount(work, string(sprint.Review)), Landed: landed}
	w, m := sprint.NewTable(sprint.Work), sprint.NewTable(sprint.Merge)
	w.SetProps(work.Props)
	m.SetProps(merge.Props)
	snap := &sprint.Snapshot{Now: now, Work: w, Merge: m}
	drift := sprint.DevDriftOf(snap)
	f.BaseLacks, f.DevLacks, f.LastSync = drift.BaseLacks, drift.DevLacks, drift.LastSync
	f.LastPromotion, _, _ = sprint.Promotion(snap)
	f.LanderGreen = slices.ContainsFunc(merge.Rows, func(r ntable.Row) bool { return !r.Hidden && r.Texts[sprint.CI] == "green" })
	if !slices.ContainsFunc(clocks, func(c sprint.StreamClock) bool { return c.State == sprint.StreamStopped }) {
		return f, nil
	}
	open, err := st.B.OpenNotes(ctx)
	if err != nil {
		return f, err
	}
	for _, o := range open {
		if o.Note.Type == sprint.NBaseRed || o.Note.Type == sprint.NDriftBaseRed {
			f.BaseRed = append(f.BaseRed, o.Note.What)
		}
	}
	return f, nil
}

// cellCount is the primaries in a column of the work table, every row's cell summed.
func cellCount(t ntable.Table, col string) int64 {
	j := t.Column(col)
	var n int64
	for _, r := range t.Rows {
		if j >= 0 && j < len(r.Cells) {
			n += r.Cells[j].Count
		}
	}
	return n
}

// oldestMerging is the oldest merging card's age in minutes by its accepted stamp, nil
// when none is merging or none carries one (sprintdash.MergeRowOf).
func oldestMerging(now time.Time, merging []*sprint.Card) *int {
	f := sprintdash.MergeFacts{Now: now, MergingRead: true}
	for _, c := range merging {
		at, _ := time.Parse(time.RFC3339, c.F("accepted")) // unreadable: zero, skipped
		f.MergingSince = append(f.MergingSince, at)
	}
	return sprintdash.MergeRowOf(f).OldestMergingMin
}

// judgmentRef is an open judgment naming a dealt card's primary: its note and its kind.
type judgmentRef struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Card string `json:"card"`
}

// dealtView is the view's cards and judgments (where --json --cards), from the store's
// read of the cards dealt and not finished.
func dealtView(d store.Dealt, prefix string, epoch uint64) ([]dealtCard, []judgmentRef) {
	snap := &sprint.Snapshot{Work: d.Work}
	at := func(c *sprint.Card, field string) time.Time {
		t, _ := time.Parse(time.RFC3339, c.F(field)) // unreadable or absent: zero, left out
		return t
	}
	cards := make([]dealtCard, 0, len(d.Cards))
	for _, c := range d.Cards {
		if c.F("kind") == "read" {
			continue
		}
		v := dealtCard{ID: c.ID, Primary: c.F(sprint.PrimaryField), Stream: c.F("stream"), Member: c.Row, State: c.Col,
			Branch: cmp.Or(c.F("branch"), sprint.BranchOf(prefix, epoch, c.ID, c.Int("gen"))), Tier: c.F(sprint.FieldTier), Priority: queuePriority(c)}
		own := "dealt"
		if c.Col == string(sprint.Working) {
			own = "taken"
		}
		v.Since = at(c, own)
		if field, limit, _, _ := sprint.WorkDeadline(snap, c); !at(c, field).IsZero() {
			v.Deadline = at(c, field).Add(limit)
		}
		cards = append(cards, v)
	}
	slices.SortFunc(cards, func(a, b dealtCard) int {
		return cmp.Or(cmp.Compare(a.Member, b.Member), cmp.Compare(a.ID, b.ID))
	})
	var judgments []judgmentRef
	seen := map[judgmentRef]bool{}
	for _, o := range d.Open {
		for _, p := range append([]string{o.Subject()}, o.Note.Primaries...) {
			j := judgmentRef{ID: o.Note.ID, Kind: o.Note.Type, Card: p}
			if !seen[j] && slices.ContainsFunc(cards, func(c dealtCard) bool { return c.Primary == p }) {
				seen[j] = true
				judgments = append(judgments, j)
			}
		}
	}
	slices.SortFunc(judgments, func(a, b judgmentRef) int { return cmp.Or(cmp.Compare(a.ID, b.ID), cmp.Compare(a.Card, b.Card)) })
	return cards, judgments
}

// whereRun is what one where was asked, its flags read.
type whereRun struct {
	c     common
	watch bool
	all   bool
	cards bool
	rows  bool
	// archived puts the archived streams' rows in --json's tables and rows
	archived bool
	release  releaseFlag
	every    time.Duration
	stale    time.Duration
	atEpoch  int64
}

type releaseFlag struct {
	set  bool
	name string
}

func (r *releaseFlag) String() string {
	return r.name
}

func (r *releaseFlag) Set(val string) error {
	r.set = true
	if val != "true" && val != "false" {
		r.name = val
	}
	return nil
}

func (r *releaseFlag) IsBoolFlag() bool {
	return true
}

// releaseFrame prints the cards left per release from the stream rows (docs/SPEC-SPRINT.md section 11, where --release).
func releaseFrame(releases map[string]int64, target string) string {
	var b strings.Builder
	if target != "" {
		fmt.Fprintf(&b, "RELEASE %s cards=%d\n", target, releases[target])
		return b.String()
	}
	names := slices.Collect(maps.Keys(releases))
	slices.Sort(names)
	for _, name := range names {
		fmt.Fprintf(&b, "RELEASE %s cards=%d\n", name, releases[name])
	}
	return b.String()
}

func (a *app) cmdWhere(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("where")
	watch := fs.Bool("watch", false, "redraw in place every --every until interrupted")
	every := fs.Duration("every", time.Second, "the redraw interval with --watch, above 0")
	all := fs.Bool("all", false, "draw the readers and merge tables too, hidden from the default frame (--json always carries them)")
	cards := fs.Bool("cards", false, "with --json: also every work card dealt to a fleet row and not finished (its row, state, since, deadline and branch) and the open judgments on them, as the dashboard's pull routes serve them, and every machine's lanes (lane list)")
	archived := fs.Bool("archived", false, "with --json: the archived streams' rows of the work and merge tables in tables, and their primaries in --rows, beside the live ones (stream archive); the summary and the drawn footers count only the streams on the table either way, and archived_cards and archived_landed carry theirs")
	rows := fs.Bool("rows", false, "with --json: also every primary's row of the work table (id, stream, state, score, and its fields but the brief: card <id> --brief), in work order, so a child reads every card in one call and never loops card calls")
	stale := fs.Duration("stale", defaultStale, "a stream with no progress for longer is shown stalled (--json)")
	atEpoch := fs.Int64("at-epoch", -1, "the sprint as it was at an earlier epoch (before a clear)")
	var rel releaseFlag
	fs.Var(&rel, "release", "show cards left per release, or for the named release")
	pos, err := parse(fs, args)
	if rel.set && rel.name == "" && len(pos) == 1 {
		rel.name = pos[0]
		pos = nil
	}
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
	if *cards && !c.json {
		return refuse(stderr, "where", "--cards is a field of the JSON view: give --json with it")
	}
	if *archived && !c.json {
		return refuse(stderr, "where", "--archived is a field of the JSON view: give --json with it")
	}
	if *rows && !c.json {
		return refuse(stderr, "where", "--rows is a field of the JSON view: give --json with it")
	}
	r := whereRun{c: *c, watch: *watch, all: *all, cards: *cards, rows: *rows, archived: *archived, release: rel, every: *every, stale: *stale, atEpoch: *atEpoch}
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
		v, frame, err := a.whereOf(ctx, st, r.stale, r.all, r.archived || !r.c.json)
		if err != nil {
			if ctx.Err() != nil {
				return "", 0, false // an interrupt cut the read short: the watch is over, not failed
			}
			return "", a.readFailed("where", err, stderr), false
		}
		if r.c.json && r.cards {
			d, err := st.Dealt(ctx)
			if err != nil {
				return "", a.readFailed("where", err, stderr), false
			}
			v.Cards, v.Judgments = dealtView(d, st.Names.Prefix, v.Epoch)
			v.Merging = mergingView(d.Merging)
			v.MergeRow.OldestMergingMin = oldestMerging(v.At, d.Merging)
			// every hold in force, with its reason (hold, docs/SPEC-SPRINT.md section 11): the
			// status cells read held, and this says why; read for the dashboard's form only, so
			// where --json keeps its one read of records
			if v.Holds, err = st.Holds(ctx); err != nil {
				return "", a.readFailed("where", err, stderr), false
			}
			if v.Lanes, err = st.LaneRows(ctx); err != nil {
				return "", a.readFailed("where", err, stderr), false
			}
		}
		if r.c.json && r.rows {
			s, err := st.Load(ctx, []string{sprint.Work}, nil)
			if err != nil {
				return "", a.readFailed("where", err, stderr), false
			}
			var gone *archivedView
			if !r.archived {
				gone = v.Archived
			}
			v.Rows = rowsView(s, gone)
			v.MergeRow.OldestMergingMin = oldestMerging(v.At, s.Work.Column(string(sprint.Merging)))
		}
		if r.c.json {
			b, _ := json.Marshal(v)
			return string(b) + "\n", 0, true
		}
		if r.release.set {
			return releaseFrame(v.Releases, r.release.name), 0, true
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

// where is the view and its frame, every archived stream's row in the view's tables
// (whereOf).
func (a *app) where(ctx context.Context, st *store.Store, stale time.Duration, all bool) (whereView, string, error) {
	return a.whereOf(ctx, st, stale, all, true)
}

// whereOf is the view and its frame. With archived false, the view's tables carry no
// archived stream's row (stream archive), and the frame never draws one either way;
// the summary and the drawn footers never count one (archived_cards and
// archived_landed carry their counts). The frame draws the tables of sprint.ShownOrder,
// or with all every table in sprint.AllOrder: the readers and merge tables are
// hidden from the default frame (the owner, 2026-10-02: "please hide the reader
// and merge tables"); the view for a program carries every table whichever is drawn.
func (a *app) whereOf(ctx context.Context, st *store.Store, stale time.Duration, all, archived bool) (whereView, string, error) {
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
	v := whereView{At: now, Tables: map[string]map[string]map[string]any{}, Streams: clocks, Epoch: st.PinnedEpoch(), Coordinator: coordinator}
	if moved {
		v.Seat = &seat
	}
	if v.Epoch == es.N {
		v.Cleared = es.Cleared
	}
	v.Landed, v.All = counts(shapes[0])
	v.Archived = archivedOf(shapes[0])
	if v.Archived != nil {
		v.ArchivedCards, v.ArchivedLanded = v.Archived.Cards, v.Archived.Landed
	}
	// the held cards and the landings of the hour from the tick's where
	// record: no card is read (store.WhereFacts)
	facts, err := st.WhereFacts(ctx, shapes[0].Revision)
	if err != nil {
		return whereView{}, "", err
	}
	if facts.HasStoreRTT {
		p50, p99 := facts.StoreRTTP50MS, facts.StoreRTTP99MS
		v.StoreRTTP50MS, v.StoreRTTP99MS = &p50, &p99
	}
	v.Held = int64(facts.Held)
	v.Ready = readyPrimaries(shapes[0])
	working, review, merging := pipelineCounts(shapes[0])
	v.Backup = sprint.BackupOf(int(working), int(review), int(merging))
	v.ReadsWaiting, v.Priorities, v.StreamPriorities = facts.ReadsWaiting, facts.Priorities, facts.StreamPriorities
	v.ReadsWindow = facts.ReadsWindow
	v.ReadCards = facts.ReadCards
	v.Width = upWidth(shapes[3])
	v.Buffer = fmt.Sprintf("%d/%d", v.Ready, 2*v.Width)
	v.Low = v.Ready < int64(v.Width)
	v.Tiers = facts.Tiers
	if len(facts.StageTimes.All) > 0 {
		v.StageTimes = &facts.StageTimes
	}
	// the rate is the epoch's landings, an archived stream's too: archiving lands nothing
	rate := sprint.LandingRate(facts.Landed, v.Landed+v.ArchivedLanded, facts.Machine.Spans, facts.Machine.FirstStart(es.Cleared), now)
	v.Summary = summary(shapes[0], v.Held, a.heldETA(now, etaKey{v.All + v.ArchivedCards, v.Held}, etaMinutes(shapes[0], rate)))
	if v.Done = sprintDone(shapes[0]); v.Done {
		// a sprint done: the headline is the epoch's, as the done line counts it
		v.Landed, v.All = v.Landed+v.ArchivedLanded, v.All+v.ArchivedCards
	}
	mf, err := mergeFactsOf(ctx, st, shapes[0], shapes[2], clocks, facts.Landed, now)
	if err != nil {
		return whereView{}, "", err
	}
	v.MergeRow = sprintdash.MergeRowOf(mf)

	if f.Pending != nil {
		v.Pending = f.Pending.ID
	}
	if facts.Records {
		v.Machine = st.MachineLineOf(facts.Machine, facts.Heartbeat)
	}
	v.FleetWork, v.FriendsWork = sprint.SwitchWord(shapes[0].Props, sprint.PropFleet), sprint.SwitchWord(shapes[0].Props, sprint.PropFriends)
	fleetTiers, friendsTiers := sprint.SideTiers(shapes[0].Props, sprint.PropFleetTiers), sprint.SideTiers(shapes[0].Props, sprint.PropFriendsTiers)
	v.FleetTiers, v.FriendsTiers = tiersJSON(fleetTiers), tiersJSON(friendsTiers)
	var b strings.Builder
	b.WriteString(a.seatTitle(v.Coordinator, v.Seat, now) + "\n")
	if v.Coordinator != "" && pushArmed(v.Coordinator) {
		set, ok, err := st.SeatPushes(ctx, v.Coordinator)
		if err != nil {
			return v, "", err
		}
		seat, err := st.SeatState(ctx)
		if err != nil {
			return v, "", err
		}
		set.Name = v.Coordinator
		v.Pushes = sprint.SeatPushLines(set, ok, seat.Epoch, seat.Generation, now)
		writeSeatPushLines(&b, v.Pushes)
	}
	b.WriteString("\n" + whereHeader(v.Summary, v.Machine) + "\n")
	if line := switchesLine(v.FleetWork, v.FriendsWork, fleetTiers, friendsTiers); line != "" {
		b.WriteString(line + "\n")
	}
	v.ReadsNeeded = sprint.ReadTierDefault
	if n, ok := sprint.ReadsSetting(shapes[0].Props); ok {
		v.ReadsNeeded = n
		fmt.Fprintf(&b, "reads: %d\n", n)
	}
	// the five heaviest cards, the ones the most wait on, under the summary: the tick's where
	// record carries them (weight.go; store.WhereRecord)
	if v.Critical = facts.Critical; len(v.Critical) > 0 {
		b.WriteString(sprint.CriticalLine(v.Critical) + "\n")
	}
	// beside it, the cards whose level is not normal and the streams' defaults (priority.go),
	// and the backup state while there is one
	if line := sprint.PriorityLine(v.Priorities, v.StreamPriorities); line != "" {
		b.WriteString(line + "\n")
	}
	if line := backupLine(v.Backup, working, review, merging, v.ReadsWaiting); line != "" {
		b.WriteString(line + "\n")
	}
	// the day's read spend per route, from the same record (cost_view.go). A reader's
	// own sum, the median per read and the unpriced count are the stats reads table
	// (sprint.ReaderStat); a stream's reads are cost_reads and reads_unpriced here.
	if v.ReadSpend = sprint.ReadSpendLine(facts.Streams); v.ReadSpend != "" {
		b.WriteString(v.ReadSpend + "\n")
	}
	b.WriteString("\n")
	parts := map[string]string{}
	var friendCards map[string]store.FriendRow
	for i, t := range shapes {
		logical := sprint.ViewOrder[i]
		if logical == sprint.Fleet {
			// a friend's row holds her sprint cards: counted on the friends table, never a
			// machine of the fleet table (sprint.FriendRow)
			t, friendCards = splitFriendRows(t)
		}
		if logical == sprint.Readers {
			// each reader's width beside reading, derived from its fleet row
			t = readersWidths(t, shapes[slices.Index(sprint.ViewOrder, sprint.Fleet)])
		}
		rows := map[string]map[string]any{}
		for _, r := range t.Rows {
			if !archived && (logical == sprint.Work || logical == sprint.Merge) && v.Archived.has(r.Key) {
				continue // an archived stream's row: where --json --archived
			}
			cells := map[string]any{}
			for j, col := range t.Columns {
				cells[col.Name] = ntable.CellText(t.Columns, r, j)
			}
			if logical == sprint.Fleet {
				rowCardFields(cells, facts.RowCards[r.Key])
			}
			rows[r.Key] = cells
		}
		v.Tables[logical] = rows
		if logical == sprint.Work {
			// dollars per landed card, a column of the text table and a field of each row;
			// the tiers and the spend by tier go to StreamCosts, from the tick's record (cost_view.go)
			t = perLandedColumn(t, facts.Streams)
			for _, r := range t.Rows {
				// an archived stream's costs stay in stream_costs, its row or not
				if tc, ok := facts.Streams[r.Key]; ok {
					if v.StreamCosts == nil {
						v.StreamCosts = map[string]sprint.TierCosts{}
					}
					v.StreamCosts[r.Key] = tc
				}
				if rows[r.Key] != nil {
					rows[r.Key][perLandedField] = r.Texts[perLandedColumnName]
					// the stream's default level for cards added later, beside it (priority.go)
					rows[r.Key][streamPriorityField] = sprint.PriorityNormal
					if d := v.StreamPriorities[r.Key]; d != "" {
						rows[r.Key][streamPriorityField] = d
					}
				}
			}
		}
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
		// every table shows, every stream row in it, empty or not, but an archived one,
		// which its footer does not count either: the footer sums what is drawn
		if logical == sprint.Work || logical == sprint.Merge {
			t = drawnRows(t, v.Archived)
		}
		parts[logical] = ntable.Render(t, ntable.RenderOpts{Title: logical})
		if line := archivedLine(v.Archived); logical == sprint.Work && line != "" {
			parts[logical] = strings.TrimRight(parts[logical], "\n") + "\n" + line + "\n"
		}
	}
	friends, err := st.FriendRows(ctx, now)
	if err != nil {
		return whereView{}, "", err
	}
	for i, f := range friends {
		// the counts are the friend's sprint cards on her fleet row, and nothing
		// else: ready, working, done ok and failed, all from the fleet table
		// (splitFriendRows), with width and status from the roster (store.FriendRows)
		c := friendCards[f.Name]
		friends[i].Ready = c.Ready
		friends[i].Working = c.Working
		friends[i].DealtFleet = facts.DealtFleet[f.Name]
		friends[i].OK = c.OK
		friends[i].Failed = c.Failed
		if f.Status == sprint.Down {
			friends[i].Working = 0 // down, she works nothing
		}
	}
	v.Friends = friends
	ft := a.friendsTable(friends, now)
	v.Tables[sprint.Friends] = map[string]map[string]any{}
	for _, r := range ft.Rows {
		cells := map[string]any{}
		for j, col := range ft.Columns {
			cells[col.Name] = ntable.CellText(ft.Columns, r, j)
		}
		v.Tables[sprint.Friends][r.Key] = cells
	}
	for _, f := range friends {
		if cells := v.Tables[sprint.Friends][f.Name]; cells != nil {
			n := facts.RowCards[sprint.FriendRow(f.Name)]
			if f.Status == sprint.Down {
				n = map[string]int{"reads_ready": n["reads_ready"]} // down, she works nothing
			}
			rowCardFields(cells, n)
		}
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
	if line := facts.StoreLine(); line != "" {
		b.WriteString("\n" + line + "\n")
	}
	if line := promotionLine(clocks); line != "" {
		b.WriteString("\n" + line + "\n")
	}
	a.goalsView(ctx, st, &v)
	if v.Providers, err = providersView(ctx, st, shapes, now); err != nil {
		return whereView{}, "", err
	}
	for _, c := range clocks {
		if c.Stalled(now, stale) {
			v.Stalled = append(v.Stalled, c.Stream)
		}
	}
	streamLeft := map[string]int64{}
	for _, r := range shapes[0].Rows {
		for k, col := range shapes[0].Columns {
			if col.Projection == ntable.Count && col.Name != sprint.Landed && k < len(r.Cells) {
				streamLeft[r.Key] += r.Cells[k].Count
			}
		}
	}
	v.Releases = sprint.WhereReleasesCountCardsLeft(clocks, streamLeft)
	for stream, cols := range facts.FixStates {
		row := v.Tables[sprint.Work][stream]
		if row == nil {
			continue
		}
		n := 0
		for col, count := range cols {
			existing, _ := strconv.Atoi(fmt.Sprint(row[col])) // ignored: absent column is zero
			count = min(count, existing)
			row[col] = strconv.Itoa(existing - count)
			n += count
		}
		if n > 0 {
			row["fix"] = strconv.Itoa(n)
			v.Fix += n
		}
	}

	return v, b.String(), nil
}

// promotionLine names the streams that land on dev or main, from the stream clocks (the
// control cards' one readset, store.StreamClocks; no card is read): `promotion: s1`, a
// stream marked for some repositories only with them, `s2 (owner/name)`; empty when no
// stream is marked (docs/SPEC-SPRINT.md section 7, protected-bases-pb-b.w2).
func promotionLine(clocks []sprint.StreamClock) string {
	var marked []string
	for _, c := range clocks {
		switch c.Promotion {
		case "":
		case sprint.LandProtectedAny:
			marked = append(marked, c.Stream)
		default:
			marked = append(marked, c.Stream+" ("+c.Promotion+")")
		}
	}
	if len(marked) == 0 {
		return ""
	}
	return "promotion: " + strings.Join(marked, ", ")
}

// perLandedColumnName is the work table's column of dollars per landed card, drawn
// after cost (the owner, 2026-10-04: cost visibility), and perLandedField its name in
// where --json's work rows.
const (
	perLandedColumnName = "per landed"
	perLandedField      = "per_landed"
)

// perLandedColumn is the work table with the per-landed column added: each stream's
// dollars per landed card from the tick's record (sprint.TierCosts) when it has one, else
// from the row's own cost cell over its landed count (sprint.PerLandedOf). The column
// folds nothing: the sprint's figure is the hero's.
func perLandedColumn(t ntable.Table, streams map[string]sprint.TierCosts) ntable.Table {
	t.Columns = append(slices.Clone(t.Columns), ntable.Column{Name: perLandedColumnName, Projection: ntable.Text, Fold: ntable.None})
	rows := make([]ntable.Row, len(t.Rows))
	for i, r := range t.Rows {
		texts := map[string]string{}
		maps.Copy(texts, r.Texts)
		if tc, ok := streams[r.Key]; ok {
			texts[perLandedColumnName] = tc.PerLanded
		} else {
			landed := 0
			if j := t.Column(sprint.Landed); j >= 0 && j < len(r.Cells) && !r.Cells[j].Unread {
				landed = int(r.Cells[j].Count)
			}
			texts[perLandedColumnName] = sprint.PerLandedOf(r.Texts[sprint.Cost], landed)
		}
		r.Texts = texts
		rows[i] = r
	}
	t.Rows = rows
	return t
}

// rowCardFields writes on a row of tables its fleet row's counts (sprint.RowCardFields): its
// working cards by level, highest first, summing to its working, and its reads ready; each
// "0" when it has none, a count cell's text as every cell of tables is.
func rowCardFields(cells map[string]any, n map[string]int) {
	for _, k := range sprint.RowCardFields {
		cells[k] = strconv.Itoa(n[k])
	}
}

// splitFriendRows is the fleet table without the friends' rows (sprint.FriendRow), and
// each friend's sprint cards counted off her row: ready, working, and done ok and failed.
func splitFriendRows(t ntable.Table) (ntable.Table, map[string]store.FriendRow) {
	at := map[string]int{}
	for j, c := range t.Columns {
		at[c.Name] = j
	}
	count := func(r ntable.Row, col string) int {
		if j, ok := at[col]; ok && j < len(r.Cells) {
			return int(r.Cells[j].Count)
		}
		return 0
	}
	out := map[string]store.FriendRow{}
	machines := t
	machines.Rows = nil
	for _, r := range t.Rows {
		name, ok := sprint.FriendOfRow(r.Key)
		if !ok {
			machines.Rows = append(machines.Rows, r)
			continue
		}
		out[name] = store.FriendRow{Name: name, Ready: count(r, string(sprint.Ready)), Working: count(r, string(sprint.Working)),
			OK: count(r, sprint.DoneOK), Failed: count(r, sprint.DoneFailed)}
	}
	return machines, out
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
// in the order given: her sprint cards' counts in ready, working and the
// hidden ok and failed, her width and her status as text; done and ok% are the
// table's own formulas over the counts (ntable.CellText), as the fleet
// table's are.
func (a *app) friendsTable(friends []store.FriendRow, now time.Time) ntable.Table {
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
			Texts: map[string]string{sprint.FieldWidth: strconv.Itoa(f.Width), sprint.Status: a.statusCell(f, now), sprint.Active: activeCell(f, now),
				sprint.Tokens: sprint.FriendTokensCell(f.Tokens, f.Charged, f.Billing)}})
	}
	return t
}

// activeCell is the friends table's active cell: how long ago her session last wrote a file
// under her working directory and outbox, as her daemon walked them and her beat carried it
// ("-" when no beat has reported one). A down or held friend's is still her last.
func activeCell(f store.FriendRow, now time.Time) string {
	if f.Active.IsZero() {
		return "-"
	}
	return ageWord(now.Sub(f.Active)) + " ago"
}

// allRow is the label of the one row the view draws for the readers and the
// merge tables: blank, as the work table's footer is (the owner, 2026-10-02:
// "please remove 'all'").
const allRow = ""

// readersAll is the readers table as the view's text draws it: one row, unlabelled,
// whose cells are the sums over every reader (hidden rows, readers away or down,
// counted as the footer counted them), and no footer, which would say the same
// thing twice: one row summing every reader is enough, since the view shows
// reader progress overall. A cell some reader's
// set did not come back for prints "?", as the footer's sum did. Display only:
// the stored table, its rows and where --json are as they were.
func readersAll(t ntable.Table) ntable.Table {
	// the width cell sums the readers that have one (readersWidths), as the
	// fleet's footer sums its members'; "-" when none has
	total, any := 0, false
	for _, r := range t.Rows {
		if n, err := strconv.Atoi(r.Texts[sprint.FieldWidth]); err == nil {
			total, any = total+n, true
		}
	}
	width := "-"
	if any {
		width = strconv.Itoa(total)
	}
	texts := map[string]string{sprint.FieldWidth: width}
	if slices.Contains(columnNames(t.Columns), sprint.ReaderTiers) {
		word := readerTiersSummary(t)
		if word == "" {
			word = sprint.ReaderTiersDefault
		}
		texts[sprint.ReaderTiers] = word
	}
	return allOf(t, texts)
}

// readerTiersSummary is the tiers word of the one readers row: the word the
// rows share, or the distinct words in row order. An empty cell prints default.
func readerTiersSummary(t ntable.Table) string {
	var words []string
	seen := map[string]bool{}
	for _, r := range t.Rows {
		w := sprint.ReaderTiersShown(r.Texts[sprint.ReaderTiers])
		if seen[w] {
			continue
		}
		seen[w] = true
		words = append(words, w)
	}
	return strings.Join(words, ",")
}

// readersWidths is the readers table with a width column beside reading, each
// reader's the width of its fleet row (sprint.ReaderWidth: reader-<m> runs at
// m's width, the fleet table's width cell), "-" for a reader named for no
// fleet row. Display only: the readers table holds no width column; the width
// is derived, and where --json carries it on each reader's row.
func readersWidths(t ntable.Table, fleet ntable.Table) ntable.Table {
	widths := map[string]string{}
	for _, r := range fleet.Rows {
		widths[r.Key] = r.Texts[sprint.FieldWidth]
	}
	at := slices.Index(columnNames(t.Columns), sprint.Reading) + 1
	cols := slices.Clone(t.Columns)
	cols = slices.Insert(cols, at, ntable.Column{Name: sprint.FieldWidth, Projection: ntable.Text, Fold: ntable.None})
	rows := make([]ntable.Row, len(t.Rows))
	for i, r := range t.Rows {
		row := r
		row.Cells = slices.Insert(slices.Clone(r.Cells), min(at, len(r.Cells)), ntable.Cell{})
		row.Texts = maps.Clone(r.Texts)
		if row.Texts == nil {
			row.Texts = map[string]string{}
		}
		width := "-"
		if m, ok := sprint.ReaderMachine(r.Key); ok && widths[m] != "" {
			width = widths[m]
		}
		row.Texts[sprint.FieldWidth] = width
		if slices.Contains(columnNames(cols), sprint.ReaderTiers) {
			row.Texts[sprint.ReaderTiers] = sprint.ReaderTiersShown(row.Texts[sprint.ReaderTiers])
		}
		rows[i] = row
	}
	t.Columns, t.Rows = cols, rows
	return t
}

// columnNames is the columns' names in order.
func columnNames(cols []ntable.Column) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Name
	}
	return out
}

// mergeAll is the merge table as the view's text draws it, the same way as
// readers, one summed row:
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

// whereHeader is the one line under the title of the where view
// (docs/SPEC-SPRINT.md section 1): STOPPED when the machine is stopped, DONE
// when it stopped because the sprint is done, matching the view's state text,
// and the progress line, with no machine text, when it is running; a RUNNING
// machine whose last tick is late keeps the progress line, the machine's
// "running (tick late 16s)" after it. Nothing else follows any of them.
// switchesLine is the sides' settings that are not their defaults, one line: while every
// tier is open to both sides, the work switches that are off ("fleet: off", "friends: off"),
// nothing while both are on; while a side's tiers are set, both sides, each its switch and
// its tiers ("fleet: on, tiers flash  friends: on, tiers all").
func switchesLine(fleet, friends string, fleetTiers, friendsTiers []string) string {
	if fleetTiers == nil && friendsTiers == nil {
		var off []string
		if fleet == sprint.SwitchOff {
			off = append(off, "fleet: off")
		}
		if friends == sprint.SwitchOff {
			off = append(off, "friends: off")
		}
		return strings.Join(off, "  ")
	}
	side := func(name, sw string, tiers []string) string {
		if sw != sprint.SwitchOff {
			sw = sprint.SwitchOn
		}
		return name + ": " + sw + ", tiers " + cmp.Or(strings.Join(tiers, ","), sprint.TiersAll)
	}
	return side("fleet", fleet, fleetTiers) + "  " + side("friends", friends, friendsTiers)
}

// tiersJSON is a side's tiers as where --json carries them: "all", or the list.
func tiersJSON(tiers []string) any {
	if tiers == nil {
		return sprint.TiersAll
	}
	return tiers
}

func whereHeader(summary, machine string) string {
	state := strings.TrimPrefix(machine, "machine: ")
	switch {
	case strings.HasPrefix(state, "STOPPED") || state == store.DoneState:
		return state // DONE, as the view says, when the sprint is done
	case state == "running" || state == "":
		return strings.TrimSpace(summary)
	}
	return strings.TrimSpace(summary + "  " + state) // running (tick late 16s)
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
	if sprint.IsAlias(*open) {
		full, err := unalias(ctx, st, []string{*open})
		if err != nil {
			return refuse(stderr, "inbox", "--open: "+err.Error())
		}
		*open = full[0]
	}
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
		src, err := a.storeSource(ctx, st, c.redis, *deadline, *stale)
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
		if fresh, after, err = a.waitNew(ctx, src, seenFresh(seenKeys(first.groups)), lineRunning(first.machine), *timeout); err != nil {
			return a.waitFailed(err, stderr)
		}
		stopped := lineRunning(first.machine) && !lineRunning(after.machine)
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
		now := a.now()
		judgments, happened := inboxActs(groups, now)
		holder, err := st.B.Coordinator(ctx)
		if err != nil {
			return a.readFailed("inbox", err, stderr)
		}
		summary, queue := sprint.SeatInbox(v.Open, now, *deadline)
		out := map[string]any{"groups": groups, "judgments": judgments, "happened": happened, "done": mach.Done(),
			"last": v.Last, "cursor": v.Cursor, "at": now, "machine": machine, "coordinator": holder}
		if summary != "" {
			out["summary"] = summary
		}
		if len(queue) > 0 {
			out["queue"] = queue
		}
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
	summary, queue := sprint.SeatInbox(v.Open, now, *deadline)
	if summary != "" {
		fmt.Fprintln(stdout, summary)
		for _, l := range queue {
			fmt.Fprintln(stdout, l)
		}
		fmt.Fprintln(stdout)
	}
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
	ID        string        `json:"id"`              // what --group takes
	Alias     string        `json:"alias,omitempty"` // j<n>, what --group and --answers take in its place
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
		j := inboxJudgment{ID: g.ID, Alias: g.Alias, Kind: g.Kind, Type: g.Type, What: g.What, Stream: g.Stream, Size: g.Size, Cards: cards, Notes: nonNil(g.Notes),
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
	if g.Behind >= sprint.CriticalBehind {
		l = fmt.Sprintf("CRITICAL %d behind: %s", g.Behind, l) // the cards that wait on it (weight.go)
	}
	if g.Alias != "" {
		l += "  alias=" + g.Alias
	}
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
	Primary *sprint.Card `json:"primary"`
	// Column is the live table placement, as linePlace reads it, never the
	// legacy place:work field (docs/SPEC-SPRINT.md section 11).
	Column  string `json:"column"`
	Tier    string `json:"tier"`            // the tier it is on (sprint.CardTiers)
	Ceiling string `json:"ceiling"`         // the highest the machine escalates it to
	Grade   string `json:"grade,omitempty"` // nova-decide's grade, as the card holds it (sprint.FieldGrade)
	// Who is the worker its brief's WHO line names (sprint.FieldWho): friend for any
	// friend, friend.<name> for one; absent on a machine's card.
	Who string `json:"who,omitempty"`
	// Priority is its level on the ladder (sprint.CardPriority: blocker, critical, normal,
	// low; a read card's is reader) and PrioritySource where it comes from (set, computed,
	// default).
	Priority       string             `json:"priority"`
	PrioritySource string             `json:"priority_source"`
	Work           []*sprint.Card     `json:"work_cards"`
	Reads          []*sprint.Card     `json:"read_cards"`
	Merge          *sprint.Card       `json:"merge,omitempty"`
	Open           []sprint.Open      `json:"open,omitempty"`
	Needs          []sprint.NeedState `json:"needs,omitempty"`
	NeededBy       []string           `json:"needed_by,omitempty"`
	Held           *sprint.Hold       `json:"held,omitempty"` // what holds it now
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
	brief := fs.Bool("brief", false, "the brief alone, as the card holds it, and nothing else (a card with no brief is refused, exit 1); not with --fields")
	all := fs.Bool("all", false, "every card on the table, one JSON object a line (its fields, column, needs and brief length), in one read; with --json, and no id")
	stream := fs.String("stream", "", "--all of one stream's cards")
	pos, err := parse(fs, args)
	if bulk := *all || *stream != ""; err == nil && bulk {
		if len(pos) > 0 || *brief || *fields || !c.json {
			return refuse(stderr, "card", "--all and --stream print every card, one JSON object a line: give --json, and no id, --brief or --fields; run: nova-sprint card --all --json")
		}
		st, err := a.storeAt(*c, *atEpoch)
		if err != nil {
			return refuse(stderr, "card", err.Error())
		}
		return a.cardsBulk(st, *stream, stdout, stderr)
	}
	if err != nil || len(pos) != 1 {
		return refuse(stderr, "card", argErr("wants one primary id ", err, pos...))
	}
	if *brief && *fields {
		return refuse(stderr, "card", "--brief prints the brief alone and --fields every field: give one of them")
	}
	id := pos[0]
	st, err := a.storeAt(*c, *atEpoch)
	if err != nil {
		return refuse(stderr, "card", err.Error())
	}
	ctx := context.Background()
	// one read of the tables serves the card, what holds it and its place in line (an
	// earlier epoch is told no hold)
	read := st.CardHeld
	if *atEpoch >= 0 {
		read = st.CardOf
	}
	v, err := read(ctx, id)
	if err != nil {
		return a.readFailed("card", err, stderr)
	}
	if v.Primary == nil {
		fmt.Fprintf(stderr, "%s card: no primary %s; run: nova-sprint where\n", prog, oneline.Escape(id))
		return 1
	}
	if *brief {
		return printBrief(stdout, stderr, id, v.Primary.F("brief"), c.json)
	}
	// What holds it, so nothing stalls without a named reason: an outside actor, the
	// next tick, an open judgment, what it waits on, or the machine STOPPED.
	held := v.Hold
	// its own lines, from the card log index, never the whole log (store.CardLog)
	lines, err := st.CardLog(ctx, id)
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
		level, source := sprint.CardPriority(v.Primary)
		b, _ := json.Marshal(cardView{Primary: v.Primary, Column: v.Primary.Col, Tier: tier, Ceiling: ceiling, Grade: v.Primary.F(sprint.FieldGrade), Who: v.Primary.F(sprint.FieldWho), Priority: level, PrioritySource: source, Work: v.Work, Reads: v.Reads, Merge: v.Merge, Open: v.Open, Needs: v.Needs, NeededBy: v.NeededBy, Held: held,
			Cost: sprint.CardCostOf(v.Primary), Timeline: events, Texts: texts})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	if !*fields {
		place := ""
		if v.Primary.Placed() {
			place = linePlace(v.Primary, v.Column)
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
		// and its grade, nova-decide's convergence grade before its first deal (decide.go)
		grade := ""
		if g, ok := decide.ParseDecided(v.Primary.F(sprint.FieldGrade)); ok {
			grade = " grade=" + g.Value + ":" + strconv.FormatFloat(g.P, 'f', 2, 64)
		}
		fmt.Fprintf(stdout, "CARD OK id=%s epoch=%d work_cards=%d read_cards=%d open=%d%s tier=%s ceiling=%s%s%s\n", oneline.Escape(id), epoch, len(v.Work), len(v.Reads), len(v.Open), priorityWord(v.Primary), tier, ceiling, grade, whoWord(v.Primary))
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
	fmt.Fprintf(stdout, "CARD OK id=%s epoch=%d work_cards=%d read_cards=%d open=%d%s%s\n", oneline.Escape(id), epoch, len(v.Work), len(v.Reads), len(v.Open), whoWord(v.Primary), priorityWord(v.Primary))
	return 0
}

// cardLine is one card as card --all --json prints it: its id, stream, column
// (place), score, needs, the length of its brief in bytes, and every other field
// (the brief's text is `card <id> --brief`).
type cardLine struct {
	ID       string            `json:"id"`
	Stream   string            `json:"stream"`
	Column   string            `json:"column"`
	Score    float64           `json:"score"`
	Needs    []string          `json:"needs"`
	BriefLen int               `json:"brief_len"`
	Fields   map[string]string `json:"fields"`
}

// cardsBulk is card --all --json: every card on the work table (one stream's
// with stream), in its stream's line, from one read of the table; no log is
// read, and no hold is counted (card <id> tells one card's).
func (a *app) cardsBulk(st *store.Store, stream string, stdout, stderr io.Writer) int {
	s, err := st.Load(context.Background(), []string{sprint.Work}, nil)
	if err != nil {
		return a.readFailed("card", err, stderr)
	}
	cards := s.Work.Column(sprint.States...)
	slices.SortStableFunc(cards, func(x, y *sprint.Card) int {
		return cmp.Or(cmp.Compare(x.Row, y.Row), cmp.Compare(x.Score, y.Score), cmp.Compare(x.ID, y.ID))
	})
	for _, c := range cards {
		if stream != "" && c.Row != stream {
			continue
		}
		fields := maps.Clone(c.Fields)
		delete(fields, "brief")
		if fields == nil {
			fields = map[string]string{}
		}
		needs := sprint.Split(c.F("needs"))
		if needs == nil {
			needs = []string{}
		}
		b, err := json.Marshal(cardLine{ID: c.ID, Stream: c.Row, Column: c.Col, Score: c.Score, Needs: needs, BriefLen: len(c.F("brief")), Fields: fields})
		if err != nil {
			return a.readFailed("card", err, stderr)
		}
		fmt.Fprintln(stdout, string(b))
	}
	return 0
}

// whoWord is the CARD OK line's who of a friend's card (sprint.FieldWho: who=friend for
// any friend, who=friend.<name> for one); nothing for a machine's card.
func whoWord(pr *sprint.Card) string {
	if w := pr.F(sprint.FieldWho); w != "" {
		return " who=" + oneline.Field(w)
	}
	return ""
}

// priorityWord is the CARD OK line's priority (sprint.CardPriority), always printed.
func priorityWord(pr *sprint.Card) string {
	l, _ := sprint.CardPriority(pr)
	return " priority=" + l
}

// queuePriority is a queue card's level: a primary's own (sprint.CardPriority), a work or a
// read card's as sprint.QueuePriority says it.
func queuePriority(c *sprint.Card) string {
	if c.F("kind") == "primary" {
		l, _ := sprint.CardPriority(c)
		return l
	}
	return sprint.QueuePriority(c)
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
		status = "FAILED"
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
				stats[i].RestedUntil = "open" // until paid
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
		// the judgment bar rides here for answer, which reads it through the server
		bar, err := st.JudgmentBar(ctx)
		if err != nil {
			return a.readFailed("routes", err, stderr)
		}
		b, _ := json.Marshal(map[string]any{"tiers": sprint.TierRoutes(rs), "routes": stats, "decide_judgment_bar": bar})
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

// printBrief is card --brief: the brief alone, as the card holds it, so a child
// gets a brief's text out without the story in front of it (the owner, 2026-10-03,
// the comfort list: "card --fields is the only way to get a brief's text out, and it
// prints the story first"). A card with no brief is refused, exit 1, naming the
// verb that gives one. --json is one object, id and brief.
func printBrief(stdout, stderr io.Writer, id, brief string, asJSON bool) int {
	if brief == "" {
		fmt.Fprintf(stderr, "%s card: %s has no brief; run: nova-sprint brief %s --brief-file <path>\n", prog, oneline.Escape(id), oneline.Escape(id))
		return 1
	}
	if asJSON {
		b, _ := json.Marshal(map[string]string{"id": id, "brief": brief})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprintln(stdout, brief)
	return 0
}

// primaryRow is one primary's row of the work table as where --json --rows carries
// it: its place and score, and every field but the brief (card <id> --brief prints
// that; the comfort list of 2026-10-03, item 8: a child's loop of card calls timed
// out against the store).
type primaryRow struct {
	ID     string `json:"id"`
	Stream string `json:"stream"`
	Column string `json:"column"`
	// State is a deprecated alias of Column for one release (SPEC-SPRINT section 11).
	State  string            `json:"state"`
	Score  float64           `json:"score"`
	Fields map[string]string `json:"fields"`
}

// rowsView is every placed primary of the work table, in work order (stream, then
// score and id), with its fields but the brief, but an archived stream's that gone
// names (nil names none: where --json --rows --archived).
func rowsView(s *sprint.Snapshot, gone *archivedView) []primaryRow {
	cards := s.Work.Column(sprint.States...)
	rows := make([]primaryRow, 0, len(cards))
	for _, c := range cards {
		if gone.has(c.Row) {
			continue // an archived stream's: where --json --rows --archived
		}
		fields := make(map[string]string, len(c.Fields))
		for k, v := range c.Fields {
			if k != "brief" {
				fields[k] = v
			}
		}
		rows = append(rows, primaryRow{ID: c.ID, Stream: c.Row, Column: c.Col, State: c.Col, Score: c.Score, Fields: fields})
	}
	slices.SortStableFunc(rows, func(a, b primaryRow) int {
		return cmp.Or(cmp.Compare(a.Stream, b.Stream), cmp.Compare(a.Score, b.Score), cmp.Compare(a.ID, b.ID))
	})
	return rows
}

// statusCell is a friend's status as the table shows it: the word (up, held or
// down), and for a friend held or down with a reason, the reason and when she
// is expected back: `down (opus rate limited, until 6:00 PM)`.
func (a *app) statusCell(f store.FriendRow, now time.Time) string {
	if f.Reason == "" && f.Until.IsZero() {
		return f.Status
	}
	why := f.Reason
	if !f.Until.IsZero() {
		why = strings.TrimPrefix(why+", until "+a.clock12(f.Until, now), ", ")
	}
	return f.Status + " (" + why + ")"
}
