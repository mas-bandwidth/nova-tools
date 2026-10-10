package tablemodel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
	tassert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests here reach the store readers of capture.go at the unit tier, where
// no redis-server may be started (STANDARD section 8): each reader runs over
// captureUnitCoverStore, a go-redis client whose ProcessHook answers every
// command from a table and never calls the next hook, so nothing is dialed.
// The readers parse flat []any replies of strings, the shape list, pairs and
// pairMap read. The real table.lua behind those replies, and capture and
// replayReceipts end to end, stay with the functional tier (functional_test.go).

// captureUnitCoverSeq is a table entry that answers one command repeatedly, one
// reply per call, so a reader that reads the same key twice (execute's first
// XLEN against its last, or the revisions before and after a receipt) sees the
// store move.
type captureUnitCoverSeq []any

// captureScript is the store behind a reader test's client: the reply each
// command gets, and the record of every command it was sent.
type captureScript struct {
	replies map[string]any
	sent    []string
}

func (s *captureScript) DialHook(next redis.DialHook) redis.DialHook { return next }

func (s *captureScript) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// captureUnitCoverStore builds the scripted client once: a go-redis client
// whose Dialer refuses and whose ProcessHook answers each command from replies
// and records the command line. An unlisted HGETALL or ZRANGE answers an empty
// list; any other unlisted command is a missing script and fails the check.
func captureUnitCoverStore(t *testing.T, replies map[string]any) (*Store, *captureScript) {
	t.Helper()
	s := &captureScript{replies: replies}
	c := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0", MaxRetries: -1,
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("the scripted store is never dialed")
		}})
	c.AddHook(s)
	t.Cleanup(func() { _ = c.Close() })
	return &Store{ctx: context.Background(), c: c}, s
}

func (s *captureScript) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		args := cmd.Args()
		line := make([]string, 0, len(args))
		for _, a := range args {
			line = append(line, fmt.Sprint(a))
		}
		key := strings.Join(line, " ")
		s.sent = append(s.sent, key)
		answer, ok := s.replies[key]
		if q, isSeq := answer.(captureUnitCoverSeq); isSeq {
			answer = q[0]
			if len(q) > 1 {
				s.replies[key] = q[1:]
			}
		}
		if !ok {
			switch strings.ToUpper(fmt.Sprint(args[0])) {
			case "HGETALL", "ZRANGE":
				answer = []any{}
			default:
				return fmt.Errorf("capture: no scripted reply for %q", key)
			}
		}
		if answer == nil {
			cmd.(*redis.Cmd).SetErr(redis.Nil)
			return redis.Nil
		}
		cmd.(*redis.Cmd).SetVal(answer)
		return nil
	}
}

// captureUnitCoverCallKey is the command line call sends for one verb: FCALL,
// the function table.lua registers, the pass-everything argc, the arguments and
// the JSON options.
func captureUnitCoverCallKey(verb string, args []string, opts callOptions) string {
	line := []string{"FCALL", fcallName(verb), "0"}
	line = append(line, args...)
	return strings.Join(append(line, js(opts)), " ")
}

// captureUnitCoverSeedReplies scripts the writes seed makes: the epoch, the
// member epochs, the tables' create, row_add and cell_add, and the external
// set. seed reads nothing.
func captureUnitCoverSeedReplies() map[string]any {
	replies := map[string]any{
		"HSET replay:epoch n 1": int64(1),
		"ZADD external 2 m2":    int64(1),
	}
	for _, m := range members {
		replies[fmt.Sprintf("HSET table::member:%s epoch %d", m.id, m.epoch)] = int64(1)
	}
	for _, tbl := range tables {
		replies[captureUnitCoverCallKey("create", []string{tbl, fieldsJSON()}, options(1, "seed"))] = []any{"OK"}
		for _, row := range rowKeys {
			replies[captureUnitCoverCallKey("row_add", []string{tbl, row, "{}"}, options(1, "seed"))] = []any{"ROW"}
		}
	}
	replies[captureUnitCoverCallKey("cell_add", []string{"t1", "r1", "c1", "1", "m1"}, options(1, "seed"))] = []any{"OK"}
	return replies
}

