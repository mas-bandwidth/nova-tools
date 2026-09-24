package route

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 22, 23, 42, 22, 0, time.UTC)

// The DONE-WHEN control for #2729 (rule 1): opencode's UnknownError, an
// err_xxxxxxxx id or a gateway 5xx is a provider error at any wall (3 s, 75 s,
// 1,502 s), not only at 15 s or less as the launcher had it; an attempt with
// no token and no error is its own class; a model that answered and failed
// is a card failure and never counts against the route.
func TestUnknownErrorAtAnyWallIsProvider(t *testing.T) {
	rows := readTails(t)
	walls := map[Class]int{}
	for _, r := range rows {
		got := Classify(r.a)
		if got != r.want {
			t.Errorf("wall=%s tokens=%d %q: class %s, want %s", r.a.Wall, r.a.Tokens, r.a.Tail, got, r.want)
		}
		walls[got]++
		if death := got.Death(); death != (r.want == ClassProvider || r.want == ClassNoTokenNoError) {
			t.Errorf("%q: Death()=%v for class %s", r.a.Tail, death, got)
		}
	}
	if walls[ClassProvider] != 6 || walls[ClassNoTokenNoError] != 1 || walls[ClassFail] != 3 {
		t.Fatalf("fixture classes %v, want 6 provider-error, 1 no-token-no-error, 3 fail", walls)
	}
	// A pass is a pass, whatever the tail quotes.
	if c := Classify(Attempt{Passed: true, Tail: `"name": "UnknownError"`}); c != ClassPass {
		t.Fatalf("a passed attempt classified %s", c)
	}
}

// Rule 2: more than 5 of the last 50 attempts dying as a provider error or
// with no token and no error benches the route, with a receipt; 5 of 50 does
// not; deaths that have left the 50-attempt window do not count; card
// failures never count.
func TestRouteBenchedAbove10PercentOverLast50(t *testing.T) {
	h := NewHealth()
	unknown := Attempt{Route: "ords41", Wall: 75 * time.Second, Tail: `"name": "UnknownError"`}
	silent := Attempt{Route: "ords41", Wall: 41 * time.Second}
	cardFail := Attempt{Route: "ords41", Wall: 5 * time.Second, Tokens: 380, Tail: "The run tool is not available."}
	pass := Attempt{Route: "ords41", Passed: true, Tokens: 9000}

	for i := 0; i < 44; i++ {
		record(t, h, pass, i)
	}
	for i := 0; i < 20; i++ { // card failures are the card's, not the route's
		record(t, h, cardFail, 44+i)
	}
	if h.Benched("ords41") {
		t.Fatal("card failures benched the route")
	}
	for i := 0; i < 5; i++ {
		a := unknown
		if i%2 == 1 {
			a = silent
		}
		if rc := record(t, h, a, 64+i); rc != "" {
			t.Fatalf("death %d of 50 benched the route: %s", i+1, rc)
		}
	}
	if d, w := h.DeathRate("ords41"); d != 5 || w != 50 {
		t.Fatalf("death rate %d/%d, want 5/50", d, w)
	}
	if h.Benched("ords41") {
		t.Fatal("5 of 50 (10%) benched the route; the rule is more than 10%")
	}
	rc := record(t, h, unknown, 69)
	if !h.Benched("ords41") {
		t.Fatal("6 of 50 did not bench the route")
	}
	for _, want := range []string{"ROUTE-BENCHED route=ords41", "rule=rate", "deaths=6/50", "class=provider-error"} {
		if !strings.Contains(rc, want) {
			t.Fatalf("receipt %q lacks %q", rc, want)
		}
	}
	if !strings.Contains(h.Report(), "HEALTH route=ords41 attempts=50 deaths=6/50 benched=true rule=rate") {
		t.Fatalf("report:\n%s", h.Report())
	}

	// The window is the last 50 attempts: five old deaths, then 50 passes, then one death.
	h2 := NewHealth()
	for i := 0; i < 5; i++ {
		record(t, h2, unknown, i)
	}
	for i := 0; i < 50; i++ {
		record(t, h2, pass, 5+i)
	}
	record(t, h2, silent, 55)
	if d, _ := h2.DeathRate("ords41"); d != 1 || h2.Benched("ords41") {
		t.Fatalf("aged deaths counted: %d/50 benched=%v", d, h2.Benched("ords41"))
	}
}

