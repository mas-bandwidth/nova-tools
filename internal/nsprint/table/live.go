// live.go: the live layout of nova-sprint table (#2674), a Go port of the
// bash of record rowan-tools bin/sprint-table-redis. It reads the keys that
// script reads and prints its layout byte for byte, but for one line:
//
//	SPRINT TABLE / the one count / blank / the bench block / two blanks /
//	the friend block.
//
// The progress line is the one count (ws.SprintCounts.Header, nova-tools
// #4411): the numbers sprint status, ws counts and the sprint table print,
// the streams' sentinels never counted. The bash printed sprint-xy's
// sprint:<S>:xy (or SPRINT-XY.txt, and a stale line when both were
// missing); that key is no longer read, and MaskXY is the one documented
// difference the parity control and --compare mask on both sides.
//
// Keys (all read-only; each has one writer elsewhere):
//
//	friend:<f>            HMGET at up ready working done   (friend-row)
//	friend:<f>:down       GET, a set value prints "down"   (out-of-credits)
//	ws:order, ws:<s>:*,   the one count (ws.CountsReader): the memberships ride
//	ws:log, sprint:order  the SCAN walk's first round trip, every count the pipeline
//	s:<S>:pitstop         HGETALL, the pitstop verb's hash: the title reads
//	                      *** PIT STOP *** <why> since <at> (#3423, #3887)
//	bench:*               SCAN COUNT 1000, then HGETALL    (bench-row, card-dealer)
//	bench:<b>:cards:<w>   ZCARD, w = ready working done ok fail (the card move, #3692)
//	ci:nomirror:<b>       SMEMBERS, the repos the bench has no mirror of (ns_ci_release, #3724)
//	bench:_pool:cards:ready ZCARD, the pool line: cards not dealt (#2733)
//	sprint:<S>:cards      EXISTS, the sprint has cards: the pool line prints at 0
//
// A host row's ready, working, done, ok and fail are the ZCARDs of its card
// views, read in the same pipeline (nova-tools#3692, ONE PLACE: every card is
// in one place, and these sets are the bench view of it). The bash
// bench-row's queue, working, done, ok and fail fields are no longer read: it
// counted job dirs, and cards that had ended printed as "-". host, at and
// load1 still come from the bench's own hash.
//
// A host row ends " | nomirror=<repos>" while ci:nomirror:<b> is non-empty
// (#3804): every CI claim of those repos skips the bench until its mirror
// exists, so the defect is on the row; "?" when the set could not be read.
//
// Bench keys are found by SCAN, as the bash does (a cursor walk; the bench
// ACL user has no KEYS and no EVAL_RO); every value is then read in ONE
// pipelined round trip.
package table

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pitstop"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
)

// LiveConfig names what the bash hard-codes; nothing here is compiled in.
type LiveConfig struct {
	Friends  []string      // the roster, in display order
	Sprint   string        // the pit stop (pitstop.Key) and the one count's sprint; "" the open one
	RowStale time.Duration // a friend row older than this prints "stale" (bash ROW_STALE_S=10)
	// BenchStale: a host row whose own at is older than this prints "stale";
	// zero is 2 x BenchBeatInterval (#3372). The bash of record used 60 s.
	BenchStale time.Duration
}

// BenchBeatInterval is the cadence a bench writes its host row at.
const BenchBeatInterval = time.Second

// FriendRow is one friend:<f> hash plus its down flag, raw.
type FriendRow struct {
	Name                         string
	At, Up, Ready, Working, Done string
	OK, Fail                     string // friend:<f>:cards:ok|fail ZCARDs: its copies ended (#3929)
	Down                         string
	// Stale is the whole table's count of the friend's working set members
	// with no live child (#3892): "" on the bash layout, "?" unread.
	Stale string
}

