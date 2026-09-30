package sprintfn

import (
	"reflect"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// timeReq is a sprint part of the time rules' writes alone, in a step of lease
// generation 1 (a claim writes the step's generation).
func timeReq(tm *SprintTime) *Request {
	r := sprintReq(&SprintPart{Time: tm})
	r.Meta.Gen = 1
	return r
}

func decPtr(s string) *tset.Decimal { d := tset.Decimal(s); return &d }

// TestSprintPartTimeWrites: the sprint part writes the time rules' writes
// (2.3 R14, R17, R18; rules_time.go TimeWrites): each due entry moved to its
// running time, each goal claimed at R with the step's lease generation, and
// R17's clock fields set or cleared; the Lua half gives the same reply and the
// same commands in the same order. A second run moves no entry and writes no
// clock field that holds its value already. A malformed write, a claim in a
// step of no generation, and behind beside a tick end are REQUEST.
func TestSprintPartTimeWrites(t *testing.T) {
	t.Parallel()
	tw, _, _, clk := partsTwin(t)
	fleetSeed(t, tw)
	seed(tw, Command("HSET", sk("clock"), kindHash, "stopped_ms", "0", "stopped_since_ms", "1000", "stophold_ms", "",
		"due_since_ms", "", "stopraised_ms", "900"),
		Command("ZADD", ek("due"), kindZSet, "4000", "remind:bob"))
	h := newLuaParts(t)
	tm := &SprintTime{
		Due:   []DueAt{{Key: "remind:bob", At: "9000"}, {Key: "remind:ann", At: "9000"}},
		Goals: []GoalClaim{{Person: "bob", R: "8000"}, {Person: "ann", R: "8000"}},
		Clock: &ClockWrite{DueSince: decPtr("7000"), StopRaised: decPtr("")},
	}
	out, _ := diffParts(t, h, tw, clk, timeReq(tm), nil)
	plan, _ := out[PartSprint].plan.(map[string]any)
	if out[PartSprint].refusal != nil || !reflect.DeepEqual(plan["due"], []any{"remind:ann", "remind:bob"}) ||
		!reflect.DeepEqual(plan["claimed"], []any{"ann", "bob"}) || plan["clock"] != true {
		t.Fatalf("the time writes: %+v %v", plan, out[PartSprint].refusal)
	}
	mustStep(t, tw, timeReq(tm))
	keys := tw.SprintKeys()
	if s := keys[ek("due")].ZSet; s["remind:ann"] != 9000 || s["remind:bob"] != 9000 {
		t.Fatalf("the due set: %v", s)
	}
	if g := keys[sk("goal:ann")].Hash; g["claimed_r"] != "8000" || g["claimed_gen"] != "1" {
		t.Fatalf("ann's claim: %v", g)
	}
	if c := keys[sk("clock")].Hash; c["due_since_ms"] != "7000" || c["stopraised_ms"] != "" || c["stopped_since_ms"] != "1000" {
		t.Fatalf("the clock: %v", c)
	}
	out, _ = diffParts(t, h, tw, clk, timeReq(tm), nil)
	if plan, _ := out[PartSprint].plan.(map[string]any); plan["due"] != nil || plan["clock"] != nil || plan["claimed"] == nil {
		t.Fatalf("a second run: %+v", plan)
	}
	for name, bad := range map[string]*Request{
		"nothing":            timeReq(&SprintTime{}),
		"a due entry twice":  timeReq(&SprintTime{Due: []DueAt{{Key: "a", At: "1"}, {Key: "a", At: "2"}}}),
		"a time not exact":   timeReq(&SprintTime{Due: []DueAt{{Key: "a", At: "9007199254740992"}}}),
		"a time not whole":   timeReq(&SprintTime{Due: []DueAt{{Key: "a", At: "01"}}}),
		"a person twice":     timeReq(&SprintTime{Goals: []GoalClaim{{Person: "ann", R: "1"}, {Person: "ann", R: "1"}}}),
		"a person not an id": timeReq(&SprintTime{Goals: []GoalClaim{{Person: "a b", R: "1"}}}),
		"an empty clock":     timeReq(&SprintTime{Clock: &ClockWrite{}}),
		"a clock not whole":  timeReq(&SprintTime{Clock: &ClockWrite{DueSince: decPtr("x")}}),
		"a claim of no generation": func() *Request {
			r := timeReq(&SprintTime{Goals: []GoalClaim{{Person: "ann", R: "1"}}})
			r.Meta.Gen = 0
			return r
		}(),
		"behind beside a tick end": func() *Request {
			r := timeReq(&SprintTime{Due: []DueAt{{Key: "behind", At: "1"}}})
			r.Sprint.TickEnd = &TickEnd{Backlog: "3"}
			return r
		}(),
		"the unarm beside a tick end": func() *Request {
			r := timeReq(&SprintTime{UnarmBehind: true})
			r.Sprint.TickEnd = &TickEnd{Backlog: "3"}
			return r
		}(),
	} {
		out, _ := diffParts(t, h, tw, clk, bad, nil)
		if ref := out[PartSprint].refusal; ref == nil || ref.Code != CodeRequest {
			t.Fatalf("%s: %v, want REQUEST", name, ref)
		}
	}
	seed(tw, Command("SET", sk("goal:cy"), kindString, "x"))
	out, _ = diffParts(t, h, tw, clk, timeReq(&SprintTime{Goals: []GoalClaim{{Person: "cy", R: "1"}}}), nil)
	if ref := out[PartSprint].refusal; ref == nil || ref.Code != CodeWrongType {
		t.Fatalf("a goal record of another type: %v", ref)
	}
}

// TestSprintPartUnarmsBehind (2.3 R18, "armed again with the new backlog"):
// R18's re-arm removes behind_n, and the next tick end arms both, the entry at
// R + 5 min and behind_n at the backlog it finds, so an armed 1000 that fires
// on 999 is 999 after the next tick end. The Lua half gives the same reply and
// commands; an unarm with behind_n unset writes nothing.
func TestSprintPartUnarmsBehind(t *testing.T) {
	t.Parallel()
	tw, _, _, clk := partsTwin(t)
	fleetSeed(t, tw)
	h := newLuaParts(t)
	mustStep(t, tw, sprintReq(&SprintPart{TickEnd: &TickEnd{Backlog: "1000"}}))
	if n := tw.SprintKeys()[ek("tick")].Hash["behind_n"]; n != "1000" {
		t.Fatalf("armed at %q", n)
	}
	seed(tw, Command("ZREM", ek("due"), kindZSet, "behind")) // the pop takes the entry
	unarm := timeReq(&SprintTime{UnarmBehind: true})
	out, _ := diffParts(t, h, tw, clk, unarm, nil)
	if plan, _ := out[PartSprint].plan.(map[string]any); out[PartSprint].refusal != nil || plan["unarmed"] != true {
		t.Fatalf("the unarm: %+v %v", plan, out[PartSprint].refusal)
	}
	mustStep(t, tw, unarm)
	if _, set := tw.SprintKeys()[ek("tick")].Hash["behind_n"]; set {
		t.Fatalf("behind_n after the unarm: %v", tw.SprintKeys()[ek("tick")].Hash)
	}
	out, _ = diffParts(t, h, tw, clk, unarm, nil)
	if plan, _ := out[PartSprint].plan.(map[string]any); out[PartSprint].refusal != nil || plan["unarmed"] != nil {
		t.Fatalf("an unarm of nothing: %+v %v", plan, out[PartSprint].refusal)
	}
	mustStep(t, tw, sprintReq(&SprintPart{TickEnd: &TickEnd{Backlog: "999"}}))
	keys := tw.SprintKeys()
	if n := keys[ek("tick")].Hash["behind_n"]; n != "999" {
		t.Fatalf("the tick end armed %q", n)
	}
	if _, there := keys[ek("due")].ZSet["behind"]; !there {
		t.Fatalf("the tick end armed no entry: %v", keys[ek("due")].ZSet)
	}
}
