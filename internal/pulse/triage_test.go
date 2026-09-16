package pulse

// G2's red tests: every one of the seven cases builds a packet under the ceiling, and the
// one line the packet demands round-trips through the parser. What they pin is the price of
// a decision: a packet that grew with its evidence would be a coordinator turn again.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bigEvidence is the largest plausible state for one case: a hundred RESULT lines, each
// longer than a line ever is, and a rule table of twenty rows. A packet that fits THIS fits
// the day.
func bigEvidence() ([]string, []RuleRow) {
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, fmt.Sprintf("RESULT card-%03d sha=0123456789ab %s", i, strings.Repeat("x", 600)))
	}
	var rules []RuleRow
	for i := 0; i < 20; i++ {
		rules = append(rules, RuleRow{
			Kind:      "signature",
			Condition: fmt.Sprintf("condition-%02d-%s", i, strings.Repeat("y", 200)),
			Verdict:   "REFUSE",
		})
	}
	return lines, rules
}

// TestTriagePacketIsUnderTheCeilingForEveryCase: each of the seven kinds builds a packet,
// the packet is under PacketMax bytes, its line 1 binds the bytes below it, and it says what
// the one line back must be. The mutation that matters: a packet that pastes its evidence
// in whole -- 60k bytes, and the cheap decision is not cheap any more.
func TestTriagePacketIsUnderTheCeilingForEveryCase(t *testing.T) {
	lines, rules := bigEvidence()
	for _, kind := range TriageKinds {
		for i := range rules {
			rules[i].Kind = kind
		}
		p := Packet{Case: kind, Ref: "card-892", Result: lines, Refusal: "REFUSED " + kind + ": " + strings.Repeat("z", 900), Rules: rules}
		card := p.Card()
		if len(card) > PacketMax {
			t.Errorf("%s packet is %d bytes, over %d", kind, len(card), PacketMax)
		}
		if len(card) == 0 {
			t.Fatalf("%s packet is empty", kind)
		}
		head, body, ok := strings.Cut(card, "\n")
		if !ok {
			t.Fatalf("%s packet has no line 1", kind)
		}
		sum := sha256.Sum256([]byte(body))
		want := fmt.Sprintf("RESULT triage-%s-card-892 sha=%s", kind, hex.EncodeToString(sum[:])[:12])
		if head != want {
			t.Errorf("%s line 1 is\n  %s\nwant\n  %s", kind, head, want)
		}
		for _, must := range []string{
			"Do not run go build",
			"MODEL: " + TriageRoute,
			"TRIAGE " + kind + " <verdict> <rule-row-or-NEW>",
			"RULES ",
			"REFUSAL ",
		} {
			if !strings.Contains(card, must) {
				t.Errorf("%s packet does not carry %q", kind, must)
			}
		}
		// The evidence is what gets cut, never the rules or the verdict shape.
		if !strings.Contains(card, "result lines dropped") {
			t.Errorf("%s packet dropped no evidence and still fits; the fixture is not the largest state", kind)
		}
	}
}

// TestTriageVerdictRoundTrips: the line the card demands is the line the parser reads, for
// every case and every verdict. The mutation that matters: a parser that accepts a
// four-field line whose verdict is a word nobody defined.
func TestTriageVerdictRoundTrips(t *testing.T) {
	for _, kind := range TriageKinds {
		for _, verdict := range TriageVerdicts {
			in := Verdict{Case: kind, Verdict: verdict, Rule: "condition-03"}
			got, err := ParseTriage(in.Line())
			if err != nil {
				t.Fatalf("%s %s: %v", kind, verdict, err)
			}
			if got != in {
				t.Errorf("round trip gave %+v, want %+v", got, in)
			}
		}
	}
	// A verdict with no rule is NEW, and it still round-trips.
	got, err := ParseTriage(Verdict{Case: "nosha", Verdict: "HOLD"}.Line())
	if err != nil || got.Rule != NewRule {
		t.Errorf("an empty rule gave %+v, %v; want rule %s", got, err, NewRule)
	}
}

// TestTriageParserRefusesAnythingElse: a verdict this tool half-read is a verdict nobody
// approved, and every refusal says what was wrong.
func TestTriageParserRefusesAnythingElse(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"I think this should be refused.", "no TRIAGE line"},
		{"TRIAGE nosha REFUSE", "want 4"},
		{"TRIAGE nosha MAYBE row-1", "not one of"},
		{"TRIAGE invented REFUSE row-1", "not one of"},
		{"TRIAGE nosha REFUSE row-1\nTRIAGE scope ADMIT row-2", "two TRIAGE lines"},
	} {
		if _, err := ParseTriage(c.in); err == nil {
			t.Errorf("%q was accepted; it must not be", c.in)
		} else if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q gave %q, want it to name %q", c.in, err, c.want)
		}
	}
	// The one thing a route does that must still work: the line with a sentence before it
	// is a route that talked, and the line is still the answer.
	if _, err := ParseTriage("Here is my answer.\nTRIAGE scope ADMIT condition-01\n"); err != nil {
		t.Errorf("a preamble made the answer unreadable: %v", err)
	}
}

