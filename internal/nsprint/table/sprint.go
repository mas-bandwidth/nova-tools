// sprint.go: the whole sprint table (#3530, part of #3662), the one screen
// Glenn watches, read from Redis once a second. It replaces the bash
// renderers of 2026-09-24 (a streams renderer that asked GitHub, an ETA
// script, sprint-table-redis for the host and friend blocks, and a shell loop
// that stitched their files together). Layout, one blank line between blocks:
//
//	SPRINT TABLE [*** PIT STOP ***]
//
//	<left>/<y> left, <z>% done -> ~<eta>m
//
//	stream | waiting | ready | working | reading | merging | landed   (rows in ws:order, all-zero rows hidden, total)
//	LAND stream=<s> members=<n> head=<sha8> ci=<word> age=<d>   (one per open landing, #3900)
//
// reading is ZCARD ws:<s>:reading (#3929). merging prints <read>/<unread>
// (merging.go, #3900) until a ws:<s>:reading set exists; from then on the
// unread cards are the reading column and merging is the read cards alone:
// the same ReadSplit, its other source.
//
//	friend | ready | working | done | status        (status up|down)
//
//	host | ready | working | done | ok | fail | ok% | load
//
// Keys, every one read in ONE pipelined round trip per tick (a second round
// trip only on the tick a membership set changed; never KEYS, never SCAN):
//
//	ws:order                ZRANGE, the streams in rank order (the ws index, #3662)
//	ws:<s>:<state>          ZCARD for waiting, ready, working, reading, merging, landed
//	EVAL_RO detailScript    read only: ws:<s>:merging with each card's pr:<name>:<n>
//	                        head/state/stream/reads, cfg:land, and every
//	                        land:<repo>:<slug> of land:<repo>:streams with its PR's ci
//	ws:log                  XRANGE over the last hour: the landed rate for the ETA
//	ws:done0                HGETALL: each friend's done count at the last `table clear`
//	benches                 SMEMBERS, the bench list; per member (#2389, each
//	                        bench's own keys, never a bash-written row):
//	bench:<b>:cards:<w>     ZCARD for w = ready, working (the card views, #3692)
//	bench:<b>:cards:ok|fail ZCOUNT from the current sprint's start (every score is
//	                        the card's created_at; the done column's scope below),
//	                        all of the set with no sprint: done = ok + fail,
//	                        ok% = ok / done (#3894)
//	bench:<b>:beat          HMGET load1 at (the bench's own `bench beat` loop)
//	friends                 SMEMBERS when no roster is given; friend:<f> HMGET at, up
//	friend:<f>:cards:<w>    ZCARD for ready, working: the friend's cells are
//	                        the sizes of the sets its tasks move through (the card
//	                        model, rowan-new specs/ws-index.md; nova-tools#3779)
//	friend:<f>:cards:done   ZCOUNT from the current sprint's start to +inf: done
//	                        counts only this sprint's cards (#3883); ZCARD beside
//	                        it for the done base of a `table clear`
//	sprint:order            ZRANGE -1 -1 when no sprint is named: the newest
//	                        sprint opened is the current one
//	s:<S>                   HGET opened_at, the current sprint's start (ms)
//	sprint:<S>:cards        ZRANGE 0 0 WITHSCORES: the oldest card's created_at,
//	                        the start of a sprint with no opened_at
//	friend:<f>:down         EXISTS (a string or a hash; either means down)
//	s:<S>:pitstop           EXISTS, with the legacy sprint:<S>:pitstop
//
// The membership of ws:order, benches and friends, and the current sprint
// with its start, are kept from the previous tick, so the tick's one pipeline
// reads the sets and every member's values together; a tick that finds a set
// or the sprint's start changed reads again at once with the new ones, so a
// row is never rendered from last second's membership or scope.
package table

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// WSStates are the six per-stream sets the table counts, in reply order.
// reading sits between working and merging (Glenn 2026-09-25: "split merging
// into separate reading | merging columns"): a card whose PR is being read.
var WSStates = []string{"waiting", "ready", "working", "reading", "merging", "landed"}

