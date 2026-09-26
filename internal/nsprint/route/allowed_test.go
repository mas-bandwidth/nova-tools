package route

import (
	"strings"
	"testing"
)

// The DONE-WHEN control for #2895: a card routed to a dropped route is
// REFUSED, whatever its rung and work type, and the refusal names the rule.
func TestAllowedRoutesRefusesADroppedRoute(t *testing.T) {
	t.Parallel()

	tab := loadNoProbe(t)
	flashDropped := []string{"orgptnano", "ornemotron", "orgeminilite"}
	proDropped := []string{"ocpro", "ordspro", "ocglm53", "orkimicode", "orminimax", "orqwenplus", "orgemini25pro", "orhaiku", "ordevstral", "ocmuse", "ormuse", "ormimo26pro"}
	types := append([]string{""}, tab.Types()...)
	for _, typ := range types {
		for _, r := range flashDropped {
			assertRefused(t, tab, Card{Rung: "flash", Type: typ, Route: r}, "dropped")
		}
		for _, r := range proDropped {
			assertRefused(t, tab, Card{Rung: "pro", Type: typ, Route: r}, "dropped")
		}
	}
	// Every dropped row in the table is refused, not only the ones named above.
	n := 0
	for _, row := range tab.Rows() {
		if row.State == Dropped {
			n++
			assertRefused(t, tab, Card{Rung: row.Rung, Route: row.Route}, "dropped")
		}
	}
	if n != len(flashDropped)+len(proDropped) {
		t.Fatalf("table carries %d dropped rows, want %d", n, len(flashDropped)+len(proDropped))
	}
}

func assertRefused(t *testing.T, tab *Table, c Card, want string) {
	t.Helper()
	err := tab.Check(c)
	if err == nil {
		t.Errorf("card %+v was allowed; want REFUSED", c)
		return
	}
	if !strings.HasPrefix(err.Error(), "REFUSED ") || !strings.Contains(err.Error(), want) {
		t.Errorf("card %+v: %q, want REFUSED naming %q", c, err, want)
	}
}

// The five flash leaders and the two opencode pro routes of the 12:16 AM
// pattern read are the rung lists; nothing else is allowed by default.
func TestAllowedRoutesPerRungFromTheRanking(t *testing.T) {
	t.Parallel()

	tab, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"flash": "orqwen38 ords41 ormimo26 ormimo ords",
		"pro":   "ocqwenplus ocminimax",
	}
	for rung, w := range want {
		got := strings.Join(tab.Allowed(rung, ""), " ")
		if got != w {
			t.Errorf("allowed %s = %q, want %q", rung, got, w)
		}
		for _, r := range strings.Fields(w) {
			if err := tab.Check(Card{Rung: rung, Type: "recut", Route: r}); err != nil {
				t.Errorf("leader %s on %s refused: %v", r, rung, err)
			}
		}
	}
}

func TestAllowedRoutesRefusesWrongRungHeldAndUnknown(t *testing.T) {
	t.Parallel()

	tab := loadNoProbe(t)
	for _, tc := range []struct {
		card Card
		want string
	}{
		{Card{Rung: "pro", Route: "orqwen38"}, "rung flash"},
		{Card{Rung: "flash", Route: "ocglmflash"}, "held"},
		{Card{Rung: "pro", Route: "orkimi3"}, "held"},
		{Card{Rung: "pro", Route: "orglm53"}, "held"},
		{Card{Rung: "flash", Route: "nosuchroute"}, "not in the table"},
		{Card{Rung: "turbo", Route: "orqwen38"}, "rung"},
	} {
		assertRefused(t, tab, tc.card, tc.want)
	}
}

// A work-type row widens its rung by held routes the report measured on that
// type: kimi-k3 on cell3, glm-5.3-flash on nx. Nowhere else.
func TestAllowedRoutesWorkTypeRowWidensOnlyThatType(t *testing.T) {
	t.Parallel()

	tab := loadNoProbe(t)
	if err := tab.Check(Card{Rung: "pro", Type: "cell3", Route: "orkimi3"}); err != nil {
		t.Errorf("kimi-k3 on cell3: %v", err)
	}
	assertRefused(t, tab, Card{Rung: "pro", Type: "recut", Route: "orkimi3"}, "held")
	if err := tab.Check(Card{Rung: "flash", Type: "nx", Route: "ocglmflash"}); err != nil {
		t.Errorf("glm-5.3-flash on nx: %v", err)
	}
}