// Rule 3: a failed known-answer probe benches the route at once; card passes
// and the window ageing never unbench it; only a passing known-answer probe
// does, and it starts the route on a clean window.
func TestRouteBenchedAfterFailedKnownAnswerProbe(t *testing.T) {
	h := NewHealth()
	probe := Attempt{Route: "orgeminilite", KnownAnswer: true, Wall: 5 * time.Second, Tokens: 380,
		Tail: "I cannot fulfill this request. The run tool is not available."}
	rc := record(t, h, probe, 0)
	if !h.Benched("orgeminilite") || !strings.Contains(rc, "ROUTE-BENCHED route=orgeminilite rule=probe") {
		t.Fatalf("a failed known-answer probe did not bench: benched=%v receipt=%q", h.Benched("orgeminilite"), rc)
	}
	for i := 0; i < 60; i++ {
		record(t, h, Attempt{Route: "orgeminilite", Passed: true, Tokens: 100}, 1+i)
	}
	if !h.Benched("orgeminilite") {
		t.Fatal("card passes unbenched the route")
	}
	rc = record(t, h, Attempt{Route: "orgeminilite", KnownAnswer: true, Passed: true, Tokens: 100}, 61)
	if h.Benched("orgeminilite") || !strings.Contains(rc, "ROUTE-UNBENCHED route=orgeminilite rule=probe") {
		t.Fatalf("a passing known-answer probe did not unbench: receipt=%q", rc)
	}
	if d, _ := h.DeathRate("orgeminilite"); d != 0 {
		t.Fatalf("unbench left %d deaths in the window", d)
	}
	// A route benched by rate is unbenched the same way, and only that way.
	for i := 0; i < 6; i++ {
		record(t, h, Attempt{Route: "orqwen38", Tail: "err_4f1c09ab"}, 100+i)
	}
	if !h.Benched("orqwen38") {
		t.Fatal("six err_ ids did not bench orqwen38")
	}
	record(t, h, Attempt{Route: "orqwen38", KnownAnswer: true, Tail: `"name": "UnknownError"`}, 106)
	if !h.Benched("orqwen38") {
		t.Fatal("a failed probe unbenched orqwen38")
	}
	record(t, h, Attempt{Route: "orqwen38", KnownAnswer: true, Passed: true, Tokens: 50}, 107)
	if h.Benched("orqwen38") {
		t.Fatal("a passing probe did not unbench orqwen38")
	}
}

