package life_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// TestServeBriefFromCompleteCard (nova-tools#3911): a task whose card record
// task:<id> carries its route gets the friend brief rendered from that
// record (taskcard.RenderBrief) at @brief, and the start receipt says so.
func TestServeBriefFromCompleteCard(t *testing.T) {
	st, client := seedSeat(t, 1)
	ctx := context.Background()
	pushWork(t, st, "w1")
	if err := client.HSet(ctx, taskcard.Key("w1"), "route", "friend", "stream", "swarm: cards",
		"done_when", "go test ./internal/x -run TestX passes", "paths", "internal/x/x.go",
		"body", "build the thing\nDONE-WHEN: go test ./internal/x -run TestX passes").Err(); err != nil {
		t.Fatal(err)
	}
	cfg := serveConfig(t, "sess-1", 1, "done")
	if _, err := life.ServeOnce(ctx, st, cfg); err != nil {
		t.Fatalf("serve once: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(cfg.Dir, "s1", "w1-1", "brief.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"TASK: w1\n", "STREAM: swarm: cards\n", "PATHS: internal/x/x.go\n",
		"DONE-WHEN: go test ./internal/x -run TestX passes\n", "\n> build the thing\n"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("brief lacks %q:\n%s", want, b)
		}
	}
	found := false
	for _, e := range client.XRange(ctx, life.LogKey("emma"), "-", "+").Val() {
		if d, _ := e.Values["detail"].(string); e.Values["kind"] == "start" && strings.Contains(d, "brief=card") {
			found = true
		}
	}
	if !found {
		t.Error("no start receipt with brief=card")
	}
}
