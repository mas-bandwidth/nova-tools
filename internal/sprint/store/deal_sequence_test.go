package store

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The rolling indexes continue across plans (errata 3, amendment 5, the
// owner's form: each index a uint64 counter from 0 that goes up with every
// placement, the next name the counter modulo the count; amendment 10, the
// stream indexes; tla/SprintEvents.tla, dcur and acur): the owner's fleet on
// the store, eight machines and three streams, a tick at a time. Each tick's
// deal is one plan that reaches every machine with room while ready cards
// remain (the whole fleet in one update), starting at the member the counter
// names, and the counter after the tick is the placements made: the exact
// sequence across ticks and across the manifests of one plan, never a plan
// that starts at the first member again ("ticking halves"). The ask's reader
// index and the three stream indexes of the work table (the deal's, the
// ask's, the accept's) are pinned the same way. The same drivers run on the
// Mem backend here and on a real Redis with the table layer's functions
// loaded in the functional tier (deal_sequence_functional_test.go).

// seqStreams are the streams of the runs.
var seqStreams = []string{"s1", "s2", "s3"}

// seqReaders are the harness's readers.
var seqReaders = []string{"reader-a", "reader-b", "reader-c"}

// indexPast is the name a rolling index's counter is past: the name before
// the one the counter names; "" at 0.
func indexPast(names []string, value string) string {
	order := append([]string(nil), names...)
	slices.Sort(order)
	c, err := strconv.ParseUint(value, 10, 64)
	if err != nil || c == 0 || len(order) == 0 {
		return ""
	}
	return order[(c-1)%uint64(len(order))]
}

// seqFleet is eight machines of the width, each beating, the machine
// running, and no card yet.
func seqFleet(h *harness, width int) {
	h.t.Helper()
	h.mu.Lock()
	h.live = append([]string(nil), widthMembers...)
	h.mu.Unlock()
	h.beat()
	for _, m := range widthMembers {
		h.must(FleetStep(sprint.FleetReq{Op: "up", Member: m, Width: width}))
	}
	h.startMachine()
	// every member up before the first card, the presence changes applied
	for i := 0; i < 3; i++ {
		h.machine()
		h.tick(time.Second)
	}
}

// seqBatch adds n ready cards spread over the streams, one to each in turn.
func seqBatch(h *harness, tick, n int) {
	h.t.Helper()
	by := map[string][]string{}
	for i := 0; i < n; i++ {
		st := seqStreams[i%len(seqStreams)]
		by[st] = append(by[st], fmt.Sprintf("%s-t%d-%03d", st, tick, i))
	}
	for _, st := range seqStreams {
		if len(by[st]) > 0 {
			h.must(AddStep(sprint.AddReq{Stream: st, IDs: by[st]}))
		}
	}
}

// seqPart is what one part of a tick (or a step) moved: the members dealt to,
// the stream of each card, and the readers asked of each primary.
type seqPart struct {
	parts   int
	members []string
	streams []string
	readers [][]string
}

// seqMoves reads the moved lines of the parts named name.
func seqMoves(res TickResult, name string) seqPart {
	var out seqPart
	for _, p := range res.Parts {
		if p.Name != name {
			continue
		}
		out.parts++
		out.add(p.Moved)
	}
	return out
}

func (o *seqPart) add(moved []string) {
	for _, line := range moved {
		id, _, _ := strings.Cut(line, " ")
		st, _, _ := strings.Cut(id, "-")
		if _, m, ok := strings.Cut(line, " member="); ok {
			m, _, _ = strings.Cut(m, " ")
			o.members = append(o.members, m)
		}
		if _, rs, ok := strings.Cut(line, " asked of "); ok {
			o.readers = append(o.readers, strings.Split(rs, ", "))
		}
		o.streams = append(o.streams, st)
	}
}

// seqRound is n names round order from the one the counter names.
func seqRound(order []string, counter uint64, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = order[(counter+uint64(i))%uint64(len(order))]
	}
	return out
}

// seqTurns is the owner's form taken k times over names with counts left of
// each: from the name the counter names, the first with one left, the counter
// moved past it (up by one for the placement and by one for each name passed
// over). It returns the names taken and the counter after.
func seqTurns(names []string, counts map[string]int, counter uint64, k int) ([]string, uint64) {
	left := map[string]int{}
	for n, c := range counts {
		left[n] = c
	}
	n := uint64(len(names))
	var out []string
	for len(out) < k {
		took := false
		for i := uint64(0); i < n; i++ {
			x := names[(counter+i)%n]
			if left[x] > 0 {
				left[x]--
				out = append(out, x)
				counter += i + 1
				took = true
				break
			}
		}
		if !took {
			break
		}
	}
	return out, counter
}

