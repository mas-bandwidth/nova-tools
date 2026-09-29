package ws

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // the ETA prints Eastern on every bench, zone files or not

	"github.com/redis/go-redis/v9"
)

// The one count of the sprint (Glenn 2026-09-26 1:32 PM ET: five printouts of
// one store in one minute printed five numbers; "a table with five answers is
// trusted zero times"). Every place that prints the sprint's progress or its
// counts (sprint status, the table's headline and stream block, ws counts,
// scope ls, stream ls) prints from one SprintCounts read here, so two
// printouts of the same store at the same instant carry the same numbers.
//
// The definition, fixed here:
//
//	the sprint's streams  ws:order, the ws index (it holds one sprint at a time)
//	cells                 per stream, the card count of ws:<s>:<state> for the
//	                      six states of Stream: the set's ZCARD less the
//	                      stream's sentinel when it is in that set
//	                      (QueueCardCount, count.go). The sentinel is the
//	                      stream's stop, not work: it is in no cell, no
//	                      total, no x/y and no left (the coordinator's ruling
//	                      on Glenn's "zeros everywhere after sprint clear")
//	Total                 every card in the six sets: waiting + ready +
//	                      working + review + merging + landed. A card in done
//	                      (closed: cancelled, or cleared by sprint clear) or
//	                      parked is in none of them and is not counted
//	Done                  landed
//	x/y                   Done/Total; left = Total - Done
//	eta                   now + left / (cards, sentinels aside, moved to
//	                      landed in ws:log over the hour before now), in
//	                      Eastern; "-" with no card, "done" when all landed
//
// Parked is read beside the six (scope ls prints it), its sentinel aside
// too, and is never in Total. After sprint clear every count is 0.
// Every value comes from ONE pipeline of plain reads (ZRANGE, ZCARD, ZSCORE,
// HGET, XRANGE: inside the table tick's command allowlist): the memberships
// (ws:order, and sprint:order when no sprint is named) are kept from the
// previous read and re-read in the same pipeline, and a read that finds one
// changed reads again, the way the table's tick works.

// CountsCells is the number of count cells per stream: the six of Stream.
const CountsCells = 6

// LogWindowMax bounds the hour of ws:log one read counts; a sprint landing
// more than this many cards an hour shows at least this rate.
const LogWindowMax = 5000

// StreamCounts is one stream's cells in Stream order, and its parked set.
// Unread marks a cell whose ZCARD did not come back: it prints "?", never a
// false 0.
type StreamCounts struct {
	Stream string
	Cells  [CountsCells]int64
	Parked int64
	Unread [CountsCells]bool
}

// Sum is the stream's cards in the six sets.
func (s StreamCounts) Sum() int64 {
	var n int64
	for _, c := range s.Cells {
		n += c
	}
	return n
}

// AnyUnread says a cell did not come back.
func (s StreamCounts) AnyUnread() bool { return slices.Contains(s.Unread[:], true) }

// Cell is the named state's count (one of Stream).
func (s StreamCounts) Cell(state string) int64 {
	if i := slices.Index(Stream, state); i >= 0 {
		return s.Cells[i]
	}
	return 0
}

// ActiveDuty is one duty card in working state.
type ActiveDuty struct {
	ID        string
	Stream    string
	Friend    string
	EST       time.Duration
	CreatedAt time.Time
	Age       time.Duration
	Overdue   bool
}

// LandDuty describes an active stream land duty.
type LandDuty struct {
	ID     string
	Stream string
	Age    time.Duration
	EST    time.Duration
}

// SprintCounts is the sprint's progress at one read: what Counts returns.
type SprintCounts struct {
	// Sprint is the sprint named, or the open one (the last member of
	// sprint:order whose status is not closed); "" when there is none.
	Sprint string
	// Status is the open sprint's s:<S> status; "" when none is open.
	Status string
	// Streams are ws:order's streams in rank order; Total is their column
	// sums (Stream "total").
	Streams []StreamCounts
	Total   StreamCounts
	// Duties is the list of active working duty cards.
	Duties []ActiveDuty
	// LandDuty is the active stream land duty, if any.
	LandDuty *LandDuty
	// LandedHour is the cards (sentinels aside) moved to landed in ws:log
	// over the hour before At, the ETA's rate; LogErr is set when ws:log did not come back.
	LandedHour int64
	LogErr     error
	// Epoch is the sprint epoch every cell was read under (#4238).
	Epoch uint64
	// At is the instant the read was made for: the ETA is measured from it.
	At time.Time
}

