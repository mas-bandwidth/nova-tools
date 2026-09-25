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
//	stream | waiting | ready | working | review | reading | merging | landed   (rows in ws:order, all-zero rows hidden, total)
//	REVIEW stream=<s> over=<n> oldest=<id> age=<d> max=<d>     (a stream with cards in review past cfg:review max_age, #4072)
//
//	consumer | ready | working | done | ok | fail | ok% | status | load   (one row per consumer, total)
//
// review is ZCARD ws:<s>:review (#4072): a card whose consumer copy failed,
// waiting for its typed verdict (nova-sprint review post); it is left, never
// done. reading is ZCARD ws:<s>:reading (#3929). merging prints
// <read>/<unread> (merging.go, #3900) until a ws:<s>:reading set exists; from
// then on the unread cards are the reading column and merging is the read
// cards alone: the same ReadSplit, its other source.
//
// The consumer table is ONE table (#4071, Glenn 2026-09-25 2:40 PM: "friends
// can fuck up cards too"): a row per consumer, friends and benches alike,
// named <kind>:<name> (friend:emma, bench:hetzner) so the kind shows. Every
// cell is one ZCARD of <kind>:<name>:cards:<set> for set = ready, working,
// ok, fail; done = ok + fail and ok% = ok / done are derived, never stored,
// with no sprint window and no base from a clear. status is up when the
// consumer's own beat (<kind>:<name>:beat at, ms) is under a minute old and
// <kind>:<name>:down does not exist, else down (the row still shows its
// cards); load is the beat's load1 (a bench's; - when the beat has none).
// The old friend:<f> row hash and bench:<b> hash are never read.
//
// Keys, every one read in ONE pipelined round trip per tick (a second round
// trip only on the tick a membership set changed; never KEYS, never SCAN):
//
//	ws:order                ZRANGE, the streams in rank order (the ws index, #3662)
//	ws:<s>:<state>          ZCARD for waiting, ready, working, review, reading, merging, landed
//	EVAL_RO detailScript    read only: ws:<s>:merging with each card's pr:<name>:<n>
//	                        head/state/stream/reads, cfg:land, every
//	                        land:<repo>:<slug> of land:<repo>:streams with its PR's
//	                        ci, and cfg:review max_age with every ws:<s>:review
//	                        card's review_at
//	ws:log                  XRANGE over the last hour: the landed rate for the ETA
//	friends, benches,       SMEMBERS: the consumers (friends the --friends roster
//	consumers               when given; then the benches; then any other
//	                        enrolled consumer), each once
//	<c>:cards:<set>         ZCARD for set = ready, working, ok, fail
//	<c>:beat                HMGET load1 at
//	<c>:down                EXISTS (a string or a hash; either means down)
//	s:<S>:pitstop           EXISTS, with the legacy sprint:<S>:pitstop
//
// The membership of ws:order, friends, benches and consumers is kept from
// the previous tick, so the tick's one pipeline reads the sets and every
// member's values together; a tick that finds a set changed reads again at
// once with the new ones, so a row is never rendered from last second's
// membership.
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

// WSStates are the seven per-stream sets the table counts, in reply order.
// review sits between working and reading (#4072: a failed card waits there
// for its verdict); reading between working and merging (Glenn 2026-09-25:
// "split merging into separate reading | merging columns"): a card whose PR
// is being read.
var WSStates = []string{"waiting", "ready", "working", "review", "reading", "merging", "landed"}

// ConsumerSets are the four <kind>:<name>:cards:<set> sets a consumer row
// counts, in column order (done = ok + fail is derived).
var ConsumerSets = []string{"ready", "working", "ok", "fail"}

// SprintConfig is what the whole table is told; everything else is read.
type SprintConfig struct {
	// Sprint names the pit stop keys s:<Sprint>:pitstop and
	// sprint:<Sprint>:pitstop; empty reads no pit stop.
	Sprint string
	// Friends is the friend rows' order; empty reads the friends SET,
	// sorted by name.
	Friends []string
	// LockKey/LockToken, when set, refresh the writer's lock in the tick's
	// own pipeline (see AcquireLock); the read reports LockLost when it is gone.
	LockKey, LockToken string
	LockTTL            time.Duration
	// ReadingSet takes the merging split from ws:<s>:reading (#3929) even
	// before any card is in one: merging prints the read count alone, not
	// <read>/<unread>. Without it the switch happens on the first tick that
	// finds a reading set non-empty and stays. The reading column is always
	// printed.
	ReadingSet bool
}

