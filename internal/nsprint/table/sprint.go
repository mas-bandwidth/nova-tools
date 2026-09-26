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
//	stream | waiting | ready | working | review | merging | landed   (rows in ws:order, all-zero rows hidden, total)
//	REVIEW stream=<s> over=<n> oldest=<id> age=<d> max=<d>     (a stream with cards in review past cfg:review max_age, #4072)
//
//	consumer | ready | working | done | ok | fail | ok% | status | load   (one row per consumer, total)
//
// review is ZCARD ws:<s>:review: one state for every card past working that
// is not yet merging (Glenn 2026-09-26 8:20 AM ET, "let's do review": a card
// whose consumer copy failed and waits for its typed verdict (#4072), and a
// card whose PR waits for its read; the old reading set and the merging
// <read>/<unread> split (#3900, #3929) are gone). Every cell is one plain
// ZCARD; it is left, never done.
//
// The consumer table is ONE table (#4071, Glenn 2026-09-25 2:40 PM: "friends
// can fuck up cards too"): a row per consumer, friends and benches alike,
// named <kind>:<name> (friend:emma, bench:hetzner) so the kind shows. Every
// cell is one ZCARD of <kind>:<name>:cards:<set> for set = ready, working,
// ok, fail; done = ok + fail and ok% = ok / done are derived, never stored,
// with no sprint window and no base from a clear. The cells count consumer
// work only, by structure (nova-tools#4237): a record naming a probe is
// refused by every move into <kind>:<name>:cards:<set> (cm_zadd and the
// move gates in 02_card_move.lua), and a probe's result lives on the bench
// beat (bench:<b>:beat probe, fleet build), which this table never counts;
// after sprint clear and a fleet roll every row reads 0 | 0 | 0 | -.
// status is up when the
// consumer's own beat (<kind>:<name>:beat at, ms) is under a minute old and
// <kind>:<name>:down does not exist, else down (the row still shows its
// cards); an up consumer whose desired hash has paused 1 (worker pause,
// #4308) prints paused instead; an up bench whose last fleet play stopped
// (bench:<b>:play result failed:<role>, #4356) prints behind: <role>; load is the beat's cpu or load1 (a bench
// beat's, or a friend beat's, which `nova-sprint friend beat` measures on
// the machine the friend's session runs on, #4233; - when the beat has
// none). The old friend:<f> row hash and bench:<b> hash are never read, and
// no friend's load is read from another consumer's beat.
//
// Keys, every one read in ONE pipelined round trip per tick (a second round
// trip only on the tick a membership set changed; never KEYS, never SCAN):
//
//	ws:order                ZRANGE, the streams in rank order (the ws index, #3662)
//	ws:<s>:<state>          ZCARD for waiting, ready, working, review, merging, landed
//	ws:log                  XRANGE over the last hour: the landed rate for the ETA
//	friends, benches,       SMEMBERS: the consumers (friends the --friends roster
//	consumers               when given; then the benches; then any other
//	                        enrolled consumer), each once
//	<c>:cards:<set>         ZCARD for set = ready, working, ok, fail
//	<c>:beat                HMGET load1 at
//	<c>:down                EXISTS (a string or a hash; either means down)
//	<c>:desired             HGET paused (1: the status reads paused while up)
//	bench:<b>:play          HGET result (failed:<role>: the status reads behind: <role>)
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
	"github.com/mas-bandwidth/nova-tools/internal/gh"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
)

// WSStates are the six per-stream sets the table counts, in reply order:
// the stream line ws.Stream (Glenn 2026-09-26). review is where a card waits
// for a verdict or a read.
var WSStates = ws.Stream

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
}

// StreamRow is one stream's six counts, in WSStates order. Unread marks a
// cell whose ZCARD did not come back: it prints "?", never a false 0 (a 0
// there would also feed XY's left, pct and eta).
type StreamRow struct {
	Name                                             string
	Waiting, Ready, Working, Review, Merging, Landed int64
	Unread                                           [6]bool
}

// AnyUnread says a cell of the row did not come back.
func (r StreamRow) AnyUnread() bool {
	for _, u := range r.Unread {
		if u {
			return true
		}
	}
	return false
}

// Total is every task in the stream's six sets.
func (r StreamRow) Total() int64 {
	return r.Waiting + r.Ready + r.Working + r.Review + r.Merging + r.Landed
}

