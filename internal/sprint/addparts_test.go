package sprint

import (
	"math"
	"strings"
	"testing"
)

// The pure half of the add in parts (IT19): the reservation, the layout of an
// op's items, the insertion's scores, the request's own refusals and the early
// cycle check. The verbs' tests (internal/sprint/verbs, add_test.go) drive the
// same functions on the twin.

// TestReserveCountStreams: part 1 reserves score .. score + n + gates - 1 over
// every stream named, each stream's ids and gates from the counter's id:<s>
// and gate:<s>, and raises each field past what it reserved (1.5.4); a stream
// the add opens raises streams (3).
func TestReserveCountStreams(t *testing.T) {
	t.Parallel()
	n, err := ParseNext(NextFields([]string{"a", "b"}), map[string]string{"score": "41", "streams": "7", "id:a": "10", "gate:a": "3"})
	if err != nil {
		t.Fatal(err)
	}
	res := Reserve(n, AddReq{Stream: "a,b", Count: 25, Every: 10})
	if res.Base != 41 || res.Total != 2*(25+2) {
		t.Fatalf("base %d, total %d; want 41 and 54", res.Base, res.Total)
	}
	res.Open(n, []string{"b"})
	want := map[string]string{"score": "95", "id:a": "35", "gate:a": "5", "id:b": "26", "gate:b": "3", "streams": "8"}
	for f, v := range want {
		if res.Counter[f] != v {
			t.Fatalf("the counter's %s is set to %q, want %q (all: %v)", f, res.Counter[f], v, res.Counter)
		}
	}
	if len(res.Counter) != len(want) {
		t.Fatalf("the counter change %v sets more than %v", res.Counter, want)
	}
	// Named ids reserve the score alone; an absent counter starts at 1.
	named := Reserve(Next{}, AddReq{Stream: "a", IDs: []string{"x", "y"}})
	if named.Base != 1 || named.Counter["score"] != "3" || len(named.Counter) != 1 {
		t.Fatalf("named: base %d, counter %v", named.Base, named.Counter)
	}
	// An insertion reserves no integer (U2).
	if ins := Reserve(n, AddReq{Stream: "a", IDs: []string{"x"}, After: "p"}); !ins.Insert || len(ins.Counter) != 0 {
		t.Fatalf("an insertion reserved %v", ins.Counter)
	}
	for id, want := range map[string]bool{"a-10": true, "a-34": true, "a-35": false, "a-9": false, "b-1": true, "b-25": true,
		"b-26": false, "a-gate-3": false, "c-1": false} {
		if got := res.Makes(AddReq{Stream: "a,b", Count: 25, Every: 10}, id); got != want {
			t.Fatalf("Makes(%s) = %v, want %v", id, got, want)
		}
	}
	if _, err := ParseNext([]string{"score"}, map[string]string{"score": "1.5"}); err == nil {
		t.Fatal("a counter that is not a whole number was taken")
	}
}

// TestAddItemLayout: a gate follows every Every cards of each stream, none
// after the last unless Last, and the items' scores are the reservation's in
// order; the part size leaves room for the control cards part 1 creates.
func TestAddItemLayout(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		r    AddReq
		want string
	}{
		{AddReq{Stream: "s", Count: 5, Every: 2}, "s-1 s-2 s-gate-1 s-3 s-4 s-gate-2 s-5"},
		{AddReq{Stream: "s", Count: 4, Every: 2}, "s-1 s-2 s-gate-1 s-3 s-4"},
		{AddReq{Stream: "s", Count: 4, Every: 2, Last: true}, "s-1 s-2 s-gate-1 s-3 s-4 s-gate-2"},
		{AddReq{Stream: "s,t", Count: 2}, "s-1 s-2 t-1 t-2"},
	} {
		res := Reserve(Next{Score: 100}, c.r)
		var got []string
		for j := 0; j < res.Total; j++ {
			it := res.Item(c.r, j, nil)
			if it.Score != float64(100+j) {
				t.Fatalf("%+v: item %d at %v", c.r, j, it.Score)
			}
			got = append(got, it.ID)
		}
		if strings.Join(got, " ") != c.want {
			t.Fatalf("%+v: %q, want %q", c.r, strings.Join(got, " "), c.want)
		}
	}
	res := Reserve(Next{}, AddReq{Stream: "s", Count: 5000})
	res.Open(Next{}, []string{"s"})
	res.Chunk = 2000
	if a, b, c := res.PartSize(0), res.PartSize(1999), res.PartSize(3999); a != 1999 || b != 2000 || c != 1001 {
		t.Fatalf("part sizes %d, %d, %d; want 1,999 (one control card), 2,000, 1,001", a, b, c)
	}
}