// TestTriageNewVerdictBecomesAPendingRule: the lesson becomes a row the same hour, and the
// row says nobody has read it yet.
func TestTriageNewVerdictBecomesAPendingRule(t *testing.T) {
	queue := t.TempDir()
	v := Verdict{Case: "orphan", Verdict: "RETRY", Rule: NewRule}
	wrote, err := v.Apply(queue, "no job dir 10 min after launch")
	if err != nil || !wrote {
		t.Fatalf("Apply gave %v, %v; want a row written", wrote, err)
	}
	rows := ReadRules(queue)
	if len(rows) != 1 {
		t.Fatalf("RULES.tsv has %d rows, want 1", len(rows))
	}
	// The row goes through the one writer of RULES.tsv (rules.go): five columns and the
	// state, with `-` where a triage-written row has nothing to say. The columns are tab
	// separated, so a space inside a cell stays a space and the row reads as a sentence.
	want := RuleRow{Kind: "orphan", Condition: "no job dir 10 min after launch", Verdict: "RETRY", Since: "-", Source: "triage", State: "pending"}
	if rows[0] != want {
		t.Errorf("row is %+v, want %+v", rows[0], want)
	}
	// A verdict that named a row writes nothing: it was already policy.
	wrote, err = Verdict{Case: "orphan", Verdict: "RETRY", Rule: "condition-01"}.Apply(queue, "x")
	if err != nil || wrote {
		t.Errorf("a verdict naming a row wrote %v, %v; want nothing written", wrote, err)
	}
	if len(ReadRules(queue)) != 1 {
		t.Errorf("the table grew on a verdict that decided by a row")
	}
}

// TestTriageOffersOnlyTheRowsThatCouldDecide: a row of another kind can only mislead the
// route, so it is nowhere in the packet.
func TestTriageOffersOnlyTheRowsThatCouldDecide(t *testing.T) {
	queue := t.TempDir()
	for _, r := range []RuleRow{
		{Kind: "scope", Condition: "outside-workset", Verdict: "REFUSE"},
		{Kind: "nosha", Condition: "line1-has-no-sha", Verdict: "RETRY"},
		{Kind: "*", Condition: "second-abstain", Verdict: "ESCALATE"},
	} {
		if err := AppendRule(queue, r); err != nil {
			t.Fatal(err)
		}
	}
	p := BuildPacket(queue, "scope", "card-1", []string{"RESULT card-1"}, "REFUSED scope")
	if len(p.Rules) != 2 {
		t.Fatalf("rules=%d, want 2 (its own kind and the every-kind row): %+v", len(p.Rules), p.Rules)
	}
	card := p.Card()
	if strings.Contains(card, "line1-has-no-sha") {
		t.Error("the packet carries a row of another kind")
	}
	if !strings.Contains(card, "second-abstain") {
		t.Error("the packet drops the row written for every kind")
	}
}

// TestTriageVerbWritesTheCard drives the verb end to end over an evidence file.
func TestTriageVerbWritesTheCard(t *testing.T) {
	queue := t.TempDir()
	if err := os.MkdirAll(filepath.Join(queue, "UNDECIDED"), 0o755); err != nil {
		t.Fatal(err)
	}
	evidence := "RESULT card-892 sha=0123456789ab\nABSTAIN reason=idle=300\nPERMISSION denied: reading outside the job directory\n"
	if err := os.WriteFile(filepath.Join(queue, "UNDECIDED", "fence.txt"), []byte(evidence), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AppendRule(queue, RuleRow{Kind: "fence", Condition: "outside-job-dir", Verdict: "RETRY"}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "card.md")
	var stdout, stderr bytes.Buffer
	if exit := Triage(TriageInput{Case: "fence", Queue: queue, Out: out, Ref: "card-892", Stdout: &stdout, Stderr: &stderr}); exit != 0 {
		t.Fatalf("exit %d: %s%s", exit, stdout.String(), stderr.String())
	}
	if n := strings.Count(strings.TrimSpace(stdout.String()), "\n"); n != 0 {
		t.Errorf("the verb printed %d lines, want 1:\n%s", n+1, stdout.String())
	}
	if !strings.HasPrefix(stdout.String(), "TRIAGE OK case=fence ref=card-892 route="+TriageRoute) {
		t.Errorf("the line is %q", stdout.String())
	}
	card, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(card), "PERMISSION denied") {
		t.Error("the packet dropped the refusal line, which is the evidence that decides")
	}
	if !strings.Contains(string(card), "outside-job-dir") {
		t.Error("the packet dropped the candidate rule row")
	}

	// A case nobody named has no rows to offer and is refused.
	var s2, e2 bytes.Buffer
	if exit := Triage(TriageInput{Case: "invented", Queue: queue, Out: out, Stdout: &s2, Stderr: &e2}); exit != 2 {
		t.Errorf("an unknown case exited %d, want 2", exit)
	}
	if !strings.Contains(e2.String(), "not one of") {
		t.Errorf("the refusal does not name the seven: %s", e2.String())
	}
}
