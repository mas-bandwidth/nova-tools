package pulse

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func stateValue(t *testing.T, queue, key string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(queue, "state.tsv"))
	if err != nil {
		t.Fatalf("reading the state file: %v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		k, v, ok := strings.Cut(line, "\t")
		if ok && k == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// cut-is-the-only-numberer: fifty concurrent cuts under one queue take fifty distinct
// numbers and none is handed out twice — pit stop 3 bug 1, five hand-numbered replay cards
// colliding with five launched cards of the same numbers (issue #828, class B).
func TestFiftyConcurrentCutsNeverShareANumber(t *testing.T) {
	queue := t.TempDir()
	const n = 50
	got := make([]int, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i], errs[i] = NextCardNumber(queue)
		}(i)
	}
	wg.Wait()
	seen := map[int]bool{}
	for i, v := range got {
		if errs[i] != nil {
			t.Fatalf("cut %d: %v", i, errs[i])
		}
		if seen[v] {
			t.Fatalf("number %d was handed out twice: %v", v, got)
		}
		seen[v] = true
	}
	for i := 1; i <= n; i++ {
		if !seen[i] {
			t.Errorf("number %d was never handed out: %v", i, got)
		}
	}
	if v := stateValue(t, queue, "next_card"); v != "51" {
		t.Errorf("next_card = %q after %d cuts, want 51", v, n)
	}
}

// the legacy NEXT file is seeded from and mirrored, so the manager tier's own nextNumber —
// which another card retires — never hands out a number a cut has already used.
func TestNumberSeedsFromLegacyNextAndMirrorsIt(t *testing.T) {
	queue := t.TempDir()
	if err := os.WriteFile(filepath.Join(queue, "NEXT"), []byte("8130\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := NextCardNumber(queue)
	if err != nil {
		t.Fatal(err)
	}
	if n != 8130 {
		t.Errorf("first number = %d, want 8130 (the legacy NEXT seeds the state file)", n)
	}
	raw, err := os.ReadFile(filepath.Join(queue, "NEXT"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(raw)) != "8131" {
		t.Errorf("legacy NEXT = %q, want 8131 (the mirror keeps the two readers in step)", strings.TrimSpace(string(raw)))
	}
	if v := stateValue(t, queue, "next_card"); v != "8131" {
		t.Errorf("next_card = %q, want 8131", v)
	}
}

// the state file is one file with many facts: a key this verb does not own survives the write.
func TestNumberKeepsEveryOtherStateKey(t *testing.T) {
	queue := t.TempDir()
	if err := os.WriteFile(filepath.Join(queue, "state.tsv"), []byte("next_card\t7\ngt\t41\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := NextCardNumber(queue)
	if err != nil {
		t.Fatal(err)
	}
	if n != 7 {
		t.Errorf("number = %d, want 7 (the state file's next_card is the only source)", n)
	}
	if v := stateValue(t, queue, "gt"); v != "41" {
		t.Errorf("gt = %q after a cut, want 41 (a key this verb does not own is never dropped)", v)
	}
}
