package sprintfn

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// TestTwinKeyRangeReadsSprintKeys: Layer 1's range in its raw key form reads the
// sprint's own sorted sets from the twin (L1 7; the agenda's and the held queue's
// heads of RT1, 1.4.2): members by score then member, the limit with has_more,
// Desc from the highest, and REQUEST for records, a limit below one or a bad
// bound, LIMIT over 2,000, WRONGTYPE on a key of another type.
func TestTwinKeyRangeReadsSprintKeys(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	h.write(Command("ZADD", xp+"agenda@0", kindZSet, "3", "deal", "1", "needs:p1", "3", "ask:p2", "7", "resolve:s1"),
		Command("SET", xp+"coordinator", kindString, "me"))
	read := func(q tset.ReadQuery) Result {
		t.Helper()
		res, err := Read(context.Background(), h.tw, &ReadRequest{Epoch: "0", Tset: []tset.ReadQuery{q}})
		var ie *ItemError
		if errors.As(err, &ie) {
			return Result{Refusal: ie.Refusal} // refused before dispatch, by the read's static check
		}
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := read(tset.ReadQuery{Kind: "range", Key: xp + "agenda@0", Min: "-inf", Max: "+inf", Limit: 3})
	if res.Read == nil {
		t.Fatalf("refused: %+v", res.Refusal)
	}
	a := res.Read.Tset[0]
	if !reflect.DeepEqual(a.IDs, []string{"needs:p1", "ask:p2", "deal"}) || !reflect.DeepEqual(a.Scores, []string{"1", "3", "3"}) || !a.HasMore {
		t.Fatalf("head %+v", a)
	}
	a = read(tset.ReadQuery{Kind: "range", Key: xp + "agenda@0", Min: "(3", Max: "+inf", Limit: 5, Desc: true}).Read.Tset[0]
	if !reflect.DeepEqual(a.IDs, []string{"resolve:s1"}) || a.HasMore {
		t.Fatalf("desc above 3: %+v", a)
	}
	a = read(tset.ReadQuery{Kind: "range", Key: xp + "heldq@0", Min: "-inf", Max: "+inf", Limit: 5}).Read.Tset[0]
	if len(a.IDs) != 0 || a.HasMore {
		t.Fatalf("an absent key: %+v", a)
	}
	for _, q := range []tset.ReadQuery{
		{Kind: "range", Key: xp + "agenda@0", Min: "-inf", Max: "+inf", Limit: 0},
		{Kind: "range", Key: xp + "agenda@0", Min: "x", Max: "+inf", Limit: 1},
		{Kind: "range", Key: xp + "agenda@0", Min: "-inf", Max: "+inf", Limit: 1, Records: true},
	} {
		if r := read(q); r.Refusal == nil || r.Refusal.Code != CodeRequest {
			t.Fatalf("%+v: %+v, want REQUEST", q, r)
		}
	}
	if r := read(tset.ReadQuery{Kind: "range", Key: xp + "agenda@0", Min: "-inf", Max: "+inf", Limit: 2001}); r.Refusal == nil || r.Refusal.Code != CodeLimit {
		t.Fatalf("a limit over 2,000: %+v, want LIMIT", r)
	}
	if r := read(tset.ReadQuery{Kind: "range", Key: xp + "coordinator", Min: "-inf", Max: "+inf", Limit: 1}); r.Refusal == nil || r.Refusal.Code != CodeWrongType {
		t.Fatalf("a string key: %+v, want WRONGTYPE", r)
	}
}

// TestEncodedSizeIsTheSentBytes: EncodedSize is both halves as sent, and a
// request the static checks refuse has no size.
func TestEncodedSizeIsTheSentBytes(t *testing.T) {
	t.Parallel()
	req := seedRequest()
	n, ref := EncodedSize(testPrefix, req)
	enc, ref2 := encodeStep(testPrefix, req)
	if ref != nil || ref2 != nil || n != len(enc.raw)+len(enc.sprint) || n == 0 {
		t.Fatalf("size %d (%v), encoded %d+%d (%v)", n, ref, len(enc.raw), len(enc.sprint), ref2)
	}
	if _, ref := EncodedSize(testPrefix, &Request{Epoch: "x"}); ref == nil || ref.Code != CodeRequest {
		t.Fatalf("a bad epoch: %v", ref)
	}
}