func TestParseRefusesATypeRowThatAddsADroppedRoute(t *testing.T) {
	t.Parallel()

	src := `routes:
  - route: a
    rung: flash
    model: m/a
    state: allowed
  - route: b
    rung: flash
    model: m/b
    state: dropped
    why: "Q -1"
types:
  - type: read3
    rung: flash
    add: [b]
    why: "try"
`
	if _, err := Parse([]byte(src)); err == nil || !strings.Contains(err.Error(), "dropped") {
		t.Fatalf("parse: %v, want a refusal naming the dropped route", err)
	}
	for _, bad := range []string{
		"routes:\n  - route: a\n    rung: flash\n    model: m\n    state: maybe\n",
		"routes:\n  - route: a\n    rung: flash\n    model: m\n    state: allowed\n    colour: red\n",
		"routes:\n  - route: a\n    rung: flash\n    model: m\n    state: allowed\n  - route: a\n    rung: flash\n    model: m\n    state: allowed\n",
		"routes:\n  - route: a\n    rung: flash\n    model: m\n    state: allowed\n    run: many\n",
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("parse accepted:\n%s", bad)
		}
	}
}

func TestRenderPrintsTheTableWithTheNumbers(t *testing.T) {
	t.Parallel()

	tab, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	out := tab.Render()
	for _, want := range []string{
		"ROUTES source=",
		"orqwen38", "qwen/qwen3.8-flash", "allowed", "$0.04", "0.01",
		"orgptnano", "dropped", "Q -0.83",
		"ocqwenplus", "$0.88", "0.78",
		"TYPE cell3 pro +orkimi3",
		"ALLOWED flash orqwen38 ords41 ormimo26 ormimo ords",
		"ALLOWED pro ocqwenplus ocminimax",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
}

// The rule line and the table cannot disagree (stella's hold on #3003): every
// row's state is recomputed from its numbers and flags by the rule written at
// the top of routes.yaml, and a held or dropped row's why names the step of
// the rule that put it there.
func TestTheTableFollowsItsRule(t *testing.T) {
	t.Parallel()

	tab, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Q <= 0 with n >= 8 and useful < 3/4 of the rung median", "Pareto-dominated with Q <= 0", "best third"} {
		if !strings.Contains(tab.Rule, want) {
			t.Errorf("rule line %q does not say %q", tab.Rule, want)
		}
	}
	derived := tab.Derive()
	for _, row := range tab.Rows() {
		d, ok := derived[row.Route]
		if !ok {
			t.Errorf("%s: rule gave no state", row.Route)
			continue
		}
		if d.State != row.State {
			t.Errorf("%s is %s in routes.yaml but the rule gives %s (%s)", row.Route, row.State, d.State, d.Reason)
			continue
		}
		if row.State != Allowed && !strings.Contains(strings.ToLower(row.Why), strings.ToLower(d.Reason)) {
			t.Errorf("%s is %s by %q but its why does not say so: %q", row.Route, row.State, d.Reason, row.Why)
		}
	}
}

// The rule is not vacuous: the rows stella named move when their numbers do.
func TestDeriveMovesARowWhenItsNumbersMove(t *testing.T) {
	t.Parallel()

	tab, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if d := tab.Derive()["ocminimax"]; d.State != Allowed || d.Reason != "best third" {
		t.Fatalf("ocminimax derived %+v, want allowed by best third", d)
	}
	i := tab.byRoute["ocminimax"]
	n := *tab.rows[i].Numbers
	n.U8, n.U9 = 4, 3 // 4 of 13 at 8+: Q <= 0 and useful far below the pro rung
	tab.rows[i].Numbers = &n
	if d := tab.Derive()["ocminimax"]; d.State != Dropped || d.Reason != "quality rule" {
		t.Fatalf("ocminimax at 4 of 13 derived %+v, want dropped by the quality rule", d)
	}
	j := tab.byRoute["orglm53"]
	m := *tab.rows[j].Numbers
	m.Q = -0.01
	tab.rows[j].Numbers = &m
	if d := tab.Derive()["orglm53"]; d.State != Dropped || d.Reason != "Pareto-dominated" {
		t.Fatalf("orglm53 at Q -0.01 derived %+v, want dropped as Pareto-dominated", d)
	}
}

