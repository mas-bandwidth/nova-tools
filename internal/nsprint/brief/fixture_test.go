package brief

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// fixtureTask is testdata/<name>.task.json: the task hash a real brief was
// handed out against.
type fixtureTask struct {
	Note   string            `json:"note"`
	Sprint string            `json:"sprint"`
	ID     string            `json:"id"`
	Hash   map[string]string `json:"hash"`
}

// TestBriefLintRefusesWrongCoauthorFixture20260923 is the clean-tree negative
// control of nova-tools#3154: the real 2026-09-23 build brief told Opus 5.5
// children to sign as Claude Fable 5.1. Loaded against its task hash, lint must
// refuse it on field=coauthor. This test lands in the first commit, before any
// lint check exists, so it is red there and green at head.
func TestBriefLintRefusesWrongCoauthorFixture20260923(t *testing.T) {
	st, client := briefRedis(t)
	raw, err := os.ReadFile(filepath.Join("testdata", "wrong-coauthor-2026-09-23.task.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fx fixtureTask
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("fixture task: %v", err)
	}
	if fx.Sprint == "" || fx.ID == "" || fx.Hash["title"] == "" || fx.Hash["kind"] == "" {
		t.Fatalf("fixture task is incomplete: %+v", fx)
	}
	// Test-only bare write: the fixture hash as it stood that day.
	if err := client.HSet(context.Background(), "s:"+fx.Sprint+":task:"+fx.ID, fx.Hash).Err(); err != nil {
		t.Fatal(err)
	}
	code, lines := runLint(t, st, filepath.Join("testdata", "wrong-coauthor-2026-09-23.brief"), fx.Sprint+"/"+fx.ID)
	if code != 1 {
		t.Fatalf("lint of the wrong-co-author brief: exit %d, want 1; lines %q", code, lines)
	}
	want := "BRIEF REFUSED task=" + fx.ID + " field=coauthor want=opus-5.5 got=fable-5.1"
	co := linesWithField(lines, "coauthor")
	if len(co) != 1 || co[0] != want {
		t.Fatalf("coauthor refusal: got %q, want exactly %q", co, want)
	}
}
