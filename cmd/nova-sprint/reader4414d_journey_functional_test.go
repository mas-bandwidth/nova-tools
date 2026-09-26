//go:build functional

package main

// Cold-reader journey probe for #4414 round 2, written by the reader
// rowan-opus (2026-09-26), copied in with credit unchanged. One throwaway store, the real verbs through the
// CLI where they exist (task take, card deal, card work) and ns_card_deal
// through DealFunctions.Reserve: land alpha, resolve, reopen alpha, assert
// every door refuses, reland, assert every door proceeds.

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

func TestReader4414dJourney(t *testing.T) {
	t.Parallel()
	addr, c, st := rdStore(t)
	ctx := context.Background()
	const bench = "b1"
	lease := sdLease(t, st)
	fence := c.HGet(ctx, "lease:reconciler", "token").Val()
	for _, cmd := range [][]any{
		{"SADD", "benches", bench},
		{"HSET", "bench:" + bench + ":desired", "slots", "4"},
		{"HSET", "bench:" + bench + ":state", "state", "UP"},
	} {
		if err := c.Do(ctx, cmd...).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if err := sdCard(t, c, "A1", "alpha", ""); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"C", "D"} {
		if err := sdCard(t, c, id, "beta-"+strings.ToLower(id), "alpha:sentinel"); err != nil {
			t.Fatal(err)
		}
	}
	sdQueue(t, st, "B", "alpha:sentinel")
	sdSprintCard(t, c, "Q", "alpha:sentinel") // the bare sentinel form on a sprint card
	sdLand(t, c, "A1")
	if _, _, err := sdResolve(c, lease); err != nil {
		t.Fatal(err)
	}
	if w := sdField(t, c, "C", "where"); w != "ready" {
		t.Fatalf("C after land+resolve: where=%s", w)
	}
	if w, code := sdWhy(t, c, "B"); code != 0 {
		t.Fatalf("B after land+resolve: %d %q", code, w)
	}
	cli := func(sub string, args ...string) (int, string) {
		var out, errOut bytes.Buffer
		code := runCardMove(ctx, sub, append([]string{"--redis", addr, "--actor", "reader"}, args...), &out, &errOut)
		return code, strings.TrimSpace(out.String() + errOut.String())
	}
	// D's copy dealt while alpha is landed
	as, _ := taskcard.ParseConsumer("friend:f1")
	dd, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: as, N: 1, IDs: []string{"D"}, By: "reader"})
	if err != nil || len(dd) != 1 {
		t.Fatalf("deal D while landed: %+v %v", dd, err)
	}
	// reopen alpha
	if err := sdCard(t, c, "A2", "alpha", ""); err != nil {
		t.Fatal(err)
	}
	fns := &reconcile.DealFunctions{Client: c, Actor: "reconciler"}
	// door 1: task take B through the CLI, byte for byte ready --why
	var why, whyErr, out, errOut bytes.Buffer
	readyReport(ctx, c, mapForge{}, "", "B", &why, &whyErr)
	code := runTaskTakeAs(ctx, []string{"--redis", addr, "--sprint", sdSprint, "--id", "B", "--as", "f1"}, "f1", &out, &errOut)
	t.Logf("reopened: task take B exit %d %q; ready --why B %q", code, out.String(), why.String())
	if code != 7 || !bytes.Equal(out.Bytes(), why.Bytes()) {
		t.Fatalf("door task take B let it through or lost the reader line")
	}
	// door 2: card deal C through the CLI
	code, line := cli("deal", "--to", "friend:f1", "--ids", "C")
	t.Logf("reopened: card deal C exit %d %q", code, line)
	if code == 0 || c.HGet(ctx, "task:C", "copy").Val() != "" {
		t.Fatalf("door card deal C dealt with alpha reopened")
	}
	// door 3: ns_card_deal of sprint card Q (bare alpha:sentinel)
	res, err := fns.Reserve(ctx, fence, bench, []deal.Card{{Sprint: sdSprint, Label: "Q"}})
	t.Logf("reopened: ns_card_deal Q %+v %v; Go gate %q", res, err, sdGate(t, c)["Q"])
	if err != nil || len(res) != 0 {
		t.Fatalf("door ns_card_deal dealt Q with alpha reopened")
	}
	// door 4: card work of D's already-dealt copy through the CLI
	code, line = cli("work", "--as", "friend:f1", "--ids", dd[0].Copy)
	t.Logf("reopened: card work %s exit %d %q", dd[0].Copy, code, line)
	if code == 0 {
		t.Fatalf("door card work started D's copy with alpha reopened")
	}
	// reland: A2 lands, the sentinel with it; all four proceed
	sdLand(t, c, "A2")
	if _, _, err := sdResolve(c, lease); err != nil {
		t.Fatal(err)
	}
	t.Logf("relanded: sentinel where=%s", sdField(t, c, "alpha:sentinel", "where"))
	out.Reset()
	errOut.Reset()
	if code := runTaskTakeAs(ctx, []string{"--redis", addr, "--sprint", sdSprint, "--id", "B", "--as", "f1"}, "f1", &out, &errOut); code != 0 {
		t.Fatalf("relanded: task take B exit %d %q %q", code, out.String(), errOut.String())
	}
	if code, line := cli("deal", "--to", "friend:f1", "--ids", "C"); code != 0 {
		t.Fatalf("relanded: card deal C exit %d %q", code, line)
	}
	if res, err := fns.Reserve(ctx, fence, bench, []deal.Card{{Sprint: sdSprint, Label: "Q"}}); err != nil || len(res) != 1 {
		t.Fatalf("relanded: ns_card_deal Q %+v %v", res, err)
	}
	if code, line := cli("work", "--as", "friend:f1", "--ids", dd[0].Copy); code != 0 {
		t.Fatalf("relanded: card work %s exit %d %q", dd[0].Copy, code, line)
	}
	t.Logf("relanded: all four doors proceed")
}
