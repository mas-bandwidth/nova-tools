package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// This file is the merge layer's typed classification (`packet --decide`). It labels a
// packet entry with a risk score and a scope answer and NOTHING else: the classification
// never merges, never pushes, never approves a review and never clears a branch protection
// (SPEC-DECIDE rule 7). The state sent is the entry's public packet fields -- the pointers
// the packet already hands over and never the diff (SPEC-MERGE rule 23) -- so no private
// repository content leaves the lane.

// mergePacketState is the bounded public state one classification is asked about: the
// entry's kind, head, checks, gate, holds and read requirement, plus the card's public
// summary when one was given. It is assembled from the packet's own line fields and
// carries no diff and no record note.
func mergePacketState(e *merge.Entry, c merge.Classification, holds int, card string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "merge packet entry %s\n", oneline.Field(e.ID()))
	if e.IsPR() {
		fmt.Fprintf(&b, "kind: pull request #%d\n", e.PR)
	} else {
		fmt.Fprintf(&b, "kind: branch %s\n", oneline.Escape(e.Branch))
	}
	fmt.Fprintf(&b, "head: %s\n", oneline.Escape(merge.Short(e.OID)))
	fmt.Fprintf(&b, "checks: green=%d pending=%d red=%d\n", c.Checks.Green, c.Checks.Pending, c.Checks.Red)
	fmt.Fprintf(&b, "gate: %s\n", oneline.Escape(orDash(c.Gate.Kind)))
	fmt.Fprintf(&b, "holds: %d\n", holds)
	fmt.Fprintf(&b, "needs_read: %s\n", oneline.Escape(orDash(e.NeedsRead)))
	if strings.TrimSpace(card) != "" {
		fmt.Fprintf(&b, "card:\n%s\n", oneline.Escape(oneline.Cap(card, oneline.TailBytes)))
	}
	return b.String()
}

// orDash is the empty field's dash, so a state line never reads as a missing key.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// mergePacketQuestions are the two candidates a packet entry is classified on: its review
// risk and whether this packet's read is the right scope. They advise; they never
// authorize (rule 6).
func mergePacketQuestions() map[string]decide.Question {
	return map[string]decide.Question{
		"risk":  {Instructions: "Score the merge risk of this change from 0 to 3: 0 docs or tests only; 1 a small local change; 2 touches a shared package or a contract; 3 touches CI, secrets, sandbox or permissions.", Score: []string{"docs or tests only", "small local change", "touches a shared package or a contract", "touches CI, secrets, sandbox or permissions"}},
		"scope": {Instructions: "Does this change stay within the scope its packet's entry describes, so one reader is enough?", Noul: true},
	}
}

// mergePacketEvidence is the pointer beside the answer: the entry the state came from, so
// a human can retrieve it (rule 10).
func mergePacketEvidence(e *merge.Entry) string {
	if e.IsPR() {
		return fmt.Sprintf("pr#%d", e.PR)
	}
	return "branch#" + oneline.Field(e.Branch)
}

// classifyPacketEntry asks the typed-decision route one advisory risk/scope classification
// for a handed-over entry and returns the one PACKET DECIDE line, or "" when the provider
// could not answer. It is the caller's closure handed to merge.Pass.PacketAnnotate. card
// is the card file's public summary, or "" for none.
func classifyPacketEntry(ctx context.Context, client *decide.Client, floor float64, card string) func(e *merge.Entry, c merge.Classification, holds int) string {
	return func(e *merge.Entry, c merge.Classification, holds int) string {
		answers, _, err := client.Decide(ctx, mergePacketState(e, c, holds, card), mergePacketQuestions())
		if err != nil {
			// A provider that could not answer is a suggestion with no answer, never a
			// licence to change the packet: the line is the news.
			return "PACKET DECIDE entry=" + oneline.Field(e.ID()) + " below=all evidence=" + mergePacketEvidence(e) + " note=" + oneline.Escape(oneline.Cap(err.Error(), oneline.TailBytes))
		}
		// The merge layer writes the entry itself, and the floor/suggestion shape is
		// slice 1's decide.Line reused, so a below-floor answer is a suggestion here the
		// same way it is everywhere else.
		line := strings.TrimSpace(decide.Line("", answers, floor))
		return fmt.Sprintf("PACKET DECIDE entry=%s %s evidence=%s", oneline.Field(e.ID()), line, mergePacketEvidence(e))
	}
}
