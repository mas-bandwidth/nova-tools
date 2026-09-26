//go:build functional

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
)

// TestSprintClearZerosBothTables (Glenn 2026-09-26 8:40 AM ET: "reset the
// sprint table. zeros everywhere"; 8:41 AM: "make sprint clearing a verb. It
// should be simple and fast"): over the fixture, `sprint clear` refuses while
// cards are working or merging, clears everything with --force in one call,
// writes the checkpoint first, and the next tick of the table reads zero in
// every stream and consumer cell.
func TestSprintClearZerosBothTables(t *testing.T) {
	addr, client := wholeTableRedis(t)
	ctx := context.Background()
	code, _, stderr := runSprint("sprint", "clear", "--redis", addr, "--why", "fresh run")
	if code != 1 || !strings.Contains(stderr, "INFLIGHT") {
		t.Fatalf("cards in flight: exit %d stderr %q", code, stderr)
	}
	cp := filepath.Join(t.TempDir(), "clear.tsv")
	code, stdout, stderr := runSprint("sprint", "clear", "--redis", addr, "--why", "fresh run", "--force", "--by", "rowan", "--checkpoint", cp)
	if code != 0 || !strings.HasPrefix(stdout, "CLEARED streams=") || !strings.Contains(stdout, " by=rowan ms=") {
		t.Fatalf("clear: exit %d\n%s%s", code, stdout, stderr)
	}
	b, err := os.ReadFile(cp)
	if err != nil || !strings.HasPrefix(string(b), "# nova-sprint sprint clear checkpoint at=") || !strings.Contains(string(b), "\tworking\t") {
		t.Fatalf("checkpoint: %v\n%s", err, b)
	}
	now := time.Now()
	snap, err := table.NewSprintReader(client, table.SprintConfig{}).Read(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range snap.Streams {
		if r.Total() != 0 {
			t.Fatalf("stream %q is not zero after the clear: %+v", r.Name, r)
		}
	}
	for _, c := range snap.Consumers {
		if c.Ready != 0 || c.Working != 0 || c.OK != 0 || c.Fail != 0 {
			t.Fatalf("consumer %s is not zero after the clear: %+v", c.ID(), c)
		}
	}
	// a second clear is a no-op that still answers CLEARED
	code, stdout, _ = runSprint("sprint", "clear", "--redis", addr, "--why", "again", "--force")
	if code != 0 || !strings.Contains(stdout, " cards=0 ") {
		t.Fatalf("second clear: exit %d %s", code, stdout)
	}
}