// Sum builds the counts of streams with their total, the one place a total
// is summed.
func Sum(streams []StreamCounts) SprintCounts {
	c := SprintCounts{Streams: streams, Total: StreamCounts{Stream: "total"}}
	for _, row := range streams {
		for j := range row.Cells {
			c.Total.Cells[j] += row.Cells[j]
			c.Total.Unread[j] = c.Total.Unread[j] || row.Unread[j]
		}
		c.Total.Parked += row.Parked
	}
	return c
}

// All is Total: every card in the sprint's six stream sets.
func (c SprintCounts) All() int64 { return c.Total.Sum() }

// Done is the landed cards.
func (c SprintCounts) Done() int64 { return c.Total.Cell(Landed) }

// Left is All less Done.
func (c SprintCounts) Left() int64 { return c.All() - c.Done() }

// Pct is Done over All as a whole percent (0 with no card).
func (c SprintCounts) Pct() int64 {
	if c.All() == 0 {
		return 0
	}
	return c.Done() * 100 / c.All()
}

// Unread says a cell of some stream did not come back: the numbers are not
// the store's and print "?".
func (c SprintCounts) Unread() bool { return c.Total.AnyUnread() }

// ETA is the eta word: "-" when there is no card (a total of 0 has no eta,
// never the current time), "done" when every card landed, "?" when nothing
// landed in the hour (or ws:log or a cell was not read), else HH:MM ET (the
// clock Glenn reads), +<n>d when it is days out.
func (c SprintCounts) ETA() string {
	left := c.Left()
	switch {
	case c.Unread():
		return "?"
	case c.All() == 0:
		return "-"
	case left == 0:
		return "done"
	case c.LogErr != nil || c.LandedHour <= 0 || c.At.IsZero():
		return "?"
	}
	return Eastern(c.At, c.At.Add(time.Duration(left*int64(time.Hour)/c.LandedHour)))
}

// Eastern prints t as HH:MM ET, with +<n>d when t falls n calendar days after
// now there (an eta a week out is not the clock time of today); with no zone
// database it prints UTC and says so.
func Eastern(now, t time.Time) string {
	loc, zone := time.UTC, "UTC"
	if l, err := time.LoadLocation("America/New_York"); err == nil {
		loc, zone = l, "ET"
	}
	now, t = now.In(loc), t.In(loc)
	out := t.Format("15:04") + " " + zone
	day := func(x time.Time) time.Time { return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, time.UTC) }
	if d := int(day(t).Sub(day(now)).Hours() / 24); d > 0 {
		out += " +" + strconv.Itoa(d) + "d"
	}
	return out
}

// Header is the one progress line: landed/total done N%, left L, eta HH:MM ET [land: <stream> <age>].
func (c SprintCounts) Header() string {
	if c.Unread() {
		return "?/? done ?%, left ?, eta ?"
	}
	h := fmt.Sprintf("%d/%d done %d%%, left %d, eta %s", c.Done(), c.All(), c.Pct(), c.Left(), c.ETA())
	if c.LandDuty != nil {
		h += fmt.Sprintf(" land: %s %s", c.LandDuty.Stream, formatDuration(c.LandDuty.Age))
	}
	return h
}

// Receipt is the same numbers as one key=value line (ws counts).
func (c SprintCounts) Receipt() string {
	cells := make([]any, 0, CountsCells)
	for i := range c.Total.Cells {
		if c.Total.Unread[i] {
			cells = append(cells, "?")
		} else {
			cells = append(cells, strconv.FormatInt(c.Total.Cells[i], 10))
		}
	}
	sprint := c.Sprint
	if sprint == "" {
		sprint = "-"
	}
	head := fmt.Sprintf("COUNTS sprint=%s streams=%d waiting=%s ready=%s working=%s review=%s merging=%s landed=%s parked=%d",
		append([]any{sprint, len(c.Streams)}, append(cells, c.Total.Parked)...)...)
	if c.Unread() {
		return head + " total=? done=?/? pct=? left=? eta=?"
	}
	return fmt.Sprintf("%s total=%d done=%d/%d pct=%d left=%d eta=%q", head, c.All(), c.Done(), c.All(), c.Pct(), c.Left(), c.ETA())
}