// BenchRow is one bench:<b> hash, values already sanitized the way the bash's
// awk does.
type BenchRow struct {
	Key    string // the key suffix, bench:<Key>
	Fields map[string]string
	// NoMirror is ci:nomirror:<Key>, sorted and comma-joined: "" when the
	// set is empty, "?" when it could not be read (#3804).
	NoMirror string
}

// NoMirrorKey is ci:nomirror:<bench>, written only by the ci_run.lua
// functions (internal/nsprint/ci names the same key).
func NoMirrorKey(bench string) string { return "ci:nomirror:" + bench }

// LiveSnapshot is one read of the live keyspace.
type LiveSnapshot struct {
	Config  LiveConfig
	Friends []FriendRow
	// Counts is the one count (ws.Counts) the progress line prints; At zero
	// is never read.
	Counts ws.SprintCounts
	// Pitstop is s:<S>:pitstop, the one key `nova-sprint pitstop set|clear`
	// writes (#3887): while it exists the title says PIT STOP with its why
	// and at. A key of another type there (the 09-23 string) still stops the
	// dealer, so it shows too, By wrongtype, until the verb repairs it.
	Pitstop pitstop.Stop
	Benches []BenchRow
	// Pool is the pool line's count: the ZCARD of bench:_pool:cards:ready
	// (the undealt view the card move keeps, #2733) whenever that view has
	// cards or the sprint's roster exists; otherwise the retired card-dealer
	// bench:pool hash's queue, as the bash of record read it. PoolPresent
	// says whether the line prints.
	Pool        string
	PoolPresent bool
	// Stale is set by the loop when this tick's read failed and the friend
	// rows and the counts are the last good read (LastGood is when).
	Stale    bool
	LastGood time.Time
}

