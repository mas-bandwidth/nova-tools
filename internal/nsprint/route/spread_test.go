package route

import (
	"fmt"
	"strings"
	"testing"
)

// spreadVia is Glenn's four providers and the via each is served by.
var spreadVia = map[string]string{
	"deepseek":   "deepseek",
	"opencode":   "opencode",
	"openrouter": "openrouter",
	"mercury":    ViaDatacenter,
}

// The spread is exactly Glenn's ruling: flash on all four providers, pro on
// all but Mercury, every route real, of its tier, on its provider's via,
// never dropped or dead, and no DeepSeek model through a router.
func TestSpreadRoutesAreValid(t *testing.T) {
	tab, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"flash": "deepseek:dsflash opencode:ocglmflash openrouter:orqwen38 mercury:mercury",
		"pro":   "deepseek:dspro opencode:ocqwenplus openrouter:orkimi3",
	}
	for tier, w := range want {
		var got []string
		for _, s := range tab.Spread(tier) {
			got = append(got, s.Provider+":"+s.Routes[0])
			if v, ok := spreadVia[s.Provider]; !ok || v != s.Via {
				t.Errorf("spread %s %s: via %s, want %s", tier, s.Provider, s.Via, v)
			}
			for _, name := range s.Routes {
				i, ok := tab.byRoute[name]
				if !ok {
					t.Errorf("spread %s %s names unknown route %s", tier, s.Provider, name)
					continue
				}
				r := tab.rows[i]
				if r.Rung != tier || r.Via != s.Via || r.State == Dropped || r.Flag == FlagDead {
					t.Errorf("spread %s %s names %s: rung %s via %s state %s flag %q", tier, s.Provider, name, r.Rung, r.Via, r.State, r.Flag)
				}
				if s.Provider != "deepseek" && strings.Contains(r.Model, "deepseek") {
					t.Errorf("spread %s %s names %s (%s): a router row carries no DeepSeek model", tier, s.Provider, name, r.Model)
				}
				if p := strings.SplitN(r.Launch(), "/", 2)[0]; p != map[string]string{"deepseek": "deepseek", "opencode": "opencode", "openrouter": "openrouter", "mercury": "inception"}[s.Provider] {
					t.Errorf("spread %s %s: %s launches as %s", tier, s.Provider, name, r.Launch())
				}
			}
		}
		if g := strings.Join(got, " "); g != w {
			t.Errorf("spread %s = %q, want %q", tier, g, w)
		}
	}
}

// Eight flash labels reach all four flash providers and six pro labels all
// three pro providers; each label's pick is stable and is its provider's
// first route. spread-flash-1..8 index 3 2 1 0 3 2 1 0 and spread-pro-1..6
// index 2 0 1 0 1 2 under fnv32a.
func TestSpreadPickCoversEveryProvider(t *testing.T) {
	tab, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		tier string
		n    int
		want string
	}{
		{"flash", 8, "mercury:mercury openrouter:orqwen38 opencode:ocglmflash deepseek:dsflash mercury:mercury openrouter:orqwen38 opencode:ocglmflash deepseek:dsflash"},
		{"pro", 6, "openrouter:orkimi3 deepseek:dspro opencode:ocqwenplus deepseek:dspro opencode:ocqwenplus openrouter:orkimi3"},
	} {
		var got []string
		providers := map[string]bool{}
		for i := 1; i <= tc.n; i++ {
			label := fmt.Sprintf("spread-%s-%d", tc.tier, i)
			r, s, err := tab.Pick(tc.tier, label)
			if err != nil {
				t.Fatalf("%s: %v", label, err)
			}
			if again, _, _ := tab.Pick(tc.tier, label); again.Route != r.Route {
				t.Fatalf("%s picked %s then %s", label, r.Route, again.Route)
			}
			if r.Route != s.Routes[0] {
				t.Fatalf("%s picked %s, not its provider's first route %s", label, r.Route, s.Routes[0])
			}
			providers[s.Provider] = true
			got = append(got, s.Provider+":"+r.Route)
		}
		if len(providers) != len(tab.Spread(tc.tier)) {
			t.Errorf("%d %s labels reach %d of %d providers", tc.n, tc.tier, len(providers), len(tab.Spread(tc.tier)))
		}
		if g := strings.Join(got, " "); g != tc.want {
			t.Errorf("%s picks\n%s\nwant\n%s", tc.tier, g, tc.want)
		}
	}
	for _, bad := range [][2]string{{"turbo", "x"}, {"flash", ""}} {
		if _, _, err := tab.Pick(bad[0], bad[1]); err == nil {
			t.Errorf("Pick(%q, %q) picked a route", bad[0], bad[1])
		}
	}
}

