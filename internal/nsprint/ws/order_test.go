package ws

import (
	"errors"
	"math/rand"
	"strings"
	"testing"
)

// orderFixture is five cards and the stop: a diamond a -> {b, c} -> d, and
// e sharing a path with d. By issue alone e (#45) would go first, before
// a (#50); the paths edge puts it after d (#40), the lower issue sharing
// its path.
func orderFixture() []OrderCard {
	return []OrderCard{
		{ID: "a", Issue: 50, Paths: []string{"internal/a"}},
		{ID: "b", Issue: 20, Paths: []string{"internal/b"}, Deps: []string{"a"}},
		{ID: "c", Issue: 30, Paths: []string{"internal/c"}, Deps: []string{"a", "other-stream:sentinel"}},
		{ID: "d", Issue: 40, Paths: []string{"./internal/x/"}, Deps: []string{"b", "c"}},
		{ID: "e", Issue: 45, Paths: []string{"internal/x/e.go"}},
		{ID: "s:sentinel", Sentinel: true},
	}
}

func orderIDs(o []Ordered) string {
	ids := make([]string, len(o))
	for i, c := range o {
		ids[i] = c.ID
	}
	return strings.Join(ids, " ")
}

func reasonLines(rs []Reason) string {
	var b []string
	for _, r := range rs {
		b = append(b, r.Card+" <- "+r.String())
	}
	return strings.Join(b, "\n")
}

// TestOrderIsStableAcrossShuffles: the diamond, the shared path and the
// stop give one order and one set of reasons, whatever the input's order.
func TestOrderIsStableAcrossShuffles(t *testing.T) {
	t.Parallel()
	const want = "a b c d e s:sentinel"
	wantReasons := strings.Join([]string{
		"b <- a (reason: depends-on)",
		"c <- a (reason: depends-on)",
		"c <- b (reason: issue)",
		"d <- b (reason: depends-on)",
		"d <- c (reason: depends-on)",
		"e <- d (reason: paths internal/x)",
		"s:sentinel <- every other card (reason: sentinel)",
	}, "\n")
	rng := rand.New(rand.NewSource(4322))
	for k := 0; k < 200; k++ {
		cards := orderFixture()
		rng.Shuffle(len(cards), func(i, j int) { cards[i], cards[j] = cards[j], cards[i] })
		got, reasons, err := Order(cards)
		if err != nil {
			t.Fatal(err)
		}
		if orderIDs(got) != want {
			t.Fatalf("shuffle %d: order %q, want %q", k, orderIDs(got), want)
		}
		for i, c := range got {
			if c.Rank != i+1 {
				t.Fatalf("rank of %s is %d at %d", c.ID, c.Rank, i)
			}
		}
		if reasonLines(reasons) != wantReasons {
			t.Fatalf("shuffle %d: reasons\n%s\nwant\n%s", k, reasonLines(reasons), wantReasons)
		}
	}
	// Without the paths edge the tie-break alone would put e first.
	cards := orderFixture()
	cards[4].Paths = []string{"internal/e"}
	got, _, err := Order(cards)
	if err != nil || orderIDs(got) != "e a b c d s:sentinel" {
		t.Fatalf("no shared path: %q %v", orderIDs(got), err)
	}
}

// TestOrderRefusesACycleByName: b <- d <- b is refused, the cycle named
// from its least card, and nothing is ordered.
func TestOrderRefusesACycleByName(t *testing.T) {
	t.Parallel()
	cards := orderFixture()
	cards[1].Deps = []string{"a", "d"} // b now waits on d, which waits on b
	got, reasons, err := Order(cards)
	var ce *CycleError
	if !errors.As(err, &ce) || got != nil || reasons != nil {
		t.Fatalf("got %v %v %v, want a CycleError", got, reasons, err)
	}
	if err.Error() != "DEPENDS-ON cycle b -> d -> b" {
		t.Fatalf("error %q", err)
	}
}

// TestOrderMutationFlipsExactlyTheEdge: flipping d <- c to c <- d moves c
// after d and changes nothing else; a paths edge that DEPENDS-ON
// contradicts is dropped (DEPENDS-ON wins).
func TestOrderMutationFlipsExactlyTheEdge(t *testing.T) {
	t.Parallel()
	cards := orderFixture()
	cards[2].Deps = []string{"a", "d"}
	cards[3].Deps = []string{"b"}
	got, reasons, err := Order(cards)
	if err != nil {
		t.Fatal(err)
	}
	if orderIDs(got) != "a b d c e s:sentinel" {
		t.Fatalf("flipped: %q", orderIDs(got))
	}
	if !strings.Contains(reasonLines(reasons), "c <- d (reason: depends-on)") {
		t.Fatalf("reasons\n%s", reasonLines(reasons))
	}
	// d (#40) depends on e (#45) though they share a path: e goes first.
	cards = orderFixture()
	cards[3].Deps = []string{"b", "c", "e"}
	got, reasons, err = Order(cards)
	if err != nil || orderIDs(got) != "e a b c d s:sentinel" {
		t.Fatalf("depends-on over paths: %q %v", orderIDs(got), err)
	}
	if strings.Contains(reasonLines(reasons), "paths") {
		t.Fatalf("a contradicted paths edge was kept:\n%s", reasonLines(reasons))
	}
}