// ReadLive reads the live keyspace: the SCAN cursor walk for bench:* (its
// first page pipelined with the one count's memberships), then one pipeline
// for every value, the one count's cells among them: two round trips while
// ws:order holds still between them.
func ReadLive(ctx context.Context, client redis.UniversalClient, cfg LiveConfig) (*LiveSnapshot, error) {
	counts := &ws.CountsReader{Sprint: cfg.Sprint}
	var keys []string
	var cursor uint64
	for first := true; ; first = false {
		var page []string
		var next uint64
		var err error
		if first {
			pipe := client.Pipeline()
			scan := pipe.Scan(ctx, cursor, "bench:*", 1000)
			order, sprints := counts.QueueMembers(ctx, pipe)
			if _, xerr := pipe.Exec(ctx); xerr != nil && !errors.Is(xerr, redis.Nil) && !isReplyError(xerr) {
				return nil, fmt.Errorf("scan bench:*: %w", xerr)
			}
			if err := counts.Prime(order, sprints); err != nil {
				return nil, err
			}
			page, next, err = scan.Result()
		} else {
			page, next, err = client.Scan(ctx, cursor, "bench:*", 1000).Result()
		}
		if err != nil {
			return nil, fmt.Errorf("scan bench:*: %w", err)
		}
		keys = append(keys, page...)
		cursor = next
		if cursor == 0 {
			break
		}
	}
	benchKeys := dedupSorted(keys)

	now := time.Now()
	pipe := client.Pipeline()
	countsCmd := counts.Queue(ctx, pipe, now)
	type friendCmds struct {
		row  *redis.SliceCmd
		down *redis.StringCmd
		// #3206 PR A: ns_friend_down writes a HASH {reason, actor, at}; the
		// GET above fails WRONGTYPE on it (tolerated), so its reason and
		// existence are read beside the legacy string.
		downReason *redis.StringCmd
		downExists *redis.IntCmd
	}
	fc := make([]friendCmds, len(cfg.Friends))
	for i, name := range cfg.Friends {
		fc[i].row = pipe.HMGet(ctx, "friend:"+name, "at", "up", "ready", "working", "done")
		fc[i].down = pipe.Get(ctx, "friend:"+name+":down")
		fc[i].downReason = pipe.HGet(ctx, "friend:"+name+":down", "reason")
		fc[i].downExists = pipe.Exists(ctx, "friend:"+name+":down")
	}
	// The pit stop is the verb's hash, read in the same pipeline (#3887).
	var pit *redis.MapStringStringCmd
	if cfg.Sprint != "" {
		pit = pipe.HGetAll(ctx, pitstop.Key(cfg.Sprint))
	}
	poolView := pipe.ZCard(ctx, PoolViewKey)
	var roster *redis.IntCmd
	if cfg.Sprint != "" {
		roster = pipe.Exists(ctx, "sprint:"+cfg.Sprint+":cards")
	}
	hashes := make([]*redis.MapStringStringCmd, len(benchKeys))
	cards := make([][]*redis.IntCmd, len(benchKeys))
	nomirror := make([]*redis.StringSliceCmd, len(benchKeys))
	for i, key := range benchKeys {
		hashes[i] = pipe.HGetAll(ctx, key)
		if name := strings.TrimPrefix(key, "bench:"); name != "pool" && !strings.Contains(name, ":") {
			for _, w := range benchCardCells {
				cards[i] = append(cards[i], pipe.ZCard(ctx, key+":cards:"+w))
			}
			nomirror[i] = pipe.SMembers(ctx, NoMirrorKey(name))
		}
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) && !isReplyError(err) {
		return nil, fmt.Errorf("pipeline: %w", err)
	}
	// The friend block is all-or-nothing, as in the bash: a failed ws:order
	// read (the one count's) means the connection is not answering.
	got, changed, err := countsCmd.Result()
	if err != nil {
		return nil, err
	}
	if changed {
		// ws:order or sprint:order moved since the SCAN trip: read the
		// counts again (a third round trip only on that race)
		if got, err = counts.Read(ctx, client, now); err != nil {
			return nil, err
		}
	}
	snap := &LiveSnapshot{Config: cfg, Counts: got, Pitstop: readPitstop(cfg.Sprint, pit)}
	for i, name := range cfg.Friends {
		row := FriendRow{Name: name}
		if got, err := fc[i].row.Result(); err == nil && len(got) == 5 {
			row.At, row.Up, row.Ready, row.Working, row.Done = pipeValue(got[0]), pipeValue(got[1]), pipeValue(got[2]), pipeValue(got[3]), pipeValue(got[4])
		}
		if got, err := fc[i].down.Result(); err == nil {
			row.Down = flatten(got)
		} else if fc[i].downExists.Val() == 1 {
			row.Down = flatten(fc[i].downReason.Val())
			if row.Down == "" {
				row.Down = "down"
			}
		}
		snap.Friends = append(snap.Friends, row)
	}
	for i, key := range benchKeys {
		h, err := hashes[i].Result()
		if key == "bench:pool" {
			snap.PoolPresent = true
			snap.Pool = "0"
			if err == nil && sanitize(h["queue"]) != "" {
				snap.Pool = sanitize(h["queue"])
			}
			continue
		}
		if err != nil || len(h) == 0 {
			continue
		}
		fields := make(map[string]string, len(h))
		for f, v := range h {
			fields[f] = sanitize(v)
		}
		cardCells(fields, cards[i])
		snap.Benches = append(snap.Benches, BenchRow{Key: strings.TrimPrefix(key, "bench:"), Fields: fields, NoMirror: noMirrorCell(nomirror[i])})
	}
	poolFromView(snap, poolView, roster)
	return snap, nil
}

// readPitstop is the stop from the pipelined HGETALL: the hash as the verb
// wrote it, or a stop by "wrongtype" when another type holds the key (the
// HGETALL answered WRONGTYPE), or none.
func readPitstop(sprint string, cmd *redis.MapStringStringCmd) pitstop.Stop {
	if cmd == nil {
		return pitstop.Stop{Sprint: sprint}
	}
	h, err := cmd.Result()
	if err != nil {
		if strings.HasPrefix(err.Error(), "WRONGTYPE") {
			return pitstop.Stop{Sprint: sprint, Set: true, By: "wrongtype"}
		}
		return pitstop.Stop{Sprint: sprint}
	}
	return pitstop.FromHash(sprint, h)
}

