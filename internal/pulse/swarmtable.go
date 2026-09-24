package pulse

// `nova-pulse status --store <redis>`: THE SWARM TABLE, read from the fleet Redis where
// every bench pushes its own row once a second (nova-tools #2561, replacing
// bin/sprint-table-redis, bin/stuck-done's display half and bin/friends-line).
//
// Glenn 2026-09-22: "the sprint table becomes the swarm table" (#2593). The per-bench table
// -- host, queue, working, done, ok, fail, ok%, load -- is the SWARM table; a SPRINT is a
// bounded task set, and its COWS line (closed / open / working, with the x/y and the
// percent) rides on the bottom of the swarm table when there is a current sprint.
//
// ZERO SSH. bin/stuck-done reached seven benches over ssh to count what they had; the row
// each bench pushes carries it. A bench whose row expired is ABSENT from the table, which is
// the whole point of the five-second TTL: a row nobody is pushing any more is not a row of
// zeros, because a row of zeros reads as a bench with nothing to do.
//
// The columns, the widths, the separators and the totals line are byte for byte the ones
// bin/sprint-table-redis printed, so the table Glenn already reads does not change shape on
// the day it changes implementation. Only the header word does: SPRINT TABLE became
// SWARM TABLE.

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The table's one format, in one place.
const (
	swarmTableHeadFmt  = "%-10s | %5s | %7s | %5s | %5s | %5s | %4s | %6s\n"
	swarmTableRowFmt   = "%-10s | %5s | %7s | %5s | %5s | %5s | %3s%% | %6s\n"
	swarmTableTotalFmt = "%-10s | %5s | %7s | %5s | %5s | %5s | %3s%% |\n"
	swarmTableRule     = "-----------+-------+---------+-------+-------+-------+------+-------"
)

// FriendPrefix is where friend presence lives: `friend:<name>` is set with a TTL by that
// friend's harness (#2610) and `friend:<name>:last` is the last time it was seen.
const FriendPrefix = "friend:"

// SprintCurrentKey names the sprint whose COWS line the table carries; SprintPrefix is that
// sprint's own hash.
const (
	SprintCurrentKey = "sprint:current"
	SprintPrefix     = "sprint:"
)

// StuckDone is the fleet-wide total of DONE cards sitting on a bench with no PR, and when
// it was last measured.
type StuckDone struct {
	Total string // kept as text: an absent total prints `?`, never 0
	At    string
}

// Friend is one friend's presence: up with an age, AWAY with how long and when last seen,
// or none.
type Friend struct {
	Name string
	Up   bool
	// Since is how long the friend has been up (Up) or has been away (not Up). It is zero
	// when nothing was ever seen.
	Since time.Duration
	Last  string // the last-seen stamp, for an AWAY line
	Ever  bool   // false means nothing has ever been seen for this name: `none`
}

// Line is one friend's word on the friends: line.
func (f Friend) Line() string {
	switch {
	case f.Up:
		return fmt.Sprintf("%s up %s", f.Name, shortAge(f.Since))
	case !f.Ever:
		return f.Name + " none"
	case f.Last != "":
		return fmt.Sprintf("%s AWAY %s (last %s)", f.Name, shortAge(f.Since), f.Last)
	default:
		return fmt.Sprintf("%s AWAY %s", f.Name, shortAge(f.Since))
	}
}

// Cows is a sprint's closed / open / working, the four counts of #2593's COWS.
type Cows struct {
	Sprint  string
	Closed  int
	Open    int
	Working int
}

// Total is C+O+W. There is no separate total field, because a total that can disagree with
// its parts is a total nobody can check.
func (c Cows) Total() int { return c.Closed + c.Open + c.Working }

// Pct is the closed share, integer, floored at 0 for an empty sprint.
func (c Cows) Pct() int {
	if c.Total() <= 0 {
		return 0
	}
	return 100 * c.Closed / c.Total()
}

// Line is the sentence `sprint status` prints and the swarm table carries.
func (c Cows) Line() string {
	return fmt.Sprintf("S=%s C=%d O=%d W=%d  %d/%d %d%%",
		c.Sprint, c.Closed, c.Open, c.Working, c.Closed, c.Total(), c.Pct())
}