// FriendWheres are the three friend:<f>:cards:<where> sets the friend block
// counts, in column order.
var FriendWheres = []string{"ready", "working", "done"}

// FriendCardsKey is one friend's set of task ids at where.
func FriendCardsKey(friend, where string) string { return "friend:" + friend + ":cards:" + where }

// DoneBaseKey holds each friend's done count at the last `table clear`
// (#3637): the friend block shows done minus this, so a clear zeroes the
// column without touching the index sets friend-row counts.
const DoneBaseKey = "ws:done0"

// SprintConfig is what the whole table is told; everything else is read.
type SprintConfig struct {
	// Sprint names the pit stop keys s:<Sprint>:pitstop and
	// sprint:<Sprint>:pitstop; empty reads no pit stop.
	Sprint string
	// Friends is the roster in display order; empty reads the friends SET,
	// sorted by name.
	Friends []string
	// RowStale: a friend row whose at is older than this is down.
	RowStale time.Duration
	// LockKey/LockToken, when set, refresh the writer's lock in the tick's
	// own pipeline (see AcquireLock); the read reports LockLost when it is gone.
	LockKey, LockToken string
	LockTTL            time.Duration
	// LandRepos are the owner/name repos whose open landings print as LAND
	// lines; empty is DefaultLandRepos.
	LandRepos []string
	// ReadingSet takes the merging split from ws:<s>:reading (#3929) even
	// before any card is in one: merging prints the read count alone, not
	// <read>/<unread>. Without it the switch happens on the first tick that
	// finds a reading set non-empty and stays. The reading column is always
	// printed.
	ReadingSet bool
}

// StreamRow is one stream's six counts, in WSStates order.
type StreamRow struct {
	Name                                              string
	Waiting, Ready, Working, Reading, Merging, Landed int64
	// MergingRead is how many cards of merging the lander would take (a
	// read at head >= cfg:land, no hold), from the PR records; -1 unknown.
	MergingRead int64
}

// Total is every task in the stream's six sets.
func (r StreamRow) Total() int64 {
	return r.Waiting + r.Ready + r.Working + r.Reading + r.Merging + r.Landed
}

// SprintSnapshot is one tick's read.
type SprintSnapshot struct {
	Config     SprintConfig
	Pitstop    bool
	Streams    []StreamRow
	LandedHour int64 // ws:log moves to landed in the hour before the read
	Hosts      []HostRow
	Friends    []FriendRow
	DoneBase   map[string]string
	// DoneAll is each friend's ZCARD friend:<f>:cards:done, every card it
	// ever finished: the measure a `table clear` base (DoneBase) is taken in.
	DoneAll map[string]int64
	// DoneSprint is the sprint the done column is scoped to (#3883): the
	// named sprint, else the newest of sprint:order; empty when there is none.
	DoneSprint string
	// DoneSince is the ZCOUNT min of the done column: the sprint's start in
	// ms, or -inf when no sprint start is known (every card counts).
	DoneSince string
	// DoneFrom names where DoneSince came from: "opened_at" (s:<S> opened_at,
	// written by sprint open), "oldest card" (a sprint with no opened_at is
	// open since the created_at of the first member of sprint:<S>:cards), or
	// empty for -inf.
	DoneFrom string
	// Landings are the open landings, one LAND line each (#3900).
	Landings []LandRow
	// ReadSource is where ReadSplit reads: ReadFromRecords or ReadFromSet.
	ReadSource string
	// RoundTrips is how many pipelines the read took: 1 in steady state.
	RoundTrips int
	// LockLost says the tick found the writer's lock held by someone else.
	LockLost bool
	// Stale is set when this tick's read failed; the rows are LastGood's.
	Stale    bool
	LastGood time.Time
}

// HostRow is one bench of the host block, read from the keys the bench's own
// work writes (#2389): Ready and Working are the ZCARDs of its card views,
// Load its beat's load1. Down says the beat is gone (its TTL is 3 beat
// intervals) or older than hostBeatStale: the row still shows its cards.
// OK and Fail are the cards that ended on the bench this sprint (#3894), the
// sizes of bench:<b>:cards:ok|fail from the sprint's start; Unread says
// one of those two counts did not come back, so done, ok, fail and ok% print
// "?", never a false 0.
type HostRow struct {
	Name           string
	Ready, Working int64
	OK, Fail       int64
	Unread         bool
	Load           string
	Down           bool
}