// pitstopTitle is line 1 while a stop is set: SPRINT TABLE *** PIT STOP ***
// then the why and since <at> (UTC, as pitstop status prints it), and the
// streams a scoped stop holds or an all stop has lifted.
func pitstopTitle(p pitstop.Stop) string {
	var b strings.Builder
	b.WriteString("SPRINT TABLE *** PIT STOP ***")
	if p.By == "wrongtype" {
		b.WriteString(" (s:" + p.Sprint + ":pitstop is not a hash; nova-sprint pitstop clear repairs it)")
		return b.String()
	}
	if why := strings.TrimSpace(flatten(p.Why)); why != "" {
		b.WriteString(" " + why)
	}
	if p.At > 0 {
		b.WriteString(" since " + time.UnixMilli(p.At).UTC().Format("2006-01-02T15:04:05Z"))
	}
	if p.Streams != nil {
		b.WriteString(" streams=" + quoteJoin(p.Streams))
	} else if len(p.Lifted) > 0 {
		b.WriteString(" lifted=" + quoteJoin(p.Lifted))
	}
	return b.String()
}

func quoteJoin(xs []string) string {
	q := make([]string, len(xs))
	for i, x := range xs {
		q[i] = strconv.Quote(flatten(x))
	}
	return strings.Join(q, ",")
}

// PoolViewKey is the undealt pool, the card move's bench view for cards with
// no bench (card.BenchCardsKey("_pool", "ready")).
const PoolViewKey = "bench:_pool:cards:ready"

// poolFromView sets the pool line from the undealt view (#2733): the dealer's
// bench:pool hash is retired and nothing writes it, so the line reads the set
// the card move keeps. It prints when the view has cards or the sprint has a
// roster (an empty pool is "0", not no line); a view that could not be read
// prints "?", never a false 0. With neither, the legacy hash (if any) stands.
func poolFromView(snap *LiveSnapshot, view, roster *redis.IntCmd) {
	live := roster != nil && roster.Err() == nil && roster.Val() > 0
	n, err := view.Result()
	switch {
	case err != nil && live:
		snap.Pool, snap.PoolPresent = "?", true
	case err == nil && (n > 0 || live):
		snap.Pool, snap.PoolPresent = strconv.FormatInt(n, 10), true
	}
}

// benchCardCells are the host row's card columns, each a ZCARD of
// bench:<b>:cards:<cell>; the first is the row's queue column (ready).
var benchCardCells = []string{"ready", "working", "done", "ok", "fail"}

// cardCells writes the card view counts over the bench hash's own queue,
// working, done, ok and fail (and drops the dealer's queue override): the
// row prints the sets. A count that could not be read (the ZCARD errored)
// is "?", never a false 0; the row and its column total print "?".
func cardCells(fields map[string]string, cmds []*redis.IntCmd) {
	if len(cmds) != len(benchCardCells) {
		return
	}
	delete(fields, "dealer_queue")
	delete(fields, "dealer_at")
	for j, w := range benchCardCells {
		field := w
		if w == "ready" {
			field = "queue"
		}
		if cmds[j].Err() != nil {
			fields[field] = "?"
			continue
		}
		fields[field] = strconv.FormatInt(cmds[j].Val(), 10)
	}
}

// noMirrorCell is the host row's nomirror value: the set's members sorted and
// comma-joined, "" when empty, "?" when the SMEMBERS errored.
func noMirrorCell(cmd *redis.StringSliceCmd) string {
	if cmd == nil {
		return ""
	}
	repos, err := cmd.Result()
	if err != nil {
		return "?"
	}
	sort.Strings(repos)
	for i, r := range repos {
		repos[i] = sanitize(r)
	}
	return strings.Join(repos, ",")
}