// NotOpen is a read for a named sprint that is not the open one (#4411): the
// ws index holds the open sprint's streams only, so its counts are never
// returned under another name. Open is "" when no sprint is open.
type NotOpen struct {
	Name string // the sprint named
	Open string // the open sprint
}

// Error names both: --sprint <name>: not the open sprint; open=<open|->.
func (e *NotOpen) Error() string {
	open := e.Open
	if open == "" {
		open = "-"
	}
	return fmt.Sprintf("--sprint %s: not the open sprint; open=%s", e.Name, open)
}

// CellReader queues the cells of every stream of ws:order under the epoch
// the reader keys by, and answers them once the pipeline ran. The default
// (nil) is QueueStreamCounts; the sprint table reads them through its
// streams table instead (internal/nsprint/table/streams.go, bound to the
// same sets and counting through the same primitive), so the headline and
// the block stay one count (#4411) while the block is a table.
type CellReader interface {
	Queue(ctx context.Context, pipe redis.Pipeliner, epoch uint64, streams []string) CellsCmd
}

// CellsCmd is one queued read of every stream's cells.
type CellsCmd interface {
	// Rows is one StreamCounts per stream queued, in order; an error is the
	// read failing whole (a cell that did not come back is Unread, not an
	// error).
	Rows() ([]StreamCounts, error)
}

// CountsReader reads SprintCounts, keeping the memberships across reads so a
// reader that reads again (the table's tick) takes one pipeline.
type CountsReader struct {
	// Sprint names the sprint; "" reads the open one. A named sprint that is
	// not the open one is refused with *NotOpen: the open sprint is read
	// either way (sprint:order and each member's status, in the pipeline).
	Sprint string
	// Cells reads the stream cells in place of QueueStreamCounts when set.
	Cells CellReader

	streams []string
	sprints []string
	open    string
	// epoch is sprint:epoch as the last read found it: the cells are keyed
	// by it, and a read that finds another epoch after its cells is read
	// again, like a membership change (#4238)
	epoch  uint64
	duties []string
	primed bool
}

// Epoch is the sprint epoch the reader keys its cells by: the one the last
// read found.
func (r *CountsReader) Epoch() uint64 { return r.epoch }

// Streams is ws:order as the last read found it, the streams the next
// read's cells are queued for.
func (r *CountsReader) Streams() []string { return r.streams }

// CountsCmd is one queued read.
type CountsCmd struct {
	r       *CountsReader
	at      time.Time
	order   *redis.StringSliceCmd
	sprintQ *redis.StringSliceCmd
	states  []*redis.StringCmd // HGET s:<S> status per cached sprint
	log     *redis.XMessageSliceCmd
	cells   CellsCmd // the cells of every cached stream
	// epochQ is sprint:epoch read after every cell (#4238): a clear that
	// lands before or between the cells shows as another epoch
	epochQ   *redis.StringCmd
	dutySet  *redis.StringSliceCmd
	dutyCmds map[string]*redis.SliceCmd
}

// Queue queues the read made for now on pipe.
func (r *CountsReader) Queue(ctx context.Context, pipe redis.Pipeliner, now time.Time) *CountsCmd {
	q := &CountsCmd{r: r, at: now, order: pipe.ZRange(ctx, "ws:order", 0, -1)}
	// the open sprint, named or not: a named one is checked against it
	q.sprintQ = pipe.ZRange(ctx, "sprint:order", 0, -1)
	for _, name := range r.sprints {
		// the status field on s:<S> (what sprint open, close and the
		// pit stop use): closed is out, any other status is open
		q.states = append(q.states, pipe.HGet(ctx, "s:"+name, "status"))
	}
	hourAgo := now.Add(-time.Hour).UnixMilli()
	q.log = pipe.XRangeN(ctx, "ws:log", strconv.FormatInt(hourAgo, 10), "+", LogWindowMax)
	cells := r.Cells
	if cells == nil {
		cells = defaultCells{}
	}
	q.cells = cells.Queue(ctx, pipe, r.epoch, r.streams)
	q.epochQ = pipe.HGet(ctx, EpochKey, EpochField)
	q.dutySet = pipe.SMembers(ctx, "duty:working")
	if len(r.duties) > 0 {
		q.dutyCmds = make(map[string]*redis.SliceCmd, len(r.duties))
		for _, id := range r.duties {
			q.dutyCmds[id] = pipe.HMGet(ctx, "task:"+id, "where", "kind", "stream", "friend", "est", "created_at")
		}
	}
	return q
}

