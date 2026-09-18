package worklang_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/worklang"
)

// mustExpandPlan parses a fixture plan and expands it, failing the test on a
// refusal. The fixture is data, never a program; there is no network and no
// model call.
func mustExpandPlan(t *testing.T, body string) []worklang.Card {
	t.Helper()
	plan, err := worklang.ParsePlan("work.work", []byte(body), worklang.DefaultLimits())
	if err != nil {
		t.Fatalf("fixture plan was refused: %v", err)
	}
	cards, err := worklang.ExpandPlan(plan)
	if err != nil {
		t.Fatalf("fixture plan did not expand: %v", err)
	}
	return cards
}

// renderAll renders every card through the deterministic printer.
func renderAll(t *testing.T, cards []worklang.Card) map[string][]byte {
	t.Helper()
	out := make(map[string][]byte, len(cards))
	for _, c := range cards {
		out[c.Node] = worklang.RenderCard(c)
	}
	return out
}

// The expansion contract of docs/SPEC-WORKLANG.md, seen red first:
// worklang-expansion-is-deterministic-and-replayable,
// worklang-card-carries-its-budget-and-floor, and
// worklang-duplicate-branch-name-refuses.
func TestWorklangExpand(t *testing.T) {
	// A two-node plan: n1 has no need, n2 needs n1. Both hand-written.
	const twoNodes = `(:plan :version 1
 (:goal :id "g" :acceptance ((:id "a1" :kind :test :subject "test:x" :predicate :passes)))
 (:node :id "n1" :kind docs :repo "o/r" :base "dev" :needs ()
  :inputs ((:spec "docs/a.md:1-2"))
  :output (:branch "rowan/n1-a" :green ("test:a"))
  :budget (:minutes 30 :tokens 120000 :model-floor sonnet)
  :affinity (:bench verify :route "deepseek-flash"))
 (:node :id "n2" :kind go-fix :repo "o/r" :base "dev" :needs ("n1")
  :inputs ((:spec "docs/b.md:3-4"))
  :output (:branch "rowan/n2-b" :green ("test:b"))
  :budget (:minutes 45 :tokens 180000 :model-floor opus)
  :affinity (:bench local :route "deepseek-flash"))
 (:clip :per-node))`

	// worklang-expansion-is-deterministic-and-replayable: the same plan and
	// pinned facts expand twice to byte-identical cards; a re-expansion after
	// one fact changes appends only the new card, minting no id.
	t.Run("worklang-expansion-is-deterministic-and-replayable", func(t *testing.T) {
		first := renderAll(t, mustExpandPlan(t, twoNodes))
		second := renderAll(t, mustExpandPlan(t, twoNodes))
		if len(first) != 2 {
			t.Fatalf("cards = %d, want 2", len(first))
		}
		for id, want := range first {
			got, ok := second[id]
			if !ok {
				t.Fatalf("second expansion minted no card for %s", id)
			}
			if string(got) != string(want) {
				t.Errorf("card %s is not byte-identical across expansions:\n%q\n%q", id, want, got)
			}
		}

		// Re-expansion into the same out directory after one fact changes: a
		// third node is added, and only its card is written. The existing cards
		// are untouched and no id is minted for them again.
		out := t.TempDir()
		cards := mustExpandPlan(t, twoNodes)
		written, err := worklang.ExpandDir(out, cards)
		if err != nil {
			t.Fatalf("first expansion: %v", err)
		}
		if written != 2 {
			t.Fatalf("first expansion wrote %d cards, want 2", written)
		}
		before, err := os.ReadFile(filepath.Join(out, "n2", "card"))
		if err != nil {
			t.Fatalf("read n2 card: %v", err)
		}

		const threeNodes = `(:plan :version 1
 (:goal :id "g" :acceptance ((:id "a1" :kind :test :subject "test:x" :predicate :passes)))
 (:node :id "n1" :kind docs :repo "o/r" :base "dev" :needs ()
  :inputs ((:spec "docs/a.md:1-2"))
  :output (:branch "rowan/n1-a" :green ("test:a"))
  :budget (:minutes 30 :tokens 120000 :model-floor sonnet)
  :affinity (:bench verify :route "deepseek-flash"))
 (:node :id "n2" :kind go-fix :repo "o/r" :base "dev" :needs ("n1")
  :inputs ((:spec "docs/b.md:3-4"))
  :output (:branch "rowan/n2-b" :green ("test:b"))
  :budget (:minutes 45 :tokens 180000 :model-floor opus)
  :affinity (:bench local :route "deepseek-flash"))
 (:node :id "n3" :kind audit :repo "o/r" :base "dev" :needs ()
  :inputs ((:issue "o/r#9"))
  :output (:branch "rowan/n3-c" :green ("test:c"))
  :budget (:minutes 15 :tokens 60000 :model-floor sonnet)
  :affinity (:bench verify :route "deepseek-flash"))
 (:clip :per-node))`

		cards = mustExpandPlan(t, threeNodes)
		written, err = worklang.ExpandDir(out, cards)
		if err != nil {
			t.Fatalf("re-expansion: %v", err)
		}
		if written != 1 {
			t.Fatalf("re-expansion wrote %d cards, want only the new card (1)", written)
		}
		after, err := os.ReadFile(filepath.Join(out, "n2", "card"))
		if err != nil {
			t.Fatalf("read n2 card after re-expansion: %v", err)
		}
		if string(after) != string(before) {
			t.Errorf("re-expansion rewrote an existing card:\n%q\n%q", before, after)
		}
		if _, err := os.Stat(filepath.Join(out, "n3", "card")); err != nil {
			t.Errorf("the new card was not appended: %v", err)
		}
	})

	// worklang-card-carries-its-budget-and-floor: a card carries its minutes,
	// tokens and model floor, and a route below the floor is refused, never
	// downgraded.
	t.Run("worklang-card-carries-its-budget-and-floor", func(t *testing.T) {
		cards := mustExpandPlan(t, twoNodes)
		card := worklang.RenderCard(cards[0])
		for _, want := range []string{
			"card n1", "30m", "120000 tokens", "model-floor sonnet",
			"bench=verify", "route=deepseek-flash", "rowan/n1-a", "test:a",
		} {
			if !strings.Contains(string(card), want) {
				t.Errorf("card does not carry %q:\n%s", want, card)
			}
		}

		below := `(:plan :version 1
 (:node :id "n1" :kind docs :repo "o/r" :base "dev"
  :output (:branch "rowan/n1-a" :green ("test:a"))
  :budget (:minutes 30 :tokens 120000 :model-floor sonnet)
  :affinity (:bench verify :route "deepseek-flash" :model haiku))
 (:clip :per-node))`
		plan, err := worklang.ParsePlan("work.work", []byte(below), worklang.DefaultLimits())
		if err != nil {
			t.Fatalf("fixture plan was refused: %v", err)
		}
		_, err = worklang.ExpandPlan(plan)
		if err == nil {
			t.Fatal("a route below the model floor was accepted instead of refused")
		}
		ref := assertRefusal(t, err)
		msg := ref.Error()
		for _, want := range []string{"haiku", "sonnet", "deepseek-flash"} {
			if !strings.Contains(msg, want) {
				t.Errorf("below-floor refusal does not name %q: %s", want, msg)
			}
		}
	})

	// worklang-duplicate-branch-name-refuses: two nodes deriving the same
	// :branch string is refused at expansion naming both, before any card is
	// written.
	t.Run("worklang-duplicate-branch-name-refuses", func(t *testing.T) {
		dup := `(:plan :version 1
 (:node :id "n1" :kind docs :repo "o/r" :base "dev"
  :output (:branch "rowan/same-name" :green ("test:a"))
  :budget (:minutes 30 :tokens 120000 :model-floor sonnet)
  :affinity (:bench verify :route "deepseek-flash"))
 (:node :id "n2" :kind docs :repo "o/r" :base "dev"
  :output (:branch "rowan/same-name" :green ("test:b"))
  :budget (:minutes 30 :tokens 120000 :model-floor sonnet)
  :affinity (:bench verify :route "deepseek-flash"))
 (:clip :per-node))`
		plan, err := worklang.ParsePlan("work.work", []byte(dup), worklang.DefaultLimits())
		if err != nil {
			t.Fatalf("fixture plan was refused: %v", err)
		}
		_, err = worklang.ExpandPlan(plan)
		if err == nil {
			t.Fatal("a duplicate branch name was accepted instead of refused")
		}
		ref := assertRefusal(t, err)
		msg := ref.Error()
		for _, want := range []string{"n1", "n2", "rowan/same-name"} {
			if !strings.Contains(msg, want) {
				t.Errorf("duplicate-branch refusal does not name %q: %s", want, msg)
			}
		}
	})
}