// captureUnitCoverEventPairs flattens a receipt event to the field, value
// order an XREVRANGE entry holds.
func captureUnitCoverEventPairs(e Event) []any {
	keys := []string{"verb", "args", "rev_before", "rev_after", "epoch", "actor", "fence", "idem", "outcome", "cells", "members"}
	pairs := make([]any, 0, len(keys)*2)
	for _, k := range keys {
		pairs = append(pairs, k, e[k])
	}
	return pairs
}

// TestTablemodelCaptureCoverJsPanicsOnAChannel pins js's failure: a value JSON
// cannot marshal is a failed check, not a silent zero.
func TestTablemodelCaptureCoverJsPanicsOnAChannel(t *testing.T) {
	t.Parallel()
	tassert.Panics(t, func() { js(make(chan int)) }, "js must panic on a value encoding/json cannot marshal")
}

// TestTablemodelCaptureCoverRevisionReadsTheStoreRevision pins revision: an
// absent revision key is 0, and a numeric reply is that number.
func TestTablemodelCaptureCoverRevisionReadsTheStoreRevision(t *testing.T) {
	t.Parallel()
	none, _ := captureUnitCoverStore(t, map[string]any{"HGET table:t1:revision n": nil})
	tassert.Equal(t, 0, revision(none, "t1"), "a store with no revision key is at revision 0")

	four, _ := captureUnitCoverStore(t, map[string]any{"HGET table:t1:revision n": "4"})
	tassert.Equal(t, 4, revision(four, "t1"), "revision reads the number the store carries")
}

// TestTablemodelCaptureCoverSeedWritesTheStartingStore pins seed's write
// order: the epoch first, then each member's epoch, the two tables with their
// rows and the one cell, and the external set last.
func TestTablemodelCaptureCoverSeedWritesTheStartingStore(t *testing.T) {
	t.Parallel()
	r, script := captureUnitCoverStore(t, captureUnitCoverSeedReplies())
	require.NoError(t, guard(func() { seed(r) }), "seed refused a fully scripted store")

	want := []string{"HSET replay:epoch n 1"}
	for _, m := range members {
		want = append(want, fmt.Sprintf("HSET table::member:%s epoch %d", m.id, m.epoch))
	}
	for _, tbl := range tables {
		want = append(want, captureUnitCoverCallKey("create", []string{tbl, fieldsJSON()}, options(1, "seed")))
		for _, row := range rowKeys {
			want = append(want, captureUnitCoverCallKey("row_add", []string{tbl, row, "{}"}, options(1, "seed")))
		}
	}
	want = append(want, captureUnitCoverCallKey("cell_add", []string{"t1", "r1", "c1", "1", "m1"}, options(1, "seed")))
	want = append(want, "ZADD external 2 m2")
	require.Equal(t, want, script.sent, "seed's commands, in order")
	require.Equal(t, "HSET replay:epoch n 1", script.sent[0], "seed sets the epoch first")
	require.Equal(t, "ZADD external 2 m2", script.sent[len(script.sent)-1], "seed writes the external set last")
}

// TestTablemodelCaptureCoverSeedFailsWhenCreateRefuses pins seed's refusal: a
// create the store answers REFUSED fails the check under the table's name.
func TestTablemodelCaptureCoverSeedFailsWhenCreateRefuses(t *testing.T) {
	t.Parallel()
	replies := captureUnitCoverSeedReplies()
	replies[captureUnitCoverCallKey("create", []string{"t1", fieldsJSON()}, options(1, "seed"))] = []any{"REFUSED"}
	r, _ := captureUnitCoverStore(t, replies)
	err := guard(func() { seed(r) })
	require.ErrorContains(t, err, "seed create t1", "seed's failure names the table it was creating")
}

