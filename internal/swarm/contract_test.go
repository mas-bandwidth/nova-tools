package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// WriteResult writes a RESULT.md at path from the given lines.
func writeResult(t *testing.T, path string, lines ...string) {
	t.Helper()
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const contractLine = "RESULT: READ card-142 at abc123 verdict=APPROVE findings=3"

// A REPORT WHOSE FIRST LINE IS NOT THE CARD'S CONTRACT LINE IS REFUSED, and the
// refusal names the first sixty characters the report actually holds, so a person
// reading the line can see which text the contract was compared against.
func TestResultRefusesWrongLine1(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "RESULT.md")
	writeResult(t, path, "a title the worker wrote from some older prompt", "disposition", "evidence one")

	got, err := CheckResult(path, Contract{Label: "card-142", ContractLine: contractLine})
	if err != nil {
		t.Fatal(err)
	}
	if got.OK {
		t.Errorf("a report whose line 1 is not the contract was accepted: %s", got.Line)
	}
	if !strings.HasPrefix(got.Line, "RESULT REFUSED") {
		t.Errorf("the grammar is `RESULT REFUSED <label> <reason>`, got: %s", got.Line)
	}
	if !strings.Contains(got.Line, "a title the worker wrote from some older prompt") {
		t.Errorf("the refusal must name the offending text found on line 1: %s", got.Line)
	}
}

// LINE 2 IS THE DISPOSITION AND IS RETURNED VERBATIM, punctuation and all. It is
// the one line a coordinator reads back out of the receipt, so no escaping is
// allowed to mangle it.
func TestResultReturnsLine2Verbatim(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "RESULT.md")
	disposition := "the disposition, with commas: approval — and a dash"
	writeResult(t, path, contractLine, disposition, "evidence one", "evidence two")

	got, err := CheckResult(path, Contract{Label: "card-142", ContractLine: contractLine})
	if err != nil {
		t.Fatal(err)
	}
	if !got.OK {
		t.Fatalf("a report with a matching line 1 was refused: %s", got.Line)
	}
	if got.Line2 != disposition {
		t.Errorf("line 2 is returned verbatim; got %q want %q", got.Line2, disposition)
	}
	if !strings.HasSuffix(got.Line, "line2="+oneline.Quote(disposition)) {
		t.Errorf("the grammar carries line 2 quoted, verbatim inside the quotes: %s", got.Line)
	}
}

// THE REST OF THE REPORT IS BOUNDED TO N LINES. A report may hold any number of
// evidence lines; the contract returns at most N (default 20), so a coordinator's
// window never holds an unbounded transcript.
func TestResultBoundsEvidenceLines(t *testing.T) {
	dir := t.TempDir()

	many := func(n int) []string {
		out := []string{contractLine, "disposition"}
		for i := 0; i < n; i++ {
			out = append(out, "evidence line "+string(rune('a'+i%26)))
		}
		return out
	}

	t.Run("explicit bound", func(t *testing.T) {
		path := filepath.Join(dir, "bounded.md")
		writeResult(t, path, many(50)...)
		got, err := CheckResult(path, Contract{Label: "card-142", ContractLine: contractLine, MaxLines: 3})
		if err != nil {
			t.Fatal(err)
		}
		if got.OK && len(got.Evidence) != 3 {
			t.Errorf("evidence is bounded to --max; got %d lines want 3", len(got.Evidence))
		}
	})

	t.Run("default bound", func(t *testing.T) {
		path := filepath.Join(dir, "default.md")
		writeResult(t, path, many(50)...)
		got, err := CheckResult(path, Contract{Label: "card-142", ContractLine: contractLine})
		if err != nil {
			t.Fatal(err)
		}
		if !got.OK {
			t.Fatal(got.Line)
		}
		if len(got.Evidence) != 20 {
			t.Errorf("the default bound is 20; got %d lines", len(got.Evidence))
		}
		if got.LineCount != 52 {
			t.Errorf("line count reports the WHOLE report, not the bound: got %d want 52", got.LineCount)
		}
	})
}

// THE RECEIPT IS WRITTEN BESIDE THE REPORT AND CARRIES THE CARD'S SHA-256, so the
// retained report is keyed by what the worker was actually asked to do.
func TestReceiptCarriesCardHash(t *testing.T) {
	dir := t.TempDir()
	result := filepath.Join(dir, "RESULT.md")
	writeResult(t, result, contractLine, "disposition", "evidence")
	card := []byte("the card text the worker was handed\n")

	got, err := CheckResult(result, Contract{Label: "card-142", ContractLine: contractLine, Card: card})
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := result + ".receipt"
	if err := WriteReceipt(receiptPath, got, Contract{Label: "card-142", Card: card}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "card_sha256="+HashBytes(card)) {
		t.Errorf("the receipt carries the card hash; got:\n%s", text)
	}
	if !strings.Contains(text, "label=card-142") {
		t.Errorf("the receipt carries the job label; got:\n%s", text)
	}
	if !strings.Contains(text, "line2=disposition") {
		t.Errorf("the receipt carries RESULT line 2; got:\n%s", text)
	}
}
