package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

// TestDealVerbKeepsTheLoadCeilingTheFillDropped is the hold on #3304 (Stella, at 6f15bc9f):
// the fill's load brake went and no executable called the dealer, so a hot bench took every
// card. `nova-pulse deal` is that executable: over one Redis with a hot bench (25 per core)
// and a cool one, every card goes to the cool bench's ready queue with its ROUTE: and
// MODEL: written, and the hot bench's queue stays empty; with only the hot bench, nothing
// is dealt and every card is HELD naming the ceiling.
func TestDealVerbKeepsTheLoadCeilingTheFillDropped(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.HSet("bench:hot", "working", "0", "load1", "200", "ncpu", "8")
	mr.HSet("bench:hot:desired", "slots", "8", "legs", "go")
	mr.HSet("bench:cool", "working", "0", "load1", "2", "ncpu", "8")
	mr.HSet("bench:cool:desired", "slots", "4", "legs", "go")

	root := t.TempDir()
	undealt := filepath.Join(root, "undealt")
	ready := filepath.Join(root, "ready")
	table := filepath.Join(root, "routes.tsv")
	if err := os.WriteFile(table, []byte("*\troute-a\tmodel-a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(undealt, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"card-1.md", "card-2.md", "card-3.md"} {
		if err := os.WriteFile(filepath.Join(undealt, n), []byte("KIND: build\nLEG: go\nDEPENDS-ON: -\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	deal := func(benches string) (int, string, string) {
		var out, errb bytes.Buffer
		code := run([]string{"deal", "--undealt", undealt, "--ready-root", ready, "--bench", benches,
			"--redis", mr.Addr(), "--route-table", table}, &out, &errb, time.Now())
		return code, out.String(), errb.String()
	}

	code, out, errs := deal("hot")
	if code != 0 {
		t.Fatalf("deal to the hot bench exited %d: %s", code, errs)
	}
	if got := globNames(t, filepath.Join(ready, "hot")); len(got) != 0 {
		t.Fatalf("a bench at 25 per core was dealt %v; the ceiling is the dealer's now", got)
	}
	if !strings.Contains(out, "held=3") || !strings.Contains(out, "load ceiling") {
		t.Fatalf("want every card HELD naming the load ceiling, got:\n%s", out)
	}

	code, out, errs = deal("hot,cool")
	if code != 0 {
		t.Fatalf("deal exited %d: %s", code, errs)
	}
	if got := globNames(t, filepath.Join(ready, "hot")); len(got) != 0 {
		t.Fatalf("the hot bench was dealt %v with a cool bench beside it", got)
	}
	got := globNames(t, filepath.Join(ready, "cool"))
	if len(got) != 3 {
		t.Fatalf("the cool bench was dealt %v, want all 3 cards; out:\n%s", got, out)
	}
	raw, err := os.ReadFile(filepath.Join(ready, "cool", "card-1.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "ROUTE: route-a") || !strings.Contains(string(raw), "MODEL: model-a") {
		t.Fatalf("a dealt card carries no ROUTE:/MODEL:, which the fill refuses:\n%s", raw)
	}
	if left := globNames(t, undealt); len(left) != 0 {
		t.Fatalf("cards left undealt: %v", left)
	}
}

func globNames(t *testing.T, dir string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "card-*.md"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range paths {
		out = append(out, filepath.Base(p))
	}
	return out
}

// TestDealRefusesANonFiniteLoadCeiling is Stella's hold on #3304 at 4c63ac13: cmdDeal
// refused only a negative --max-load-per-core, so NaN and +Inf passed, and in the dealer a
// load ratio is never greater than NaN or +Inf, which silently switched the ceiling off (0
// is the one documented no-ceiling value). Over a bench at 25 per core, every non-finite
// value must refuse with exit 2, naming the flag, before any card is dealt.
func TestDealRefusesANonFiniteLoadCeiling(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.HSet("bench:hot", "working", "0", "load1", "200", "ncpu", "8")
	mr.HSet("bench:hot:desired", "slots", "8", "legs", "go")

	for _, v := range []string{"NaN", "nan", "+Inf", "Inf", "inf", "-Inf", "-1"} {
		t.Run(v, func(t *testing.T) {
			root := t.TempDir()
			undealt := filepath.Join(root, "undealt")
			ready := filepath.Join(root, "ready")
			table := filepath.Join(root, "routes.tsv")
			if err := os.WriteFile(table, []byte("*\troute-a\tmodel-a\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(undealt, 0o755); err != nil {
				t.Fatal(err)
			}
			for _, n := range []string{"card-1.md", "card-2.md"} {
				if err := os.WriteFile(filepath.Join(undealt, n), []byte("KIND: build\nLEG: go\nDEPENDS-ON: -\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var out, errb bytes.Buffer
			code := run([]string{"deal", "--undealt", undealt, "--ready-root", ready, "--bench", "hot",
				"--redis", mr.Addr(), "--route-table", table, "--max-load-per-core", v}, &out, &errb, time.Now())
			if code != 2 {
				t.Fatalf("--max-load-per-core %s exited %d, want 2 (refused); out:\n%s\nstderr:\n%s", v, code, out.String(), errb.String())
			}
			if !strings.Contains(errb.String(), "--max-load-per-core") {
				t.Fatalf("the refusal does not name --max-load-per-core:\n%s", errb.String())
			}
			if got := globNames(t, filepath.Join(ready, "hot")); len(got) != 0 {
				t.Fatalf("--max-load-per-core %s dealt %v to a bench at 25 per core", v, got)
			}
			if left := globNames(t, undealt); len(left) != 2 {
				t.Fatalf("--max-load-per-core %s moved cards before refusing: undealt now %v", v, left)
			}
			if strings.Contains(out.String(), "DEAL ") {
				t.Fatalf("--max-load-per-core %s ran a deal pass before refusing:\n%s", v, out.String())
			}
		})
	}
}
