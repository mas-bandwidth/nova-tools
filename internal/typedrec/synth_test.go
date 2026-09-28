package typedrec

import (
	"strings"
	"testing"
)

// TestSynthesizeFromTwoModelLines is nova-tools#3689: the model writes line 1
// and line 2 only (or with a wrong BRANCH, the quack cards' shape); the
// wrapper's Synthesize makes a record ParseResult validates, with every typed
// field the wrapper's and the model's BRANCH gone. RED WITHOUT Synthesize: the
// two-line file is `SCHEMA missing`, and the wrong-BRANCH file contradicts the
// wrapper's branch.
func TestSynthesizeFromTwoModelLines(t *testing.T) {
	t.Parallel()

	const line1 = "RESULT: quack-fix sha=0123456789ab nova-tools fix: a card"
	const branch = "nova/flash-0924/quack-fix-a1"
	facts := WrapperFacts{
		Kind: KindFix, Attempt: 1, Repo: "mas-bandwidth/nova-tools", Branch: branch,
		Paths: []string{"internal/x/x.go", "internal/x/x_test.go"},
		Check: "pass", Red: "not-run", Green: "go test ./internal/x -run ^TestX$ -count=1: pass 0.10s; ok",
		Gates: []string{"go test ./internal/x -run ^TestX$ -count=1: pass 0.10s"},
	}
	opt := ParseOptions{ExpectedKind: KindFix, ExpectedAttempt: 1, ExpectedRepo: "mas-bandwidth/nova-tools", ExpectedBranch: branch, ContractLine: line1}
	for name, model := range map[string]string{
		"two-lines":    line1 + "\nDONE\n",
		"with-note":    line1 + "\nDONE\nthe flaky retry is still owed\n",
		"wrong-branch": line1 + "\nDONE\nBRANCH: rowan/quack-fix\nKIND: fix\nCHECK: fail\nATTEMPT: 7\n## Gates\n- make preflight: pass\n",
		"long-format":  Exemplar(KindFix),
	} {
		t.Run(name, func(t *testing.T) {
			if name == "long-format" {
				model = line1 + "\n" + strings.SplitN(model, "\n", 2)[1]
			}
			out := Synthesize([]byte(model), facts)
			res := ParseResult(out, opt)
			if !res.Valid {
				t.Fatalf("synthesized record invalid: field=%s defect=%s line=%d\n%s", res.Field, res.Defect, res.Line, out)
			}
			if res.Branch != branch || res.Check != "pass" || res.Attempt != 1 || strings.Join(res.Paths, " ") != "internal/x/x.go internal/x/x_test.go" {
				t.Fatalf("record %+v, want the wrapper's branch, check, attempt and paths\n%s", res, out)
			}
			if got := res.Sections["Gates"]; len(got) != 1 || !strings.Contains(got[0], "go test ./internal/x") {
				t.Fatalf("Gates %q, want the wrapper's one row", got)
			}
			if strings.Contains(string(out), "rowan/quack-fix") || strings.Contains(string(out), "make preflight") {
				t.Fatalf("a model-written wrapper field survived:\n%s", out)
			}
			owed := res.Sections["Left owed"]
			switch name {
			case "with-note":
				if len(owed) != 1 || owed[0] != "- the flaky retry is still owed" {
					t.Fatalf("Left owed %q, want the model's note", owed)
				}
			case "two-lines", "wrong-branch":
				if len(owed) != 1 || owed[0] != "- none" {
					t.Fatalf("Left owed %q, want - none", owed)
				}
			}
		})
	}

	// Without the wrapper the two-line file is not a record.
	if res := ParseResult([]byte(line1+"\nDONE\n"), opt); res.Valid {
		t.Fatal("a two-line file validated without the wrapper's fields")
	}
}

// TestSynthesizeKeepsJudgementFields: a read's FINDINGS/FLOOR/SUGGEST/PR/HEAD
// and its ## Findings are the model's (the wrapper cannot know them); BRANCH
// and PATHS, which a read does not know, are not written.
func TestSynthesizeKeepsJudgementFields(t *testing.T) {
	t.Parallel()

	const line1 = "RESULT: r1 sha=0123456789ab nova-tools read: read PR 812"
	head := strings.Repeat("a", 40)
	model := line1 + "\nDONE\nPR: 812\nHEAD: " + head + "\nFINDINGS: 1\nFLOOR: LOW\nSUGGEST: APPROVE\nBRANCH: x\n## Findings\n- LOW a.go:1 `x` y\n"
	out := Synthesize([]byte(model), WrapperFacts{Kind: KindRead, Attempt: 2, Repo: "mas-bandwidth/nova-tools", Branch: "nova/s/r1-a2", Paths: []string{"a.go"}})
	res := ParseResult(out, ParseOptions{ExpectedKind: KindRead, ExpectedAttempt: 2, ContractLine: line1})
	if !res.Valid || res.Findings != 1 || res.Head != head || res.Suggest != "APPROVE" || res.Check != "not-run" {
		t.Fatalf("read record %+v (field=%s defect=%s)\n%s", res, res.Field, res.Defect, out)
	}
	if strings.Contains(string(out), "BRANCH:") || strings.Contains(string(out), "PATHS:") {
		t.Fatalf("a read record carries BRANCH or PATHS:\n%s", out)
	}
}

// TestResultFormatIsTheTwoLineContract: the brief is line 1 and line 2 (and,
// for a kind that needs one, the fields only the model can know); it never asks
// for a field the wrapper writes.
func TestResultFormatIsTheTwoLineContract(t *testing.T) {
	t.Parallel()

	for _, kind := range Kinds {
		f := ResultFormat(kind)
		if !strings.Contains(f, "line 1: this card's line 1, verbatim") || !strings.Contains(f, "`DONE`, `ABSTAIN <why>` or `BLOCKED <why>`") {
			t.Fatalf("KIND %s: no two-line contract:\n%s", kind, f)
		}
		for _, owned := range WrapperOwned {
			if strings.Contains(f, "`"+owned+":") {
				t.Errorf("KIND %s: the brief asks the model for %s, a field the wrapper writes:\n%s", kind, owned, f)
			}
		}
		fields, sections := JudgementFields(kind)
		for _, x := range fields {
			if !strings.Contains(f, "`"+x+": <value>`") {
				t.Errorf("KIND %s: the brief does not name %s, which only the model knows", kind, x)
			}
		}
		for _, s := range sections {
			if !strings.Contains(f, "`## "+s+"`") {
				t.Errorf("KIND %s: the brief does not name ## %s", kind, s)
			}
		}
	}
	if f, s := JudgementFields(KindFix); len(f) != 0 || len(s) != 0 {
		t.Fatalf("a fix card needs nothing from the model beyond two lines, got %v %v", f, s)
	}
}
