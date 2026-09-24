package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// stella's hold on nova-tools#2762, first item: a positive --wall under the
// ledger's whole-millisecond resolution would be stored as wall_ms=0 and read
// back as a zero-cost measurement. It is refused, and nothing is written.
func TestDogfoodQuestionRefusesSubMillisecondWall(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "dogfood-ledger")
	code, stdout, stderr := invoke("dogfood", "question", "--ledger", ledger,
		"--kind", "uid", "--mode", "link", "--tokens", "10", "--wall", "500us", "--now", "2026-09-20T09:00:00Z")
	if code != 2 {
		t.Fatalf("dogfood question --wall 500us exit=%d, want 2\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "1ms resolution") {
		t.Fatalf("stderr does not name the 1ms resolution:\n%s", stderr)
	}
	if _, err := os.Stat(filepath.Join(ledger, dogfoodQuestionsFile)); !os.IsNotExist(err) {
		t.Fatalf("a refused question wrote a ledger file (stat err=%v)", err)
	}
	code, stdout, stderr = invoke("dogfood", "question", "--ledger", ledger,
		"--kind", "uid", "--mode", "link", "--tokens", "10", "--wall", "1ms", "--now", "2026-09-20T09:00:00Z")
	if code != 0 {
		t.Fatalf("dogfood question --wall 1ms exit=%d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	rows, err := dogfoodLoadQuestions(filepath.Join(ledger, dogfoodQuestionsFile))
	if err != nil || len(rows) != 1 || rows[0].WallMS != 1 {
		t.Fatalf("after --wall 1ms rows=%+v err=%v, want one row with wall_ms=1", rows, err)
	}
}

// stella's hold on nova-tools#2762, second item: the append path read and
// rewrote the whole ledger, so concurrent recorders lost rows. Many concurrent
// appends must all land, each as its own parseable line.
func TestDogfoodAppendKeepsEveryConcurrentRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), dogfoodQuestionsFile)
	const n = 64
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- dogfoodAppend(path, dogfoodQuestion{Kind: "uid", Mode: "link", Tokens: int64(i), WallMS: 1,
				At: fmt.Sprintf("2026-09-20T09:%02d:00Z", i%60)})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("dogfoodAppend: %v", err)
		}
	}
	rows, err := dogfoodLoadQuestions(path)
	if err != nil {
		t.Fatalf("load after concurrent appends: %v", err)
	}
	if len(rows) != n {
		t.Fatalf("concurrent appends kept %d rows, want %d", len(rows), n)
	}
	seen := map[int64]bool{}
	for _, r := range rows {
		seen[r.Tokens] = true
	}
	if len(seen) != n {
		t.Fatalf("concurrent appends kept %d distinct rows, want %d", len(seen), n)
	}
}
