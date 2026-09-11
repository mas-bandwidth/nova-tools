package merge

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Rule 22's second tripwire: EVERY GIT OPERATION ON A LANE CHECKOUT -- the CAS loop, THE
// PULL AND THE FOLD, the fetch of dry-run -- RUNS UNDER ONE CHECKOUT LOCK.
//
// The fold reads the checkout's work tree. A concurrent flush does `reset --hard` in that
// window, so a fold outside the lock can read a half-restored tree: the reset has removed
// the loser's record and the restore has not yet put it back. That is the same window the
// outbox exists for, read from the other side.

// underTheCallersLock is the set of helpers whose doc says the caller holds the lock. It
// is a list rather than a comment convention so that a new helper is a decision.
var underTheCallersLock = map[string]string{
	"flush":         "the CAS loop; Deliver and Pull hold the lock around it",
	"fetchAndReset": "one step of the CAS loop",
	"confirm":       "the confirming fetch of the CAS loop",
	"deliveredOK":   "the outbox sweep at the end of the CAS loop",
	"count":         "pulled=, counted inside Pull's lock",
	"nothingStaged": "the porcelain of one CAS round, inside flush's lock",
	"backoff":       "the CAS loop's wait, inside flush's lock",
	"outbox":        "the outbox, which is outside the branch, read inside flush's lock",
	"writeOutbox":   "the outbox, which is outside the branch, written before the lock is taken",
	"LockCheckout":  "the lock itself",
}

func TestEveryCheckoutOperationIsUnderTheCheckoutLock(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "records.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || fn.Body == nil {
			continue
		}
		star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		if id, ok := star.X.(*ast.Ident); !ok || id.Name != "Records" {
			continue
		}
		touches, locks := false, false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "r" && (sel.Sel.Name == "Git" || sel.Sel.Name == "Lane") {
					touches = true
				}
				if sel.Sel.Name == "LockCheckout" {
					locks = true
				}
			}
			return true
		})
		if !touches {
			continue
		}
		seen++
		if locks {
			continue
		}
		if _, ok := underTheCallersLock[fn.Name.Name]; ok {
			continue
		}
		t.Errorf("Records.%s reaches the lane checkout and takes no checkout lock; rule 22 puts every Git operation on a lane checkout -- the CAS loop, the pull and the fold, dry-run's fetch -- under one checkout lock", fn.Name.Name)
	}
	if seen < 5 {
		t.Fatalf("this tripwire found only %d methods reaching the checkout; it was looking in the wrong place and would have passed by checking nothing", seen)
	}
}

// And the lock is a real one: a fold while another holder has the checkout refuses inside
// the wait, and the refusal says who holds it.
func TestAFoldWaitsForTheCheckoutLockAndNamesTheHolder(t *testing.T) {
	lane := t.TempDir()
	for _, d := range []string{ReadsDir, GatesDir} {
		if err := os.MkdirAll(filepath.Join(lane, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	release, err := Lock(filepath.Join(lane, CheckoutLock), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	r := NewRecords(lane, "nova-merge/lane", "origin", NewGit(lane, time.Second, nil), 200*time.Millisecond)
	if _, err := r.Fold(); err == nil {
		t.Fatal("a fold ran while another holder had the checkout; the reset of a concurrent flush is exactly what it would have read")
	} else if !strings.Contains(err.Error(), "pid=") {
		t.Errorf("the refusal must say who holds it, got: %v", err)
	}
}