// TestTablemodelCaptureCoverSnapshotReadsTheScriptedStore pins snapshot's
// reads: the active epoch, the live tables it does not see dropped, the one
// row and its binding, the scores in their cells and the external set, and
// every member's place.
func TestTablemodelCaptureCoverSnapshotReadsTheScriptedStore(t *testing.T) {
	t.Parallel()
	r, _ := captureUnitCoverStore(t, map[string]any{
		"HGET replay:epoch n":                          "1",
		"ZRANGE table:t1:1:rows 0 -1":                  []any{"r1"},
		"HGETALL table:t1:1:row:r1":                    []any{"key:c2", "external"},
		"ZRANGE table:t1:1:cell:r1:c1 0 -1 WITHSCORES": []any{"m1", "1"},
		"ZRANGE external 0 -1 WITHSCORES":              []any{"m2", "2"},
		"HGETALL table::member:m1":                     []any{"epoch", "1", "place:t1", "r1:c1"},
		"HGETALL table::member:m2":                     []any{"epoch", "2"},
		"HGETALL table::member:m3":                     []any{"epoch", "1"},
	})
	want := State{
		Live:  []Phys{{"t1", 1}, {"t1", 2}, {"t2", 1}, {"t2", 2}},
		Rows:  []RowAt{{Phys{"t1", 1}, "r1"}},
		Binds: []Bind{{mkCell("t1", 1, "r1", "c2"), External}},
		Data: []Datum{
			{mkCell("t1", 1, "r1", "c1"), "m1", 1},
			{External, "m2", 2},
		},
		Place: []Placement{
			{Member: "m1", Locations: []Location{
				{Phys{"t1", 1}, mkCell("t1", 1, "r1", "c1")},
				{Phys{"t1", 2}, NoPlace},
				{Phys{"t2", 1}, NoPlace},
				{Phys{"t2", 2}, NoPlace},
			}},
			{Member: "m2", Locations: []Location{
				{Phys{"t1", 1}, NoPlace}, {Phys{"t1", 2}, NoPlace}, {Phys{"t2", 1}, NoPlace}, {Phys{"t2", 2}, NoPlace},
			}},
			{Member: "m3", Locations: []Location{
				{Phys{"t1", 1}, NoPlace}, {Phys{"t1", 2}, NoPlace}, {Phys{"t2", 1}, NoPlace}, {Phys{"t2", 2}, NoPlace},
			}},
		},
		Active: 1,
		Seen:   map[string]int{"w1": 1},
	}
	got := snapshot(r, map[string]int{"w1": 1})
	require.Equal(t, want, got, "snapshot of the scripted store")
}

// TestTablemodelCaptureCoverSnapshotDropsADefinitionPresentZero pins the one
// override snapshot honors: a definition whose _present is 0 is not live.
func TestTablemodelCaptureCoverSnapshotDropsADefinitionPresentZero(t *testing.T) {
	t.Parallel()
	r, _ := captureUnitCoverStore(t, map[string]any{
		"HGET replay:epoch n":           "1",
		"HGETALL table:t1:1:definition": []any{"_present", "0"},
		"HGETALL table::member:m1":      []any{"epoch", "1"},
		"HGETALL table::member:m2":      []any{"epoch", "2"},
		"HGETALL table::member:m3":      []any{"epoch", "1"},
	})
	got := snapshot(r, nil)
	require.Equal(t, []Phys{{"t1", 2}, {"t2", 1}, {"t2", 2}}, got.Live, "a dropped definition leaves the live set")
}

// TestTablemodelCaptureCoverSnapshotRefusesABindingOtherThanExternal pins the
// controlled-trace guard: a binding to anything but the external set is a
// failed check named by its cause.
func TestTablemodelCaptureCoverSnapshotRefusesABindingOtherThanExternal(t *testing.T) {
	t.Parallel()
	r, _ := captureUnitCoverStore(t, map[string]any{
		"HGET replay:epoch n":         "1",
		"ZRANGE table:t1:1:rows 0 -1": []any{"r1"},
		"HGETALL table:t1:1:row:r1":   []any{"key:c2", "table:t2:1:cell:r1:c1"},
	})
	err := guard(func() { snapshot(r, nil) })
	require.ErrorContains(t, err, "unexpected binding in controlled trace", "a foreign binding is named")
}

