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
// DeepSeek direct and OpenCode (Mercury is flash only; OpenRouter is flash
// only since #3949 took kimi-k3 out of pro), every route real, of its tier,
// on its provider's via, never dropped or dead, no DeepSeek model through a
// router, and no kimi model on the pro tier at all.
func TestSpreadRoutesAreValid(t *testing.T) {
	t.Parallel()

	tab, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"flash": "deepseek:dsflash opencode:ocglmflash openrouter:orqwen38 mercury:mercury",
		"pro":   "deepseek:dspro opencode:ocqwenplus",
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
				if tier == "pro" && (name == "orkimi3" || strings.Contains(r.Model, "kimi")) {
					t.Errorf("spread pro %s names %s (%s): kimi-k3 crashed 14 of 14 in swarm-0925a (#3949)", s.Provider, name, r.Model)
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

// Eight flash labels reach all four flash providers and six pro labels both
// pro providers; each label's pick is stable and is its provider's first
// route. spread-flash-1..8 index 3 2 1 0 3 2 1 0 and spread-pro-1..6 take
// slots 2 0 1 0 1 2 of pro's three (dspro owns slot 0, ocqwenplus, share 2,
// slots 1 and 2) under fnv32a.
func TestSpreadPickCoversEveryProvider(t *testing.T) {
	t.Parallel()

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
		{"pro", 6, "opencode:ocqwenplus deepseek:dspro opencode:ocqwenplus deepseek:dspro opencode:ocqwenplus opencode:ocqwenplus"},
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
	t.Parallel()

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

// #3949: kimi-k3 (orkimi3) crashed 14 of 14 pro cards in swarm-0925a, so it
// leaves the pro spread and its share goes to qwen3.6-plus: opencode owns two
// of pro's three slots. Every pro label that took the old openrouter slot
// (slot 2) now picks ocqwenplus and every other label keeps its route, so
// the change moves only kimi's cards; the quack labels #3742 walled on mimo
// never pick a kimi or dead route; and a pro spread row naming the dead
// ormimo26pro still does not parse.
func TestProSpreadKimiShareGoesToQwen(t *testing.T) {
	t.Parallel()

	tab, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	shares := map[string]int{}
	for _, s := range tab.Spread("pro") {
		shares[s.Provider] = s.Share
	}
	if fmt.Sprint(shares) != "map[deepseek:1 opencode:2]" {
		t.Fatalf("pro shares = %v, want deepseek 1 and opencode 2", shares)
	}
	n := map[string]int{}
	for i := 0; i < 300; i++ {
		label := fmt.Sprintf("s00-%04d-pro", i)
		r, _, err := tab.Pick("pro", label)
		if err != nil {
			t.Fatal(err)
		}
		want := "ocqwenplus"
		if SpreadIndex(label, 3) == 0 {
			want = "dspro"
		}
		if r.Route != want {
			t.Fatalf("%s (old slot %d) picked %s, want %s", label, SpreadIndex(label, 3), r.Route, want)
		}
		n[r.Route]++
	}
	if n["ocqwenplus"] < 2*n["dspro"]-60 || n["orkimi3"] != 0 {
		t.Errorf("300 pro labels split %v, want ocqwenplus about twice dspro and no orkimi3", n)
	}
	row := tab.rows[tab.byRoute["ormimo26pro"]]
	if row.State != Dropped || row.Flag != FlagDead {
		t.Fatalf("ormimo26pro is %s flag %q, want dropped and dead", row.State, row.Flag)
	}
	assertRefused(t, tab, Card{Rung: "pro", Route: "ormimo26pro"}, "dead")
	for _, label := range []string{"s00-0302-quack-hulk-pro", "s00-0601-quack-vision-pro"} {
		r, _, err := tab.Pick("pro", label)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if r.Route == "ormimo26pro" || r.Route == "orkimi3" {
			t.Errorf("%s picked %s", label, r.Route)
		}
	}
	anchor := "    routes: [ocqwenplus, ocminimax]\n"
	back := strings.Replace(string(routesYAML), anchor, anchor+"    why: \"x\"\n  - tier: pro\n    provider: openrouter\n    via: openrouter\n    routes: [ormimo26pro]\n", 1)
	if back == string(routesYAML) {
		t.Fatal("the pro opencode spread row moved; this test cannot bite")
	}
	if _, err := Parse([]byte(back)); err == nil || !strings.Contains(err.Error(), "ormimo26pro, which is dropped") {
		t.Errorf("a spread naming ormimo26pro parsed: %v", err)
	}
}

// A spread share is a whole number 1..MaxShare; anything else does not parse.
func TestParseSpreadShare(t *testing.T) {
	t.Parallel()

	base := "routes:\n  - route: good\n    rung: flash\n    model: m1\n    via: opencode\n    state: held\n    why: \"held\"\nspread:\n"
	row := func(share string) string {
		return "  - tier: flash\n    provider: opencode\n    via: opencode\n    share: " + share + "\n    routes: [good]\n    why: \"w\"\n"
	}
	tab, err := Parse([]byte(base + row("3")))
	if err != nil || tab.Spread("flash")[0].Share != 3 {
		t.Fatalf("share 3: %v", err)
	}
	for _, bad := range []string{"0", "-1", "9", "two", "1.5"} {
		if _, err := Parse([]byte(base + row(bad))); err == nil || !strings.Contains(err.Error(), "share") {
			t.Errorf("share %s parsed: %v", bad, err)
		}
	}
}