// Parse refuses a spread row that names an unknown, wrong-rung, wrong-via,
// dropped or dead route, or a provider twice in a tier, and admits a held one.
func TestParseSpreadValidation(t *testing.T) {
	base := `routes:
  - route: good
    rung: flash
    model: m1
    via: opencode
    state: held
    why: "held"
  - route: gone
    rung: flash
    model: m2
    via: opencode
    state: dropped
    why: "dropped"
  - route: deadr
    rung: flash
    model: m3
    via: opencode
    state: held
    flag: dead
    why: "dead"
  - route: prorow
    rung: pro
    model: m4
    via: opencode
    state: held
    why: "held"
spread:
`
	row := func(provider, via, routes string) string {
		return fmt.Sprintf("  - tier: flash\n    provider: %s\n    via: %s\n    routes: [%s]\n    why: \"w\"\n", provider, via, routes)
	}
	if _, err := Parse([]byte(base + row("opencode", "opencode", "good"))); err != nil {
		t.Fatalf("a held spread route was refused: %v", err)
	}
	for name, tc := range map[string]struct{ body, want string }{
		"unknown":    {row("opencode", "opencode", "nosuch"), "unknown route"},
		"wrong rung": {row("opencode", "opencode", "prorow"), "rung pro"},
		"wrong via":  {row("openrouter", "openrouter", "good"), "whose via is opencode"},
		"dropped":    {row("opencode", "opencode", "gone"), "dropped"},
		"dead":       {row("opencode", "opencode", "deadr"), "dead"},
		"twice":      {row("opencode", "opencode", "good") + row("opencode", "opencode", "good"), "twice"},
		"no why":     {"  - tier: flash\n    provider: opencode\n    via: opencode\n    routes: [good]\n", "needs"},
	} {
		_, err := Parse([]byte(base + tc.body))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error naming %q", name, err, tc.want)
		}
	}
}

// #3742: mimo-v2.6-pro walled 3 of 3 one-line quack cards, so ormimo26pro is
// dropped as a dead route and OpenRouter's pro slot is kimi-k3 (orkimi3) for
// a measured A/B. The quack labels that walled pick orkimi3, and the embedded
// table with ormimo26pro back in the openrouter pro row does not parse.
func TestProSpreadOpenRouterSlotIsKimiNotDeadMimo(t *testing.T) {
	tab, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	row := tab.rows[tab.byRoute["ormimo26pro"]]
	if row.State != Dropped || row.Flag != FlagDead {
		t.Fatalf("ormimo26pro is %s flag %q, want dropped and dead", row.State, row.Flag)
	}
	assertRefused(t, tab, Card{Rung: "pro", Route: "ormimo26pro"}, "dead")
	for _, label := range []string{"s00-0302-quack-hulk-pro", "s00-0601-quack-vision-pro"} {
		r, s, err := tab.Pick("pro", label)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if s.Provider == "openrouter" && r.Route != "orkimi3" {
			t.Errorf("%s hashes to openrouter and picked %s, want orkimi3", label, r.Route)
		}
		if r.Route == "ormimo26pro" {
			t.Errorf("%s picked the dead ormimo26pro", label)
		}
	}
	for _, s := range tab.Spread("pro") {
		if s.Provider == "openrouter" && (s.Routes[0] != "orkimi3" || strings.Contains(strings.Join(s.Routes, " "), "ormimo26pro")) {
			t.Errorf("pro openrouter spread row = %v, want orkimi3 first and no ormimo26pro", s.Routes)
		}
	}
	back := strings.Replace(string(routesYAML), "routes: [orkimi3, ormimopro, orglm53]", "routes: [ormimo26pro, ormimopro, orglm53, orkimi3]", 1)
	if back == string(routesYAML) {
		t.Fatal("the pro openrouter spread row moved; this test cannot bite")
	}
	if _, err := Parse([]byte(back)); err == nil || !strings.Contains(err.Error(), "ormimo26pro, which is dropped") {
		t.Errorf("a spread naming ormimo26pro parsed: %v", err)
	}
}