// Done is every card that ended on the bench: ok plus fail.
func (r HostRow) Done() int64 { return r.OK + r.Fail }

// okPct is ok over done as a whole percent, "-" while nothing is done.
func okPct(ok, done int64) string {
	if done == 0 {
		return "-"
	}
	return strconv.FormatInt(100*ok/done, 10) + "%"
}

// hostBeatStale is how old a beat's own at may be before the row prints
// down, for a beat key that outlived its TTL.
const hostBeatStale = 60 * time.Second

// SprintReader reads the whole table, keeping set membership across ticks.
type SprintReader struct {
	Client  redis.UniversalClient
	Config  SprintConfig
	streams []string
	benches []string
	friends []string
	// sprint, since and from are the done column's scope from the last tick.
	sprint, since, from string
	primed              bool
	// sawReading is set on the first tick a reading set was non-empty: the
	// split stays ReadFromSet from then on, so the merging cell never flaps.
	sawReading bool
}

// NewSprintReader is a reader with no membership yet: its first Read takes
// two round trips (the sets, then the values).
func NewSprintReader(client redis.UniversalClient, cfg SprintConfig) *SprintReader {
	return &SprintReader{Client: client, Config: cfg}
}

// Read is one tick at now.
func (r *SprintReader) Read(ctx context.Context, now time.Time) (*SprintSnapshot, error) {
	// Four: a first tick can learn the sets, then the newest sprint, then
	// that sprint's start before its values are read in one pipeline.
	for trips := 1; trips <= 4; trips++ {
		snap, changed, err := r.readOnce(ctx, now)
		if err != nil {
			return nil, err
		}
		if !changed {
			snap.RoundTrips = trips
			return snap, nil
		}
	}
	return nil, errors.New("membership changed on four reads in a row")
}