// SwarmState is everything one render needs, already read.
type SwarmState struct {
	Rows    []BenchRow
	Stuck   StuckDone
	Friends []Friend
	Sprint  *Cows // nil when there is no current sprint: the line is omitted, not faked
}

// RenderSwarmTable prints the table. It takes state and a clock and reads nothing, so the
// tests assert the bytes without a store.
func RenderSwarmTable(st SwarmState, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "SWARM TABLE  %s  (each bench pushes its row every 1 s; absent = no row for 5 s)\n\n",
		now.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, swarmTableHeadFmt, "host", "queue", "working", "done", "ok", "fail", "ok%", "load")
	b.WriteString(swarmTableRule + "\n")

	rows := append([]BenchRow(nil), st.Rows...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Host < rows[j].Host })

	var tq, tw, tr columnTotal
	var td, to, tf int
	for _, r := range rows {
		fmt.Fprintf(&b, swarmTableRowFmt, r.Host,
			r.QueueCell(), r.WorkingCell(), r.DoneCell(),
			r.OKCell(), r.FailCell(), r.OKPctCell(), r.Load1)
		tq.add(r.Queue, r.QueueMark)
		tw.add(r.Working, r.WorkingMark)
		tr.add(0, r.ResultsMark)
		td += r.Done
		to += r.OK
		tf += r.Fail
	}
	b.WriteString(swarmTableRule + "\n")
	totalPct := 0
	if td > 0 {
		totalPct = 100 * to / td
	}
	// done, ok, fail and ok% share one reader per row, so they share one total's mark.
	fmt.Fprintf(&b, swarmTableTotalFmt, "total",
		tq.text(), tw.text(), tr.textFor(td),
		tr.textFor(to), tr.textFor(tf), tr.textFor(totalPct))
	b.WriteString("\n")

	// Why a `?` is a `?`: one line per unreadable source, with the bench that said it. A
	// question mark with no reason beside it would send somebody to the bench to find out.
	for _, r := range rows {
		for _, u := range r.Unavailable {
			fmt.Fprintf(&b, "unavailable: %s %s\n", r.Host, u)
		}
	}

	total := st.Stuck.Total
	if strings.TrimSpace(total) == "" {
		// A count nobody took is a DASH, never a zero -- the rule status --html already
		// follows. Here the shell printed `?` and the readers know that mark, so it stays.
		total = "?"
	}
	if strings.TrimSpace(st.Stuck.At) != "" {
		fmt.Fprintf(&b, "stuck-DONE: %s  (bench:stuck_done at %s)\n", total, st.Stuck.At)
	} else {
		fmt.Fprintf(&b, "stuck-DONE: %s\n", total)
	}

	if len(st.Friends) > 0 {
		words := make([]string, 0, len(st.Friends))
		for _, f := range st.Friends {
			words = append(words, f.Line())
		}
		fmt.Fprintf(&b, "friends: %s\n", strings.Join(words, " · "))
	}
	if st.Sprint != nil {
		b.WriteString(st.Sprint.Line() + "\n")
	}
	return b.String()
}

// columnTotal sums one column across the rows. A `?` anywhere makes the total `?`: a sum
// with an unknown part is unknown. A `-` adds nothing, and a column where EVERY row is `-`
// totals `-`, because none of the benches took that count.
type columnTotal struct {
	sum, rows, absent int
	unknown           bool
}

func (t *columnTotal) add(n int, mark string) {
	t.rows++
	switch mark {
	case CellUnavailable:
		t.unknown = true
	case CellAbsent:
		t.absent++
	default:
		t.sum += n
	}
}

func (t columnTotal) text() string { return t.textFor(t.sum) }

// textFor is the total cell for a number summed elsewhere under this column's marks.
func (t columnTotal) textFor(n int) string {
	switch {
	case t.unknown:
		return CellUnavailable
	case t.rows > 0 && t.absent == t.rows:
		return CellAbsent
	}
	return strconv.Itoa(n)
}

