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
