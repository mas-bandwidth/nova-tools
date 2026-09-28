package card_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
)

// TestNoCommitEnd is the rule on its own: only a code card, harness DONE,
// NO-COMMIT and a model line 2 of DONE turns into FAILED no-commit.
func TestNoCommitEnd(t *testing.T) {
	t.Parallel()

	out := t.TempDir()
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(out, "RESULT.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	done := card.WrapperEnd{Outcome: "DONE", Reason: "done", Commit: "NO-COMMIT", PushedSHA: card.NoCommit}
	write("RESULT: x\nDONE\n")
	if got, ok := card.NoCommitEnd("fix", out, done); !ok || got.Outcome != "FAILED" || got.Reason != "no-commit" || got.Why != card.NoCommitWhy {
		t.Fatalf("fix DONE NO-COMMIT = %+v %v, want FAILED no-commit", got, ok)
	}
	for _, kind := range []string{"script", "read", "report"} {
		if _, ok := card.NoCommitEnd(kind, out, done); ok {
			t.Fatalf("%s card with no commit failed; it legitimately commits nothing", kind)
		}
	}
	for name, end := range map[string]card.WrapperEnd{
		"committed": {Outcome: "DONE", Reason: "done", Commit: "COMMITTED", PushedSHA: strings.Repeat("a", 40)},
		"oversize":  {Outcome: "DONE", Reason: "done", Commit: "OVERSIZE big.bin", PushedSHA: card.NoCommit},
		"crashed":   {Outcome: "FAILED", Reason: "crash", Commit: "NO-COMMIT", PushedSHA: card.NoCommit},
	} {
		if _, ok := card.NoCommitEnd("fix", out, end); ok {
			t.Fatalf("%s end turned into no-commit", name)
		}
	}
	for _, body := range []string{"RESULT: x\nABSTAIN scope\n", "RESULT: x\nBLOCKED deps\n", "RESULT: x\n", "RESULT: x\nDONE but\n"} {
		write(body)
		if _, ok := card.NoCommitEnd("fix", out, done); ok {
			t.Fatalf("line 2 of %q turned into no-commit; only a model DONE does", body)
		}
	}
	if err := os.Remove(filepath.Join(out, "RESULT.md")); err != nil {
		t.Fatal(err)
	}
	if _, ok := card.NoCommitEnd("fix", out, done); ok {
		t.Fatal("no RESULT.md turned into no-commit")
	}
}