// noMirrorSuffix ends a host row: " | nomirror=<repos>" or nothing.
func (row BenchRow) noMirrorSuffix() string {
	if row.NoMirror == "" {
		return ""
	}
	return " | nomirror=" + row.NoMirror
}

// FailedLive is the tick whose read failed: the friend, counts and pit stop values
// of last (nil when there never was a good read), no bench rows, and a stale
// line (the bash's LAST_FROWS path).
func FailedLive(cfg LiveConfig, last *LiveSnapshot) *LiveSnapshot {
	snap := &LiveSnapshot{Config: cfg, Stale: true}
	if last != nil {
		snap.Friends, snap.Counts, snap.Pitstop, snap.LastGood = last.Friends, last.Counts, last.Pitstop, last.LastGood
	}
	return snap
}

const liveBenchRule = "-----------+-------+---------+-------+-------+-------+------+-------\n"
const liveFriendRule = "-----------+-------+---------+-------+------------\n"

// RenderLive prints the bash layout at now.
func (s *LiveSnapshot) RenderLive(now time.Time) string {
	var b strings.Builder
	if s.Pitstop.Set {
		b.WriteString(pitstopTitle(s.Pitstop) + "\n")
	} else {
		b.WriteString("SPRINT TABLE\n")
	}
	b.WriteString("\n")
	// the one count (#4411); never read prints every number "?"
	if s.Counts.At.IsZero() {
		b.WriteString(NeverCounted + "\n")
	} else {
		b.WriteString(s.Counts.Header() + "\n")
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "%-10s | %5s | %7s | %5s | %5s | %5s | %4s | %6s\n", "host", "ready", "working", "done", "ok", "fail", "ok%", "load")
	b.WriteString(liveBenchRule)
	var tq, tw, td, to, tf int64
	unread := map[string]bool{}
	for _, row := range s.Benches {
		c, show := row.cells(now)
		if !show {
			continue
		}
		if s.benchRowStale(row.Fields, now) {
			fmt.Fprintf(&b, "%-10s | %5s | %7s | %5s | %5s | %5s | %4s | %6s%s\n", c.host, "stale", "?", "?", "?", "?", "?", "?", row.noMirrorSuffix())
			continue
		}
		for cell := range c.unread {
			unread[cell] = true
		}
		pct := "0%"
		if c.unread["done"] || c.unread["ok"] {
			pct = "?"
		} else if c.done > 0 {
			pct = strconv.FormatInt(100*c.ok/c.done, 10) + "%"
		}
		fmt.Fprintf(&b, "%-10s | %5s | %7s | %5s | %5s | %5s | %4s | %6s%s\n", c.host, c.queue,
			c.num("working", c.working), c.num("done", c.done), c.num("ok", c.ok), c.num("fail", c.fail), pct, c.load, row.noMirrorSuffix())
		qn, _ := strconv.ParseInt(c.queue, 10, 64)
		tq, tw, td, to, tf = tq+qn, tw+c.working, td+c.done, to+c.ok, tf+c.fail
	}
	b.WriteString(liveBenchRule)
	total := benchCells{unread: unread}
	tpct := "0%"
	if unread["done"] || unread["ok"] {
		tpct = "?"
	} else if td > 0 {
		tpct = strconv.FormatInt(100*to/td, 10) + "%"
	}
	fmt.Fprintf(&b, "%-10s | %5s | %7s | %5s | %5s | %5s | %4s |\n", "total", total.num("queue", tq), total.num("working", tw),
		total.num("done", td), total.num("ok", to), total.num("fail", tf), tpct)
	if s.PoolPresent {
		fmt.Fprintf(&b, "pool: %s undealt\n", s.Pool)
	}
	b.WriteString("\n\n")

	fmt.Fprintf(&b, "%-10s | %5s | %7s | %5s | %-10s\n", "friend", "ready", "working", "done", "status")
	b.WriteString(liveFriendRule)
	var fq, fw, fd int64
	if len(s.Friends) == 0 {
		for _, name := range s.Config.Friends {
			fmt.Fprintf(&b, "%-10s | %5s | %7s | %5s | %-10s\n", name, "-", "-", "-", "?")
		}
	}
	for _, row := range s.Friends {
		q, w, d := orDash(row.Ready), orDash(row.Working), orDash(row.Done)
		fq, fw, fd = fq+digitsOnly(q), fw+digitsOnly(w), fd+digitsOnly(d)
		fmt.Fprintf(&b, "%-10s | %5s | %7s | %5s | %-10s\n", row.Name, q, w, d, s.friendStatus(row, now))
	}
	b.WriteString(liveFriendRule)
	fmt.Fprintf(&b, "%-10s | %5d | %7d | %5d |\n", "total", fq, fw, fd)
	if s.Stale {
		if !s.LastGood.IsZero() {
			fmt.Fprintf(&b, "stale: %ds (Redis did not answer; rows are the last good read)\n", now.Unix()-s.LastGood.Unix())
		} else {
			b.WriteString("stale: never read (Redis did not answer since start)\n")
		}
	}
	return b.String()
}

