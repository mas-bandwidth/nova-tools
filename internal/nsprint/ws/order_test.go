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
