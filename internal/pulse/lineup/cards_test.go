package lineup

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testCard(name string, lines ...string) Card {
	return Card{Name: name, Raw: []byte(strings.Join(append(lines, "", "STEP 1. cd repo", ""), "\n"))}
}

var goodHeader = []string{
	"RESULT x sha=09fbedc90521 -- a card",
	"KIND: fix",
	"LEG: go",
	"REPO: mas-bandwidth/nova-tools",
	"base-sha: 09fbedc905218d5d4bdf5d039d0baff366ef2545",
	"PATHS: internal/pulse/wire.go internal/pulse/wire_test.go cmd/nova-pulse/lineup_cards.go internal/pulse/lineup/*",
	"MODEL: opencode/deepseek-v4-flash",
	"NO-SUBAGENTS: work in this session only",
}

func without(lines []string, prefix string) []string {
	var out []string
	for _, l := range lines {
		if !strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	return out
}

func cleanLint(Card) (string, error) { return "", nil }

func TestPlanCIPackagesFromPaths(t *testing.T) {
	p, err := PlanCI(testCard("c.md", goodHeader...))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"./cmd/nova-pulse", "./internal/pulse", "./internal/pulse/lineup"}
	if !reflect.DeepEqual(p.Packages, want) || p.Repo != "mas-bandwidth/nova-tools" || !strings.HasPrefix(p.BaseSHA, "09fbedc9") {
		t.Fatalf("plan %+v, want packages %v", p, want)
	}
	for _, drop := range []string{"REPO:", "base-sha:", "PATHS:"} {
		if _, err := PlanCI(testCard("c.md", without(goodHeader, drop)...)); err == nil {
			t.Errorf("a card without %s planned a ci card", drop)
		}
	}
	docs := append(without(goodHeader, "PATHS:"), "PATHS: docs/NOTES.md")
	if _, err := PlanCI(testCard("c.md", docs...)); err == nil {
		t.Error("a go card whose PATHS name no package planned a ci card that tests nothing")
	}
	lisp := append(without(without(goodHeader, "PATHS:"), "LEG:"), "LEG: lisp", "PATHS: src/card.lisp")
	if _, err := PlanCI(testCard("c.md", lisp...)); err != nil {
		t.Errorf("a non-go card with PATHS was refused: %v", err)
	}
}

func TestCardChecksNameEachFailingCard(t *testing.T) {
	cc := CardChecks{
		Lint: func(c Card) (string, error) {
			switch c.Name {
			case "lint.md":
				return "paths-at-base: 6: internal/pulse/gone.go", nil
			case "broken.md":
				return "", errors.New("exec: nova-swarm: not found")
			}
			return "", nil
		},
		AllowedRoutes: []string{"opencode/deepseek-v4-flash"},
	}
	cards := []Card{
		testCard("good.md", goodHeader...),
		testCard("lint.md", goodHeader...),
		testCard("broken.md", goodHeader...),
		testCard("route.md", append(without(goodHeader, "MODEL:"), "MODEL: openrouter/gpt-nano")...),
		testCard("dealt.md", without(goodHeader, "MODEL:")...),
		testCard("nosub.md", without(goodHeader, "NO-SUBAGENTS:")...),
		testCard("emptysub.md", append(without(goodHeader, "NO-SUBAGENTS:"), "NO-SUBAGENTS:")...),
		testCard("noci.md", without(goodHeader, "base-sha:")...),
	}
	var got []string
	for _, f := range cc.Check(cards) {
		got = append(got, f.Card+" "+f.Check)
	}
	want := []string{"lint.md lint", "broken.md lint", "route.md route", "nosub.md no-subagents", "emptysub.md no-subagents", "noci.md ci-dry-run"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("findings %v, want %v", got, want)
	}
}

func TestCardChecksQueueFindings(t *testing.T) {
	got := CardChecks{Lint: cleanLint}.Check(nil)
	if len(got) != 2 || got[0].Check != CheckQueue || got[1].Check != CheckQueue {
		t.Fatalf("an empty queue with no routes: %+v, want two queue findings", got)
	}
	got = CardChecks{AllowedRoutes: []string{"r"}}.Check([]Card{testCard("c.md", goodHeader...)})
	if len(got) == 0 || got[0].Check != CheckLint {
		t.Fatalf("no lint: %+v, want a lint RED", got)
	}
}

func TestReadQueueFrontThenPending(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"pending/card-2.md", "front/card-9.md", "pending/card-1.md", "cut.md", "ROUTES-code", "pending/notes.txt"} {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cards, err := ReadQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range cards {
		names = append(names, c.Name)
	}
	if want := []string{"cut.md", "card-9.md", "card-1.md", "card-2.md"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("queue %v, want %v", names, want)
	}
	if _, err := ReadQueue(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("a missing queue read as empty")
	}
}

func TestReadRoutesSkipsCommentsAndBlanks(t *testing.T) {
	f := filepath.Join(t.TempDir(), "ROUTES-code")
	if err := os.WriteFile(f, []byte("# allowed\n\nopencode/a\n  opencode/b  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRoutes(f)
	if err != nil || !reflect.DeepEqual(got, []string{"opencode/a", "opencode/b"}) {
		t.Fatalf("routes %v err %v", got, err)
	}
}
