//go:build functional

package main

// Cold-reader probes for #4414, written by the reader rowan-opus
// (2026-09-26) and copied in with credit; changed only to open with
// t.Parallel() and hand the CLI its seat by value (runTaskTakeAs), not
// t.Setenv. The claim lands
// BEFORE the reopen; a second row on the same edge after it; the CLI take
// line against ready --why byte for byte; the card doors on the same path.

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

func rdStore(t *testing.T) (string, *redis.Client, *store.Store) {
	t.Helper()
	addr, c := wstest.Start(t)
	ctx := context.Background()
	pipe := c.Pipeline()
	pipe.HSet(ctx, "s:"+sdSprint, "status", "open")
	pipe.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sdSprint})
	pipe.SAdd(ctx, "sprints", sdSprint)
	pipe.SAdd(ctx, "friends", "f1")
	pipe.HSet(ctx, "friend:f1:desired", "slots", 8, "paused", "0")
	pipe.HSet(ctx, "friend:f1:beat", "host", "fixture", "at", strconv.FormatInt(time.Now().UnixMilli(), 10))
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return addr, c, store.New(c)
}

// Probe 1, queue: B claimed while alpha is landed, then alpha reopens.
func TestReader4414ClaimBeforeReopenQueue(t *testing.T) {
	t.Parallel()
	addr, c, st := rdStore(t)
	ctx := context.Background()
	const stop = "alpha:sentinel"
	if err := sdCard(t, c, "A1", "alpha", ""); err != nil {
		t.Fatal(err)
	}
	sdQueue(t, st, "B", stop)
	sdQueue(t, st, "C", stop)
	sdLand(t, c, "A1")
	sdAccept(t, c, "alpha")
	claimB, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: sdSprint, ID: "B", As: "f1"})
	if err != nil || !ok {
		t.Fatalf("take B while landed: %v %v", ok, err)
	}
	if err := sdCard(t, c, "A2", "alpha", ""); err != nil {
		t.Fatal(err)
	}
	t.Logf("after reopen: stop where=%s; B state=%s where=%s; C state=%s where=%s waits_on=%q",
		sdField(t, c, stop, "where"), sdField(t, c, "B", "state"), sdField(t, c, "B", "where"),
		sdField(t, c, "C", "state"), sdField(t, c, "C", "where"), sdField(t, c, "C", "waits_on"))
	// C: refused at every queue door
	_, ok, err = task.Take(ctx, st, task.TakeRequest{Sprint: sdSprint, ID: "C", As: "f1"})
	var blocked *task.BlockedError
	if ok || !errors.As(err, &blocked) {
		t.Fatalf("take C after reopen: ok=%v err=%v", ok, err)
	}
	claims, err := task.TakeAvailable(ctx, st, "f1", sdSprint, "", 4, "f1", "")
	if len(claims) != 0 {
		t.Fatalf("take (no id) after reopen claimed %+v (%v)", claims, err)
	}
	// B's done after the reopen
	got, err := task.Done(ctx, st, task.DoneRequest{Sprint: sdSprint, ID: "B", Token: claimB.Token, Evidence: "https://example.test/B"})
	t.Logf("done B (claimed before reopen): %s %v", got, err)
	// Probe 4: the CLI line byte for byte against ready --why
	var why, whyErr bytes.Buffer
	readyReport(ctx, c, mapForge{}, "", "C", &why, &whyErr)
	var out, errOut bytes.Buffer
	code := runTaskTakeAs(ctx, []string{"--redis", addr, "--sprint", sdSprint, "--id", "C", "--as", "f1"}, "f1", &out, &errOut)
	t.Logf("CLI take C: exit %d out %q err %q; ready --why C: %q", code, out.String(), errOut.String(), why.String())
	if code != 7 || !bytes.Equal(out.Bytes(), why.Bytes()) {
		t.Fatalf("CLI BLOCKED line not byte-identical to ready --why")
	}
	sdLand(t, c, "A2")
	sdAccept(t, c, "alpha")
	if _, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: sdSprint, ID: "C", As: "f1"}); err != nil || !ok {
		t.Fatalf("take C after reland: %v %v", ok, err)
	}
}

