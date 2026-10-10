package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The where record (docs/SPEC-SPRINT.md section 1): what where shows that the
// work table's count cells do not carry, the held cards and the landings of
// the last hour, counted by the tick from the sprint it reads anyway and kept
// in one key, so where reads no card (the owner's requirement of 2026-09-30,
// one read a tick, and where under 1 s at 3,000 cards: at 2,843 it read every
// waiting and landed card's record, 5 to 7 s on the live store, 2026-10-02).
//
// Held is not a count a step can keep by adding one: a card is held when it
// waits, through a need or its place in line, on a sentinel not released or a
// card admitted held, so one release or one need frees or holds a chain. The
// tick counts it whole (sprint.HeldBack) from its twin, which holds every card,
// whenever the work table or the fleet table has moved since the record was counted.

// keyWhere is the where record's key, under the deployment's prefix.
const keyWhere = "where" // STRING, the where record (JSON)

// WhereRecord is the where record: the epoch and the work table's revision it
// was counted at, the held cards there (sprint.HeldBack), and the landings
// LandingRate may count from then on (sprint.RecentLandings), in Unix seconds,
// oldest first.
type WhereRecord struct {
	Epoch    uint64                `json:"epoch"`
	Rev      uint64                `json:"rev"`
	Held     int                   `json:"held"`
	Landings []int64               `json:"landings,omitempty"`
	Critical []sprint.CriticalCard `json:"critical,omitempty"` // the five heaviest (weight.go)
	// Tiers counts every card by its brief's tier, and Streams carries each stream's
	// tiers, dollars per landed card and spend by tier (sprint.TierCosts, cost_view.go):
	// counted here from the cards the tick reads, never by where from per-card reads.
	Tiers   map[string]int              `json:"tiers,omitempty"`
	Streams map[string]sprint.TierCosts `json:"streams,omitempty"`
	// StageTimes is the median and p90 of each stage over the cards landed in the last day
	// (sprint.CycleTimes, docs/SPEC-SPRINT.md, cycle-time-breakdownb.w1), as of the count.
	StageTimes sprint.StageTimes `json:"stage_times,omitzero"`
	// DealtFleet is each friend's count of the fleet's cards on her row
	// (sprint.FriendsDealtFleet), where --json's dealt_fleet.
	DealtFleet map[string]int `json:"dealt_fleet,omitempty"`
	// ReadsWaiting is the reads wanted now and not asked over the primaries in review
	// (sprint.ReadsWaiting), Priorities every open primary whose level is not normal, by
	// level (sprint.PriorityCounts), and StreamPriorities each stream's default level that
	// is not normal (sprint.StreamPriorities), as of the count.
	ReadsWaiting     int                 `json:"reads_waiting,omitempty"`
	Priorities       map[string][]string `json:"priorities,omitempty"`
	StreamPriorities map[string]string   `json:"stream_priorities,omitempty"`
	// ReadsWindow is the ok and broken verdicts over the last 30 minutes of running time
	// (sprint.ReadsWindowOf), as of the count.
	ReadsWindow sprint.ReadsWindowView `json:"reads_window,omitzero"`
	// FleetRev is the fleet table's revision at the count: a take moves the fleet alone.
	// RowCards is each fleet row's cards by level and its reads, ReadCards the epoch's read
	// cards (sprint.RowCardCounts), the dashboard's rows and read_cards.
	FleetRev  uint64                    `json:"fleet_rev,omitempty"`
	RowCards  map[string]map[string]int `json:"row_cards,omitempty"`
	ReadCards sprint.ReadCardCounts     `json:"read_cards"`
	FixStates map[string]map[string]int `json:"fix_states,omitempty"`
	// Stats is the stats record the spend and per landed were counted from (statsStamp: the
	// last tidy of the streams and the reset's mark); a record whose stamp is not the stats
	// record's is counted again and never taken, though no table moved.
	Stats string `json:"stats,omitempty"`
}

