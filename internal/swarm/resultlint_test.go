package swarm

import (
	"strings"
	"testing"
)

// The fixtures are line 1 and line 2 of real RESULT.md files, copied verbatim from the job
// directories on the benches on 2026-09-23 ~04:00Z (reports/failed-cards-2026-09-22.md in
// rowan-new, classes 10 malformed-result and 19 blocked-head-matches-base). The label and
// bench are named so a reader can go and look.

// templateDones is every RESULT of the 2026-09-22 sprint whose line 2 begins DONE and
// carries the template's `<-` arrow: the model copied `DONE <- or: ABSTAIN <why> | BLOCKED
// <why>` and the harvest counted a DONE. The report's snapshot (01:45Z) counted five of
// them (nx-g39, nx-c37, rule3-toolwork-158, rule3-bus-reply-871, rule3-test-168); the
// other three landed on the benches around and after it.
var templateDones = []struct{ label, bench, line1, line2 string }{
	{"card-nx-g39-pprof-in-long-verbs", "space",
		"RESULT: nx-g39-pprof-in-long-verbs sha=39d1d6a5c9182967405eb6211fadeb2eaf3dffe4 -- feature 32",
		"DONE            <- or: ABSTAIN <why> | BLOCKED <why>"},
	{"card-nx-c37-load-shaped-flakes", "vision",
		"RESULT: nx-c37-load-shaped-flakes sha=09fbedc90521 -- #2452 #2460 (timing under load), #1699 (reused counter)",
		"DONE            <- or: ABSTAIN <why> | BLOCKED <why>"},
	{"card-rule3-toolwork-158", "hulk",
		"RESULT: rule3-toolwork-158 sha=09fbedc90521 -- rule read docs/SPEC-TOOLWORK.md:158 (rule 7) against internal/docs",
		"DONE            <- or: BLOCKED head=09fbedc905218d5d4bdf5d039d0baff366ef2545"},
	{"card-rule3-bus-reply-871", "vision",
		"RESULT: rule3-bus-reply-871 sha=09fbedc90521 -- rule read docs/SPEC-BUS-REPLY.md:871 (rule 1) against internal/bus",
		"DONE            <- ABSTAIN"},
	{"card-rule3-test-168", "hulk",
		"RESULT: rule3-test-168 sha=09fbedc90521 -- rule read docs/SPEC-TEST.md:168 (rule 49) against internal/docs",
		"DONE            <- or: ABSTAIN <why> | BLOCKED <why>"},
	{"card-cell3-elixir-bool-values-valid-data", "hetzner",
		"RESULT: cell3-elixir-bool-values-valid-data sha=ead804093232 -- schema elixir/BoolValuesValidData Boolean values: valid-data write/read acceptance: the assertion that proves it, red...",
		"DONE            <- or: ABSTAIN <why> | BLOCKED <why> | RED <line> | ABSTAIN CONFORMS <file:line> <make target or CI job that runs it>"},
	{"card-00-holdfix-2867-50366419", "hetzner",
		"RESULT: holdfix-2867-50366419 sha=09fbedc90521 -- answer the friend HOLD on PR #2867 (rowan/nx-g03-issues-by-verb)",
		"DONE            <- or: ABSTAIN <why> | BLOCKED <why>"},
	{"card-00-holdfix-2865-3230b3b1", "vision",
		"RESULT: holdfix-2865-3230b3b1 sha=09fbedc90521 -- answer the friend HOLD on PR #2865 (rowan/fix3-nova-tools-2314)",
		"DONE            <- push and REPAIR comment blocked by missing GitHub auth; code fix complete"},
}