// shortAge is the age a presence line carries: seconds under a minute, minutes under an
// hour, then hours and minutes. It is the shape bin/friends-line printed.
func shortAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// SwarmStoreReader is the store side of the table, as an interface, so the tests read a
// written map and the verb reads Redis.
type SwarmStoreReader interface {
	// Keys returns the keys matching a glob pattern.
	Keys(ctx context.Context, pattern string) ([]string, error)
	// Hash returns one hash whole; a key that is not there is an empty map and no error.
	Hash(ctx context.Context, key string) (map[string]string, error)
	// Get returns one string key; ok is false when it is not there.
	Get(ctx context.Context, key string) (value string, ok bool, err error)
}

// ReadSwarmState reads everything the table shows. The roster is the names a `friends:` line
// must mention even when their key is absent: a friend with no key at all is `none`, and
// without a roster there is nobody to say that about -- so the names are a flag, never a
// list baked into this file.
func ReadSwarmState(ctx context.Context, r SwarmStoreReader, roster []string, now time.Time) (SwarmState, error) {
	var st SwarmState

	keys, err := r.Keys(ctx, BenchKeyPrefix+"*")
	if err != nil {
		return st, fmt.Errorf("scan %s*: %w", BenchKeyPrefix, err)
	}
	for _, k := range keys {
		if k == StuckDoneKey {
			continue // the fleet-wide total, not a host row: it shares the prefix because
			// the bench ACL user may only write bench:*
		}
		h, err := r.Hash(ctx, k)
		if err != nil {
			return st, fmt.Errorf("hgetall %s: %w", k, err)
		}
		if len(h) == 0 {
			continue // the row expired between the scan and the read; an absent bench
		}
		st.Rows = append(st.Rows, benchRowFromHash(k, h))
	}

	if h, err := r.Hash(ctx, StuckDoneKey); err == nil && len(h) > 0 {
		st.Stuck = StuckDone{Total: h["total"], At: h["at"]}
	}

	st.Friends, err = readFriends(ctx, r, roster, now)
	if err != nil {
		return st, err
	}
	st.Sprint, err = readSprint(ctx, r)
	if err != nil {
		return st, err
	}
	return st, nil
}

// benchRowFromHash reads one pushed row. The host falls back to the key's own suffix, so a
// half-written row still shows up under the right name instead of as a blank line. A count
// keeps the mark the bench pushed (`-` absent, `?` unreadable), and a count that is MISSING
// or will not parse is `?`, never 0: the reader must not turn back into a zero what the
// bench was careful not to call one.
func benchRowFromHash(key string, h map[string]string) BenchRow {
	host := h["host"]
	if strings.TrimSpace(host) == "" {
		host = strings.TrimPrefix(key, BenchKeyPrefix)
	}
	load := h["load1"]
	if strings.TrimSpace(load) == "" {
		load = "-"
	}
	r := BenchRow{
		Host:  host,
		Load1: load,
		NCPU:  atoiOrZero(h["ncpu"]),
		At:    h["at"],
	}
	r.Queue, r.QueueMark = cellFromHash(h["queue"])
	r.Working, r.WorkingMark = cellFromHash(h["working"])
	var doneMark, okMark, failMark string
	r.Done, doneMark = cellFromHash(h["done"])
	r.OK, okMark = cellFromHash(h["ok"])
	r.Fail, failMark = cellFromHash(h["fail"])
	// done, ok and fail are one reader's on the bench; the worst of the three marks stands
	// for all of them, and the ints behind a mark are zeroed so no total counts them.
	r.ResultsMark = worstMark(doneMark, okMark, failMark)
	if r.ResultsMark != "" {
		r.Done, r.OK, r.Fail = 0, 0, 0
	}
	for _, u := range strings.Split(h["unavailable"], "\n") {
		if u = strings.TrimSpace(u); u != "" {
			r.Unavailable = append(r.Unavailable, u)
		}
	}
	return r
}

// cellFromHash is one pushed count: the number, or its mark.
func cellFromHash(v string) (int, string) {
	v = strings.TrimSpace(v)
	switch v {
	case CellAbsent:
		return 0, CellAbsent
	case CellUnavailable, "":
		return 0, CellUnavailable
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, CellUnavailable
	}
	return n, ""
}

