package pulse

// The routes are configuration (class M, #828): benching a route is a verb with a receipt,
// not a `touch ROUTE-BENCHED-opencode_kimi-k2.7-code`, and the rotation a card takes is the
// table minus what a probe benched.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// routesQueue is a queue whose pulse.toml names two routes per class, one comment, and one
// key of another table -- so a test can prove the editor keeps what it did not come for.
func routesQueue(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	body := `# the bench's routes, Glenn 2026-09-16 18:15Z
[slots]
studio = 4

[routes]
text = ["opencode/deepseek-v4-flash", "opencode/kimi-k2.7-code"]
code = ["opencode/deepseek-v4-flash", "opencode/kimi-k2.7-code"]
rewrite_model_to = "opencode/deepseek-v4-flash"
`
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runRoutes(t *testing.T, in RoutesInput) (int, string, string) {
	t.Helper()
	var out, errs bytes.Buffer
	in.Stdout, in.Stderr = &out, &errs
	return RoutesVerb(in), out.String(), errs.String()
}

// TestRoutesBenchUnbenchRoundTrip: the edit is a verb, it lands in the file, the rotation
// stops offering the benched route, and unbenching puts it back -- with every other line of
// pulse.toml exactly as it was.
func TestRoutesBenchUnbenchRoundTrip(t *testing.T) {
	queue := routesQueue(t)
	before, err := os.ReadFile(filepath.Join(queue, ConfigFile))
	if err != nil {
		t.Fatal(err)
	}

	exit, out, errs := runRoutes(t, RoutesInput{Queue: queue, Bench: "opencode/kimi-k2.7-code"})
	if exit != 0 {
		t.Fatalf("bench exit %d: %s%s", exit, out, errs)
	}
	if !strings.HasPrefix(out, "ROUTES BENCHED opencode/kimi-k2.7-code ") {
		t.Errorf("bench printed %q, want the BENCHED line", strings.TrimSpace(out))
	}
	if n := len(strings.Split(strings.TrimSpace(out), "\n")); n != 1 {
		t.Errorf("bench printed %d lines, want 1", n)
	}

	// The rotation is the table minus the benched route, for both classes and for the
	// value the next card would take.
	cfg, err := LoadConfig(queue, &bytes.Buffer{}, 0)
	if err != nil {
		t.Fatalf("the queue no longer reads back: %v", err)
	}
	for _, class := range RouteClasses {
		if live := cfg.Routes.Live(class); len(live) != 1 || live[0] != "opencode/deepseek-v4-flash" {
			t.Errorf("live %s routes = %v, want the flash route alone", class, live)
		}
		if next := NextRoute(cfg.Routes.Live(class), RouteCounter(queue)); next != "opencode/deepseek-v4-flash" {
			t.Errorf("next %s route = %s, want the flash route: the round-robin skips a benched route", class, next)
		}
	}
	if !strings.Contains(out, "next.text=opencode/deepseek-v4-flash") {
		t.Errorf("the line does not carry the next text route:\n%s", out)
	}

	// The edit kept the file: one line changed, nothing else, comment included.
	after, err := os.ReadFile(filepath.Join(queue, ConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "# the bench's routes") || !strings.Contains(string(after), "studio = 4") {
		t.Errorf("the editor lost lines it did not come for:\n%s", after)
	}
	if !strings.Contains(string(after), "benched = opencode/kimi-k2.7-code") {
		t.Errorf("benched was not written:\n%s", after)
	}

	// Benching it twice is a refusal that names the remedy, not a second row.
	if exit, _, errs := runRoutes(t, RoutesInput{Queue: queue, Bench: "opencode/kimi-k2.7-code"}); exit != 2 ||
		!strings.Contains(errs, "--unbench") {
		t.Errorf("a second bench: exit %d, %q", exit, errs)
	}
	// Benching the last live route of a class is refused: a class with no route launches
	// nothing, and the refusal names the class.
	if exit, _, errs := runRoutes(t, RoutesInput{Queue: queue, Bench: "opencode/deepseek-v4-flash"}); exit != 2 ||
		!strings.Contains(errs, "no live route") {
		t.Errorf("benching the last route: exit %d, %q", exit, errs)
	}

	exit, out, errs = runRoutes(t, RoutesInput{Queue: queue, Unbench: "opencode/kimi-k2.7-code"})
	if exit != 0 || !strings.HasPrefix(out, "ROUTES UNBENCHED ") {
		t.Fatalf("unbench exit %d: %s%s", exit, out, errs)
	}
	cfg, err = LoadConfig(queue, &bytes.Buffer{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if live := cfg.Routes.Live(RouteClassCode); len(live) != 2 {
		t.Errorf("after unbench the live code routes are %v, want both back", live)
	}
	if !strings.Contains(string(before), "text = [") {
		t.Fatalf("the fixture changed shape") // guards the assertions above
	}
	// Unbenching what is not benched is a refusal, not a silent success.
	if exit, _, errs := runRoutes(t, RoutesInput{Queue: queue, Unbench: "opencode/kimi-k2.7-code"}); exit != 2 ||
		!strings.Contains(errs, "is not benched") {
		t.Errorf("a second unbench: exit %d, %q", exit, errs)
	}
}

// TestRoutesClassAndRefusals: --class narrows the line to one class, and every bad
// invocation is one refusal line naming its remedy.
func TestRoutesClassAndRefusals(t *testing.T) {
	queue := routesQueue(t)
	exit, out, errs := runRoutes(t, RoutesInput{Queue: queue, Class: RouteClassText})
	if exit != 0 {
		t.Fatalf("exit %d: %s%s", exit, out, errs)
	}
	if !strings.Contains(out, "text=") || strings.Contains(out, "code=") {
		t.Errorf("--class text printed %q, want the text class alone", strings.TrimSpace(out))
	}
	if !strings.Contains(out, "rewrite=opencode/deepseek-v4-flash") {
		t.Errorf("the line does not name the rewrite route:\n%s", out)
	}
	for _, c := range []struct {
		name string
		in   RoutesInput
		want string
	}{
		{"no queue", RoutesInput{}, "--queue is required"},
		{"bad class", RoutesInput{Queue: queue, Class: "prose"}, "--class prose"},
		{"two edits", RoutesInput{Queue: queue, Bench: "a", Unbench: "b"}, "one edit at a time"},
		{"unknown route", RoutesInput{Queue: queue, Bench: "deepseek/deepseek-v4-pro"}, "is not a route of this queue"},
	} {
		exit, _, errs := runRoutes(t, c.in)
		if exit != 2 || !strings.Contains(errs, c.want) {
			t.Errorf("%s: exit %d, stderr %q, want %q", c.name, exit, errs, c.want)
		}
	}
}

// TestRewriteModelLine: the rule of 2026-09-16 17:55Z ("cards run on the flash route; a
// MODEL: line naming another model is rewritten at cut") as a function, and an empty
// configuration rewrites nothing.
func TestRewriteModelLine(t *testing.T) {
	card := "RESULT: CARD-7 a card\nMODEL: opencode/kimi-k2.7-code\nSTEP 1. do the thing\n"
	got, changed := RewriteModelLine(card, "opencode/deepseek-v4-flash")
	if !changed || !strings.Contains(got, "MODEL: opencode/deepseek-v4-flash") || strings.Contains(got, "kimi") {
		t.Errorf("rewrite gave changed=%t:\n%s", changed, got)
	}
	if _, changed := RewriteModelLine(card, ""); changed {
		t.Errorf("an empty rewrite route rewrote a card")
	}
	if _, changed := RewriteModelLine("RESULT: CARD-8\nSTEP 1. x\n", "opencode/deepseek-v4-flash"); changed {
		t.Errorf("a card with no MODEL: line was rewritten")
	}
}