// defaultCells is the CellReader of every reader but the sprint table:
// QueueStreamCounts per stream, the six sets then parked.
type defaultCells struct{}

func (defaultCells) Queue(ctx context.Context, pipe redis.Pipeliner, epoch uint64, streams []string) CellsCmd {
	q := defaultCellsCmd{streams: streams}
	for _, s := range streams {
		q.cells = append(q.cells, QueueStreamCounts(ctx, pipe, epoch, s))
	}
	return q
}

type defaultCellsCmd struct {
	streams []string
	cells   [][]*CardCountCmd // per stream: the six, then parked
}

func (q defaultCellsCmd) Rows() ([]StreamCounts, error) {
	rows := make([]StreamCounts, 0, len(q.streams))
	for i, s := range q.streams {
		row := StreamCounts{Stream: s}
		for j, cmd := range q.cells[i][:CountsCells] {
			n, err := cmd.Result()
			row.Cells[j], row.Unread[j] = n, err != nil && !errors.Is(err, redis.Nil)
		}
		row.Parked = q.cells[i][CountsCells].Val()
		rows = append(rows, row)
	}
	return rows, nil
}

// QueueMembers queues the membership reads a caller makes in its own earlier
// round trip (ws:order and sprint:order); Prime
// takes their answers, so the caller's next pipeline carries every count and
// a cold read needs no round trip of its own (the live layout's SCAN trip
// carries them).
func (r *CountsReader) QueueMembers(ctx context.Context, pipe redis.Pipeliner) (order, sprints *redis.StringSliceCmd) {
	return pipe.ZRange(ctx, "ws:order", 0, -1), pipe.ZRange(ctx, "sprint:order", 0, -1)
}

// Prime sets the memberships QueueMembers read. A read that finds them moved
// since reads again (Read), as after any membership change.
func (r *CountsReader) Prime(order, sprints *redis.StringSliceCmd) error {
	o, err := order.Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("zrange ws:order: %w", err)
	}
	var sp []string
	if sprints != nil {
		if sp, err = sprints.Result(); err != nil && !errors.Is(err, redis.Nil) {
			return fmt.Errorf("zrange sprint:order: %w", err)
		}
	}
	r.streams, r.sprints, r.primed = o, sp, true
	return nil
}

// PrimeEpoch sets the epoch the next read keys its cells by, when the caller
// read sprint:epoch in its own earlier round trip (the live layout's SCAN
// trip); the read still checks it after its cells.
func (r *CountsReader) PrimeEpoch(e uint64) { r.epoch = e }

// SprintName is the sprint the reader counts for: the named one, or the open
// one as of the last read.
func (r *CountsReader) SprintName() string {
	if r.Sprint != "" {
		return r.Sprint
	}
	return r.open
}

