package route

import (
	"strings"
	"testing"
)

func foldFixture() *Fold {
	return &Fold{
		Type: "fix", Kind: KindPR, Metric: MetricLanded, Sprint: "s1", FoldSHA: "0123456789abcdef", At: "2026-09-23T09:00:00Z",
		ProbationShare: "0.1", RouteMinLanded: 3, RouteBenchAfter: 15,
		Routes: []FoldRoute{
			{Route: "ordspro", Status: StatusIn, Metric: "5", Cards: 3, PRs: 3, Landed: 3, Useful: 3, USD: "15", Reason: "landed 3 of 3"},
			{Route: "orhaiku", Status: StatusProbation, Metric: "2", Cards: 1, PRs: 1, Landed: 1, Useful: 1, USD: "2", Reason: "landed 1 of 1, under route_min_landed 3"},
			{Route: "ormuse", Status: StatusIn, Metric: "1", Cards: 3, PRs: 3, Landed: 3, Useful: 3, USD: "3", Reason: "landed 3 of 3"},
			{Route: "ocglm53", Status: StatusBenched, Metric: "-", Cards: 40, PRs: 40, USD: "0.4", Reason: "landed 0 of 40"},
		},
	}
}

func hashOf(f *Fold) map[string]string {
	kv := f.Fields()
	h := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		h[kv[i]] = kv[i+1]
	}
	return h
}

// The hash the fold writes reads back as the same ranking.
func TestFoldFieldsRoundTrip(t *testing.T) {
	t.Parallel()

	f := foldFixture()
	got, err := ParseFold("fix", hashOf(f))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Order(), " ") != "ordspro orhaiku ormuse" || strings.Join(got.Benched(), " ") != "ocglm53" {
		t.Fatalf("order %v benched %v", got.Order(), got.Benched())
	}
	if r, _ := got.Route("ocglm53"); r != f.Routes[3] {
		t.Fatalf("benched row %+v, want %+v", r, f.Routes[3])
	}
	if got, err := ParseFold("fix", nil); got != nil || err != nil {
		t.Fatalf("an absent key is no fold: %v %v", got, err)
	}
}

// A record the fold would not write is refused, so the router fails closed.
func TestParseFoldRefusesADamagedRecord(t *testing.T) {
	t.Parallel()

	for name, edit := range map[string]func(map[string]string){
		"other type":           func(h map[string]string) { h["type"] = "nx" },
		"read kind on landed":  func(h map[string]string) { h["kind"] = KindRead },
		"no fold sha":          func(h map[string]string) { delete(h, "fold_sha") },
		"benched in the order": func(h map[string]string) { h["routes"] += " ocglm53"; h["benched"] = "" },
		"in on the bench list": func(h map[string]string) { h["ocglm53.status"] = StatusIn },
		"count not a count":    func(h map[string]string) { h["ordspro.landed"] = "three" },
	} {
		h := hashOf(foldFixture())
		edit(h)
		if f, err := ParseFold("fix", h); err == nil {
			t.Errorf("%s: parsed %+v, want a refusal", name, f)
		}
	}
}

// The static table keeps the last word on rung and on a dead route; no fold
// for the type is the static check alone.
func TestCheckFoldKeepsRungAndDeadAndFallsBack(t *testing.T) {
	t.Parallel()

	tab, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	f := foldFixture()
	if err := tab.CheckFold(Card{Rung: "pro", Type: "fix", Route: "ormuse"}, f); err == nil || !strings.Contains(err.Error(), "is dead") {
		t.Errorf("a dead route the fold ranks in: %v, want REFUSED dead", err)
	}
	if err := tab.CheckFold(Card{Rung: "flash", Type: "fix", Route: "ordspro"}, f); err == nil || !strings.Contains(err.Error(), "rung pro route") {
		t.Errorf("a pro route on a flash card: %v, want REFUSED rung", err)
	}
	if err := tab.CheckFold(Card{Rung: "pro", Type: "fix", Route: "orhaiku"}, f); err != nil {
		t.Errorf("a probation route: %v, want allowed (the cap is the dealer's share)", err)
	}
	if err := tab.CheckFold(Card{Rung: "pro", Type: "nx", Route: "ordspro"}, f); err == nil {
		t.Errorf("a fix record checked for an nx card: allowed, want REFUSED")
	}
	if a, b := tab.CheckFold(Card{Rung: "pro", Type: "fix", Route: "ordspro"}, nil), tab.Check(Card{Rung: "pro", Type: "fix", Route: "ordspro"}); a == nil || b == nil || a.Error() != b.Error() {
		t.Errorf("no fold: CheckFold %v, Check %v; want the same refusal", a, b)
	}
	if got := strings.Join(tab.AllowedFold("pro", "fix", f), " "); got != "ordspro orhaiku" {
		t.Errorf("AllowedFold(pro, fix) = [%s], want [ordspro orhaiku]: benched and dead left out", got)
	}
}