// TestTablemodelCaptureCoverSnapshotRefusesAChangedMemberEpoch pins the member
// guard: a member whose stored epoch is not the model's is a failed check.
func TestTablemodelCaptureCoverSnapshotRefusesAChangedMemberEpoch(t *testing.T) {
	t.Parallel()
	r, _ := captureUnitCoverStore(t, map[string]any{
		"HGET replay:epoch n":      "1",
		"HGETALL table::member:m1": []any{"epoch", "2"},
	})
	err := guard(func() { snapshot(r, nil) })
	require.ErrorContains(t, err, "member epoch changed: m1", "a moved member epoch is named")
}

// TestTablemodelCaptureCoverSnapshotRefusesAPlaceWithNoColumn pins the place
// guard: a member place that is not row:column is a failed check.
func TestTablemodelCaptureCoverSnapshotRefusesAPlaceWithNoColumn(t *testing.T) {
	t.Parallel()
	r, _ := captureUnitCoverStore(t, map[string]any{
		"HGET replay:epoch n":      "1",
		"HGETALL table::member:m1": []any{"epoch", "1", "place:t1", "r1"},
	})
	err := guard(func() { snapshot(r, nil) })
	require.ErrorContains(t, err, "has no column", "a place with no column is named")
}

// captureUnitCoverChangeEvent is the receipt a call makes when it changes the
// store: verb, arguments, the revisions around it, its options and an empty
// cell and member delta.
func captureUnitCoverChangeEvent() Event {
	return Event{
		"verb":       "cell_add",
		"args":       `["t1","r1","c1","1","m1"]`,
		"rev_before": "4",
		"rev_after":  "5",
		"epoch":      "1",
		"actor":      "w1",
		"fence":      "fixture-fence",
		"idem":       "fixture-attempt",
		"outcome":    "changed",
		"cells":      "[]",
		"members":    "[]",
	}
}

// TestTablemodelCaptureCoverReceiptReturnsNilForARefusal pins receipt's
// refusal: a REFUSED reply with the revision unchanged is no event.
func TestTablemodelCaptureCoverReceiptReturnsNilForARefusal(t *testing.T) {
	t.Parallel()
	args, opts := []string{"t1", "r1", "c1", "1", "m1"}, options(1, "w1")
	r, _ := captureUnitCoverStore(t, map[string]any{"HGET table:t1:revision n": "4"})
	var got Event
	require.NoError(t, guard(func() { got = receipt(r, "cell_add", args, opts, []any{"REFUSED", "BOUND"}, 4) }))
	require.Nil(t, got, "a refusal has no event")
}

// TestTablemodelCaptureCoverReceiptRefusesARefusalThatAdvancedRevision pins
// the refusal guard: a REFUSED reply that moved the revision is a failed
// check.
func TestTablemodelCaptureCoverReceiptRefusesARefusalThatAdvancedRevision(t *testing.T) {
	t.Parallel()
	args, opts := []string{"t1", "r1", "c1", "1", "m1"}, options(1, "w1")
	r, _ := captureUnitCoverStore(t, map[string]any{"HGET table:t1:revision n": "5"})
	err := guard(func() { receipt(r, "cell_add", args, opts, []any{"REFUSED"}, 4) })
	require.ErrorContains(t, err, "refusal advanced revision", "a refusal that moved the revision is named")
}