func (r *SprintReader) readOnce(ctx context.Context, now time.Time) (*SprintSnapshot, bool, error) {
	cfg := r.Config
	pipe := r.Client.Pipeline()
	order := pipe.ZRange(ctx, "ws:order", 0, -1)
	benchSet := pipe.SMembers(ctx, "benches")
	var friendSet *redis.StringSliceCmd
	if len(cfg.Friends) == 0 {
		friendSet = pipe.SMembers(ctx, "friends")
	}
	var pit *redis.IntCmd
	if cfg.Sprint != "" {
		pit = pipe.Exists(ctx, "s:"+cfg.Sprint+":pitstop", "sprint:"+cfg.Sprint+":pitstop")
	}
	hourAgo := now.Add(-time.Hour).UnixMilli()
	log := pipe.XRangeN(ctx, "ws:log", strconv.FormatInt(hourAgo, 10), "+", logWindowMax)
	base := pipe.HGetAll(ctx, DoneBaseKey)
	var newest *redis.StringSliceCmd
	if cfg.Sprint == "" {
		newest = pipe.ZRange(ctx, "sprint:order", -1, -1)
	}
	scope := cfg.Sprint
	if scope == "" {
		scope = r.sprint
	}
	var opened *redis.StringCmd
	var oldest *redis.ZSliceCmd
	if scope != "" {
		opened = pipe.HGet(ctx, "s:"+scope, "opened_at")
		oldest = pipe.ZRangeWithScores(ctx, "sprint:"+scope+":cards", 0, 0)
	}
	since := r.since
	if since == "" {
		since = "-inf"
	}
	var lock *redis.Cmd
	if cfg.LockKey != "" {
		lock = pipe.Eval(ctx, lockRefreshScript, []string{cfg.LockKey}, cfg.LockToken, lockTTL(cfg).Milliseconds())
	}
	counts := make([][]*redis.IntCmd, len(r.streams))
	for i, s := range r.streams {
		for _, state := range WSStates {
			counts[i] = append(counts[i], pipe.ZCard(ctx, "ws:"+s+":"+state))
		}
	}
	// The merging split from records only while there is no reading set.
	var fromRecords []string
	if !cfg.ReadingSet && !r.sawReading {
		fromRecords = r.streams
	}
	repos := landRepos(cfg)
	detail := pipe.EvalRO(ctx, detailScript, nil, detailArgs(fromRecords, repos)...)
	type hostCmds struct {
		ready, working, ok, fail *redis.IntCmd
		beat                     *redis.SliceCmd
	}
	hosts := make([]hostCmds, len(r.benches))
	for i, b := range r.benches {
		hosts[i] = hostCmds{
			ready:   pipe.ZCard(ctx, "bench:"+b+":cards:ready"),
			working: pipe.ZCard(ctx, "bench:"+b+":cards:working"),
			ok:      pipe.ZCount(ctx, "bench:"+b+":cards:ok", since, "+inf"),
			fail:    pipe.ZCount(ctx, "bench:"+b+":cards:fail", since, "+inf"),
			beat:    pipe.HMGet(ctx, "bench:"+b+":beat", "load1", "at"),
		}
	}
	roster := cfg.Friends
	if len(roster) == 0 {
		roster = r.friends
	}
	rows := make([]*redis.SliceCmd, len(roster))
	cells := make([][]*redis.IntCmd, len(roster))
	doneAll := make([]*redis.IntCmd, len(roster))
	downs := make([]*redis.IntCmd, len(roster))
	for i, f := range roster {
		rows[i] = pipe.HMGet(ctx, "friend:"+f, "at", "up")
		for _, w := range FriendWheres {
			if w == "done" {
				// Only this sprint's cards: the set is scored by created_at.
				cells[i] = append(cells[i], pipe.ZCount(ctx, FriendCardsKey(f, w), since, "+inf"))
				continue
			}
			cells[i] = append(cells[i], pipe.ZCard(ctx, FriendCardsKey(f, w)))
		}
		doneAll[i] = pipe.ZCard(ctx, FriendCardsKey(f, "done"))
		downs[i] = pipe.Exists(ctx, "friend:"+f+":down")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) && !isReplyError(err) {
		return nil, false, fmt.Errorf("pipeline: %w", err)
	}
	// A dead connection fails every command; the order read standing is the
	// tick's proof that Redis answered.
	gotOrder, err := order.Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, false, fmt.Errorf("zrange ws:order: %w", err)
	}
	gotBenches, _ := benchSet.Result()
	sort.Strings(gotBenches)
	var gotFriends []string
	if friendSet != nil {
		gotFriends, _ = friendSet.Result()
		sort.Strings(gotFriends)
	}
	gotSprint := cfg.Sprint
	if newest != nil {
		gotSprint = ""
		if names, err := newest.Result(); err == nil && len(names) == 1 {
			gotSprint = names[0]
		}
	}
	gotSince, gotFrom := doneScope(opened, oldest)
	changed := !r.primed || !slices.Equal(gotOrder, r.streams) || !slices.Equal(gotBenches, r.benches) ||
		(friendSet != nil && !slices.Equal(gotFriends, r.friends)) ||
		gotSprint != scope || gotSince != since
	r.primed = true
	if changed {
		r.streams, r.benches, r.friends = gotOrder, gotBenches, gotFriends
		r.sprint = gotSprint
		// The start read is of scope; a new sprint's start is read next trip.
		r.since, r.from = gotSince, gotFrom
		if gotSprint != scope {
			r.since, r.from = "-inf", ""
		}
		return nil, true, nil
	}
	r.from = gotFrom

	snap := &SprintSnapshot{Config: cfg, DoneBase: map[string]string{}, DoneAll: map[string]int64{},
		DoneSprint: scope, DoneSince: since, DoneFrom: r.from}
	if pit != nil && pit.Val() > 0 {
		snap.Pitstop = true
	}
	if msgs, err := log.Result(); err == nil {
		for _, m := range msgs {
			if to, _ := m.Values["to"].(string); to == "landed" {
				snap.LandedHour++
			}
		}
	}
	if h, err := base.Result(); err == nil {
		snap.DoneBase = h
	}
	if lock != nil {
		if n, err := lock.Int64(); err != nil || n == 0 {
			snap.LockLost = true
		}
	}
	for i, s := range r.streams {
		row := StreamRow{Name: s, MergingRead: -1}
		cells := []*int64{&row.Waiting, &row.Ready, &row.Working, &row.Reading, &row.Merging, &row.Landed}
		for j, c := range counts[i] {
			*cells[j] = c.Val()
		}
		if row.Reading > 0 {
			r.sawReading = true
		}
		snap.Streams = append(snap.Streams, row)
	}
	snap.ReadSource = ReadFromRecords
	if cfg.ReadingSet || r.sawReading {
		snap.ReadSource = ReadFromSet
	}
	if v, err := detail.Result(); err == nil {
		applyDetail(snap, v, fromRecords, repos)
	}
	for i, b := range r.benches {
		row := HostRow{Name: b, Ready: hosts[i].ready.Val(), Working: hosts[i].working.Val(), Load: "-", Down: true}
		ok, okErr := hosts[i].ok.Result()
		fail, failErr := hosts[i].fail.Result()
		row.OK, row.Fail, row.Unread = ok, fail, okErr != nil || failErr != nil
		if got, err := hosts[i].beat.Result(); err == nil && len(got) == 2 {
			load, at := sanitize(pipeValue(got[0])), pipeValue(got[1])
			if atMS, err := strconv.ParseInt(at, 10, 64); err == nil && now.UnixMilli()-atMS <= hostBeatStale.Milliseconds() {
				row.Down = false
				if load != "" {
					row.Load = load
				}
			}
		}
		snap.Hosts = append(snap.Hosts, row)
	}
	for i, f := range roster {
		row := FriendRow{Name: f}
		if got, err := rows[i].Result(); err == nil && len(got) == 2 {
			row.At, row.Up = pipeValue(got[0]), pipeValue(got[1])
		}
		counts := []*string{&row.Ready, &row.Working, &row.Done}
		for j, c := range cells[i] {
			if n, err := c.Result(); err == nil {
				*counts[j] = strconv.FormatInt(n, 10)
			}
		}
		if n, err := doneAll[i].Result(); err == nil {
			snap.DoneAll[f] = n
		}
		if downs[i].Val() > 0 {
			row.Down = "down"
		}
		snap.Friends = append(snap.Friends, row)
	}
	return snap, false, nil
}