// prop is a table's property as a counter.
func prop(t *testing.T, s *sprint.Snapshot, table, name string) uint64 {
	t.Helper()
	v, _ := s.T(table).Prop(name)
	if v == "" {
		return 0
	}
	c, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		t.Fatalf("%s's %s is %q, not a counter: the index is a uint64 (errata 3 amendment 5)", table, name, v)
	}
	return c
}

// dealSequenceAcrossTicks runs ticks ticks of batch ready cards each and
// asserts the exact deal sequence: tick 1 from m1, each later tick from the
// member the counter names, every card of the batch dealt in the tick's one
// deal part (the whole fleet in one update), and the fleet table's deal_index
// after each tick the placements made so far.
func dealSequenceAcrossTicks(t *testing.T, h *harness, ticks, batch int) {
	seqFleet(h, 64)
	var counter uint64
	for i := 1; i <= ticks; i++ {
		seqBatch(h, i, batch)
		res := h.machine()
		got := seqMoves(res, "deal")
		at := prop(t, h.snap(), sprint.Fleet, sprint.PropDealIndex)
		t.Logf("tick %d: deal parts=%d deal_index=%d deals=%v", i, got.parts, at, got.members)
		want := seqRound(widthMembers, counter, batch)
		if !slices.Equal(got.members, want) {
			t.Fatalf("tick %d dealt %v, want %v: each tick's deal starts at the member the counter names, and deals the whole batch", i, got.members, want)
		}
		if got.parts != 1 {
			t.Fatalf("tick %d ran %d deal parts, want one: the deal is one plan a tick", i, got.parts)
		}
		counter += uint64(batch)
		if at != counter {
			t.Fatalf("tick %d: deal_index %d after the deal, want %d, the placements made", i, at, counter)
		}
		if n := len(h.snap().Work.Column(sprint.Ready)); n != 0 {
			t.Fatalf("tick %d left %d ready: every ready card the fleet has room for is dealt in the tick", i, n)
		}
		h.tick(time.Second)
	}
}

// The owner's small batches: 20 ready a tick over three streams, eight
// machines of width 64. Tick 1 m1..m8 m1..m8 m1..m4, tick 2 from m5, tick 3
// from where tick 2 ended; the counter 20, 40, 60.
func TestTheDealContinuesAcrossTicksInExactSequence(t *testing.T) {
	t.Parallel()
	dealSequenceAcrossTicks(t, newHarness(t), 3, 20)
}

// A deal of 150 a tick is cut into two manifests of the fleet table (the
// table layer's 128 changed entries): the counter continues across the
// manifests of one plan and across the ticks.
func TestTheDealContinuesAcrossTheManifestsOfAPlan(t *testing.T) {
	t.Parallel()
	dealSequenceAcrossTicks(t, newHarness(t), 3, 150)
}

// seqRun is what the streams run did, a tick at a time.
type seqRun struct {
	deal, ask, accept                []seqPart
	dealCounts, askCounts, accCounts []map[string]int    // what each step could take, by stream, before it
	props                            []map[string]uint64 // every index after the tick and its accept
}

// streamsRun is eight machines of width 2 (room 16 a tick) and three streams
// of 40 ready: each tick deals 16, asks two readers of every primary finished
// the tick before, and after the tick the readers report ok, the coordinator
// accepts every primary read, and every member works its cards. What each
// step could take is read before it, for the expected sequences.
func streamsRun(t *testing.T, h *harness, ticks int) seqRun {
	seqFleet(h, 2)
	for _, st := range seqStreams {
		h.must(AddStep(sprint.AddReq{Stream: st, Count: 40}))
	}
	var r seqRun
	for i := 1; i <= ticks; i++ {
		s := h.snap()
		ready, review := map[string]int{}, map[string]int{}
		for _, st := range seqStreams {
			ready[st] = len(s.Work.Cell(st, sprint.Ready))
			for _, c := range s.Work.Cell(st, sprint.Review) {
				if c.F("asked") == "" {
					review[st]++
				}
			}
		}
		res := h.machine()
		r.dealCounts, r.askCounts = append(r.dealCounts, ready), append(r.askCounts, review)
		r.deal, r.ask = append(r.deal, seqMoves(res, "deal")), append(r.ask, seqMoves(res, "ask"))
		h.readAll()
		acc := map[string]int{}
		for _, st := range seqStreams {
			for _, c := range h.snap().Work.Cell(st, sprint.Review) {
				if c.F("asked") != "" {
					acc[st]++
				}
			}
		}
		r.accCounts = append(r.accCounts, acc)
		var a seqPart
		a.parts = 1
		a.add(h.run(AcceptStep(sprint.AcceptReq{})).Moved)
		r.accept = append(r.accept, a)
		for _, m := range widthMembers {
			h.work(m)
		}
		after := h.snap()
		r.props = append(r.props, map[string]uint64{
			"deal":          prop(t, after, sprint.Fleet, sprint.PropDealIndex),
			"ask":           prop(t, after, sprint.Readers, sprint.PropAskIndex),
			"stream deal":   prop(t, after, sprint.Work, sprint.PropStreamIndex),
			"stream ask":    prop(t, after, sprint.Work, sprint.PropAskStreamIndex),
			"stream accept": prop(t, after, sprint.Work, sprint.PropAcceptStreamIndex),
		})
		t.Logf("tick %d: deal streams %v; ask streams %v readers %v; accept streams %v; indexes %v",
			i, r.deal[i-1].streams, r.ask[i-1].streams, r.ask[i-1].readers, r.accept[i-1].streams, r.props[i-1])
		h.tick(time.Second)
	}
	return r
}

