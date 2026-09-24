package main

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/route"
)

// TestRoutesTierPrintsAllowedOnly: routes --tier <flash|pro> prints exactly the
// tier's allowed routes, in table order, each with its <via>/<model> launch
// string; no held or dropped route is ever printed.
func TestRoutesTierPrintsAllowedOnly(t *testing.T) {
	tab, err := route.Load()
	if err != nil {
		t.Fatal(err)
	}
	notAllowed := 0
	for _, tier := range []string{"flash", "pro"} {
		var want []string
		for _, r := range tab.Rows() {
			if r.Rung != tier {
				continue
			}
			if r.State == route.Allowed && r.Flag != route.FlagBenched && r.Flag != route.FlagDead {
				want = append(want, r.Route+" "+r.Via+"/"+r.Model)
			} else {
				notAllowed++
			}
		}
		code, out, errOut := runSprint("routes", "--tier", tier)
		if code != 0 {
			t.Fatalf("--tier %s: exit %d stderr %q", tier, code, errOut)
		}
		got := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
		if len(want) == 0 || strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("--tier %s printed\n%s\nwant\n%s", tier, out, strings.Join(want, "\n"))
		}
		for _, line := range got {
			f := strings.Fields(line)
			if len(f) != 2 || !(strings.HasPrefix(f[1], "opencode/") || strings.HasPrefix(f[1], "openrouter/")) {
				t.Fatalf("--tier %s line %q is not <route> opencode|openrouter/<model>", tier, line)
			}
		}
	}
	if notAllowed == 0 {
		t.Fatal("the table has no held or dropped route, so this test cannot bite")
	}
	// ocflash (opencode/deepseek-v4-flash, the fixed NOVA_CARD_MODEL every bench ran) is held: never printed
	if _, out, _ := runSprint("routes", "--tier", "flash"); strings.Contains(out, "ocflash ") {
		t.Fatalf("--tier flash printed the held route ocflash:\n%s", out)
	}
	for _, args := range [][]string{{"routes", "--tier", "turbo"}, {"routes", "--tier", "pro", "--rung", "pro"}} {
		if code, out, _ := runSprint(args...); code != 2 || out != "" {
			t.Fatalf("%v: exit %d stdout %q, want exit 2 and nothing printed", args, code, out)
		}
	}
}

// TestRoutesTierLabelPicksTheSpread: routes --tier <t> --label <card> prints
// the one spread route the card runs on, "<route> <launch>", deterministic per
// label; eight flash labels reach all four providers (Mercury launches as
// inception/mercury-2.5) and six pro labels all three. --spread prints the
// table; a bad tier, an empty label or --label without --tier prints nothing.
func TestRoutesTierLabelPicksTheSpread(t *testing.T) {
	want := map[string][]string{
		"flash": {
			"mercury inception/mercury-2.5",
			"orqwen38 openrouter/qwen/qwen3.8-flash",
			"ocglmflash opencode/glm-5.3-flash",
			"dsflash deepseek/deepseek-v4-flash",
		},
		"pro": {
			"ormimo26pro openrouter/xiaomi/mimo-v2.6-pro",
			"dspro deepseek/deepseek-v4-pro",
			"ocqwenplus opencode/qwen3.6-plus",
		},
	}
	for tier, lines := range want {
		n := map[string]int{"flash": 8, "pro": 6}[tier]
		seen := map[string]bool{}
		for i := 1; i <= n; i++ {
			label := "spread-" + tier + "-" + string(rune('0'+i))
			code, out, errOut := runSprint("routes", "--tier", tier, "--label", label)
			if code != 0 {
				t.Fatalf("%s: exit %d stderr %q", label, code, errOut)
			}
			seen[strings.TrimSuffix(out, "\n")] = true
		}
		for _, l := range lines {
			if !seen[l] {
				t.Errorf("--tier %s over %d labels never printed %q (printed %v)", tier, n, l, seen)
			}
		}
		if len(seen) != len(lines) {
			t.Errorf("--tier %s printed %d distinct routes, want %d: %v", tier, len(seen), len(lines), seen)
		}
	}
	code, out, _ := runSprint("routes", "--spread")
	if code != 0 || !strings.Contains(out, "mercury") || !strings.Contains(out, "dspro") || strings.Count(out, "\n") != 8 {
		t.Fatalf("--spread: exit %d\n%s", code, out)
	}
	for _, args := range [][]string{
		{"routes", "--tier", "turbo", "--label", "x"},
		{"routes", "--label", "x"},
		{"routes", "--spread", "--rung", "pro"},
		{"routes", "--tier", "pro", "--label", "x", "--spread"},
	} {
		if code, out, _ := runSprint(args...); code != 2 || out != "" {
			t.Fatalf("%v: exit %d stdout %q, want exit 2 and nothing printed", args, code, out)
		}
	}
}