// TestTablemodelCaptureCoverReceiptReadsTheEventAndItsReply pins receipt's
// main path: the change advances one revision, the entry and the call's reply
// agree, and the event comes back with its id.
func TestTablemodelCaptureCoverReceiptReadsTheEventAndItsReply(t *testing.T) {
	t.Parallel()
	args, opts := []string{"t1", "r1", "c1", "1", "m1"}, options(1, "w1")
	event := captureUnitCoverChangeEvent()
	reply := []any{"OK", []any{"RECEIPT", "7-0", "1", "4", "5", "changed"}}
	r, _ := captureUnitCoverStore(t, map[string]any{
		"HGET table:t1:revision n":               "5",
		"XREVRANGE table:t1:changes + - COUNT 1": []any{[]any{"7-0", captureUnitCoverEventPairs(event)}},
	})
	var got Event
	require.NoError(t, guard(func() { got = receipt(r, "cell_add", args, opts, reply, 4) }))
	require.Equal(t, "cell_add", got["verb"], "the event names the verb")
	require.Equal(t, "7-0", got["id"], "the event carries the entry's id")
	require.Equal(t, "4", got["rev_before"], "the event starts where the store was")
	require.Equal(t, "5", got["rev_after"], "the event ends one later")
}

// TestTablemodelCaptureCoverReceiptRefusesARevisionGap pins the change guard:
// a revision that moved by more than one is a failed check.
func TestTablemodelCaptureCoverReceiptRefusesARevisionGap(t *testing.T) {
	t.Parallel()
	args, opts := []string{"t1", "r1", "c1", "1", "m1"}, options(1, "w1")
	r, _ := captureUnitCoverStore(t, map[string]any{"HGET table:t1:revision n": "6"})
	err := guard(func() { receipt(r, "cell_add", args, opts, []any{"OK"}, 4) })
	require.ErrorContains(t, err, "revision gap", "a skipped revision is named")
}

// TestTablemodelCaptureCoverReceiptRefusesAReplyWithoutTheReceipt pins the
// reply guard: a call whose reply does not end in the event's receipt is a
// failed check.
func TestTablemodelCaptureCoverReceiptRefusesAReplyWithoutTheReceipt(t *testing.T) {
	t.Parallel()
	args, opts := []string{"t1", "r1", "c1", "1", "m1"}, options(1, "w1")
	event := captureUnitCoverChangeEvent()
	r, _ := captureUnitCoverStore(t, map[string]any{
		"HGET table:t1:revision n":               "5",
		"XREVRANGE table:t1:changes + - COUNT 1": []any{[]any{"7-0", captureUnitCoverEventPairs(event)}},
	})
	err := guard(func() { receipt(r, "cell_add", args, opts, []any{"OK", "nope"}, 4) })
	require.ErrorContains(t, err, "does not end in this event's receipt", "a reply without the receipt is named")
}

// TestTablemodelCaptureCoverExecuteReadEpochRecordsSeen pins the read_epoch
// arm: it records the store's epoch for the writer and returns no refusal.
func TestTablemodelCaptureCoverExecuteReadEpochRecordsSeen(t *testing.T) {
	t.Parallel()
	r, _ := captureUnitCoverStore(t, map[string]any{"HGET replay:epoch n": "3"})
	seen := map[string]int{}
	refused, event := execute(r, Action{Verb: "read_epoch", Actor: "w1"}, seen, nil)
	require.Nil(t, refused, "a model-only step is never refused")
	require.Nil(t, event, "a model-only step has no event")
	require.Equal(t, 3, seen["w1"], "read_epoch records the store's epoch")
}

// TestTablemodelCaptureCoverExecuteAdvanceMovesTheStoreEpoch pins the advance
// arm: a writer at the store's epoch moves the store to the next one.
func TestTablemodelCaptureCoverExecuteAdvanceMovesTheStoreEpoch(t *testing.T) {
	t.Parallel()
	r, script := captureUnitCoverStore(t, map[string]any{
		"HGET replay:epoch n":   "3",
		"HSET replay:epoch n 4": int64(1),
	})
	seen := map[string]int{"w1": 3}
	refused, event := execute(r, Action{Verb: "advance", Actor: "w1"}, seen, nil)
	require.Nil(t, refused, "a model-only step is never refused")
	require.Nil(t, event, "a model-only step has no event")
	require.Contains(t, script.sent, "HSET replay:epoch n 4", "advance writes the next epoch")
}

