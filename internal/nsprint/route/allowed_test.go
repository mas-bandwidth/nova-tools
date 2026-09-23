package route

import (
	"strings"
	"testing"
)

// The DONE-WHEN control for #2895: a card routed to a dropped route is
// REFUSED, whatever its rung and work type, and the refusal names the rule.
func TestAllowedRoutesRefusesADroppedRoute(t *testing.T) {
	tab, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	flashDropped := []string{"orgptnano", "ornemotron", "orgeminilite"}
	proDropped := []string{"ocpro", "ordspro", "ocglm53", "orglm53", "orkimicode", "orminimax", "orqwenplus", "orgemini25pro", "orhaiku", "ordevstral", "ocmuse", "ormuse"}
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
	tab, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		card Card
		want string
	}{
		{Card{Rung: "pro", Route: "orqwen38"}, "rung flash"},
		{Card{Rung: "flash", Route: "ocglmflash"}, "held"},
		{Card{Rung: "pro", Route: "orkimi3"}, "held"},
		{Card{Rung: "flash", Route: "nosuchroute"}, "not in the table"},
		{Card{Rung: "turbo", Route: "orqwen38"}, "rung"},
	} {
		assertRefused(t, tab, tc.card, tc.want)
	}
}

// A work-type row widens its rung by held routes the report measured on that
// type: kimi-k3 on cell3, glm-5.3-flash on nx. Nowhere else.
func TestAllowedRoutesWorkTypeRowWidensOnlyThatType(t *testing.T) {
	tab, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := tab.Check(Card{Rung: "pro", Type: "cell3", Route: "orkimi3"}); err != nil {
		t.Errorf("kimi-k3 on cell3: %v", err)
	}
	assertRefused(t, tab, Card{Rung: "pro", Type: "recut", Route: "orkimi3"}, "held")
	if err := tab.Check(Card{Rung: "flash", Type: "nx", Route: "ocglmflash"}); err != nil {
		t.Errorf("glm-5.3-flash on nx: %v", err)
	}
}

func TestParseRefusesATypeRowThatAddsADroppedRoute(t *testing.T) {
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
