package main

import (
	"os"
	"strings"
	"testing"
)

// TestSpecGrammarStrikesUnimplementedVerbs holds the drift audit of
// 2026-09-15 (docs/SPEC-REVIEW.md "What it deliberately does not do"): the
// binary ships only `packet`, `version` and `help`, so the six other verbs this
// draft specifies — verdict, answer, policy, roster, dedupe, cost — must have
// their output lines struck from the grammar, not listed as live lines a reader
// could mistake for emitted output.
func TestSpecGrammarStrikesUnimplementedVerbs(t *testing.T) {
	b, err := os.ReadFile("../../docs/SPEC-REVIEW.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)

	unshipped := []string{"verdict", "answer", "policy", "roster", "dedupe", "cost"}
	for _, v := range unshipped {
		if !strings.Contains(doc, "~~"+strings.ToUpper(v)+" ") {
			t.Errorf("grammar line for %s is not struck from the grammar (want ~~%s ...)", v, strings.ToUpper(v))
		}
	}
	if strings.Contains(doc, "~~PACKET ") {
		t.Errorf("PACKET is a shipped verb; its grammar lines must not be struck")
	}
}