// TestTablemodelCaptureCoverExecuteAdvanceRefusesAStaleSeen pins the advance
// guard: a writer that has not seen the store's epoch cannot advance it.
func TestTablemodelCaptureCoverExecuteAdvanceRefusesAStaleSeen(t *testing.T) {
	t.Parallel()
	r, _ := captureUnitCoverStore(t, map[string]any{"HGET replay:epoch n": "3"})
	seen := map[string]int{"w1": 2}
	err := guard(func() { execute(r, Action{Verb: "advance", Actor: "w1"}, seen, nil) })
	require.ErrorContains(t, err, "w1 advances from epoch 2, the store is at 3", "a stale seen epoch is named")
}

// TestTablemodelCaptureCoverExecuteTableVerbReturnsARefusal pins a table
// verb's refusal: REFUSED is not an event and the refusal flag is true.
func TestTablemodelCaptureCoverExecuteTableVerbReturnsARefusal(t *testing.T) {
	t.Parallel()
	args, opts := []string{"t1", "r1", "c1", "1", "m1"}, options(1, "w1")
	r, _ := captureUnitCoverStore(t, map[string]any{
		"HGET table:t1:revision n":                      "4",
		"XLEN table:t1:changes":                         "0",
		captureUnitCoverCallKey("cell_add", args, opts): []any{"REFUSED", "BOUND"},
	})
	refused, event := execute(r, Action{Verb: "cell_add", Args: args, Actor: "w1"}, map[string]int{"w1": 1}, nil)
	require.NotNil(t, refused, "a table verb returns a refusal flag")
	require.True(t, *refused, "a REFUSED reply is a refusal")
	require.Nil(t, event, "a refusal has no event")
}

// TestTablemodelCaptureCoverExecuteTableVerbReturnsTheChangeEvent pins a table
// verb's change: the reply's receipt is the event, and the change stream grew
// by one.
func TestTablemodelCaptureCoverExecuteTableVerbReturnsTheChangeEvent(t *testing.T) {
	t.Parallel()
	args, opts := []string{"t1", "r1", "c1", "1", "m1"}, options(1, "w1")
	event := captureUnitCoverChangeEvent()
	r, _ := captureUnitCoverStore(t, map[string]any{
		"HGET table:t1:revision n":                      captureUnitCoverSeq{"4", "5"},
		"XLEN table:t1:changes":                         captureUnitCoverSeq{"0", "1"},
		captureUnitCoverCallKey("cell_add", args, opts): []any{"OK", []any{"RECEIPT", "7-0", "1", "4", "5", "changed"}},
		"XREVRANGE table:t1:changes + - COUNT 1":        []any{[]any{"7-0", captureUnitCoverEventPairs(event)}},
	})
	refused, got := execute(r, Action{Verb: "cell_add", Args: args, Actor: "w1"}, map[string]int{"w1": 1}, nil)
	require.NotNil(t, refused, "a table verb returns a refusal flag")
	require.False(t, *refused, "a change is not a refusal")
	require.NotNil(t, got, "a change has an event")
	require.Equal(t, "cell_add", got["verb"], "the event names the verb")
	require.Equal(t, "7-0", got["id"], "the event carries its id")
}

// TestTablemodelCaptureCoverExecuteSavedEventWithAGapFails pins the replay
// guard: a saved event that does not start at the store's revision fails with
// GAP before any call is made.
func TestTablemodelCaptureCoverExecuteSavedEventWithAGapFails(t *testing.T) {
	t.Parallel()
	args := []string{"t1", "r1", "c1", "1", "m1"}
	r, _ := captureUnitCoverStore(t, map[string]any{
		"HGET table:t1:revision n": "7",
		"XLEN table:t1:changes":    "0",
	})
	saved := captureUnitCoverChangeEvent()
	err := guard(func() {
		execute(r, Action{Verb: "cell_add", Args: args, Actor: "w1"}, map[string]int{"w1": 1}, saved)
	})
	require.ErrorContains(t, err, "GAP: expected revision 7", "a saved event with a gap is named")
	require.ErrorContains(t, err, "receipt starts 4", "the gap names where the receipt starts")
}