// doneScope is the done column's ZCOUNT min from one tick's reads of the
// sprint's start: opened_at (written once by sprint open), else the oldest
// card's created_at (the score of the first member of sprint:<S>:cards),
// else -inf. from names which one it is.
func doneScope(opened *redis.StringCmd, oldest *redis.ZSliceCmd) (since, from string) {
	if opened != nil {
		if v, err := opened.Result(); err == nil {
			if ms, err := strconv.ParseInt(v, 10, 64); err == nil {
				return strconv.FormatInt(ms, 10), "opened_at"
			}
		}
	}
	if oldest != nil {
		if zs, err := oldest.Result(); err == nil && len(zs) == 1 {
			return strconv.FormatInt(int64(zs[0].Score), 10), "oldest card"
		}
	}
	return "-inf", ""
}

// logWindowMax bounds the hour of ws:log one tick reads; a sprint moving more
// than this many tasks an hour shows at least this rate.
const logWindowMax = 5000

// FailedSprint is the tick whose read failed: last's rows (none when there
// never was a good read) and a stale line.
func FailedSprint(cfg SprintConfig, last *SprintSnapshot) *SprintSnapshot {
	if last == nil {
		return &SprintSnapshot{Config: cfg, Stale: true}
	}
	snap := *last
	snap.Stale = true
	return &snap
}

const streamRule = "-------------------------------+---------+-------+---------+---------+---------+-------\n"

