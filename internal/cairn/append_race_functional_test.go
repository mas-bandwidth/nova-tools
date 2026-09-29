//go:build functional

package cairn

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Twelve appends of one entry id at once, in each shape. With the check and the
// write one critical section, exactly one writes: the others read what it filed
// and answer duplicate (same words) or conflict (different words). These run
// with the real clock and real waits, so they are in the functional tier.

func raceStores(t *testing.T) map[string]string {
	t.Helper()
	bench := openedBenchStore(t)
	own := t.TempDir()
	if err := Open(own, "NEW", "", benchNow, PublishManual); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"bench": bench, "own": own}
}

func runAppends(store string, n int, text func(i int) string) (ok, dup, conflict int, other []error) {
	start := make(chan struct{})
	type result struct {
		res AppendResult
		err error
	}
	out := make(chan result, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			res, err := Append(store, "NEW", "e1", text(i), "", benchNow, PublishManual)
			out <- result{res, err}
		}(i)
	}
	close(start)
	wg.Wait()
	close(out)
	for r := range out {
		var ce *ConflictError
		switch {
		case r.err == nil && r.res.Duplicate:
			dup++
		case r.err == nil:
			ok++
		case errors.As(r.err, &ce):
			conflict++
		default:
			other = append(other, r.err)
		}
	}
	return
}

func TestConcurrentAppendsOfOneIDWithDifferentWordsHaveOneWinner(t *testing.T) {
	t.Parallel()
	for shape, store := range raceStores(t) {
		const n = 12
		ok, dup, conflict, other := runAppends(store, n, func(i int) string { return "words " + strconv.Itoa(i) })
		if ok != 1 || dup != 0 || conflict != n-1 || len(other) != 0 {
			t.Fatalf("%s: %d wrote, %d duplicates, %d conflicts, other %v; want exactly one writer and %d conflicts", shape, ok, dup, conflict, other, n-1)
		}
		if _, total, err := Index(store, "NEW", 0); err != nil || total != 1 {
			t.Fatalf("%s: index %d %v", shape, total, err)
		}
		if shape == "bench" {
			raw, _ := os.ReadFile(benchFile(store, "NEW"))
			if c := strings.Count(string(raw), " — e1\n"); c != 1 {
				t.Fatalf("bench: %d sections for e1", c)
			}
		}
	}
}

func TestConcurrentAppendsOfOneIDWithTheSameWordsWriteOnce(t *testing.T) {
	t.Parallel()
	for shape, store := range raceStores(t) {
		const n = 12
		ok, dup, conflict, other := runAppends(store, n, func(int) string { return "the same words" })
		if ok != 1 || dup != n-1 || conflict != 0 || len(other) != 0 {
			t.Fatalf("%s: %d wrote, %d duplicates, %d conflicts, other %v; want one write and %d duplicates", shape, ok, dup, conflict, other, n-1)
		}
		if _, total, err := Index(store, "NEW", 0); err != nil || total != 1 {
			t.Fatalf("%s: index %d %v", shape, total, err)
		}
	}
}