// TestInsertScoresSkipIntegers: an insertion's scores lie strictly between its
// bounds, increasing, none an integer (U2: an integer is skipped by the
// midpoint of it and the value below it); with no room, ErrNoRoom.
func TestInsertScoresSkipIntegers(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		lo, hi float64
		n      int
	}{{1, 2, 3}, {3, 10, 6}, {1, 3, 1}, {0.5, 0.75, 100}, {-2, 5, 13}} {
		sc, err := InsertScores(c.lo, c.hi, c.n)
		if err != nil {
			t.Fatalf("%v: %v", c, err)
		}
		prev := c.lo
		for _, v := range sc {
			if !(v > prev && v < c.hi) || v == math.Trunc(v) {
				t.Fatalf("%v: %v", c, sc)
			}
			prev = v
		}
	}
	if _, err := InsertScores(1.5, math.Nextafter(1.5, 2), 1); err == nil {
		t.Fatal("room was found between two adjacent floats")
	}
}

// TestCheckAddRefusals: the request's own faults are refused before anything
// is read, each naming what to fix.
func TestCheckAddRefusals(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		r    AddReq
		word string
	}{
		{AddReq{IDs: []string{"a"}}, "no stream"},
		{AddReq{Stream: "s,s", Count: 1}, "twice"},
		{AddReq{Stream: "s", IDs: []string{"a"}, Count: 2}, "one or the other"},
		{AddReq{Stream: "s"}, "names no card"},
		{AddReq{Stream: "s,t", IDs: []string{"a"}}, "several streams"},
		{AddReq{Stream: "s", IDs: []string{"a"}, Every: 2}, "go with --count"},
		{AddReq{Stream: "s", Count: 2, Last: true}, "--sentinel-last goes with"},
		{AddReq{Stream: "s", Count: 2, After: "x"}, "--count adds at the end"},
		{AddReq{Stream: "s", IDs: []string{"a", "b"}, Sentinel: true}, "one at a time"},
		{AddReq{Stream: "s", IDs: []string{"a"}, Needs: make([]string, 65)}, "at most 64"},
		{AddReq{Stream: "s", IDs: []string{"a", "a"}}, "twice"},
		{AddReq{Stream: "s", IDs: []string{"a"}, Needs: []string{"a"}}, "cycle"},
		{AddReq{Stream: "s", IDs: []string{"ctl-a"}}, "ctl-"},
		{AddReq{Stream: "s", IDs: []string{"a"}, Before: "a"}, "anchor"},
	} {
		err := CheckAdd(c.r)
		if err == nil || !strings.Contains(err.Error(), c.word) {
			t.Fatalf("%+v: %v, want a refusal naming %q", c.r, err, c.word)
		}
	}
	if err := CheckAdd(AddReq{Stream: "s,t", Count: 3, Every: 1, Last: true, Needs: []string{"n"}}); err != nil {
		t.Fatal(err)
	}
}

// TestAddCycleCut: a card the walk reached that names a new id closes a cycle;
// a walk cut at its bound is refused naming the bound and the head.
func TestAddCycleCut(t *testing.T) {
	t.Parallel()
	chain := []ChainCard{{ID: "n", Needs: []string{"m"}}, {ID: "m", Needs: []string{"w"}}}
	if why := AddCycle(chain, false, "n", func(id string) bool { return id == "w" }); !strings.Contains(why, "m needs w") {
		t.Fatalf("the cycle through m: %q", why)
	}
	if why := AddCycle(chain, true, "n", func(id string) bool { return id == "z" }); !strings.Contains(why, "2000") || !strings.Contains(why, "from n") {
		t.Fatalf("the cut walk: %q", why)
	}
	if why := AddCycle(chain, false, "n", func(id string) bool { return id == "z" }); why != "" {
		t.Fatalf("no cycle: %q", why)
	}
}