// benchRowStale is the host row's age rule (#3372): a row whose own at is
// older than Config.BenchStale (2 beat intervals by default) prints "stale"
// with no numbers and no share of the totals; it never vanishes and never
// shows old numbers. An unparseable at shows the row; an at ahead of now is
// clock skew and counts as fresh.
func (s *LiveSnapshot) benchRowStale(f map[string]string, now time.Time) bool {
	at, ok := parseUTC(f["at"])
	if !ok {
		return false
	}
	limit := s.Config.BenchStale
	if limit <= 0 {
		limit = 2 * BenchBeatInterval
	}
	return now.Sub(at) > limit
}

// benchCells is one bench row's cells, the rules the bash applies.
type benchCells struct {
	host, queue, load       string
	working, done, ok, fail int64
	// unread names the cells whose value is "?" (a card view count that
	// could not be read, #3692): they print "?", never a number.
	unread map[string]bool
}

// num prints one count cell: "?" when it could not be read.
func (c benchCells) num(cell string, v int64) string {
	if c.unread[cell] {
		return "?"
	}
	return strconv.FormatInt(v, 10)
}

// cells applies the bash's row rules: a hash whose own host field is not its
// key is no row; the dealer's queue count wins while dealer_at is at most
// 30 s old; a missing load prints "-". The beat's age is not a cell rule:
// an old row prints "stale" (benchRowStale, #3372) instead of vanishing.
func (row BenchRow) cells(now time.Time) (benchCells, bool) {
	f := row.Fields
	if f["host"] != row.Key {
		return benchCells{}, false
	}
	c := benchCells{host: f["host"], queue: strconv.FormatInt(awkInt(f["queue"]), 10), load: f["load1"]}
	if dq, dat := f["dealer_queue"], f["dealer_at"]; dq != "" && dat != "" {
		if at, ok := parseUTC(dat); ok {
			if age := now.Unix() - at.Unix(); age >= 0 && age <= 30 {
				c.queue = dq
			}
		}
	}
	c.working, c.done, c.ok, c.fail = awkInt(f["working"]), awkInt(f["done"]), awkInt(f["ok"]), awkInt(f["fail"])
	for _, cell := range []string{"queue", "working", "done", "ok", "fail"} {
		if f[cell] == "?" {
			if c.unread == nil {
				c.unread = map[string]bool{}
			}
			c.unread[cell] = true
		}
	}
	if c.unread["queue"] {
		c.queue = "?"
	}
	if c.load == "" {
		c.load = "-"
	}
	return c, true
}

