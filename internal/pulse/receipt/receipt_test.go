package receipt

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// at is the one stamp these tests use; a fixed clock, because a test that reads the wall
// clock is a test that asserts the machine (AGENTS.md rule 3).
var at = time.Date(2026, 9, 22, 14, 5, 0, 0, time.UTC)

func sha() string { return "39d1d6a5c9182967405eb6211fadeb2eaf3dffe4" }

// TestReceiptAppendsOneEvent is the DONE-WHEN check: canary, conform and adopt each append
// one kind=receipt event carrying pass/fail and the sha, the lineup reads them back from the
// stream, and none of the six files is written.
func TestReceiptAppendsOneEvent(t *testing.T) {
	ctx := context.Background()
	store := NewMemStream()

	// The absence check covers every place a stray write could land: the working directory
	// the writer runs in (a fresh temp dir it is moved into) and the whole repository tree,
	// package directory included. The repo is snapshotted first so only new files count.
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot := filepath.Join(pkgDir, "..", "..", "..")
	before := forbiddenUnder(t, repoRoot)
	workDir := t.TempDir()
	t.Chdir(workDir)

	id, _, err := Append(ctx, store, Canary, true, sha(), at)
	if err != nil {
		t.Fatalf("canary Append: %v", err)
	}
	if id == "" {
		t.Fatal("canary Append returned an empty id")
	}
	if _, _, err := Append(ctx, store, Conform, false, sha(), at); err != nil {
		t.Fatalf("conform Append: %v", err)
	}
	if _, _, err := Append(ctx, store, Adopt, true, sha(), at); err != nil {
		t.Fatalf("adopt Append: %v", err)
	}

	got, err := Read(ctx, store)
	if err != nil {
		t.Fatalf("Read (the lineup): %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("the stream holds %d receipt events, want 3", len(got))
	}

	want := []struct {
		verb Verb
		pass bool
	}{
		{Canary, true},
		{Conform, false},
		{Adopt, true},
	}
	for i, w := range want {
		r := got[i]
		if r.Kind != Kind {
			t.Errorf("event %d: kind=%q, want %q", i, r.Kind, Kind)
		}
		if r.Verb != w.verb {
			t.Errorf("event %d: verb=%q, want %q", i, r.Verb, w.verb)
		}
		if r.Pass != w.pass {
			t.Errorf("event %d: pass=%v, want %v", i, r.Pass, w.pass)
		}
		if r.SHA != sha() {
			t.Errorf("event %d: sha=%q, want %q", i, r.SHA, sha())
		}
	}

	// None of the six files is written, in the working directory or anywhere in the repo.
	if found := forbiddenUnder(t, workDir); len(found) > 0 {
		t.Errorf("the receipt path wrote %v in its working directory, which DONE-WHEN forbids", found)
	}
	for p := range forbiddenUnder(t, repoRoot) {
		if !before[p] {
			t.Errorf("the receipt path wrote %s, which DONE-WHEN forbids", p)
		}
	}
}

// forbiddenUnder walks root (skipping .git) and returns the set of paths whose base name is
// one of the six receipt files, including ADOPT-<sha>.txt.
func forbiddenUnder(t *testing.T, root string) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.IsDir() && Forbidden(d.Name()) {
			found[p] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return found
}

// TestForbiddenScanBites is the control on the absence check: each of the six files planted
// in a nested directory is found by the same scan TestReceiptAppendsOneEvent uses.
func TestForbiddenScanBites(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "internal", "pulse", "receipt")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	names := append(append([]string{}, SixFiles...), AdoptPrefix+sha()+AdoptSuffix)
	if len(names) != 6 {
		t.Fatalf("the forbidden set has %d names, want 6", len(names))
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(nested, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := forbiddenUnder(t, root); len(got) != 6 {
		t.Fatalf("the scan found %d of the six planted files: %v", len(got), got)
	}
	for _, ok := range []string{"ADOPT-.txt", "ADOPT-x.md", "receipt.go", "ESCALATE.md"} {
		if Forbidden(ok) {
			t.Errorf("Forbidden(%q) = true, want false", ok)
		}
	}
}

func TestAppendRejectsEmptySHA(t *testing.T) {
	if _, _, err := Append(context.Background(), NewMemStream(), Canary, true, "", at); err == nil {
		t.Fatal("Append with an empty sha: want error, got nil")
	}
}

func TestAppendRefusesControlCharInSHA(t *testing.T) {
	if _, _, err := Append(context.Background(), NewMemStream(), Canary, true, "dead\x01beef", at); err == nil {
		t.Fatal("Append with a control character in sha: want error, got nil")
	}
}