// StreamRow is one stream's seven counts, in WSStates order.
type StreamRow struct {
	Name                                                      string
	Waiting, Ready, Working, Review, Reading, Merging, Landed int64
	// MergingRead is how many cards of merging the lander would take (a
	// read at head >= cfg:land, no hold), from the PR records; -1 unknown.
	MergingRead int64
}

// Total is every task in the stream's seven sets.
func (r StreamRow) Total() int64 {
	return r.Waiting + r.Ready + r.Working + r.Review + r.Reading + r.Merging + r.Landed
}

// SprintSnapshot is one tick's read.
type SprintSnapshot struct {
	Config     SprintConfig
	Pitstop    bool
	Streams    []StreamRow
	LandedHour int64 // ws:log moves to landed in the hour before the read
	// Consumers are the consumer table's rows, in display order (#4071).
	Consumers []ConsumerRow
	// Landings are the open landings, one LAND line each (#3900).
	Landings []LandRow
	// Reviews are the streams with a card in review past cfg:review
	// max_age, one REVIEW line each (#4072).
	Reviews []ReviewBound
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

// Consumer is one consumer of copies: <Kind>:<Name>, bench or friend.
type Consumer struct{ Kind, Name string }

// ID is the consumer's key prefix and row name, <kind>:<name>.
func (c Consumer) ID() string { return c.Kind + ":" + c.Name }

// parseConsumer reads bench:<b> or friend:<f>.
func parseConsumer(s string) (Consumer, bool) {
	kind, name, ok := strings.Cut(s, ":")
	if !ok || name == "" || strings.ContainsAny(name, ": \t") || (kind != "bench" && kind != "friend") {
		return Consumer{}, false
	}
	return Consumer{Kind: kind, Name: name}, true
}

// ConsumerRow is one row of the consumer table (#4071): the four ZCARDs of
// <kind>:<name>:cards:ready|working|ok|fail, Unread per cell when its ZCARD
// did not come back (it prints "?", never a false 0), Up from the beat and
// the down key, Load from the beat ("-" when it has none).
type ConsumerRow struct {
	Consumer
	Ready, Working, OK, Fail int64
	Unread                   [4]bool
	Up                       bool
	Load                     string
}

// Done is every copy that ended on the consumer: ok plus fail.
func (r ConsumerRow) Done() int64 { return r.OK + r.Fail }

// okPct is ok over done as a whole percent, "-" while nothing is done.
func okPct(ok, done int64) string {
	if done == 0 {
		return "-"
	}
	return strconv.FormatInt(100*ok/done, 10) + "%"
}

// FriendHostBeatKey is the beat of the machine every friend lives on today
// (the Studio; hardcoded, see readOnce).
const FriendHostBeatKey = "bench:studio:beat"

// hostBeatStale is how old a consumer beat's own at may be before its row
// prints down, for a beat key that outlived its TTL (a machine's load in
// lines.go reads the same bound).
const hostBeatStale = 60 * time.Second

// SprintReader reads the whole table, keeping set membership across ticks.
type SprintReader struct {
	Client                               redis.UniversalClient
	Config                               SprintConfig
	streams, benches, friends, consumers []string
	primed                               bool
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
	// Three: a first tick learns the sets, a set that changes under it is
	// read once more.
	for trips := 1; trips <= 3; trips++ {
		snap, changed, err := r.readOnce(ctx, now)
		if err != nil {
			return nil, err
		}
		if !changed {
			snap.RoundTrips = trips
			return snap, nil
		}
	}
	return nil, errors.New("membership changed on three reads in a row")
}

// roster is the consumer rows in display order: the friends (the configured
// order, else the friends SET sorted), then the benches, then any other
// member of the consumers SET, each once.
func (r *SprintReader) roster() []Consumer {
	friends := r.Config.Friends
	if len(friends) == 0 {
		friends = r.friends
	}
	var out []Consumer
	seen := map[string]bool{}
	add := func(c Consumer) {
		if !seen[c.ID()] {
			seen[c.ID()] = true
			out = append(out, c)
		}
	}
	for _, f := range friends {
		add(Consumer{Kind: "friend", Name: f})
	}
	for _, b := range r.benches {
		add(Consumer{Kind: "bench", Name: b})
	}
	for _, s := range r.consumers {
		if c, ok := parseConsumer(s); ok {
			add(c)
		}
	}
	return out
}

func (r *SprintReader) readOnce(ctx context.Context, now time.Time) (*SprintSnapshot, bool, error) {
	cfg := r.Config
	pipe := r.Client.Pipeline()
	order := pipe.ZRange(ctx, "ws:order", 0, -1)
	benchSet := pipe.SMembers(ctx, "benches")
	consumerSet := pipe.SMembers(ctx, "consumers")
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
	detail := pipe.EvalRO(ctx, detailScript, nil, detailArgs(fromRecords, repos, r.streams)...)
	type consumerCmds struct {
		cells [4]*redis.IntCmd
		beat  *redis.SliceCmd
		down  *redis.IntCmd
	}
	roster := r.roster()
	cmds := make([]consumerCmds, len(roster))
	for i, c := range roster {
		for j, set := range ConsumerSets {
			cmds[i].cells[j] = pipe.ZCard(ctx, c.ID()+":cards:"+set)
		}
		cmds[i].beat = pipe.HMGet(ctx, c.ID()+":beat", "load1", "at")
		cmds[i].down = pipe.Exists(ctx, c.ID()+":down")
	}
	// HARDCODED (Glenn 2026-09-25 11:25 PM ET, "everybody is on studio"): a
	// friend's load is the load of the machine it lives on, and today every
	// friend lives on the Studio, so friend rows show bench:studio:beat's
	// load1. The proper fix is the friend beat carrying its own machine's
	// load1 like the bench beat does; then this read goes.
	studio := pipe.HMGet(ctx, FriendHostBeatKey, "load1")
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) && !isReplyError(err) {
		return nil, false, fmt.Errorf("pipeline: %w", err)
	}
	// A dead connection fails every command; the order read standing is the
	// tick's proof that Redis answered.
	gotOrder, err := order.Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, false, fmt.Errorf("zrange ws:order: %w", err)
	}
	sorted := func(c *redis.StringSliceCmd) []string {
		got, _ := c.Result()
		sort.Strings(got)
		return got
	}
	gotBenches, gotConsumers := sorted(benchSet), sorted(consumerSet)
	var gotFriends []string
	if friendSet != nil {
		gotFriends = sorted(friendSet)
	}
	changed := !r.primed || !slices.Equal(gotOrder, r.streams) || !slices.Equal(gotBenches, r.benches) ||
		!slices.Equal(gotConsumers, r.consumers) || (friendSet != nil && !slices.Equal(gotFriends, r.friends))
	r.primed = true
	if changed {
		r.streams, r.benches, r.consumers, r.friends = gotOrder, gotBenches, gotConsumers, gotFriends
		return nil, true, nil
	}

	snap := &SprintSnapshot{Config: cfg}
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
	if lock != nil {
		if n, err := lock.Int64(); err != nil || n == 0 {
			snap.LockLost = true
		}
	}
	for i, s := range r.streams {
		row := StreamRow{Name: s, MergingRead: -1}
		cells := []*int64{&row.Waiting, &row.Ready, &row.Working, &row.Review, &row.Reading, &row.Merging, &row.Landed}
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
		applyDetail(snap, v, fromRecords, repos, r.streams, now)
	}
	for i, c := range roster {
		row := ConsumerRow{Consumer: c, Load: "-"}
		cells := []*int64{&row.Ready, &row.Working, &row.OK, &row.Fail}
		for j, cmd := range cmds[i].cells {
			n, err := cmd.Result()
			*cells[j], row.Unread[j] = n, err != nil
		}
		if got, err := cmds[i].beat.Result(); err == nil && len(got) == 2 {
			if load := sanitize(pipeValue(got[0])); load != "" {
				row.Load = load
			}
			if c.Kind == "friend" && row.Load == "-" {
				if got, err := studio.Result(); err == nil && len(got) == 1 {
					if load := sanitize(pipeValue(got[0])); load != "" {
						row.Load = load
					}
				}
			}
			if atMS, err := strconv.ParseInt(pipeValue(got[1]), 10, 64); err == nil && now.UnixMilli()-atMS <= hostBeatStale.Milliseconds() {
				row.Up = true
			}
		}
		if n, err := cmds[i].down.Result(); err != nil || n > 0 {
			row.Up = false
		}
		snap.Consumers = append(snap.Consumers, row)
	}
	return snap, false, nil
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