// whereOf is the where record of a snapshot holding every card of the work
// table, at now, for the machine's STOPPED spans.
func whereOf(s *sprint.Snapshot, m Machine, now time.Time) WhereRecord {
	var landed []time.Time
	for _, c := range s.Work.Column(sprint.Landed) {
		if at, err := time.Parse(time.RFC3339, c.F("landed")); err == nil {
			landed = append(landed, at)
		}
	}
	r := WhereRecord{Epoch: s.Epoch, Rev: s.Work.Revision, Held: sprint.HeldBack(s), Critical: sprint.Critical(s, 5),
		Tiers: sprint.TierCounts(s), Streams: sprint.StreamTierCosts(s), StageTimes: sprint.CycleTimes(s, now), DealtFleet: sprint.FriendsDealtFleet(s),
		ReadsWaiting: sprint.ReadsWaiting(s), Priorities: sprint.PriorityCounts(s), StreamPriorities: sprint.StreamPriorities(s),
		ReadsWindow: sprint.ReadsWindowOf(s, m.StoppedBetween)}
	if s.Fleet != nil {
		r.FleetRev = s.Fleet.Revision
	}
	r.RowCards, r.ReadCards = sprint.RowCardCounts(s)
	r.FixStates = sprint.FixStateCounts(s)
	for _, at := range sprint.RecentLandings(landed, m.Spans, m.FirstStart(s.Cleared), now) {
		r.Landings = append(r.Landings, at.Unix())
	}
	return r
}

// readWhere is the where record as stored; false when there is none or it
// does not read.
func readWhere(raw string, ok bool) (WhereRecord, bool) {
	var r WhereRecord
	if !ok || json.Unmarshal([]byte(raw), &r) != nil {
		return WhereRecord{}, false
	}
	return r, true
}

