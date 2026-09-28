package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
)

// TestTableOutAtomic is #3343's DONE-WHEN: the wide table's tick loop with
// --out publishes by writing <file>.tmp.<pid> beside <file> and renaming it,
// once a tick. Two ticks leave exactly one table whose line count is one
// tick's; a reader polling through the ticks never sees a partial or empty
// file; and the temp file is gone after each tick (the directory holds the
// out file alone). The tick source is a stub, so the test needs no server.
func TestTableOutAtomic(t *testing.T) {
	t.Parallel()
	// SLEEPS: this test waits on the wall clock (calls time.Sleep). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")

	dir := t.TempDir()
	out := filepath.Join(dir, "SPRINT-TABLE.txt")
	want := table.DefectGolden()
	wantLines := strings.Count(want, "\n")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tick := func(context.Context) (string, int, error) { return want, 0, nil }

	var stdout, stderr lockedBuffer
	done := make(chan int, 1)
	go func() {
		done <- tablePublishLoop(ctx, tick, true, 20*time.Millisecond, out, &stdout, &stderr)
	}()

	// A reader polls through the ticks: it must see a whole table every time
	// it sees anything at all, and never a partial or empty file.
	readerStop := make(chan struct{})
	badRead := make(chan string, 1)
	go func() {
		for {
			select {
			case <-readerStop:
				return
			default:
			}
			b, err := os.ReadFile(out)
			switch {
			case err == nil && string(b) != want:
				select {
				case badRead <- fmt.Sprintf("a reader saw a partial or empty table:\n%q", b):
				default:
				}
				return
			case err != nil && !os.IsNotExist(err):
				select {
				case badRead <- fmt.Sprintf("a reader failed: %v", err):
				default:
				}
				return
			}
			time.Sleep(time.Millisecond) // wall-ok: polling a condition in a test
		}
	}()

	var first os.FileInfo
	waitFor(t, "the first publish", func() bool {
		fi, err := os.Stat(out)
		if err != nil {
			return false
		}
		first = fi
		return true
	})
	if b, err := os.ReadFile(out); err != nil || string(b) != want {
		t.Fatalf("the first tick published %d lines (%v), want one whole table of %d lines", strings.Count(string(b), "\n"), err, wantLines)
	}
	waitFor(t, "a second publish", func() bool {
		fi, err := os.Stat(out)
		return err == nil && !os.SameFile(first, fi)
	})

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("wide table loop exit %d; stderr %s", code, stderr.String())
		}
	case <-time.After(holdWait()):
		t.Fatal("wide table loop did not stop on cancel")
	}
	close(readerStop)
	select {
	case bad := <-badRead:
		t.Fatal(bad)
	default:
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "SPRINT-TABLE.txt" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("the out directory holds %v after the ticks; the temp file was not renamed away", names)
	}
}