// The interim code-rung override (fold 2026-09-23, reports/fold-2026-09-22-
// landed-per-route.md: $ per LANDED code PR, not the $-per-8+-scored ranking
// above) replaces the pro rung's code list outright with exactly the three
// routes that landed: ocqwenplus, orhaiku, ordspro. It may name a route the
// ranking drops (ordspro and orhaiku are both "dropped" rows above) -- that
// is the point of an override, unlike a types row.
func TestAllowedRoutesCodeOverrideIsExactlyTheThreeThatLanded(t *testing.T) {
	t.Parallel()

	tab := loadNoProbe(t)
	want := "ocqwenplus orhaiku ordspro"
	if got := strings.Join(tab.Allowed("pro", "code"), " "); got != want {
		t.Fatalf("allowed pro/code = %q, want %q", got, want)
	}
	for _, r := range strings.Fields(want) {
		if err := tab.Check(Card{Rung: "pro", Type: "code", Route: r}); err != nil {
			t.Errorf("%s on the code override refused: %v", r, err)
		}
	}
	// ordspro and orhaiku stay dropped for every other type and for the
	// rung's default (no type) list: the override touches only pro/code.
	for _, typ := range []string{"", "recut", "cell3"} {
		for _, r := range []string{"ordspro", "orhaiku"} {
			assertRefused(t, tab, Card{Rung: "pro", Type: typ, Route: r}, "dropped")
		}
	}
	// ocminimax is allowed by default on pro (see TestAllowedRoutesPerRung
	// FromTheRanking) but is not on the code override's exact list, and
	// ormimopro was never allowed on pro at all -- both stay off pending
	// their own landed-PR evidence.
	for _, r := range []string{"ocminimax", "ormimopro"} {
		assertRefused(t, tab, Card{Rung: "pro", Type: "code", Route: r}, "not on the code override")
	}
	// Every route the fold found expensive or landing nothing stays off the
	// code rung too, whatever its state on the $-per-8+ ranking.
	for _, r := range []string{"orqwenplus", "orkimi3", "orminimax", "ocglm53", "orglm53", "orkimicode", "orgemini25pro"} {
		assertRefused(t, tab, Card{Rung: "pro", Type: "code", Route: r}, "not on the code override")
	}
	// The flash rung is untouched by this override.
	if got := strings.Join(tab.Allowed("flash", ""), " "); got != "orqwen38 ords41 ormimo26 ormimo ords" {
		t.Fatalf("flash rung moved: %q", got)
	}
}

// A benched or dead route is refused even when an override names it: the
// override list is not the last word, Check is.
func TestAllowedRoutesOverrideNeverAdmitsABenchedRoute(t *testing.T) {
	t.Parallel()

	src := `routes:
  - route: a
    rung: pro
    model: m/a
    via: openrouter
    state: allowed
    run: 20
    scored: 10
    u8: 8
    u9: 6
    usd_per_8: 1.00
    efficiency: 1.00
    q: 0.10
    tok_per_8: 5.0
    wall_s: 400
    why: "leader"
  - route: b
    rung: pro
    model: m/b
    via: openrouter
    state: held
    flag: benched
    why: "ROUTE-BENCHED"
overrides:
  - type: code
    rung: pro
    routes: [a, b]
    source: "test fold"
    why: "b should never run even though the override names it"
`
	tab, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if err := tab.Check(Card{Rung: "pro", Type: "code", Route: "a"}); err != nil {
		t.Errorf("a on the code override refused: %v", err)
	}
	assertRefused(t, tab, Card{Rung: "pro", Type: "code", Route: "b"}, "benched")
	if got := strings.Join(tab.Allowed("pro", "code"), " "); got != "a" {
		t.Fatalf("allowed pro/code = %q, want %q (b is benched)", got, "a")
	}
}

func TestParseOverridesValidation(t *testing.T) {
	t.Parallel()

	base := `routes:
  - route: a
    rung: pro
    model: m/a
    state: allowed
  - route: b
    rung: flash
    model: m/b
    state: allowed
`
	for _, tc := range []struct {
		name string
		src  string
		want string
	}{
		{"unknown route", base + "overrides:\n  - type: code\n    rung: pro\n    routes: [nosuchroute]\n    source: s\n    why: w\n", "unknown route"},
		{"wrong rung", base + "overrides:\n  - type: code\n    rung: pro\n    routes: [b]\n    source: s\n    why: w\n", "rung flash route"},
		{"missing why", base + "overrides:\n  - type: code\n    rung: pro\n    routes: [a]\n    source: s\n", "needs type, rung, routes, source and why"},
		{"duplicate rung+type", base + "overrides:\n  - type: code\n    rung: pro\n    routes: [a]\n    source: s\n    why: w\n  - type: code\n    rung: pro\n    routes: [a]\n    source: s2\n    why: w2\n", "appears twice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.src)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("parse: %v, want error containing %q", err, tc.want)
			}
		})
	}
	// Unlike a types row, an overrides row MAY name a dropped route.
	src := base + `  - route: c
    rung: pro
    model: m/c
    state: dropped
    why: "dropped"
overrides:
  - type: code
    rung: pro
    routes: [a, c]
    source: s
    why: w
`
	tab, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("overrides row naming a dropped route should parse: %v", err)
	}
	if err := tab.Check(Card{Rung: "pro", Type: "code", Route: "c"}); err != nil {
		t.Errorf("c on the code override refused despite being dropped: %v", err)
	}
}