// XY is the headline's numbers: y is every task in the streams of ws:order,
// left is y minus landed (a card in reading or merging is not done), eta is left over the landed rate of the last hour
// (at least 1 an hour, so a stall shows as a big number, never infinity).
func (s *SprintSnapshot) XY() (left, y, pct, eta int64) {
	var landed int64
	for _, r := range s.Streams {
		y += r.Total()
		landed += r.Landed
	}
	left = y - landed
	if y > 0 {
		pct = landed * 100 / y
	}
	rate := s.LandedHour
	if rate < 1 {
		rate = 1
	}
	return left, y, pct, left * 60 / rate
}

// Render prints the whole table at now.
func (s *SprintSnapshot) Render(now time.Time) string {
	var b strings.Builder
	b.Grow(4096)
	if s.Pitstop {
		b.WriteString("SPRINT TABLE *** PIT STOP ***\n\n")
	} else {
		b.WriteString("SPRINT TABLE\n\n")
	}
	if s.Stale && s.LastGood.IsZero() {
		b.WriteString("stale: never read (Redis did not answer since start)\n")
		return b.String()
	}
	left, y, pct, eta := s.XY()
	fmt.Fprintf(&b, "%d/%d left, %d%% done -> ~%dm\n\n", left, y, pct, eta)

	s.renderStreams(&b, now)

	fmt.Fprintf(&b, "%-10s | %5s | %7s | %5s | %-10s\n", "friend", "ready", "working", "done", "status")
	b.WriteString(liveFriendRule)
	var fq, fw, fd int64
	for _, row := range s.Friends {
		q, w, d := orDash(row.Ready), orDash(row.Working), orDash(s.friendDone(row))
		fq, fw, fd = fq+digitsOnly(q), fw+digitsOnly(w), fd+digitsOnly(d)
		status := "down"
		if friendState(row, now, s.Config.RowStale) == "up" {
			status = "up"
		}
		fmt.Fprintf(&b, "%-10s | %5s | %7s | %5s | %-10s\n", row.Name, q, w, d, status)
	}
	b.WriteString(liveFriendRule)
	fmt.Fprintf(&b, "%-10s | %5d | %7d | %5d |\n\n", "total", fq, fw, fd)

	fmt.Fprintf(&b, "%-10s | %5s | %7s | %5s | %5s | %5s | %4s | %6s\n", "host", "ready", "working", "done", "ok", "fail", "ok%", "load")
	b.WriteString(liveBenchRule)
	var hq, hw, hok, hfail int64
	unread := false
	for _, row := range s.Hosts {
		load := row.Load
		if row.Down {
			load = "down"
		}
		// done, ok, fail and ok% are the bench's ended cards this sprint
		// (#3894): done = ok + fail, ok% = ok / done.
		done, ok, fail, pct := strconv.FormatInt(row.Done(), 10), strconv.FormatInt(row.OK, 10), strconv.FormatInt(row.Fail, 10), okPct(row.OK, row.Done())
		if row.Unread {
			done, ok, fail, pct = "?", "?", "?", "?"
			unread = true
		}
		fmt.Fprintf(&b, "%-10s | %5d | %7d | %5s | %5s | %5s | %4s | %6s\n", row.Name, row.Ready, row.Working, done, ok, fail, pct, load)
		hq, hw, hok, hfail = hq+row.Ready, hw+row.Working, hok+row.OK, hfail+row.Fail
	}
	b.WriteString(liveBenchRule)
	td, tok, tfail, tpct := strconv.FormatInt(hok+hfail, 10), strconv.FormatInt(hok, 10), strconv.FormatInt(hfail, 10), okPct(hok, hok+hfail)
	if unread {
		td, tok, tfail, tpct = "?", "?", "?", "?"
	}
	fmt.Fprintf(&b, "%-10s | %5d | %7d | %5s | %5s | %5s | %4s |\n", "total", hq, hw, td, tok, tfail, tpct)
	if s.Stale {
		fmt.Fprintf(&b, "stale: %ds (Redis did not answer; rows are the last good read)\n", now.Unix()-s.LastGood.Unix())
	}
	return b.String()
}