// Result is the read's SprintCounts once the pipeline ran. changed says a
// membership moved under the read (or it was the first): the counts are not
// whole and the caller reads again, which queues the new members.
func (q *CountsCmd) Result() (SprintCounts, bool, error) {
	r := q.r
	order, err := q.order.Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return SprintCounts{}, false, fmt.Errorf("zrange ws:order: %w", err)
	}
	sprints, err := q.sprintQ.Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return SprintCounts{}, false, fmt.Errorf("zrange sprint:order: %w", err)
	}
	// the open sprint: the last of sprint:order whose status is not closed
	// (Glenn 2026-09-26 8:50 AM ET: one sprint active at a time)
	open, status := "", ""
	for i, name := range r.sprints {
		if st, err := q.states[i].Result(); err == nil && st != "closed" {
			open, status = name, st
		}
	}
	// An epoch that could not be read is not epoch 0: the read fails rather
	// than count another epoch's cells (#4238).
	ev, err := q.epochQ.Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return SprintCounts{}, false, fmt.Errorf("hget %s %s: %w", EpochKey, EpochField, err)
	}
	epoch, err := ParseEpoch(ev)
	if err != nil {
		return SprintCounts{}, false, err
	}
	gotDuties, err := q.dutySet.Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return SprintCounts{}, false, fmt.Errorf("smembers duty:working: %w", err)
	}
	sort.Strings(gotDuties)

	// The open sprint only names the counts; it is taken from this read's
	// statuses whenever sprint:order held still (a caller keying other
	// reads on it compares SprintName before and after).
	changed := !r.primed || !slices.Equal(order, r.streams) || !slices.Equal(sprints, r.sprints) || epoch != r.epoch || !slices.Equal(gotDuties, r.duties)
	r.primed = true
	r.open = open
	if changed {
		r.streams, r.sprints, r.epoch, r.duties = order, sprints, epoch, gotDuties
		return SprintCounts{}, true, nil
	}
	if r.Sprint != "" && r.Sprint != open {
		return SprintCounts{}, false, &NotOpen{Name: r.Sprint, Open: open}
	}
	rows, err := q.cells.Rows()
	if err != nil {
		return SprintCounts{}, false, err
	}
	c := Sum(rows)
	c.Sprint, c.Status, c.At, c.Epoch = r.SprintName(), status, q.at, epoch
	if msgs, err := q.log.Result(); err != nil && !errors.Is(err, redis.Nil) {
		c.LogErr = err
	} else {
		for _, m := range msgs {
			id, _ := m.Values["id"].(string)
			if to, _ := m.Values["to"].(string); to == Landed && !IsSentinel(id) {
				c.LandedHour++ // a stream's stop landing is not a card landed
			}
		}
	}
	for _, id := range r.duties {
		cmd, ok := q.dutyCmds[id]
		if !ok {
			continue
		}
		vals, err := cmd.Result()
		if err != nil || len(vals) < 6 {
			continue
		}
		where := pipeValue(vals[0])
		if where != "working" {
			continue
		}
		kind := pipeValue(vals[1])
		stream := pipeValue(vals[2])
		friend := pipeValue(vals[3])
		estStr := pipeValue(vals[4])
		est := parseEST(estStr, stream)
		var createdAt time.Time
		if ms, err := strconv.ParseInt(pipeValue(vals[5]), 10, 64); err == nil && ms > 0 {
			createdAt = time.UnixMilli(ms)
		} else {
			createdAt = q.at
		}
		age := q.at.Sub(createdAt)
		if age < 0 {
			age = 0
		}
		ad := ActiveDuty{
			ID:        id,
			Stream:    stream,
			Friend:    friend,
			EST:       est,
			CreatedAt: createdAt,
			Age:       age,
			Overdue:   age > est,
		}
		c.Duties = append(c.Duties, ad)
		if c.LandDuty == nil && (strings.HasSuffix(id, ":land") || strings.Contains(id, ":land-") || (kind == "duty" && stream != "ops")) {
			c.LandDuty = &LandDuty{
				ID:     id,
				Stream: stream,
				Age:    age,
				EST:    est,
			}
		}
	}
	return c, false, nil
}

// Counts is the sprint's progress now: every count in one pipeline, after
// the read that learns the memberships (two round trips from cold; a primed
// CountsReader reads in one). sprint "" is the open sprint.
func Counts(ctx context.Context, rdb redis.Cmdable, sprint string) (SprintCounts, error) {
	return (&CountsReader{Sprint: sprint}).Read(ctx, rdb, time.Now())
}

// Read is one read made for now: a pipeline, again while a membership moves
// (at most three).
func (r *CountsReader) Read(ctx context.Context, rdb redis.Cmdable, now time.Time) (SprintCounts, error) {
	for trip := 0; trip < 3; trip++ {
		pipe := rdb.Pipeline()
		q := r.Queue(ctx, pipe, now)
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) && !isReplyError(err) {
			return SprintCounts{}, fmt.Errorf("counts: %w", err)
		}
		c, changed, err := q.Result()
		if err != nil {
			return SprintCounts{}, err
		}
		if !changed {
			return c, nil
		}
	}
	return SprintCounts{}, errors.New("counts: ws:order, sprint:order or sprint:epoch changed on three reads in a row")
}

func isReplyError(err error) bool {
	var re redis.Error
	return errors.As(err, &re)
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		return "0s"
	}
	d = d.Truncate(time.Second)
	return d.String()
}

func parseEST(s, stream string) time.Duration {
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d
	}
	if stream == "ops" {
		return 5 * time.Minute
	}
	return 15 * time.Minute
}

func pipeValue(v any) string {
	if v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Sprint(v)
	}
	return strings.TrimSpace(s)
}