func TestIssueOf(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]int{
		"mas-bandwidth/nova-tools#4322": 4322,
		"#7":                            7,
		"forge/o/r/issues/9":            9,
		"forge/o/r/pull/12/":            12,
		"":                              0,
		"nova-tools-4322":               0,
	} {
		if got := IssueOf("", in); got != want {
			t.Errorf("IssueOf(%q) = %d, want %d", in, got, want)
		}
	}
}

// TestOrderBaseSkipsTheSentinel is the #4322 fix round's item (1) as a
// class test: the score base is the oldest live card that is not the
// sentinel (the sentinel, registered with the stream's first push, is
// older than every card and never the base); a stream whose one live card
// is its sentinel orders to [sentinel] with no error and takes the
// sentinel's age.
func TestOrderBaseSkipsTheSentinel(t *testing.T) {
	created := map[string]float64{"s:sentinel": 1000, "a": 0, "b": 9400, "c": 9500, "d": 9600, "e": 9450}
	cards := orderFixture()[1:] // a landed: b c d e and the stop
	out, _, err := Order(cards)
	if err != nil {
		t.Fatal(err)
	}
	if got := Base(out, func(id string) float64 { return created[id] }); got != 9400 {
		t.Fatalf("base %v, want b's created_at 9400 (not the sentinel's 1000)", got)
	}
	stop, _, err := Order([]OrderCard{{ID: "s:sentinel", Sentinel: true}})
	if err != nil || orderIDs(stop) != "s:sentinel" {
		t.Fatalf("sentinel only: %q %v", orderIDs(stop), err)
	}
	if got := Base(stop, func(id string) float64 { return created[id] }); got != 1000 {
		t.Fatalf("sentinel-only base %v, want its own 1000", got)
	}
	if got := Base(nil, nil); got != 0 {
		t.Fatalf("empty base %v", got)
	}
}

// TestStaleReadsScoresNotJustTheSequence: the same sequence with a base
// gone stale (the oldest card landed), a record with no order_score, or a
// hand score is Stale; the written order is not; a cycle is never Stale.
func TestStaleReadsScoresNotJustTheSequence(t *testing.T) {
	order := []Ordered{{OrderCard: OrderCard{ID: "b"}, Rank: 1}, {OrderCard: OrderCard{ID: "s:sentinel", Sentinel: true}, Rank: 2}}
	fresh := func() StreamOrder {
		return StreamOrder{Order: order, Scores: []float64{9400, 9401},
			StoredScore: map[string]float64{"b": 9400, "s:sentinel": 9401},
			OrderScore:  map[string]float64{"b": 9400, "s:sentinel": 9401}}
	}
	if so := fresh(); so.Stale() {
		t.Fatal("the written order read as stale")
	}
	so := fresh()
	so.StoredScore["b"], so.OrderScore["b"], so.StoredScore["s:sentinel"], so.OrderScore["s:sentinel"] = 1001, 1001, 1002, 1002
	if !so.Stale() || so.Drift() {
		t.Fatalf("a stale base: stale %v drift %v", so.Stale(), so.Drift())
	}
	so = fresh()
	delete(so.OrderScore, "b")
	if !so.Stale() {
		t.Fatal("a record with no order_score read as ordered")
	}
	so = fresh()
	so.StoredScore["b"] = 9400.5
	if !so.Stale() {
		t.Fatal("a hand score read as ordered")
	}
	so.Err = &CycleError{Cycle: []string{"x", "y", "x"}}
	if so.Stale() {
		t.Fatal("a cycle read as stale")
	}
}

// TestStreamOfTitleIsTKStreamOf: --stream wins, else the title's
// "STREAM: <s> |" prefix, else none (TK.stream_of's rule).
func TestStreamOfTitleIsTKStreamOf(t *testing.T) {
	for _, c := range []struct{ stream, title, want string }{
		{"s1", "STREAM: s2 | x", "s1"},
		{"", "  STREAM:  q: order  | fix it", "q: order"},
		{"", "STREAM: | x", ""},
		{"", "no stream here", ""},
		{"", "STREAM: s without bar", ""},
	} {
		if got := StreamOfTitle(c.stream, c.title); got != c.want {
			t.Errorf("StreamOfTitle(%q, %q) = %q, want %q", c.stream, c.title, got, c.want)
		}
	}
}

// TestScoreTextKeepsTheFraction: ws check's INVARIANTS lines print a
// score with every digit it has (the cold read's minor: %.0f hid 1.5).
func TestScoreTextKeepsTheFraction(t *testing.T) {
	for in, want := range map[float64]string{1.5: "1.5", 1758900000123: "1758900000123", 0: "0"} {
		if got := ScoreText(in); got != want {
			t.Errorf("ScoreText(%v) = %q, want %q", in, got, want)
		}
	}
}
