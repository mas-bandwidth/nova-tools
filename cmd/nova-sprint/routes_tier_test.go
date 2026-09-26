package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/route"
)

// TestRoutesTierPrintsAllowedOnly: routes --tier <flash|pro> prints exactly the
// tier's allowed routes, in table order, each with its <via>/<model> launch
// string; no held or dropped route is ever printed.
func TestRoutesTierPrintsAllowedOnly(t *testing.T) {
	t.Parallel()

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
// inception/mercury-2.5) and six pro labels both (OpenRouter is flash only
// since #3949). --spread prints the
// table; a bad tier, an empty label or --label without --tier prints nothing.
func TestRoutesTierLabelPicksTheSpread(t *testing.T) {
	t.Parallel()

	// While routes.yaml carries a probe (Glenn 2026-09-26 11:05 AM ET: "turn
	// all models back on to try again"), --tier --label spreads a tier's
	// cards over every probed route; without one it follows the measured
	// spread. Either way every route the table would pick is printed as
	// "<route> <via>/<model>" over enough labels.
	tab, err := route.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, tier := range []string{"flash", "pro"} {
		var want []string
		if pr := tab.Probe(tier); pr != nil {
			want = pr.Routes
		} else {
			for _, s := range tab.Spread(tier) {
				want = append(want, s.Routes[0])
			}
		}
		seen := map[string]bool{}
		for i := 0; i < 40*len(want); i++ {
			label := fmt.Sprintf("spread-%s-%d", tier, i)
			code, out, errOut := runSprint("routes", "--tier", tier, "--ids", label)
			if code != 0 {
				t.Fatalf("%s: exit %d stderr %q", label, code, errOut)
			}
			r, rest, ok := strings.Cut(strings.TrimSuffix(out, "\n"), " ")
			if !ok || !strings.Contains(rest, "/") {
				t.Fatalf("%s: printed %q, want \"<route> <via>/<model>\"", label, out)
			}
			seen[r] = true
		}
		for _, r := range want {
			if !seen[r] {
				t.Errorf("--tier %s never printed %s over %d labels (printed %v)", tier, r, 40*len(want), seen)
			}
		}
		if len(seen) != len(want) {
			t.Errorf("--tier %s printed %d distinct routes, want %d: %v", tier, len(seen), len(want), seen)
		}
	}
	code, out, _ := runSprint("routes", "--spread")
	if code != 0 || !strings.Contains(out, "mercury") || !strings.Contains(out, "dspro") || strings.Count(out, "\n") != 7 {
		t.Fatalf("--spread: exit %d\n%s", code, out)
	}
	for _, args := range [][]string{
		{"routes", "--tier", "turbo", "--ids", "x"},
		{"routes", "--ids", "x"},
		{"routes", "--spread", "--rung", "pro"},
		{"routes", "--tier", "pro", "--ids", "x", "--spread"},
	} {
		if code, out, _ := runSprint(args...); code != 2 || out != "" {
			t.Fatalf("%v: exit %d stdout %q, want exit 2 and nothing printed", args, code, out)
		}
	}
}