// worstMark is `?` over `-` over none.
func worstMark(marks ...string) string {
	worst := ""
	for _, m := range marks {
		if m == CellUnavailable {
			return CellUnavailable
		}
		if m == CellAbsent {
			worst = CellAbsent
		}
	}
	return worst
}

func atoiOrZero(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

// readFriends folds friend:<name> and friend:<name>:last into one line's worth of presence
// (#2610). The live key is set by the friend's own harness every 30 s with a 90 s TTL and
// carries the time it was written; the :last key outlives it so an AWAY line can say when
// the friend was last here.
//
// ABSENT IS AWAY, and a name nobody has ever seen is `none`. "We are repeatedly failing to
// notice when friends are not here" (Glenn 2026-09-22): a friend who is gone must read as
// gone on the table, not as a name the table quietly stopped printing.
func readFriends(ctx context.Context, r SwarmStoreReader, roster []string, now time.Time) ([]Friend, error) {
	keys, err := r.Keys(ctx, FriendPrefix+"*")
	if err != nil {
		return nil, fmt.Errorf("scan %s*: %w", FriendPrefix, err)
	}
	names := map[string]bool{}
	for _, n := range roster {
		if n = strings.TrimSpace(n); n != "" {
			names[n] = true
		}
	}
	for _, k := range keys {
		n := strings.TrimPrefix(k, FriendPrefix)
		n = strings.TrimSuffix(n, ":last")
		if n != "" {
			names[n] = true
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	ordered := make([]string, 0, len(names))
	for n := range names {
		ordered = append(ordered, n)
	}
	sort.Strings(ordered)

	out := make([]Friend, 0, len(ordered))
	for _, n := range ordered {
		f := Friend{Name: n}
		live, ok, err := r.Get(ctx, FriendPrefix+n)
		if err != nil {
			return nil, err
		}
		last, lastOK, err := r.Get(ctx, FriendPrefix+n+":last")
		if err != nil {
			return nil, err
		}
		switch {
		case ok:
			f.Up, f.Ever = true, true
			f.Since = ageSince(live, now)
		case lastOK:
			f.Ever = true
			f.Since = ageSince(last, now)
			f.Last = shortStamp(last)
		}
		out = append(out, f)
	}
	return out, nil
}

// ageSince reads a heartbeat's own stamp. The value is an RFC3339 time the friend's harness
// wrote; a value that will not parse gives a zero age rather than a negative one, because a
// friend who looks like they have been up for minus four hours is worse than one with no
// number at all.
func ageSince(stamp string, now time.Time) time.Duration {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(stamp))
	if err != nil {
		return 0
	}
	d := now.UTC().Sub(t.UTC())
	if d < 0 {
		return 0
	}
	return d
}

// shortStamp is the `(last 09:41Z)` form #2610 asked for: the UTC hour and minute.
func shortStamp(stamp string) string {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(stamp))
	if err != nil {
		return strings.TrimSpace(stamp)
	}
	return t.UTC().Format("15:04Z")
}

// readSprint reads the current sprint's COWS, or nil when there is no current sprint. The
// line is OMITTED rather than printed with zeros: a sprint of 0/0 0% on the table is a
// sprint somebody will act on.
func readSprint(ctx context.Context, r SwarmStoreReader) (*Cows, error) {
	name, ok, err := r.Get(ctx, SprintCurrentKey)
	if err != nil {
		return nil, err
	}
	if !ok || strings.TrimSpace(name) == "" {
		return nil, nil
	}
	h, err := r.Hash(ctx, SprintPrefix+strings.TrimSpace(name))
	if err != nil {
		return nil, err
	}
	if len(h) == 0 {
		return nil, nil
	}
	return &Cows{
		Sprint:  strings.TrimSpace(name),
		Closed:  atoiOrZero(h["closed"]),
		Open:    atoiOrZero(h["open"]),
		Working: atoiOrZero(h["working"]),
	}, nil
}