// friendStatus: down (the down flag) | ? (no row) | stale (older than
// RowStale) | down (up != 1) | up. No ages (Glenn 2026-09-23 3:30 PM ET).
func (s *LiveSnapshot) friendStatus(row FriendRow, now time.Time) string {
	return friendState(row, now, s.Config.RowStale)
}

func friendState(row FriendRow, now time.Time, stale time.Duration) string {
	if row.Down != "" {
		return "down"
	}
	digits := keepDigits(row.At)
	if len(digits) > 14 {
		digits = digits[:14]
	}
	if len(digits) != 14 {
		return "?"
	}
	at, err := time.ParseInLocation("20060102150405", digits, time.UTC)
	if err != nil {
		return "?"
	}
	age := now.Unix() - at.Unix()
	if age < 0 {
		age = 0
	}
	if stale <= 0 {
		stale = 10 * time.Second
	}
	if age > int64(stale/time.Second) {
		return "stale"
	}
	if row.Up != "1" {
		return "down"
	}
	return "up"
}

func parseUTC(s string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02T15:04:05Z", s)
	return t, err == nil
}

func dedupSorted(keys []string) []string {
	seen := make(map[string]bool, len(keys))
	var out []string
	for _, k := range keys {
		if k == "bench:stuck_done" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func isReplyError(err error) bool {
	var re redis.Error
	return errors.As(err, &re)
}

// pipeValue is redis-pipe.bash's rendering of one reply: nil -> "", CR/LF -> space.
func pipeValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return flatten(t)
	default:
		return flatten(fmt.Sprint(t))
	}
}

func flatten(s string) string { return strings.NewReplacer("\r", " ", "\n", " ").Replace(s) }

func orDash(v string) string {
	if v == "" || v == "(nil)" {
		return "-"
	}
	return v
}

// sanitize keeps what the bash's awk keeps: [A-Za-z0-9_.:T-].
func sanitize(v string) string {
	var b strings.Builder
	for _, r := range v {
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == ':' || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func keepDigits(v string) string {
	var b strings.Builder
	for _, r := range v {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// digitsOnly is the friend total's rule: only an all-digit cell adds.
func digitsOnly(v string) int64 {
	if v == "" || strings.Trim(v, "0123456789") != "" {
		return 0
	}
	n, _ := strconv.ParseInt(v, 10, 64)
	return n
}

// awkInt is awk's printf %d of a string: its leading number, truncated; 0 if none.
func awkInt(v string) int64 {
	end := 0
	for end < len(v) && (v[end] >= '0' && v[end] <= '9' || v[end] == '.' || (end == 0 && (v[end] == '-' || v[end] == '+'))) {
		end++
	}
	f, err := strconv.ParseFloat(v[:end], 64)
	for err != nil && end > 0 {
		end--
		f, err = strconv.ParseFloat(v[:end], 64)
	}
	if err != nil {
		return 0
	}
	return int64(f)
}

// NeverCounted is the progress line before any good read.
const NeverCounted = "?/? done ?%, left ?, eta ?"

// MaskXY is the one documented difference between the live layout and the
// bash of record (nova-tools #4411): the bash's progress line (sprint-xy's
// sprint:<S>:xy, or its SPRINT ? line) and its xy stale lines against the
// one count's line. It replaces the line after the title's blank with
// "X/Y MASKED" and drops every "stale: ... xy key missing" line, so the
// parity control and --compare compare every other byte.
func MaskXY(table string) string {
	lines := strings.Split(table, "\n")
	out := make([]string, 0, len(lines))
	for i, l := range lines {
		switch {
		case i == 2 && len(lines) > 1 && lines[1] == "":
			out = append(out, "X/Y MASKED")
		case strings.HasPrefix(l, "stale: ") && strings.Contains(l, "xy key missing"):
		default:
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}