// Probe 1, cards: C taken and a copy of D dealt while alpha is landed;
// alpha reopens; E (same edge) is refused at take, deal and named work.
func TestReader4414ClaimBeforeReopenCards(t *testing.T) {
	t.Parallel()
	_, c, st := rdStore(t)
	ctx := context.Background()
	if err := sdCard(t, c, "A1", "alpha", ""); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"C", "D", "E", "F"} {
		if err := sdCard(t, c, id, "beta-"+id, "alpha:sentinel"); err != nil {
			t.Fatal(err)
		}
	}
	sdLand(t, c, "A1")
	sdAccept(t, c, "alpha")
	lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "test"})
	if err != nil {
		t.Fatal(err)
	}
	var rb bytes.Buffer
	if _, err := (&reconcile.WaitingResolve{Client: c, Out: &rb}).Run(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if ids, err := taskcard.Take(ctx, c, "f1", 1, "test", "C"); err != nil || len(ids) != 1 {
		t.Fatalf("take C while landed: %v %v", ids, err)
	}
	as, _ := taskcard.ParseConsumer("friend:f1")
	dd, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: as, N: 1, IDs: []string{"D"}, By: "test"})
	if err != nil || len(dd) != 1 {
		t.Fatalf("deal D: %+v %v", dd, err)
	}
	if err := sdCard(t, c, "A2", "alpha", ""); err != nil {
		t.Fatal(err)
	}
	t.Logf("after reopen: C where=%s; D where=%s copy %s where=%s; E where=%s", sdField(t, c, "C", "where"),
		sdField(t, c, "D", "where"), dd[0].Copy, sdField(t, c, dd[0].Copy, "where"), sdField(t, c, "E", "where"))
	_, err = taskcard.Take(ctx, c, "f1", 1, "test", "E")
	whyE, codeE := sdWhy(t, c, "E")
	t.Logf("take E after reopen: %v; ready --why E: exit %d %q", err, codeE, whyE)
	if err == nil {
		t.Fatalf("take E after reopen succeeded")
	}
	d2, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: as, N: 1, IDs: []string{"F"}, By: "test"})
	t.Logf("deal F after reopen: %+v %v", d2, err)
	if len(d2) > 0 {
		t.Fatalf("deal F after reopen dealt")
	}
	w, err := taskcard.Work(ctx, c, as, "test", 1, false, dd[0].Copy)
	t.Logf("work named copy of D after reopen: %+v %v", w, err)
	if len(w.IDs) > 0 {
		t.Fatalf("named work started D's copy after reopen")
	}
}

// Probe 6: a sprint card (s:<S>:card:<label>) carries a <slug>:sentinel
// edge today; the Go gate re-judges a pooled card each pass, the FCALL at
// deal.lua:227 does not.
func TestReader4414SprintCardSentinelEdge(t *testing.T) {
	t.Parallel()
	_, c, _ := rdStore(t)
	if err := sdCard(t, c, "A1", "alpha", ""); err != nil {
		t.Fatal(err)
	}
	sdSprintCard(t, c, "Q", "alpha:sentinel")
	t.Logf("gate before land: %q", sdGate(t, c)["Q"])
	sdLand(t, c, "A1")
	sdAccept(t, c, "alpha")
	t.Logf("gate after land: %q", sdGate(t, c)["Q"])
	if err := sdCard(t, c, "A2", "alpha", ""); err != nil {
		t.Fatal(err)
	}
	why := sdGate(t, c)["Q"]
	t.Logf("gate after reopen: %q", why)
	if why == "" {
		t.Fatalf("pooled sprint card Q passes the Go gate with alpha reopened")
	}
}