// Rule 4 (the issue's control): replay the 2026-09-22 provider-incomplete
// cards; the next attempt never lands on the route that left the card
// incomplete, never on a benched route, and always on a route the #2895 table
// allows for the card's rung; the ROUTE line carries avoid=.
func TestRedealAvoidsTheFailedRoute(t *testing.T) {
	tab, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	h := NewHealth()
	for i := 0; i < 6; i++ { // ords41 is benched for the whole replay
		record(t, h, Attempt{Route: "ords41", Wall: 75 * time.Second, Tail: `"name": "UnknownError"`}, i)
	}
	rows := readProviderIncomplete(t)
	if len(rows) != 398 {
		t.Fatalf("fixture has %d rows, want the 398 provider-incomplete cards", len(rows))
	}
	dealt, moved := 0, map[string]int{}
	for _, r := range rows {
		if r.failed == "" { // the five parked cards carry no route
			continue
		}
		c := Card{Rung: r.rung, Route: r.failed}
		d, err := h.Redeal(tab, c, r.card, 2, r.failed)
		if err != nil {
			t.Fatalf("%s: %v", r.card, err)
		}
		if d.Route == r.failed || d.Route == "ords41" {
			t.Fatalf("%s redealt to %s (failed %s, benched ords41)", r.card, d.Route, r.failed)
		}
		if err := tab.Check(Card{Rung: r.rung, Route: d.Route}); err != nil {
			t.Fatalf("%s redealt to a route the table refuses: %v", r.card, err)
		}
		if !strings.Contains(d.Line(), "avoid="+r.failed) || !strings.Contains(d.Line(), "attempt=2") {
			t.Fatalf("ROUTE line %q lacks avoid=%s attempt=2", d.Line(), r.failed)
		}
		dealt++
		moved[d.Route]++
	}
	if dealt != 393 {
		t.Fatalf("redealt %d cards, want 393 (398 less 5 parked)", dealt)
	}
	if len(moved) < 3 {
		t.Fatalf("the replay piled onto %d routes (%v); the attempt hash should spread it", len(moved), moved)
	}
	// The hash takes the attempt number: with the same avoid list, attempts 2
	// and 3 of a card land on different routes for most cards, and attempt 3
	// can avoid attempt 2's route too.
	differ := 0
	for _, r := range rows {
		if r.failed == "" || r.rung != "flash" {
			continue
		}
		c := Card{Rung: r.rung, Route: r.failed}
		d2, _ := h.Redeal(tab, c, r.card, 2)
		d3, _ := h.Redeal(tab, c, r.card, 3)
		if d2.Route != d3.Route {
			differ++
		}
	}
	if differ < 100 {
		t.Fatalf("attempts 2 and 3 differ for %d flash cards; the attempt number is not in the hash", differ)
	}
	c := Card{Rung: "flash", Route: "ormimo26"}
	d2, _ := h.Redeal(tab, c, "card-read3-nova-tools-2480", 2)
	d3, err := h.Redeal(tab, c, "card-read3-nova-tools-2480", 3, d2.Route)
	if err != nil || d3.Route == d2.Route || d3.Route == "ormimo26" {
		t.Fatalf("attempt 3 %v on %s after attempt 2 on %s", err, d3.Route, d2.Route)
	}
	// Nothing left to deal to is REFUSED, never a guess.
	all := append(tab.Allowed("pro", ""), "x")
	if _, err := h.Redeal(tab, Card{Rung: "pro"}, "card-x", 2, all...); err == nil || !strings.HasPrefix(err.Error(), "REFUSED ") {
		t.Fatalf("an exhausted rung dealt: %v", err)
	}
}

func record(t *testing.T, h *Health, a Attempt, i int) string {
	t.Helper()
	a.At = t0.Add(time.Duration(i) * time.Second)
	_, receipt := h.Record(a)
	return receipt
}

type tailRow struct {
	want Class
	a    Attempt
}

func readTails(t *testing.T) []tailRow {
	t.Helper()
	var out []tailRow
	for _, f := range readTSV(t, "testdata/tails-2026-09-22.tsv", 4) {
		wall, err1 := strconv.Atoi(f[1])
		tokens, err2 := strconv.Atoi(f[2])
		if err1 != nil || err2 != nil {
			t.Fatalf("bad tails row %q", f)
		}
		out = append(out, tailRow{Class(f[0]), Attempt{Route: "r", Wall: time.Duration(wall) * time.Second, Tokens: tokens, Tail: f[3]}})
	}
	return out
}

type incompleteRow struct{ card, rung, failed string }

func readProviderIncomplete(t *testing.T) []incompleteRow {
	t.Helper()
	var out []incompleteRow
	for _, f := range readTSV(t, "testdata/provider-incomplete-2026-09-22.tsv", 3) {
		out = append(out, incompleteRow{f[0], f[1], f[2]})
	}
	return out
}

func readTSV(t *testing.T, path string, cols int) [][]string {
	t.Helper()
	fh, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	var out [][]string
	sc := bufio.NewScanner(fh)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != cols {
			t.Fatalf("%s:%d: %d columns, want %d", path, n, len(f), cols)
		}
		out = append(out, f)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(fmt.Errorf("%s: %w", path, err))
	}
	return out
}
