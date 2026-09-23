package dealer

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// fixedRouter answers one route for every card, or an error for the kinds it names.
type fixedRouter struct{ refuse map[string]bool }

func (r fixedRouter) Route(c Card) (string, string, error) {
	if r.refuse[c.Kind] {
		return "", "", fmt.Errorf("no row for kind %q", c.Kind)
	}
	return "route-a", "model-a", nil
}

func writeCard(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func readCards(t *testing.T, dir string, priority bool) []Card {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join(dir, "card-*.md"))
	sort.Strings(paths)
	var out []Card
	for _, p := range paths {
		c, err := ReadCard(p, priority)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

func names(dir string) []string {
	paths, _ := filepath.Glob(filepath.Join(dir, "card-*.md"))
	var out []string
	for _, p := range paths {
		out = append(out, filepath.Base(p))
	}
	sort.Strings(out)
	return out
}

// TestDealerNeverDealsANotReadyCard is the rule this package exists for: every card that
// fails a readiness decision stays in the pool, and every card that reaches a bench's
// ready queue passed all of them and carries its ROUTE: and MODEL:.
func TestDealerNeverDealsANotReadyCard(t *testing.T) {
	dir := t.TempDir()
	pool := filepath.Join(dir, "pool")
	writeCard(t, pool, "card-001.md", "KIND: fix\nDEPENDS-ON: -\nLEG: go\n")                    // ready: "-" is none
	writeCard(t, pool, "card-002.md", "KIND: fix\nDEPENDS-ON: card-900\nLEG: go\n")             // parent not landed
	writeCard(t, pool, "card-003.md", "KIND: fix\nDEPENDS-ON: card-901\nLEG: go\n")             // parent landed: ready
	writeCard(t, pool, "card-004.md", "KIND: fix\nLEG: squirrel\n")                             // no bench carries the leg
	writeCard(t, pool, "card-005.md", "KIND: fix\nLANE: pulse\n")                               // lane live elsewhere
	writeCard(t, pool, "card-006.md", "KIND: fix\nLANE: merge\n")                               // ready, takes lane merge
	writeCard(t, pool, "card-007.md", "KIND: fix\nLANE: merge\n")                               // lane merge dealt this pass
	writeCard(t, pool, "card-008.md", "KIND: unroutable\n")                                     // no route
	writeCard(t, pool, "card-009.md", "KIND: fix\nLEG: go\nROUTE: pinned-r\nMODEL: pinned-m\n") // ready, keeps its pin
	cards := readCards(t, pool, false)
	benches := []Bench{
		{Name: "hot", Up: true, Legs: map[string]bool{"go": true}, Slots: 10, Load1: 40, Cores: 8, LoadRead: true}, // over the ceiling
		{Name: "down", Up: false, Legs: map[string]bool{"go": true}, Slots: 10},
		{Name: "full", Up: true, Legs: map[string]bool{"go": true}, Slots: 4, Working: 3, Queued: 1, Load1: 1, Cores: 8, LoadRead: true},
		{Name: "ok", Up: true, Legs: map[string]bool{"go": true}, Slots: 8, Working: 2, Load1: 2, Cores: 8, LoadRead: true},
	}
	deps := pulse.MapDependencyChecker{"card-900": false, "card-901": true}
	plan := PlanDeal(cards, benches, map[string]string{"pulse": "card-100.md"},
		Policy{MaxLoadPerCore: 1.5}, deps, fixedRouter{refuse: map[string]bool{"unroutable": true}})

	ready := func(b string) string { return filepath.Join(dir, "bench", b, "ready") }
	if _, err := Apply(plan, ready); err != nil {
		t.Fatal(err)
	}
	if got, want := names(ready("ok")), []string{"card-001.md", "card-003.md", "card-006.md", "card-009.md"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("bench ok ready = %v, want %v\nheld: %+v", got, want, plan.Held)
	}
	for _, b := range []string{"hot", "down", "full"} {
		if got := names(ready(b)); len(got) != 0 {
			t.Fatalf("bench %s (not dealable) was dealt %v", b, got)
		}
	}
	if got, want := names(pool), []string{"card-002.md", "card-004.md", "card-005.md", "card-007.md", "card-008.md"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("pool = %v, want the five not-ready cards %v", got, want)
	}
	reasons := map[string]string{}
	for _, h := range plan.Held {
		reasons[h.Card.Name] = h.Reason
	}
	for name, want := range map[string]string{
		"card-002.md": "dependency card-900 not landed",
		"card-004.md": `carries leg "squirrel"`,
		"card-005.md": "lane pulse is live on card-100.md",
		"card-007.md": "lane merge is live on card-006.md",
		"card-008.md": "no route",
	} {
		if !strings.Contains(reasons[name], want) {
			t.Fatalf("%s held for %q, want it to say %q", name, reasons[name], want)
		}
	}
	// Every dealt card carries the dealer's pick; a pinned pick is kept.
	for _, n := range names(ready("ok")) {
		raw, _ := os.ReadFile(filepath.Join(ready("ok"), n))
		if missing := pulse.MissingRoute(filepath.Join(ready("ok"), n)); missing != "" {
			t.Fatalf("%s reached ready without %s:\n%s", n, missing, raw)
		}
		if strings.Count(string(raw), "ROUTE:") != 1 || strings.Count(string(raw), "MODEL:") != 1 {
			t.Fatalf("%s carries more than one route line:\n%s", n, raw)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(ready("ok"), "card-009.md"))
	if !strings.Contains(string(raw), "ROUTE: pinned-r\nMODEL: pinned-m") {
		t.Fatalf("a pinned route was re-picked:\n%s", raw)
	}
}

// TestDealerBackpressureHoldsBulkAndLetsPriorityFlow: over the debt cap, bulk waits and
// priority cards are still dealt, first.
func TestDealerBackpressureHoldsBulkAndLetsPriorityFlow(t *testing.T) {
	bulk := Card{Name: "card-001.md", Kind: "fix"}
	front := Card{Name: "card-00-fix.md", Kind: "fix", Priority: true}
	benches := []Bench{{Name: "b", Up: true, Slots: 4}}
	plan := PlanDeal([]Card{bulk, front}, benches, nil, Policy{ReadingDebt: 120, DebtCap: 100}, nil, fixedRouter{})
	if len(plan.Deals) != 1 || plan.Deals[0].Card.Name != "card-00-fix.md" {
		t.Fatalf("deals = %+v, want only the priority card", plan.Deals)
	}
	if len(plan.Held) != 1 || !strings.Contains(plan.Held[0].Reason, "backpressure") {
		t.Fatalf("held = %+v, want the bulk card held for backpressure", plan.Held)
	}
	plan = PlanDeal([]Card{bulk, front}, benches, nil, Policy{ReadingDebt: 50, DebtCap: 100}, nil, fixedRouter{})
	if len(plan.Deals) != 2 || plan.Deals[0].Card.Name != "card-00-fix.md" {
		t.Fatalf("under the cap deals = %+v, want both, priority first", plan.Deals)
	}
}

// TestDealerFillsToRoomAndPrefersTheMostRoom: a bench is dealt no more than its slots minus
// its live and queued cards, and each card goes where the most room is.
func TestDealerFillsToRoomAndPrefersTheMostRoom(t *testing.T) {
	var cards []Card
	for i := 1; i <= 6; i++ {
		cards = append(cards, Card{Name: fmt.Sprintf("card-%03d.md", i)})
	}
	benches := []Bench{
		{Name: "small", Up: true, Slots: 2},
		{Name: "big", Up: true, Slots: 5, Working: 1, Queued: 1},
	}
	plan := PlanDeal(cards, benches, nil, Policy{}, nil, fixedRouter{})
	per := map[string]int{}
	for _, d := range plan.Deals {
		per[d.Bench]++
	}
	if per["small"] != 2 || per["big"] != 3 || len(plan.Held) != 1 {
		t.Fatalf("per bench = %v held=%d, want small=2 big=3 and one held", per, len(plan.Held))
	}
	if plan.Deals[0].Bench != "big" {
		t.Fatalf("first card went to %s, want big (3 free against 2)", plan.Deals[0].Bench)
	}
}

// TestDealerCeilingNeedsAMeasuredLoad: with a ceiling set, a bench whose row carried no load
// is dealt nothing; with none set, it is dealt.
func TestDealerCeilingNeedsAMeasuredLoad(t *testing.T) {
	b := BenchFromRows("b", true, map[string]string{"working": "1"}, map[string]string{"slots": "4", "legs": "go, Lua"})
	if !b.Legs["go"] || !b.Legs["lua"] || b.Slots != 4 || b.Working != 1 || b.LoadRead {
		t.Fatalf("BenchFromRows = %+v", b)
	}
	c := []Card{{Name: "card-001.md", Leg: "lua"}}
	if plan := PlanDeal(c, []Bench{b}, nil, Policy{MaxLoadPerCore: 1.5}, nil, fixedRouter{}); len(plan.Deals) != 0 {
		t.Fatalf("an unmeasured bench was dealt under a ceiling: %+v", plan.Deals)
	}
	if plan := PlanDeal(c, []Bench{b}, nil, Policy{}, nil, fixedRouter{}); len(plan.Deals) != 1 {
		t.Fatalf("no ceiling, yet the bench was not dealt: %+v", plan.Held)
	}
	b = BenchFromRows("b", true, map[string]string{"load1": "4.0", "ncpu": "8"}, map[string]string{"slots": "4"})
	if !b.LoadRead || b.Load1 != 4 || b.Cores != 8 {
		t.Fatalf("load not read from the row: %+v", b)
	}
}

// TestTableRouterPicksByKindAndIsStable: the kind's rows first, then "*"; the same card
// always gets the same row.
func TestTableRouterPicksByKindAndIsStable(t *testing.T) {
	p := filepath.Join(t.TempDir(), "routes.tsv")
	if err := os.WriteFile(p, []byte("# kind\troute\tmodel\nread\tcheap\tsonnet\n*\tpro\topus\n*\tpro2\tkimi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := ReadRouteTable(p)
	if err != nil {
		t.Fatal(err)
	}
	if route, model, _ := r.Route(Card{Name: "card-1.md", Kind: "read"}); route != "cheap" || model != "sonnet" {
		t.Fatalf("read card routed %s/%s, want cheap/sonnet", route, model)
	}
	a1, _, _ := r.Route(Card{Name: "card-7.md", Kind: "fix"})
	a2, _, _ := r.Route(Card{Name: "card-7.md", Kind: "fix"})
	if a1 != a2 || (a1 != "pro" && a1 != "pro2") {
		t.Fatalf("fix card routed %q then %q", a1, a2)
	}
	if _, _, err := (TableRouter{}).Route(Card{Name: "x"}); err == nil {
		t.Fatal("an empty table picked a route")
	}
}
