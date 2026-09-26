package ws

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
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

// SprintCounts is the sprint's progress at one read: what Counts returns.
type SprintCounts struct {
	// Sprint is the sprint named, or the open one (the last member of
	// sprint:order whose status is not closed); "" when there is none.
	Sprint string
	// Status is the open sprint's s:<S> status when the read found it (no
	// sprint named); "" otherwise.
	Status string
	// Streams are ws:order's streams in rank order; Total is their column
	// sums (Stream "total").
	Streams []StreamCounts
	Total   StreamCounts
	// LandedHour is the cards (sentinels aside) moved to landed in ws:log
	// over the hour before At, the ETA's rate; LogErr is set when ws:log did not come back.
	LandedHour int64
	LogErr     error
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

// Header is the one progress line: landed/total done N%, left L, eta HH:MM ET.
func (c SprintCounts) Header() string {
	if c.Unread() {
		return "?/? done ?%, left ?, eta ?"
	}
	return fmt.Sprintf("%d/%d done %d%%, left %d, eta %s", c.Done(), c.All(), c.Pct(), c.Left(), c.ETA())
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

// CountsReader reads SprintCounts, keeping the memberships across reads so a
// reader that reads again (the table's tick) takes one pipeline.
type CountsReader struct {
	// Sprint names the sprint; "" reads the open one.
	Sprint string

	streams []string
	sprints []string
	open    string
	primed  bool
}

// CountsCmd is one queued read.
type CountsCmd struct {
	r       *CountsReader
	at      time.Time
	order   *redis.StringSliceCmd
	sprintQ *redis.StringSliceCmd
	states  []*redis.StringCmd // HGET s:<S> status per cached sprint
	log     *redis.XMessageSliceCmd
	cells   [][]*CardCountCmd // per cached stream: the six, then parked
}

// Queue queues the read made for now on pipe.
func (r *CountsReader) Queue(ctx context.Context, pipe redis.Pipeliner, now time.Time) *CountsCmd {
	q := &CountsCmd{r: r, at: now, order: pipe.ZRange(ctx, "ws:order", 0, -1)}
	if r.Sprint == "" {
		q.sprintQ = pipe.ZRange(ctx, "sprint:order", 0, -1)
		for _, name := range r.sprints {
			// the status field on s:<S> (what sprint open, close and the
			// pit stop use): closed is out, any other status is open
			q.states = append(q.states, pipe.HGet(ctx, "s:"+name, "status"))
		}
	}
	hourAgo := now.Add(-time.Hour).UnixMilli()
	q.log = pipe.XRangeN(ctx, "ws:log", strconv.FormatInt(hourAgo, 10), "+", LogWindowMax)
	for _, s := range r.streams {
		q.cells = append(q.cells, QueueStreamCounts(ctx, pipe, s))
	}
	return q
}

// QueueMembers queues the membership reads a caller makes in its own earlier
// round trip (ws:order, and sprint:order when no sprint is named); Prime
// takes their answers, so the caller's next pipeline carries every count and
// a cold read needs no round trip of its own (the live layout's SCAN trip
// carries them).
func (r *CountsReader) QueueMembers(ctx context.Context, pipe redis.Pipeliner) (order, sprints *redis.StringSliceCmd) {
	order = pipe.ZRange(ctx, "ws:order", 0, -1)
	if r.Sprint == "" {
		sprints = pipe.ZRange(ctx, "sprint:order", 0, -1)
	}
	return order, sprints
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
	var sprints []string
	open, status := "", ""
	if q.sprintQ != nil {
		if sprints, err = q.sprintQ.Result(); err != nil && !errors.Is(err, redis.Nil) {
			return SprintCounts{}, false, fmt.Errorf("zrange sprint:order: %w", err)
		}
		// the open sprint: the last of sprint:order whose status is not
		// closed (Glenn 2026-09-26 8:50 AM ET: one sprint active at a time)
		for i, name := range r.sprints {
			if st, err := q.states[i].Result(); err == nil && st != "closed" {
				open, status = name, st
			}
		}
	}
	// The open sprint only names the counts; it is taken from this read's
	// statuses whenever sprint:order held still (a caller keying other
	// reads on it compares SprintName before and after).
	changed := !r.primed || !slices.Equal(order, r.streams) || (q.sprintQ != nil && !slices.Equal(sprints, r.sprints))
	r.primed = true
	r.open = open
	if changed {
		r.streams, r.sprints = order, sprints
		return SprintCounts{}, true, nil
	}
	rows := make([]StreamCounts, 0, len(r.streams))
	for i, s := range r.streams {
		row := StreamCounts{Stream: s}
		for j, cmd := range q.cells[i][:CountsCells] {
			n, err := cmd.Result()
			row.Cells[j], row.Unread[j] = n, err != nil && !errors.Is(err, redis.Nil)
		}
		row.Parked = q.cells[i][CountsCells].Val()
		rows = append(rows, row)
	}
	c := Sum(rows)
	c.Sprint, c.Status, c.At = r.SprintName(), status, q.at
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
	return SprintCounts{}, errors.New("counts: ws:order or sprint:order changed on three reads in a row")
}

func isReplyError(err error) bool {
	var re redis.Error
	return errors.As(err, &re)
}