// headIsBase is the five of class 19: BLOCKED whose only word is `head=<the base-sha the
// card pins>`. The model read "head equals base" as a reason to stop. Nothing blocked it.
var headIsBase = []struct{ label, bench, line1, line2 string }{
	{"card-rule3-decide-1996", "space",
		"RESULT: rule3-decide-1996 sha=09fbedc905218d5d4bdf5d039d0baff366ef2545 -- rule read docs/SPEC-DECIDE.md:1996 (rule 51) against internal/decide",
		"BLOCKED head=09fbedc905218d5d4bdf5d039d0baff366ef2545"},
	{"card-read3-nova-tools-2522", "vision",
		"RESULT: read3-nova-tools-2522 sha=8359db4fec24 -- read of nova-tools#2522 at 8359db4fec24: pulse: implement Swarm Cards v2 Items A1–A7 card generator templates (#2498)",
		"BLOCKED head=8359db4fec24521b444d01bf6f90a9f981e9765e"},
	{"card-read3-nova-tools-2668", "hulk",
		"RESULT: read3-nova-tools-2668 sha=1f293d97786e -- read of nova-tools#2668 at 1f293d97786e: tools-20260922T211905Z: 1 approved PRs (#2645) — gated on hulk by land-lane",
		"BLOCKED head=1f293d97786ecd73788eae959ad50a6abbbf2a3d"},
	{"card-schema-cell-dart-hostile-input", "vision",
		"RESULT schema-cell-dart-hostile-input sha=ead804093232 -- schema cell dart/hostile-input (Hostile-input checks and negative controls): 1 open item(s), each asserted at the tip in its own file, red first",
		"BLOCKED head=ead804093232b3593facd987ebe2f726a10dd63d"},
	{"card-schema-read-spec-l1937-java", "hulk",
		`"RESULT schema-read-spec-l1937-java sha=eb12ceb36f2e -- read: does the java leg honor docs/SPEC.md:1937 (rule 1 under "4.12 Wide strings: ` + "`wstring(N)`" + `")`,
		"BLOCKED head=eb12ceb36f2ec25d0aec87b334d32e09ff95a2bb"},
}

func resultText(line1, line2 string) []byte {
	return []byte(line1 + "\n" + line2 + "\nSCHEMA: v2\nBRANCH rowan/x\nREPO mas-bandwidth/nova-tools\n")
}

func resultHasCheck(r ResultLint, check string) bool {
	for _, f := range r.Findings {
		if f.Check == check {
			return true
		}
	}
	return false
}