// SprintSnapshot is one tick's read.
type SprintSnapshot struct {
	Config SprintConfig
	// Epoch is the sprint epoch every cell of this snapshot was read under
	// (nova-tools#4238): sprint:epoch, read in the same pipeline as the
	// cells; a cell of another epoch is never shown.
	Epoch      uint64
	Pitstop    bool
	Streams    []StreamRow
	LandedHour int64 // ws:log moves to landed in the hour before the read
	GHHour     int64 // GitHub calls in the hour before the read (gh:calls:all, #4343)
	// Events is proc:progress as the progress duty wrote it (#4319): the
	// asks so far, the last EVENT line, and the last pass's duty refusals.
	// The table prints one EVENTS line from it only when non-zero.
	Events ProgressEvents
	// Consumers are the consumer table's rows, in display order (#4071).
	Consumers []ConsumerRow
	// RoundTrips is how many pipelines the read took: 1 in steady state.
	RoundTrips int
	// LockLost says the tick found the writer's lock held by someone else.
	LockLost bool
	// Stale is set when this tick's read failed; the rows are LastGood's.
	Stale    bool
	LastGood time.Time
	// Errors are the tick's partial failures (a pit stop key, the ws:log
	// window or the Studio beat that did not come back): the rows stand,
	// and each prints as one ERR line under the tables, never nothing.
	Errors []string
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
// the down key, Paused from the desired hash (worker pause, #4308), Load
// from the beat ("-" when it has none), Behind the role a bench's last
// fleet play stopped in (#4356; "" when it did not).
type ConsumerRow struct {
	Consumer
	Ready, Working, OK, Fail int64
	Unread                   [4]bool
	Up                       bool
	Paused                   bool
	Behind                   string
	Load                     string
}

// Status is the row's status cell: down, behind: <role> (up, its last
// fleet play stopped in role), paused (up and paused) or up.
func (r ConsumerRow) Status() string {
	switch {
	case !r.Up:
		return "down"
	case r.Behind != "":
		return "behind: " + r.Behind
	case r.Paused:
		return "paused"
	}
	return "up"
}

// BenchPlayKey is the bench's fleet play receipt, and PlayBehind the role
// its result says the play stopped in ("" for ok): the table's own spelling
// of fleetbuild.PlayKey and fleetbuild.BehindRole (#4356), since fleetbuild's
// tests import this package; TestPlayReceiptSpellingIsTheTables holds the
// two to one another.
func BenchPlayKey(bench string) string { return "bench:" + bench + ":play" }

// PlayBehind is the role a play receipt's result names after failed:.
func PlayBehind(result string) string {
	if role, ok := strings.CutPrefix(result, "failed:"); ok {
		return role
	}
	return ""
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

// loadValue is the row's load as a percent of every core: the beat's cpu
// (CPU busy percent over the beat interval, what Activity Monitor and top
// print; Glenn 2026-09-26 9:35 AM ET: "You are not yet normalizing by
// cores", beside an Activity Monitor at 50% while the table said 154%: a
// load average counts waiting threads) when the beat has one, else load1
// over ncpu. ok is false when the beat carries neither; raw is the plain
// load1 for a beat with no ncpu.
func loadValue(cpu, load1, ncpu string) (v float64, raw string, ok bool) {
	if v, err := strconv.ParseFloat(strings.TrimSpace(cpu), 64); err == nil && v >= 0 {
		return v, "", true
	}
	load := sanitize(load1)
	if load == "" {
		return 0, "", false
	}
	l, err := strconv.ParseFloat(load, 64)
	n, err2 := strconv.ParseFloat(strings.TrimSpace(ncpu), 64)
	if err != nil || err2 != nil || n <= 0 {
		return 0, load, true
	}
	return l / n * 100, "", true
}

// loadCell is loadValue printed for one tick, one decimal.
func loadCell(cpu, load1, ncpu string) string {
	v, raw, ok := loadValue(cpu, load1, ncpu)
	if !ok {
		return ""
	}
	if raw != "" {
		return raw
	}
	return fmt.Sprintf("%.1f%%", v)
}

// loadPercent is the load cell: the beat's load1 over its ncpu as a percent
// of every core busy, one decimal (Glenn 2026-09-26 8:33 AM ET: "normalize it
// so that 100% is total usage of all cores at 100%"; 8:38 AM: "show the load
// as 1.1%, we don't need 2 fractional values"). With no ncpu on the beat the
// raw load1 prints as before; with no load1, "".
func loadPercent(load1, ncpu string) string {
	load := sanitize(load1)
	if load == "" {
		return ""
	}
	l, err := strconv.ParseFloat(load, 64)
	n, err2 := strconv.ParseFloat(strings.TrimSpace(ncpu), 64)
	if err != nil || err2 != nil || n <= 0 {
		return load
	}
	return fmt.Sprintf("%.1f%%", l/n*100)
}

// hostBeatStale is how old a consumer beat's own at may be before its row
// prints down, for a beat key that outlived its TTL (a machine's load in
// lines.go reads the same bound).
const hostBeatStale = 60 * time.Second

// SprintReader reads the whole table, keeping set membership across ticks.
type SprintReader struct {
	Client                               redis.UniversalClient
	Config                               SprintConfig
	streams, benches, friends, consumers []string
	// sprints is sprint:order and openSprint its one member not closed:
	// the pit stop shown when Config.Sprint is empty.
	sprints    []string
	openSprint string
	// epoch is sprint:epoch as the last tick read it: the cells are keyed
	// by it, and a tick that reads another epoch is read once more, like a
	// membership change (nova-tools#4238)
	epoch  uint64
	primed bool
	// loads is each row's load samples of the last LoadWindow, newest last:
	// the cell prints the highest of them (Glenn 2026-09-26 10:52 AM ET: "The
	// CPU load updating every 1 sec is giving me anxiety. The way I usually
	// solve this is by having a 10 second sliding window, and showing the
	// highest value seen over the past 10 seconds").
	loads map[string][]loadSample
}

// LoadWindow is how long a load sample stays in a row's sliding window.
const LoadWindow = 10 * time.Second

type loadSample struct {
	at time.Time
	v  float64
}

// loadMax records v for row id at now and returns the highest sample of the
// last LoadWindow.
func (r *SprintReader) loadMax(id string, now time.Time, v float64) float64 {
	if r.loads == nil {
		r.loads = map[string][]loadSample{}
	}
	kept := r.loads[id][:0]
	for _, s := range r.loads[id] {
		if now.Sub(s.at) < LoadWindow {
			kept = append(kept, s)
		}
	}
	kept = append(kept, loadSample{at: now, v: v})
	r.loads[id] = kept
	max := v
	for _, s := range kept {
		if s.v > max {
			max = s.v
		}
	}
	return max
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
	// The pit stop is the named sprint's, else the one open sprint's (Glenn
	// 2026-09-26 8:50 AM ET: "There is only one sprint active at a current
	// time"): sprint:order and each member's state are read every tick in
	// the same pipeline, and the open one's stop key with them on the next.
	var sprintOrder *redis.StringSliceCmd
	var states []*redis.StringCmd
	pitSprint := cfg.Sprint
	if pitSprint == "" {
		sprintOrder = pipe.ZRange(ctx, "sprint:order", 0, -1)
		states = make([]*redis.StringCmd, len(r.sprints))
		for i, name := range r.sprints {
			// the sprint's status field on s:<S> (what sprint open, close
			// and pitstop.read use): "closed" is out, anything else is open
			states[i] = pipe.HGet(ctx, "s:"+name, "status")
		}
		pitSprint = r.openSprint
	}
	var pit *redis.IntCmd
	if pitSprint != "" {
		pit = pipe.Exists(ctx, "s:"+pitSprint+":pitstop", "sprint:"+pitSprint+":pitstop")
	}
	hourAgo := now.Add(-time.Hour).UnixMilli()
	log := pipe.XRangeN(ctx, "ws:log", strconv.FormatInt(hourAgo, 10), "+", logWindowMax)
	ghAll := pipe.HGetAll(ctx, gh.TotalKey)
	var lock *redis.Cmd
	if cfg.LockKey != "" {
		lock = pipe.Eval(ctx, lockRefreshScript, []string{cfg.LockKey}, cfg.LockToken, lockTTL(cfg).Milliseconds())
	}
	// Each cell is the set's cards: the ZCARD less the stream's sentinel
	// when it is in that set (ws.QueueCardCount, #4318: the stop is not a
	// card, no new column).
	counts := make([][]*ws.CardCountCmd, len(r.streams))
	for i, s := range r.streams {
		for _, state := range WSStates {
			counts[i] = append(counts[i], ws.QueueCardCount(ctx, pipe, r.epoch, s, state))
		}
	}
	type consumerCmds struct {
		cells  [4]*redis.IntCmd
		beat   *redis.SliceCmd
		down   *redis.IntCmd
		paused *redis.StringCmd
		play   *redis.StringCmd // a bench's last fleet play result (#4356)
	}
	roster := r.roster()
	cmds := make([]consumerCmds, len(roster))
	for i, c := range roster {
		for j, set := range ConsumerSets {
			cmds[i].cells[j] = pipe.ZCard(ctx, ws.ConsumerKeyAt(r.epoch, c.ID(), set))
		}
		cmds[i].beat = pipe.HMGet(ctx, c.ID()+":beat", "load1", "at", "ncpu", "cpu")
		cmds[i].down = pipe.Exists(ctx, c.ID()+":down")
		cmds[i].paused = pipe.HGet(ctx, c.ID()+":desired", "paused")
		if c.Kind == "bench" {
			cmds[i].play = pipe.HGet(ctx, BenchPlayKey(c.Name), "result")
		}
	}
	progress := pipe.HGetAll(ctx, ProgressKey)
	// THE EPOCH (nova-tools#4238): read once per tick, in the same pipeline
	// and AFTER every cell: the cells are keyed by the epoch of the last
	// tick, and a clear that lands anywhere before this read (before or
	// between the cells) shows here as another epoch, so the tick is read
	// again with the new one and never shows a frame of the old epoch's
	// cells after the clear.
	epochCmd := pipe.HGet(ctx, ws.EpochKey, ws.EpochField)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) && !isReplyError(err) {
		return nil, false, fmt.Errorf("pipeline: %w", err)
	}
	// A dead connection fails every command; the order read standing is the
	// tick's proof that Redis answered.
	gotOrder, err := order.Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, false, fmt.Errorf("zrange ws:order: %w", err)
	}
	// An epoch that could not be read is not epoch 0: the tick fails
	// rather than show another epoch's cells.
	epochVal, err := epochCmd.Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, false, fmt.Errorf("hget %s %s: %w", ws.EpochKey, ws.EpochField, err)
	}
	gotEpoch, err := ws.ParseEpoch(epochVal)
	if err != nil {
		return nil, false, err
	}
	// A membership set that did not come back would empty its rows without
	// a word: the tick fails instead, and the loop publishes the last good
	// rows with the stale line naming the read.
	sorted := func(name string, c *redis.StringSliceCmd) ([]string, error) {
		got, err := c.Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return nil, fmt.Errorf("smembers %s: %w", name, err)
		}
		sort.Strings(got)
		return got, nil
	}
	gotBenches, err := sorted("benches", benchSet)
	if err != nil {
		return nil, false, err
	}
	gotConsumers, err := sorted("consumers", consumerSet)
	if err != nil {
		return nil, false, err
	}
	var gotFriends []string
	if friendSet != nil {
		if gotFriends, err = sorted("friends", friendSet); err != nil {
			return nil, false, err
		}
	}
	var gotSprints []string
	open := ""
	if sprintOrder != nil {
		gotSprints = sprintOrder.Val()
		for i, name := range r.sprints {
			if i < len(states) && states[i] != nil && states[i].Err() == nil && states[i].Val() != "closed" {
				open = name
			}
		}
	}
	changed := !r.primed || !slices.Equal(gotOrder, r.streams) || !slices.Equal(gotBenches, r.benches) ||
		!slices.Equal(gotConsumers, r.consumers) || (friendSet != nil && !slices.Equal(gotFriends, r.friends)) ||
		!slices.Equal(gotSprints, r.sprints) || open != r.openSprint || gotEpoch != r.epoch
	r.primed = true
	if changed {
		r.streams, r.benches, r.consumers, r.friends = gotOrder, gotBenches, gotConsumers, gotFriends
		r.sprints, r.openSprint, r.epoch = gotSprints, open, gotEpoch
		return nil, true, nil
	}

	snap := &SprintSnapshot{Config: cfg, Epoch: gotEpoch}
	if pit != nil {
		if n, err := pit.Result(); err != nil && !errors.Is(err, redis.Nil) {
			// A stop that could not be read is not "no stop".
			snap.Errors = append(snap.Errors, "pit stop s:"+pitSprint+":pitstop not read: "+err.Error())
		} else if n > 0 {
			snap.Pitstop = true
		}
	}
	if msgs, err := log.Result(); err != nil && !errors.Is(err, redis.Nil) {
		// The ETA rate would read as 1 an hour with no word.
		snap.Errors = append(snap.Errors, "ws:log not read (eta rate unknown): "+err.Error())
	} else {
		for _, m := range msgs {
			id, _ := m.Values["id"].(string)
			if to, _ := m.Values["to"].(string); to == "landed" && !ws.IsSentinel(id) {
				snap.LandedHour++ // a stream's stop landing is not a card landed
			}
		}
	}
	if m, err := ghAll.Result(); err == nil {
		snap.GHHour = gh.HourOf(m, now)
	}
	if lock != nil {
		if n, err := lock.Int64(); err != nil || n == 0 {
			snap.LockLost = true
		}
	}
	if h, err := progress.Result(); err != nil && !errors.Is(err, redis.Nil) {
		// An EVENTS line that could not be read is said, never a quiet none.
		snap.Events = ProgressEvents{Unread: err.Error()}
	} else {
		snap.Events = ParseProgressEvents(h)
	}
	for i, s := range r.streams {
		row := StreamRow{Name: s}
		cells := []*int64{&row.Waiting, &row.Ready, &row.Working, &row.Review, &row.Merging, &row.Landed}
		for j, c := range counts[i] {
			n, err := c.Result()
			*cells[j], row.Unread[j] = n, err != nil && !errors.Is(err, redis.Nil)
		}
		snap.Streams = append(snap.Streams, row)
	}
	for i, c := range roster {
		row := ConsumerRow{Consumer: c, Load: "-"}
		cells := []*int64{&row.Ready, &row.Working, &row.OK, &row.Fail}
		for j, cmd := range cmds[i].cells {
			n, err := cmd.Result()
			*cells[j], row.Unread[j] = n, err != nil
		}
		if got, err := cmds[i].beat.Result(); err == nil && len(got) == 4 {
			// A friend's load is its own beat's too (#4233: friend beat
			// measures the machine the friend's session runs on); the
			// hardcoded bench:studio:beat fallback of 2026-09-25 is gone.
			v, raw, ok := loadValue(pipeValue(got[3]), pipeValue(got[0]), pipeValue(got[2]))
			switch {
			case ok && raw != "":
				row.Load = raw
			case ok:
				// the highest of the last LoadWindow, so the cell holds still
				row.Load = fmt.Sprintf("%.1f%%", r.loadMax(c.ID(), now, v))
			}
			if atMS, err := strconv.ParseInt(pipeValue(got[1]), 10, 64); err == nil && now.UnixMilli()-atMS <= hostBeatStale.Milliseconds() {
				row.Up = true
			}
		}
		if n, err := cmds[i].down.Result(); err != nil || n > 0 {
			row.Up = false
		}
		if v, err := cmds[i].paused.Result(); err == nil && v == "1" {
			row.Paused = true
		}
		if cmds[i].play != nil {
			if v, err := cmds[i].play.Result(); err == nil {
				row.Behind = PlayBehind(v)
			}
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

const streamRule = "--------------------------+---------+-------+---------+--------+---------+-------\n"

// XY is the headline's numbers: y is every task in the streams of ws:order,
// left is y minus landed (a card in review or merging is not done),
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
	// With no task in the sprint the headline is hidden too (Glenn
	// 2026-09-26 10:01 AM ET: "when there are 0 tasks in the current sprint,
	// you can hide the status x/y z% etc. make sure there is not an extra
	// newline left"): the title's blank line is then the gap before the
	// worker table.
	if left, y, pct, eta := s.XY(); y > 0 {
		fmt.Fprintf(&b, "%d/%d left, %d%% done -> ~%dm gh %d/h\n\n", left, y, pct, eta, s.GHHour)
	}

	s.renderStreams(&b)
	writeConsumerTable(&b, s.Consumers)
	if line := s.Events.Line(); line != "" {
		// One EVENTS line, only when there is one to show (#4319 item 4:
		// less is more).
		b.WriteString(line + "\n")
	}
	if s.Stale {
		fmt.Fprintf(&b, "stale: %ds (Redis did not answer; rows are the last good read)\n", now.Unix()-s.LastGood.Unix())
	}
	for _, e := range s.Errors {
		// One line per partial failure of the tick: the screen says what it
		// could not read rather than showing a quiet default.
		b.WriteString("ERR " + strings.Join(strings.Fields(e), " ") + "\n")
	}
	return b.String()
}

// consumerRule is the consumer table's rule line.
const consumerRule = "--------------------------+-------+---------+-------+------+--------+------\n"

// writeConsumerTable is the one consumer table (#4071): consumer | ready |
// working | done | ok% | status | load, then a total row whose ok% is
// derived from the totals. done is ok + fail as one number and ok% is
// ok over done (Glenn 2026-09-26 9:12 AM ET: the ok and fail columns
// folded away, "display only"; 9:20 AM: "done x/y is giving me clutter
// vibes. can we just make it one scalar 'done' again. the ok% is enough to
// see the rest"): the ok and fail sets are still read and counted apart. A cell whose ZCARD did
// not come back prints "?" (and so do done and ok% when ok or fail is
// one), never a false 0.
func writeConsumerTable(b *strings.Builder, rows []ConsumerRow) {
	// The header says worker (Glenn 2026-09-26 9:58 AM ET: "rename consumer to
	// worker, just in the display tables"); the record and the keys stay
	// consumer.
	fmt.Fprintf(b, "%-25s | %5s | %7s | %5s | %4s | %-6s | %s\n", "worker", "ready", "working", "done",
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
		fmt.Fprintf(b, "%-25s | %5s | %7s | %5s | %4s | %-6s | %s\n", name, cell[0], cell[1], done, pct, r.Status(), r.Load)
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
	fmt.Fprintf(b, "%-25s | %5s | %7s | %5s | %4s |\n", "total", cell[0], cell[1], done, pct)
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

// renderStreams is the stream block: one plain ZCARD per cell, every card in
// exactly one set. No LAND line (#4088), no REVIEW line (Glenn 2026-09-26
// 8:03 AM ET), no <read>/<unread> split (Glenn 2026-09-26 8:22 AM ET, "Let's
// remove it, and use review as that state").
func (s *SprintSnapshot) renderStreams(b *strings.Builder) {
	// An empty stream table is hidden (Glenn 2026-09-26 10:00 AM ET: "when
	// the work sprint table has no sprints in it, you can hide it. make sure
	// there is not an extra newline when it's hidden"): the headline's blank
	// line is the only gap before the worker table.
	any := false
	for _, r := range s.Streams {
		if r.Total() != 0 || r.AnyUnread() {
			any = true
			break
		}
	}
	if !any {
		return
	}
	fmt.Fprintf(b, "%-25s | %7s | %5s | %7s | %6s | %7s | %6s\n", "stream", "waiting", "ready", "working", "review",
		"merging", "landed")
	b.WriteString(streamRule)
	var tot StreamRow
	for _, r := range s.Streams {
		if r.Total() == 0 && !r.AnyUnread() {
			continue
		}
		vals := [6]int64{r.Waiting, r.Ready, r.Working, r.Review, r.Merging, r.Landed}
		tots := []*int64{&tot.Waiting, &tot.Ready, &tot.Working, &tot.Review, &tot.Merging, &tot.Landed}
		var cell [6]string
		for j, v := range vals {
			*tots[j] += v
			cell[j] = strconv.FormatInt(v, 10)
			if r.Unread[j] {
				// A ZCARD that did not come back prints "?", never a false 0.
				cell[j], tot.Unread[j] = "?", true
			}
		}
		fmt.Fprintf(b, "%-25s | %7s | %5s | %7s | %6s | %7s | %6s\n", r.Name, cell[0], cell[1], cell[2], cell[3], cell[4], cell[5])
	}
	b.WriteString(streamRule)
	totVals := [6]int64{tot.Waiting, tot.Ready, tot.Working, tot.Review, tot.Merging, tot.Landed}
	var cell [6]string
	for j, v := range totVals {
		cell[j] = strconv.FormatInt(v, 10)
		if tot.Unread[j] {
			cell[j] = "?"
		}
	}
	fmt.Fprintf(b, "%-25s | %7s | %5s | %7s | %6s | %7s | %6s\n", "total", cell[0], cell[1], cell[2], cell[3], cell[4], cell[5])
	b.WriteByte('\n')
}
