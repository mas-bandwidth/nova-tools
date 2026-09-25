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
	t.Parallel()
	st, client := seedSeat(t, 1)
	ctx := context.Background()
	// One task store (#3778): the card is pushed with its spec, so its
	// stream and route are on the record and in the queue it sits in
	// (friend:emma:cards:ready), not written onto task:w1 behind the move.
	if _, err := taskcard.Push(ctx, client, taskcard.PushRequest{ID: "w1", Stream: "swarm: cards", Friend: "emma",
		Sprint: "s1", Kind: "work", Title: "build w1", By: "coordinator",
		Spec: &taskcard.Spec{Route: taskcard.RouteFriend, DoneWhen: "go test ./internal/x -run TestX passes",
			Paths: "internal/x/x.go", Body: "build the thing\nDONE-WHEN: go test ./internal/x -run TestX passes"}}); err != nil {
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
