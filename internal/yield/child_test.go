//go:build darwin || linux

package yield

import (
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// TestChildrenFromEveryGoroutineInheritNice is the measurement that scored
// the first cut 5/10 (hetzner, 2026-09-26: 31 of 32 children of a niced
// wrapper at nice 0): after ToCI, a child started from a FRESH goroutine,
// which the runtime may fork from any of its threads, reads its own nice
// as Nice. Sixteen goroutines at once, so the runtime has reason to use
// more than one thread; the child is a shell that asks ps for its own
// nice (`ps -o ni=`), the same on darwin and Linux.
func TestChildrenFromEveryGoroutineInheritNice(t *testing.T) {
	t.Parallel()
	if n, err := currentNice(); err != nil {
		t.Fatal(err)
	} else if n > Nice {
		t.Skipf("already at nice %d, above %d", n, Nice)
	}
	if err := ToCI(); err != nil {
		t.Fatalf("ToCI: %v", err)
	}
	const goroutines = 16
	got := make([]string, goroutines)
	errs := make([]error, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, err := exec.Command("sh", "-c", "ps -o ni= -p $$").Output()
			got[i], errs[i] = strings.TrimSpace(string(out)), err
		}(i)
	}
	wg.Wait()
	for i := range got {
		if errs[i] != nil {
			t.Fatalf("child %d: nice: %v", i, errs[i])
		}
		n, err := strconv.Atoi(got[i])
		if err != nil {
			t.Fatalf("child %d printed %q, not a nice value", i, got[i])
		}
		if n != Nice {
			t.Errorf("child %d runs at nice %d, want %d: a thread of this process was not niced", i, n, Nice)
		}
	}
}
