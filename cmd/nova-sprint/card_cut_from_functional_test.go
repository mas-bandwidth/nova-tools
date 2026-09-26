//go:build functional

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestCardCutFromLandsHundredInWaiting is nova-tools#4340's DONE-WHEN on a
// real store: 100 cards land in ws:<stream>:waiting from one file with 100
// receipts, each a task card whose record carries its issue, spec and
// blocked_on; a second run refuses every row by name (EXISTS) and writes
// nothing more.
func TestCardCutFromLandsHundredInWaiting(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	forge := &fakeCutForge{}
	d := cutDeps(forge, nil)
	d.Push = func(ctx context.Context, reqs []taskcard.PushRequest) ([]taskcard.PushOutcome, error) {
		return taskcard.PushMany(ctx, client, reqs)
	}
	code, out := runCutFrom(cutFromOpts{Text: []byte(hundredRows()), Sprint: "cut-from"}, d)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if n := strings.Count("\n"+out, "\nCARD CUT row="); n != 100 {
		t.Fatalf("%d receipts, want 100:\n%s", n, out)
	}
	if n := client.ZCard(ctx, taskcard.StreamKey("swarm: cards", "waiting")).Val(); n != 100 {
		t.Fatalf("ws:swarm: cards:waiting holds %d, want 100", n)
	}
	rec, err := client.HGetAll(ctx, taskcard.Key("nova-tools-5006")).Result() // row 2, filed seventh
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"where": "waiting", "blocked_on": "nova-tools-5005", "ref": "mas-bandwidth/nova-tools#5006",
		"route": "pro", "base_sha": cutFromSHA, "paths": "cmd/nova-sprint/c2.go", "depends_on": "mas-bandwidth/nova-tools#5005"} {
		if rec[k] != want {
			t.Errorf("record %s = %q, want %q", k, rec[k], want)
		}
	}
	forge2 := &fakeCutForge{}
	d.File = forge2.file
	code, out = runCutFrom(cutFromOpts{Text: []byte(hundredRows()), Sprint: "cut-from"}, d)
	if code != 1 || strings.Count(out, "why=\"push refused: EXISTS task:nova-tools-") != 100 {
		t.Fatalf("rerun: exit %d, want 100 EXISTS refusals:\n%s", code, out)
	}
	if n := client.ZCard(ctx, taskcard.StreamKey("swarm: cards", "waiting")).Val(); n != 100 {
		t.Fatalf("rerun left %d in waiting, want 100", n)
	}
}