// TestResultLintFlagsTemplateDone is issue #2917's control: every template DONE of the
// 2026-09-22 sprint is refused as a DONE, every BLOCKED head==base is flagged, and the
// results the harvest must keep counting are not touched.
func TestResultLintFlagsTemplateDone(t *testing.T) {
	flagged := 0
	for _, c := range templateDones {
		r := LintResult(resultText(c.line1, c.line2), "")
		if r.CountsAsDone() || !resultHasCheck(r, "template-copied") {
			t.Errorf("%s (%s): line 2 %q: CountsAsDone=%v findings=%v, want template-copied and not DONE",
				c.label, c.bench, c.line2, r.CountsAsDone(), r.Findings)
			continue
		}
		flagged++
	}
	if flagged != len(templateDones) || flagged < 5 {
		t.Errorf("template DONEs flagged %d of %d, want all (the report's five among them)", flagged, len(templateDones))
	}

	flagged = 0
	for _, c := range headIsBase {
		r := LintResult(resultText(c.line1, c.line2), "")
		if r.Disposition != "BLOCKED" || !resultHasCheck(r, "blocked-head-is-base") {
			t.Errorf("%s (%s): line 2 %q: disposition=%q findings=%v, want blocked-head-is-base",
				c.label, c.bench, c.line2, r.Disposition, r.Findings)
			continue
		}
		flagged++
	}
	if flagged != 5 {
		t.Errorf("BLOCKED head==base flagged %d, want 5", flagged)
	}

	// The --base-sha a caller hands over is used when line 1 carries no sha=.
	r := LintResult(resultText("RESULT: no-pin -- a card whose line 1 lost its sha", "BLOCKED head=09fbedc905218d5d4bdf5d039d0baff366ef2545"), "09fbedc90521")
	if !resultHasCheck(r, "blocked-head-is-base") {
		t.Errorf("--base-sha not used when line 1 has no sha=: %v", r.Findings)
	}

	// Line 2 grammar: the shapes of class 10 that are not a template copy.
	for _, line2 := range []string{
		"",           // card-read3-nova-tools-2483 on hulk: `<RESULT.md line 2 empty>`
		"KIND: read", // card-schema-read-spec-l228-rust on space: the card header copied
		"DONE: all good",
		"DONEZO",
		"ABSTAIN",
		"BLOCKED",
		"<DONE | ABSTAIN <why> | BLOCKED <why>>",
		"  DONE",
	} {
		r := LintResult(resultText("RESULT x sha=09fbedc90521 -- t", line2), "")
		if r.CountsAsDone() || !resultHasCheck(r, "line2-grammar") {
			t.Errorf("line 2 %q: CountsAsDone=%v findings=%v, want line2-grammar and not DONE", line2, r.CountsAsDone(), r.Findings)
		}
	}
	if r := LintResult([]byte("RESULT only-one-line sha=09fbedc90521"), ""); r.CountsAsDone() || !resultHasCheck(r, "line2-grammar") {
		t.Errorf("a one-line RESULT: %v, want line2-grammar", r.Findings)
	}

	// Negative controls: results the harvest keeps, as written tonight.
	for _, c := range []struct{ line1, line2, disposition string }{
		{"RESULT x sha=09fbedc90521 -- t", "DONE", "DONE"},
		{"RESULT x sha=09fbedc90521 -- t", "DONE ", "DONE"},
		{"RESULT x sha=09fbedc90521 -- t", "DONE\tfix committed on rowan/x", "DONE"},
		{"RESULT x sha=09fbedc90521 -- t", "ABSTAIN out-of-scope cmd/nova-swarm/native.go needed", "ABSTAIN"},
		{"RESULT x sha=ead804093232 -- t", "ABSTAIN CONFORMS schema/go/text_test.go:12 make test-go", "ABSTAIN"},
		{"RESULT x sha=09fbedc90521 -- t", "RED TestFoo fails at base", "RED"},
		{"RESULT x sha=09fbedc90521 -- t", "GAP no test covers rule 7", "GAP"},
		{"RESULT x sha=09fbedc90521 -- t", "CONFORMS internal/docs/x.go:4", "CONFORMS"},
		// head= a different sha: the card moved, and that is a real block.
		{"RESULT cell-go-w2 sha=205a85684bc0 — schema matrix cell go/W2", "BLOCKED head=5ef591b53b3002ef0ab1cd89e8a2c275b6ca3a57", "BLOCKED"},
		// head==base WITH a reason: card-cell-java-w14 on hetzner; the reason is the block.
		{"RESULT cell-java-w14 sha=205a85684bc0 — schema matrix cell java/W14", "BLOCKED head=205a85684bc0 (javac not found in environment)", "BLOCKED"},
		// head=<git's error>: job-layout confusion (class 12), a different remedy, not this lint's.
		{"RESULT schema-cell-cs-scalar-bounds sha=ead804093232 -- t", "BLOCKED head=fatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree.", "BLOCKED"},
		{"RESULT: holdfix-2847-9e74ecc4 sha=09fbedc90521 -- t", "BLOCKED head=7ba71e03443fe8a6291e514223a088d55e91a591 reason=git auth unavailable", "BLOCKED"},
	} {
		r := LintResult(resultText(c.line1, c.line2), "")
		if len(r.Findings) != 0 || r.Disposition != c.disposition {
			t.Errorf("line 2 %q: disposition=%q findings=%v, want %s and no finding", c.line2, r.Disposition, r.Findings, c.disposition)
		}
		if got := r.CountsAsDone(); got != (c.disposition == "DONE") {
			t.Errorf("line 2 %q: CountsAsDone=%v", c.line2, got)
		}
	}

	// A CRLF result is read the way it was meant.
	if r := LintResult([]byte("RESULT x sha=09fbedc90521 -- t\r\nDONE\r\nBRANCH b\r\n"), ""); !r.CountsAsDone() || len(r.Findings) != 0 {
		t.Errorf("CRLF DONE: %v", r.Findings)
	}

	// Every check names a remedy, and every finding's check is one of them.
	for _, c := range append(append([]struct{ label, bench, line1, line2 string }{}, templateDones...), headIsBase...) {
		for _, f := range LintResult(resultText(c.line1, c.line2), "").Findings {
			if strings.TrimSpace(ResultLintRemedies[f.Check]) == "" {
				t.Errorf("check %q has no remedy", f.Check)
			}
		}
	}
	if len(ResultLintRemedies) != 3 {
		t.Errorf("ResultLintRemedies has %d checks, want 3", len(ResultLintRemedies))
	}
}