// dealStreamsAcrossTicks: the deal's stream index (stream_index) goes on
// from tick to tick: each tick's 16 cards in stream turns from the stream the
// counter names, the counter after each tick up by the cards dealt and the
// streams passed over.
func dealStreamsAcrossTicks(t *testing.T, h *harness) {
	r := streamsRun(t, h, 4)
	var counter uint64
	for i := range r.deal {
		want, next := seqTurns(seqStreams, r.dealCounts[i], counter, 16)
		if !slices.Equal(r.deal[i].streams, want) || r.deal[i].parts != 1 {
			t.Fatalf("tick %d: the deal took the streams %v in %d parts, want %v in one: stream turns from the counter %d", i+1, r.deal[i].streams, r.deal[i].parts, want, counter)
		}
		if got := r.props[i]["stream deal"]; got != next {
			t.Fatalf("tick %d: stream_index %d, want %d", i+1, got, next)
		}
		counter = next
	}
	if counter != 64 {
		t.Fatalf("stream_index %d after 64 cards dealt from three streams that never ran out, want 64", counter)
	}
}

// askAcrossTicks: the ask's reader index (ask_index) and its stream index
// (stream_index_ask) go on from tick to tick: each primary asked of the next
// two readers the counter names, the counter up by two a primary; the
// primaries in stream turns from the stream the ask's counter names.
func askAcrossTicks(t *testing.T, h *harness) {
	r := streamsRun(t, h, 4)
	var readers, streams uint64
	asked := 0
	for i := range r.ask {
		k := 0
		for _, n := range r.askCounts[i] {
			k += n
		}
		want, next := seqTurns(seqStreams, r.askCounts[i], streams, k)
		if !slices.Equal(r.ask[i].streams, want) {
			t.Fatalf("tick %d: the ask took the streams %v, want %v: stream turns from the counter %d", i+1, r.ask[i].streams, want, streams)
		}
		streams = next
		if got := r.props[i]["stream ask"]; got != streams {
			t.Fatalf("tick %d: stream_index_ask %d, want %d", i+1, got, streams)
		}
		for j, pair := range r.ask[i].readers {
			if w := seqRound(seqReaders, readers, 2); !slices.Equal(pair, w) {
				t.Fatalf("tick %d, primary %d: asked of %v, want %v: the next two readers from the counter %d", i+1, j+1, pair, w, readers)
			}
			readers += 2
		}
		asked += len(r.ask[i].readers)
		if got := r.props[i]["ask"]; got != readers {
			t.Fatalf("tick %d: ask_index %d, want %d, two a primary asked", i+1, got, readers)
		}
	}
	if asked != 48 {
		t.Fatalf("%d primaries asked in four ticks, want 48: the three ticks after the first each ask the 16 finished", asked)
	}
}

// acceptAcrossTicks: the accept's stream index (stream_index_accept) goes on
// from step to step: every primary read in stream turns from the stream the
// counter names.
func acceptAcrossTicks(t *testing.T, h *harness) {
	r := streamsRun(t, h, 4)
	var counter uint64
	accepted := 0
	for i := range r.accept {
		k := 0
		for _, n := range r.accCounts[i] {
			k += n
		}
		want, next := seqTurns(seqStreams, r.accCounts[i], counter, k)
		if !slices.Equal(r.accept[i].streams, want) {
			t.Fatalf("accept %d took the streams %v, want %v: stream turns from the counter %d", i+1, r.accept[i].streams, want, counter)
		}
		counter = next
		if got := r.props[i]["stream accept"]; got != counter {
			t.Fatalf("accept %d: stream_index_accept %d, want %d", i+1, got, counter)
		}
		accepted += len(r.accept[i].streams)
	}
	if accepted != 48 {
		t.Fatalf("%d accepted, want 48", accepted)
	}
}

func TestTheDealsStreamIndexContinuesAcrossTicks(t *testing.T) {
	t.Parallel()
	dealStreamsAcrossTicks(t, newHarness(t))
}

func TestTheAsksIndexesContinueAcrossTicks(t *testing.T) {
	t.Parallel()
	askAcrossTicks(t, newHarness(t))
}

func TestTheAcceptsStreamIndexContinuesAcrossSteps(t *testing.T) {
	t.Parallel()
	acceptAcrossTicks(t, newHarness(t))
}