// friendDone is the row's done count in the current sprint (#3883), and at
// most the cards done since the last `table clear`: a clear stores each
// friend's all-time count (DoneAll) as its base, so all-time minus base is
// what finished since the clear, and the smaller of the two is what finished
// since both the sprint opened and the clear. An all-time count below the base
// means the set itself restarted, so the base no longer applies.
func (s *SprintSnapshot) friendDone(row FriendRow) string {
	done, base := row.Done, s.DoneBase[row.Name]
	if done == "" || base == "" || strings.Trim(done, "0123456789") != "" || strings.Trim(base, "0123456789") != "" {
		return done
	}
	d, _ := strconv.ParseInt(done, 10, 64)
	b, _ := strconv.ParseInt(base, 10, 64)
	all, ok := s.DoneAll[row.Name]
	if !ok {
		all = d
	}
	if all < b || d <= all-b {
		return done
	}
	return strconv.FormatInt(all-b, 10)
}

// The writer's lock: one table writer per key, fleet-wide. AcquireLock takes
// it with SET NX PX; every tick then runs lockRefreshScript in its own
// pipeline, which extends the lease while the key still holds this writer's
// token, re-takes it if it expired, and returns 0 when someone else holds it.
const lockRefreshScript = `local v = redis.call('GET', KEYS[1])
if v == ARGV[1] then return redis.call('PEXPIRE', KEYS[1], ARGV[2]) end
if not v then redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2]); return 2 end
return 0`

const lockReleaseScript = `if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end
return 0`

func lockTTL(cfg SprintConfig) time.Duration {
	if cfg.LockTTL > 0 {
		return cfg.LockTTL
	}
	return 5 * time.Second
}

// AcquireLock takes key for token, or returns the holder's token.
func AcquireLock(ctx context.Context, client redis.UniversalClient, key, token string, ttl time.Duration) (holder string, err error) {
	ok, err := client.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return "", err
	}
	if ok {
		return token, nil
	}
	holder, err = client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return AcquireLock(ctx, client, key, token, ttl)
	}
	return holder, err
}

// ReleaseLock deletes key only while it still holds token.
func ReleaseLock(ctx context.Context, client redis.UniversalClient, key, token string) error {
	return client.Eval(ctx, lockReleaseScript, []string{key}, token).Err()
}

// renderStreams is the stream block and its LAND lines. Every cell is one
// set's ZCARD, ready and reading each their own column (a card is in exactly
// one set), except the read split: merging prints <read>/<unread> from the
// records until a reading set exists, then reading is the unread cards and
// merging the read ones. Both come from ReadSplit.
func (s *SprintSnapshot) renderStreams(b *strings.Builder, now time.Time) {
	sets := s.ReadSource == ReadFromSet
	fmt.Fprintf(b, "%-30s | %7s | %5s | %7s | %7s | %7s | %6s\n", "stream", "waiting", "ready", "working", "reading", "merging", "landed")
	b.WriteString(streamRule)
	var tot StreamRow
	var tread, tunread int64
	known := true
	for _, r := range s.Streams {
		if r.Total() == 0 {
			continue
		}
		read, unread, ok := s.ReadSplit(r)
		known = known && ok
		tread, tunread = tread+read, tunread+unread
		tot.Waiting, tot.Ready, tot.Working = tot.Waiting+r.Waiting, tot.Ready+r.Ready, tot.Working+r.Working
		tot.Reading, tot.Merging, tot.Landed = tot.Reading+r.Reading, tot.Merging+r.Merging, tot.Landed+r.Landed
		merging := strconv.FormatInt(r.Merging, 10)
		if !sets {
			merging = s.mergingCell(r)
		}
		fmt.Fprintf(b, "%-30s | %7d | %5d | %7d | %7d | %7s | %6d\n", r.Name, r.Waiting, r.Ready, r.Working, r.Reading, merging, r.Landed)
	}
	b.WriteString(streamRule)
	merging := strconv.FormatInt(tot.Merging, 10)
	if !sets && known {
		merging = fmt.Sprintf("%d/%d", tread, tunread)
	}
	fmt.Fprintf(b, "%-30s | %7d | %5d | %7d | %7d | %7s | %6d\n", "total", tot.Waiting, tot.Ready, tot.Working, tot.Reading, merging, tot.Landed)
	for _, l := range s.Landings {
		b.WriteString(l.LandLine(now))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}
