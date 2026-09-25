package worklang_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/worklang"
)

// collectSink gathers derive and cut events for assertions.
type collectSink struct {
	derives []worklang.DeriveEvent
	cuts    []worklang.CutEvent
}

func (s *collectSink) Derive(e worklang.DeriveEvent) { s.derives = append(s.derives, e) }
func (s *collectSink) Cut(e worklang.CutEvent)       { s.cuts = append(s.cuts, e) }

// TestIssue2187 pins nova-tools#2187: the work language expander emits one
// derive event per node derived from the graph (node id, parent, rule) and
// one cut event per card cut (card id, pool candidate, template, route),
// while leaving the journal as the single replayable truth.
func TestIssue2187(t *testing.T) {
	t.Parallel()

	const twoNodes = `(:plan :version 1
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

	sink := &collectSink{}

	p, err := worklang.ParsePlan("work.work", []byte(twoNodes), worklang.DefaultLimits())
	if err != nil {
		t.Fatalf("fixture plan was refused: %v", err)
	}
	cards, err := worklang.ExpandPlanWithSink(p, sink)
	if err != nil {
		t.Fatalf("plan did not expand: %v", err)
	}

	// One derive event per node.
	if len(sink.derives) != 2 {
		t.Fatalf("derive events = %d, want 2", len(sink.derives))
	}

	// First derive: n1, no parent (root node), rule=docs.
	if sink.derives[0].Node != "n1" {
		t.Errorf("first derive node = %q, want n1", sink.derives[0].Node)
	}
	if sink.derives[0].Parent != "" {
		t.Errorf("first derive parent = %q, want empty (root node)", sink.derives[0].Parent)
	}
	if sink.derives[0].Rule != "docs" {
		t.Errorf("first derive rule = %q, want docs", sink.derives[0].Rule)
	}

	// Second derive: n2, parent=n1, rule=go-fix.
	if sink.derives[1].Node != "n2" {
		t.Errorf("second derive node = %q, want n2", sink.derives[1].Node)
	}
	if sink.derives[1].Parent != "n1" {
		t.Errorf("second derive parent = %q, want n1", sink.derives[1].Parent)
	}
	if sink.derives[1].Rule != "go-fix" {
		t.Errorf("second derive rule = %q, want go-fix", sink.derives[1].Rule)
	}

	// ExpandDir: one cut event per card actually written.
	out := t.TempDir()
	written, err := worklang.ExpandDirWithSink(out, cards, sink)
	if err != nil {
		t.Fatalf("expand dir: %v", err)
	}
	if written != 2 {
		t.Fatalf("written = %d, want 2", written)
	}

	if len(sink.cuts) != 2 {
		t.Fatalf("cut events = %d, want 2", len(sink.cuts))
	}

	// First cut: card=n1, pool=verify, route=deepseek-flash.
	if sink.cuts[0].Card != "n1" {
		t.Errorf("first cut card = %q, want n1", sink.cuts[0].Card)
	}
	if sink.cuts[0].PoolCandidate != "verify" {
		t.Errorf("first cut pool candidate = %q, want verify", sink.cuts[0].PoolCandidate)
	}
	if sink.cuts[0].Route != "deepseek-flash" {
		t.Errorf("first cut route = %q, want deepseek-flash", sink.cuts[0].Route)
	}
	if sink.cuts[0].Template == "" {
		t.Error("first cut template is empty")
	}

	// Second cut: card=n2, pool=local, route=deepseek-flash.
	if sink.cuts[1].Card != "n2" {
		t.Errorf("second cut card = %q, want n2", sink.cuts[1].Card)
	}
	if sink.cuts[1].PoolCandidate != "local" {
		t.Errorf("second cut pool candidate = %q, want local", sink.cuts[1].PoolCandidate)
	}
	if sink.cuts[1].Route != "deepseek-flash" {
		t.Errorf("second cut route = %q, want deepseek-flash", sink.cuts[1].Route)
	}
	if sink.cuts[1].Template == "" {
		t.Error("second cut template is empty")
	}

	// The journal stays the replayable truth: re-expand into the same dir.
	// No new cards are written, no cut events are emitted for existing cards.
	before, err := os.ReadFile(filepath.Join(out, "n2", "card"))
	if err != nil {
		t.Fatalf("read n2 card: %v", err)
	}
	sink2 := &collectSink{}
	written2, err := worklang.ExpandDirWithSink(out, cards, sink2)
	if err != nil {
		t.Fatalf("re-expand: %v", err)
	}
	if written2 != 0 {
		t.Fatalf("re-expand wrote %d cards, want 0 (journal not rewritten)", written2)
	}
	after, err := os.ReadFile(filepath.Join(out, "n2", "card"))
	if err != nil {
		t.Fatalf("read n2 card after re-expand: %v", err)
	}
	if string(after) != string(before) {
		t.Error("re-expand rewrote an existing journal card")
	}
	if len(sink2.cuts) != 0 {
		t.Errorf("re-expand emitted %d cut events for existing cards, want 0", len(sink2.cuts))
	}
}