const streamRule = "-------------------------------+---------+-------+---------+--------+---------+---------+-------\n"

// XY is the headline's numbers: y is every task in the streams of ws:order,
// left is y minus landed (a card in review, reading or merging is not done),
// eta is left over the landed rate of the last hour (at least 1 an hour, so
// a stall shows as a big number, never infinity).
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
	writeConsumerTable(&b, s.Consumers)
	if s.Stale {
		fmt.Fprintf(&b, "stale: %ds (Redis did not answer; rows are the last good read)\n", now.Unix()-s.LastGood.Unix())
	}
	return b.String()
}

// consumerRule is the consumer table's rule line.
const consumerRule = "---------------------+-------+---------+-------+-------+-------+------+--------+------\n"

// writeConsumerTable is the one consumer table (#4071): consumer | ready |
// working | done | ok | fail | ok% | status | load, then a total row whose
// ok% is derived from the totals. A cell whose ZCARD did not come back
// prints "?" (and so do done and ok% when ok or fail is one), never a false 0.
func writeConsumerTable(b *strings.Builder, rows []ConsumerRow) {
	fmt.Fprintf(b, "%-20s | %5s | %7s | %5s | %5s | %5s | %4s | %-6s | %s\n", "consumer", "ready", "working", "done", "ok", "fail",
		"ok%", "status", "load")
	b.WriteString(consumerRule)
	// The row is the name alone (Glenn 2026-09-25 11:20 PM ET: the friend:/bench:
	// prefix is clutter); the kind shows only when a friend and a bench share
	// a name, so the two rows stay apart.
	kinds := map[string]int{}
	for _, r := range rows {
		kinds[r.Name] |= map[string]int{"friend": 1, "bench": 2}[r.Kind]
	}
	var tot [4]int64
	var unread [4]bool
	for _, r := range rows {
		name := r.Name
		if kinds[r.Name] == 3 {
			name = r.ID()
		}
		cell := [4]string{}
		for j, n := range []int64{r.Ready, r.Working, r.OK, r.Fail} {
			cell[j] = strconv.FormatInt(n, 10)
			if r.Unread[j] {
				cell[j] = "?"
				unread[j] = true
			}
			tot[j] += n
		}
		done, pct := strconv.FormatInt(r.Done(), 10), okPct(r.OK, r.Done())
		if r.Unread[2] || r.Unread[3] {
			done, pct = "?", "?"
		}
		status := "down"
		if r.Up {
			status = "up"
		}
		fmt.Fprintf(b, "%-20s | %5s | %7s | %5s | %5s | %5s | %4s | %-6s | %s\n", name, cell[0], cell[1], done, cell[2], cell[3],
			pct, status, r.Load)
	}
	b.WriteString(consumerRule)
	cell := [4]string{}
	for j := range tot {
		cell[j] = strconv.FormatInt(tot[j], 10)
		if unread[j] {
			cell[j] = "?"
		}
	}
	done, pct := strconv.FormatInt(tot[2]+tot[3], 10), okPct(tot[2], tot[2]+tot[3])
	if unread[2] || unread[3] {
		done, pct = "?", "?"
	}
	fmt.Fprintf(b, "%-20s | %5s | %7s | %5s | %5s | %5s | %4s |\n", "total", cell[0], cell[1], done, cell[2], cell[3], pct)
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

// renderStreams is the stream block, its LAND lines and its REVIEW lines.
// Every cell is one set's ZCARD, ready, review and reading each their own
// column (a card is in exactly one set), except the read split: merging
// prints <read>/<unread> from the records until a reading set exists, then
// reading is the unread cards and merging the read ones. Both come from
// ReadSplit.
func (s *SprintSnapshot) renderStreams(b *strings.Builder, now time.Time) {
	sets := s.ReadSource == ReadFromSet
	fmt.Fprintf(b, "%-30s | %7s | %5s | %7s | %6s | %7s | %7s | %6s\n", "stream", "waiting", "ready", "working", "review", "reading",
		"merging", "landed")
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
		tot.Waiting, tot.Ready, tot.Working, tot.Review = tot.Waiting+r.Waiting, tot.Ready+r.Ready, tot.Working+r.Working, tot.Review+r.Review
		tot.Reading, tot.Merging, tot.Landed = tot.Reading+r.Reading, tot.Merging+r.Merging, tot.Landed+r.Landed
		merging := strconv.FormatInt(r.Merging, 10)
		if !sets {
			merging = s.mergingCell(r)
		}
		fmt.Fprintf(b, "%-30s | %7d | %5d | %7d | %6d | %7d | %7s | %6d\n", r.Name, r.Waiting, r.Ready, r.Working, r.Review, r.Reading,
			merging, r.Landed)
	}
	b.WriteString(streamRule)
	merging := strconv.FormatInt(tot.Merging, 10)
	if !sets && known {
		merging = fmt.Sprintf("%d/%d", tread, tunread)
	}
	fmt.Fprintf(b, "%-30s | %7d | %5d | %7d | %6d | %7d | %7s | %6d\n", "total", tot.Waiting, tot.Ready, tot.Working, tot.Review,
		tot.Reading, merging, tot.Landed)
	// No LAND line (#4088): stream status carries the landings.
	for _, r := range s.Reviews {
		b.WriteString(r.Line())
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}