// keepWhere is the tick's count of the where record: nothing when the record
// is of this epoch at the work and fleet tables' revisions (an idle tick reads the
// shapes and the record, two exchanges); else the sprint from the twin, brought
// up to date, and the record written. A twin held by another step of this
// process, or a clear under the read, leaves it to the next tick.
//
// First, every tick, the card log index takes the log's new lines
// (keepLogIndex, load.go): the cursor read and the lines after it, two
// exchanges, and their entries written in one more when there are any.
func (st *Store) keepWhere(ctx context.Context, m Machine) error {
	kv, err := st.kv()
	if err != nil {
		return nil // a store that keeps no records: where counts the cards
	}
	if err := st.keepLogIndex(ctx); err != nil {
		return fmt.Errorf("the card log index: %w", err)
	}
	shapes, err := st.B.Shapes(ctx, []string{st.Names.Table(sprint.Work), st.Names.Table(sprint.Fleet)})
	if err != nil {
		return err
	}
	// the where record and the stats record (stats tidy's streams' bases) in one exchange
	vals, oks, err := getKeys(ctx, kv, []string{keyWhere, keyStats})
	if err != nil {
		return err
	}
	stats := st.statsRecordOf(vals[1], oks[1]) // permissive: an unreadable one is no tidy
	stamp := stats.statsStamp(st.epoch)
	if r, ok := readWhere(vals[0], oks[0]); ok && r.Epoch == st.epoch && r.Rev == shapes[0].Revision && r.FleetRev == shapes[1].Revision && r.Stats == stamp {
		return nil
	}
	tw := st.twin()
	if !tw.mu.TryLock() {
		return nil
	}
	snap, _, err := st.twinRead(withBudget(ctx), tw, All, nil, tickExtras, nil)
	tw.mu.Unlock()
	if errors.Is(err, errCleared) {
		return nil
	}
	if err != nil {
		return err
	}
	r := whereOf(snap, m, st.now())
	r.Stats = stamp
	// the spend since the stats reset's mark (sprint.TierCostsSince): the total, the work, the
	// reads and each tier, less the mark's; a stream the mark does not know reads as before
	if m := stats.resetIn(st.epoch); m != nil {
		for stream, tc := range r.Streams {
			if base, ok := m.Streams[stream]; ok {
				r.Streams[stream] = sprint.TierCostsSince(tc, base)
			}
		}
	}
	// per landed since the last tidy of the streams or the reset, the later (stats tidy, stats
	// reset, sprint.PerLandedSince)
	bases := stats.basesIn(st.epoch)
	for stream, b := range bases {
		tc, ok := r.Streams[stream]
		if !ok {
			continue
		}
		cost := ""
		if ctl := snap.StreamCtl(stream); ctl != nil {
			cost = ctl.F(sprint.FieldCost)
		}
		tc.PerLanded = sprint.PerLandedSince(cost, snap.Work.Count(stream, sprint.Landed), b)
		r.Streams[stream] = tc
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return kv.SetKey(ctx, keyWhere, string(b))
}

// The store round trip (store-latency-row-r.w2, docs/SPEC-SPRINT.md section
// 14): the server times one round trip to the store every StoreRTTEvery by the
// injected clock and keeps the samples of the last StoreRTTWindow, with their
// p50 and p99, in one key, so where, another process, reads them in the
// exchange it makes anyway.

// StoreRTTEvery is how often the server times a store round trip, and
// StoreRTTWindow how long a sample is kept; a record whose last sample is older
// than the window is a server that stopped measuring, and where shows none.
const (
	StoreRTTEvery  = 10 * time.Second
	StoreRTTWindow = time.Minute
)

// keyStoreRTT is the store round trip record's key, under the deployment's prefix.
const keyStoreRTT = "store_rtt" // STRING, the store round trip record (JSON)

// StoreRTTRecord is the store round trip record: the samples of the last
// StoreRTTWindow (when each round trip began, Unix milliseconds, and how long it
// took, microseconds, so a round trip under a millisecond is not zero) and their
// p50 and p99 in milliseconds.
type StoreRTTRecord struct {
	At      int64      `json:"at"` // the last sample's start, Unix milliseconds
	Samples [][2]int64 `json:"samples"`
	P50MS   float64    `json:"store_rtt_p50_ms"`
	P99MS   float64    `json:"store_rtt_p99_ms"`
}

// MeasureStoreRTT times one round trip to the store by the injected clock (the
// read of the record itself) and records it from the time it began
// (RecordStoreRTT); it returns the round trip. A store without the KV records
// measures nothing.
func (st *Store) MeasureStoreRTT(ctx context.Context) (time.Duration, error) {
	kv, err := st.kv()
	if err != nil {
		return 0, err
	}
	t0 := st.now()
	v, ok, err := kv.GetKey(ctx, keyStoreRTT)
	d := st.now().Sub(t0)
	if err != nil {
		return d, err
	}
	return d, st.keepStoreRTT(ctx, kv, readStoreRTT(v, ok), t0, d)
}

// RecordStoreRTT records one store round trip d that began at the store's now.
func (st *Store) RecordStoreRTT(ctx context.Context, d time.Duration) error {
	kv, err := st.kv()
	if err != nil {
		return err
	}
	v, ok, err := kv.GetKey(ctx, keyStoreRTT)
	if err != nil {
		return err
	}
	return st.keepStoreRTT(ctx, kv, readStoreRTT(v, ok), st.now(), d)
}

// keepStoreRTT adds the sample (at, d) to r, drops the samples older than
// StoreRTTWindow before at, and writes r with its p50 and p99. The server is
// the one writer of the record.
func (st *Store) keepStoreRTT(ctx context.Context, kv KV, r StoreRTTRecord, at time.Time, d time.Duration) error {
	cut := at.Add(-StoreRTTWindow).UnixMilli()
	kept := [][2]int64{}
	for _, s := range r.Samples {
		if s[0] >= cut {
			kept = append(kept, s)
		}
	}
	kept = append(kept, [2]int64{at.UnixMilli(), d.Microseconds()})
	r = StoreRTTRecord{At: at.UnixMilli(), Samples: kept}
	r.P50MS, r.P99MS = rttQuantile(kept, 0.50), rttQuantile(kept, 0.99)
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return kv.SetKey(ctx, keyStoreRTT, string(b))
}

// readStoreRTT is the record a read returned; none, or one that does not
// parse, is an empty record.
func readStoreRTT(v string, ok bool) StoreRTTRecord {
	var r StoreRTTRecord
	if ok && json.Unmarshal([]byte(v), &r) != nil {
		return StoreRTTRecord{}
	}
	return r
}

// rttQuantile is the nearest-rank q quantile of the samples' round trips, in
// milliseconds to the microsecond: the sorted sample at index floor(q*n), the
// last at most.
func rttQuantile(samples [][2]int64, q float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	us := make([]int64, len(samples))
	for i, s := range samples {
		us[i] = s[1]
	}
	slices.Sort(us)
	return float64(us[min(len(us)-1, int(q*float64(len(us))))]) / 1000
}

// WhereFacts is what where shows beside the tables' counts: the machine's
// records (Records: the store keeps them), the held cards, the landing
// stamps for the rate, and the store round trip the server measured.
type WhereFacts struct {
	Records   bool
	Machine   Machine
	Heartbeat Heartbeat
	Held      int
	Landed    []time.Time
	Critical  []sprint.CriticalCard // the five heaviest, from the record (weight.go)
	// Tiers and Streams are the record's counts and costs by tier (cost_view.go); nil
	// without the record.
	Tiers   map[string]int
	Streams map[string]sprint.TierCosts
	// StageTimes is the record's stage times (sprint.CycleTimes); empty without the record.
	StageTimes sprint.StageTimes
	// DealtFleet is the record's count of the fleet's cards on each friend's row
	// (sprint.FriendsDealtFleet); nil without the record.
	DealtFleet map[string]int
	// RowCards and ReadCards are the record's (sprint.RowCardCounts); nil and zero without it.
	RowCards  map[string]map[string]int
	ReadCards sprint.ReadCardCounts
	FixStates map[string]map[string]int
	// ReadsWaiting, Priorities and StreamPriorities are the record's (WhereRecord); zero
	// without the record.
	ReadsWaiting     int
	Priorities       map[string][]string
	StreamPriorities map[string]string
	// ReadsWindow is the record's verdicts window (sprint.ReadsWindowOf); zero without it.
	ReadsWindow sprint.ReadsWindowView
	// Reset is the stats reset's mark in the pinned epoch (stats_reset.go); nil with none.
	// The where record's spend is counted from it already (keepWhere); where counts the
	// fleet's done cells and the landed per card from it.
	Reset *sprint.ResetMark
	// HasStoreRTT is set when the server's store round trip record has a sample
	// within StoreRTTWindow; StoreRTTP50MS and StoreRTTP99MS are its p50 and p99.
	HasStoreRTT   bool
	StoreRTTP50MS float64
	StoreRTTP99MS float64
	// Stops is the stops record the tick last wrote (stops.go), when HasStops: every
	// automatic stop that holds and what waits on the seat, as of its At.
	Stops    StopsRecord
	HasStops bool
}

// StoreLine is where's store line, "store: rtt p50=<ms>ms p99=<ms>ms", or
// empty when the server has measured no round trip within the window.
func (f WhereFacts) StoreLine() string {
	if !f.HasStoreRTT {
		return ""
	}
	return fmt.Sprintf("store: rtt p50=%gms p99=%gms", f.StoreRTTP50MS, f.StoreRTTP99MS)
}

// WhereFacts reads the machine's records and the where record in one exchange.
// The record is taken when it is of the pinned epoch and either counted at
// workRev, the work table's revision the caller read, or kept by the RUNNING
// machine's loop: its last tick recent (MachineSilence) and not failed, and the
// record counted at or after the revision that tick saw (the heartbeat's), so
// the record is at most the tick in flight behind. A loop that ticks and does
// not keep the record (a binary from before it, a tick whose count was passed
// over) has its heartbeat move past the record, and the record is not taken.
// Otherwise (no record of this epoch: before the first tick, or after a clear;
// a STOPPED machine whose table a verb moved since its last tick; no loop
// keeping it) the held cards and the stamps are read from the cards (HeldBack,
// LandedAt), as before the record; a read of the stamps that fails leaves none,
// and the rate is the whole sprint's average, never a failed view.
func (st *Store) WhereFacts(ctx context.Context, workRev uint64) (WhereFacts, error) {
	var f WhereFacts
	if kv, err := st.kv(); err == nil {
		vals, oks, err := getKeys(ctx, kv, []string{keyMachine, keyHeartbeat, keyWhere, keyStoreRTT, keyStats, keyStops})
		if err != nil {
			return f, err
		}
		f.Records = true
		// the stats reset's mark, in the same exchange: an unreadable stats record is none
		// here, as it is no tidy (the tick says it, statsRecordOf)
		var stats StatsRecord
		if !oks[4] || json.Unmarshal([]byte(vals[4]), &stats) != nil {
			stats = StatsRecord{}
		}
		f.Reset = stats.resetIn(st.epoch)
		for i, v := range []any{&f.Machine, &f.Heartbeat} {
			if oks[i] {
				if err := json.Unmarshal([]byte(vals[i]), v); err != nil {
					return f, err
				}
			}
		}
		if r := readStoreRTT(vals[3], oks[3]); len(r.Samples) > 0 && st.now().Sub(time.UnixMilli(r.At)) <= StoreRTTWindow {
			f.HasStoreRTT, f.StoreRTTP50MS, f.StoreRTTP99MS = true, r.P50MS, r.P99MS
		}
		if r, ok := readStops(vals[5], oks[5]); ok && r.Epoch == st.epoch {
			f.Stops, f.HasStops = r, true
		}
		// a record counted from another stats record (a reset or a tidy since) is not taken
		if r, ok := readWhere(vals[2], oks[2]); ok && r.Epoch == st.epoch && r.Stats == stats.statsStamp(st.epoch) && (r.Rev == workRev || st.keptBy(r, f.Machine, f.Heartbeat)) {
			f.Held, f.Critical, f.Tiers, f.Streams, f.StageTimes, f.DealtFleet = r.Held, r.Critical, r.Tiers, r.Streams, r.StageTimes, r.DealtFleet
			f.ReadsWaiting, f.Priorities, f.StreamPriorities = r.ReadsWaiting, r.Priorities, r.StreamPriorities
			f.ReadsWindow = r.ReadsWindow
			f.RowCards, f.ReadCards = r.RowCards, r.ReadCards
			f.FixStates = r.FixStates
			for _, s := range r.Landings {
				f.Landed = append(f.Landed, time.Unix(s, 0).UTC())
			}
			return f, nil
		}
	}
	var err error
	if f.Held, err = st.HeldBack(ctx); err != nil {
		return f, err
	}
	if f.Landed, err = st.LandedAt(ctx); err != nil {
		f.Landed = nil // the whole sprint's average (sprint.LandingRate)
	}
	return f, nil
}

// keptBy says the RUNNING machine's loop keeps the record: it ticks (its last
// tick within MachineSilence, not failed) and the record is counted at or
// after the work table's revision its last tick saw.
func (st *Store) keptBy(r WhereRecord, m Machine, hb Heartbeat) bool {
	return !st.ByHand && m.Running() && hb.Error == "" && st.now().Sub(hb.Alive()) <= MachineSilence && r.Rev >= hb.Revisions[0]
}
