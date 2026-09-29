//go:build functional

package batchmodel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/tablemodel"
)

func TestEpoch(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	source, err := os.ReadFile(filepath.Join("..", "nsprint", "fn", "lua", "table.lua"))
	if err != nil {
		t.Fatal(err)
	}
	err = tablemodel.WithStore(ctx, tablemodel.ServerOptions{RedisServer: testutil.Program(t), TmpDir: t.TempDir()}, func(r *tablemodel.Store) {
		r.Cmd("FUNCTION", "LOAD", "REPLACE", "#!lua name=batch_second_epoch\n"+string(source))
		r.Cmd("HSET", "replay:epoch", "n", "1")
		fields := `{"order":"c1,c2","footer":"total","created_at":"2026-09-27T00:00:00Z","epoch_key":"replay:epoch","epoch_field":"n","col:c1":"members:none:10:c1","col:c2":"members:none:10:c2"}`
		call := func(name string, args ...any) any {
			return r.Cmd(append([]any{"FCALL", "ns_table_" + name, 0}, args...)...)
		}
		if got := call("create", "t1", fields, `{"epoch":"1"}`); !strings.HasPrefix(fmt.Sprint(got), "[OK ") {
			t.Fatalf("create: %v", got)
		}
		for _, row := range []string{"r1", "r2"} {
			if got := call("row_add", "t1", row, "{}", `{"epoch":"1"}`); !strings.HasPrefix(fmt.Sprint(got), "[ROW ") {
				t.Fatalf("row: %v", got)
			}
		}
		if got := call("cell_add", "t1", "r1", "c1", 1, "m1", `{"epoch":"1"}`); !strings.HasPrefix(fmt.Sprint(got), "[OK ") {
			t.Fatalf("cell: %v", got)
		}
		r.Cmd("HSET", ntable.MemberKey("m1"), "status", "ready")
		a := RedisCapture{Store: r}
		q := Request{Table: "t1", Epoch: "1"}
		initial, err := a.Capture(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if initial.Definitions["1"]["_present"] != "1" || len(initial.Rows["1"]) != 2 || initial.Members["m3"].Exists {
			t.Fatalf("wrong initial fixture: %+v", initial)
		}
		base := FiniteBaseline{TableRevision: initial.TableRevision, MemberRevision: map[string]string{"m1": initial.Members["m1"].Revision, "m2": "0", "m3": "0"}, Operations: initial.Operations, Receipts: initial.Receipts}
		initialModel, err := ProjectFinite(initial, base, ProjectionControl{SeenEpoch: "1", Outcome: "initial"})
		if err != nil {
			t.Fatalf("project initial: %v", err)
		}
		r.Cmd("HINCRBY", "replay:epoch", "n", 1)
		advanced, err := a.Capture(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if advanced.Epoch != "2" || len(advanced.Definitions["2"]) != 0 || !advanced.Members["m1"].Exists {
			t.Fatalf("bad advance: %+v", advanced)
		}
		advancedModel, err := ProjectFinite(advanced, base, ProjectionControl{SeenEpoch: "1", Outcome: "advance"})
		if err != nil {
			t.Fatalf("project advance: %v", err)
		}
		observed, err := replyString(r.Cmd("HGET", "replay:epoch", "n"))
		if err != nil || observed != "2" {
			t.Fatalf("refresh: %q %v", observed, err)
		}
		refreshed, err := a.Capture(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if !SameImage(advanced.Image, refreshed.Image) {
			t.Fatal("read-only epoch refresh changed complete store")
		}
		refreshedModel, err := ProjectFinite(refreshed, base, ProjectionControl{SeenEpoch: "2", Outcome: "refresh"})
		if err != nil {
			t.Fatalf("project refresh: %v", err)
		}
		bind := fmt.Sprintf(`{"fields":%s,"rows":[{"key":"r1"},{"key":"r2"}]}`, fields)
		if got := call("bind", "t1", bind, `{"epoch":"2"}`); !strings.HasPrefix(fmt.Sprint(got), "[OK ") {
			t.Fatalf("bind: %v", got)
		}
		shaped, err := a.Capture(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if shaped.Definitions["2"]["_present"] != "1" || len(shaped.Rows["2"]) != 2 || shaped.Members["m3"].Exists {
			t.Fatalf("bad shape: %+v", shaped)
		}
		if len(shaped.Events) != len(initial.Events)+1 || shaped.Events[len(initial.Events)].Fields["verb"] != "bind" {
			t.Fatalf("missing independent bind event: %+v", shaped.Events)
		}
		bindEvent := shaped.Events[len(initial.Events)]
		if _, err := ProjectFinite(shaped, base, ProjectionControl{SeenEpoch: "2", Outcome: "shape", NonBatchEvents: []StreamEvent{bindEvent}}); err != nil {
			t.Fatalf("project shape: %v", err)
		}
		canonical := []byte(fmt.Sprintf(`{"schema":1,"table":"t1","epoch":"2","expected_table_revision":%q,"operation_id":"op-epoch-two","actor":"w1","members":[{"id":"m3","expect":{"absent":true},"create":{"row":"r1","col":"c1","score":1},"set":{"status":"new"}}]}`, shaped.TableRevision))
		qDecoded, action, err := DecodeRequest(canonical, shaped, base.TableRevision, base.MemberRevision)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		reply := r.Cmd("FCALL", ntable.FnApply, 1, ntable.DefKey("t1"), "t1", string(canonical))
		if !strings.HasPrefix(fmt.Sprint(reply), "[OK [RECEIPT ") {
			t.Fatalf("apply: %v", reply)
		}
		receipt, err := DecodeAcceptedReply(reply, qDecoded, shaped)
		if err != nil {
			t.Fatalf("reply: %v", err)
		}
		final, err := a.Capture(ctx, Request{Table: "t1", Epoch: "2", OperationID: "op-epoch-two"})
		if err != nil {
			t.Fatal(err)
		}
		if !final.Members["m3"].Exists || final.Members["m3"].Epoch != "2" || final.Members["m3"].Place == nil || final.Members["m3"].Fields["status"] != "new" || !final.Members["m1"].Exists || len(final.Rows["1"]) != 2 {
			t.Fatalf("bad final: %+v", final)
		}
		if final.LastEvent == nil || final.Recorded == nil {
			t.Fatal("missing independent durable batch evidence")
		}
		streamReply := []any{"OK", []any{"RECEIPT", final.LastEvent.ID, final.LastEvent.Fields["epoch"], final.LastEvent.Fields["rev_before"], final.LastEvent.Fields["rev_after"], final.LastEvent.Fields["outcome"], final.LastEvent.Fields["batch_delta"]}}
		fromStream, err := DecodeAcceptedReply(streamReply, qDecoded, shaped)
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		final.LastReceipt = fromStream
		evidence := AcceptedEvidence{Action: action, Request: qDecoded, Before: shaped, Receipt: *receipt, Event: *final.LastEvent, Record: *final.Recorded}
		finalControl := ProjectionControl{SeenEpoch: "2", Outcome: "accepted", NonBatchEvents: []StreamEvent{bindEvent}, Accepted: []AcceptedEvidence{evidence}, Attempt: &action, Returned: receipt}
		finalModel, err := ProjectFinite(final, base, finalControl)
		if err != nil {
			t.Fatalf("project final: %v", err)
		}
		shapeModel, err := ProjectFinite(shaped, base, ProjectionControl{SeenEpoch: "2", Outcome: "shape", NonBatchEvents: []StreamEvent{bindEvent}})
		if err != nil {
			t.Fatalf("project pre-apply: %v", err)
		}
		step := Step{Index: 3, Request: qDecoded, Result: Accepted, Before: shaped, After: final, Receipt: receipt, BeforeModel: shapeModel, AfterModel: finalModel, Action: action, TableBaseline: base.TableRevision, MemberBaseline: base.MemberRevision}
		if err := ValidateStep(step); err != nil {
			t.Fatalf("validate epoch-two Apply: %v", err)
		}
		mixed := []MixedStep{
			{NonBatch: &NonBatchStep{Index: 0, Kind: AdvanceEpoch, Before: initial, After: advanced, BeforeModel: initialModel, AfterModel: advancedModel}},
			{NonBatch: &NonBatchStep{Index: 1, Kind: RefreshEpoch, Before: advanced, After: refreshed, BeforeModel: advancedModel, AfterModel: refreshedModel}},
			{NonBatch: &NonBatchStep{Index: 2, Kind: BindSecondEpoch, Before: refreshed, After: shaped, BeforeModel: refreshedModel, AfterModel: shapeModel}},
			{Batch: &step},
		}
		corruptAdvance := *mixed[0].NonBatch
		corruptAdvance.After = cloneSnapshot(corruptAdvance.After)
		corruptAdvance.After.Image["foreign:unrelated"] = []byte("injected")
		badMixed := append([]MixedStep(nil), mixed...)
		badMixed[0] = MixedStep{NonBatch: &corruptAdvance}
		if _, err := RenderMixedHarness(badMixed); err == nil || !strings.Contains(err.Error(), "unrelated key") {
			t.Fatalf("advance accepted foreign write: %v", err)
		}
		corruptBind := *mixed[2].NonBatch
		corruptBind.After = cloneSnapshot(corruptBind.After)
		oldKey := ntable.EpochPrefix("t1", 1) + ":definition"
		corruptBind.After.Image[oldKey] = append(append([]byte(nil), corruptBind.After.Image[oldKey]...), 0xff)
		badMixed = append([]MixedStep(nil), mixed...)
		badMixed[2] = MixedStep{NonBatch: &corruptBind}
		if _, err := RenderMixedHarness(badMixed); err == nil || !strings.Contains(err.Error(), "unrelated key") {
			t.Fatalf("bind accepted old-epoch write: %v", err)
		}
		corruptBind = *mixed[2].NonBatch
		corruptBind.After = cloneSnapshot(corruptBind.After)
		corruptBind.After.RowFields["2"]["r1"]["label"] = "changed"
		rowKey := ntable.RowKeyAt("t1", "r1", 2)
		corruptBind.After.Image[rowKey] = []byte("injected")
		badMixed = append([]MixedStep(nil), mixed...)
		badMixed[2] = MixedStep{NonBatch: &corruptBind}
		if _, err := RenderMixedHarness(badMixed); err == nil || !strings.Contains(err.Error(), "row r1") {
			t.Fatalf("bind accepted changed row metadata: %v", err)
		}
		corruptBind = *mixed[2].NonBatch
		corruptBind.After = cloneSnapshot(corruptBind.After)
		corruptBind.After.Definitions["2"]["col:c1"] = "unexpected"
		defKey := ntable.EpochPrefix("t1", 2) + ":definition"
		corruptBind.After.Image[defKey] = append(corruptBind.After.Image[defKey], 0xfe)
		badMixed = append([]MixedStep(nil), mixed...)
		badMixed[2] = MixedStep{NonBatch: &corruptBind}
		if _, err := RenderMixedHarness(badMixed); err == nil || !strings.Contains(err.Error(), "definition field") {
			t.Fatalf("bind accepted mismatched new definition: %v", err)
		}
		corruptBind = *mixed[2].NonBatch
		corruptBind.After = cloneSnapshot(corruptBind.After)
		corruptBind.After.RowScores["2"]["r1"] = "100"
		rowsKey := ntable.RowsKeyAt("t1", 2)
		corruptBind.After.Image[rowsKey] = append(corruptBind.After.Image[rowsKey], 0xfd)
		badMixed = append([]MixedStep(nil), mixed...)
		badMixed[2] = MixedStep{NonBatch: &corruptBind}
		if _, err := RenderMixedHarness(badMixed); err == nil || !strings.Contains(err.Error(), "row order or scores") {
			t.Fatalf("bind accepted changed row ZSET score: %v", err)
		}
		corruptBind = *mixed[2].NonBatch
		corruptBind.After = cloneSnapshot(corruptBind.After)
		if len(corruptBind.Before.Events) == 0 {
			t.Fatal("fixture lacks initial stream prefix")
		}
		corruptBind.After.Events[0].Fields["verb"] = "rewritten"
		streamKey := ntable.ChangesKey("t1")
		corruptBind.After.Image[streamKey] = append(corruptBind.After.Image[streamKey], 0xfb)
		badMixed = append([]MixedStep(nil), mixed...)
		badMixed[2] = MixedStep{NonBatch: &corruptBind}
		if _, err := RenderMixedHarness(badMixed); err == nil || !strings.Contains(err.Error(), "event prefix") {
			t.Fatalf("bind rewrite of initial stream event escaped: %v", err)
		}
		corruptBatch := step
		corruptBatch.After = cloneSnapshot(corruptBatch.After)
		corruptBatch.After.Image[oldKey] = append(append([]byte(nil), corruptBatch.After.Image[oldKey]...), 0xfe)
		if err := ValidateStep(corruptBatch); err == nil || !strings.Contains(err.Error(), "unrelated key") {
			t.Fatalf("epoch-two batch accepted old-epoch write: %v", err)
		}
		out := t.TempDir()
		if _, err := WriteMixedBundle(filepath.Join("..", "..", "tla"), out, mixed); err != nil {
			t.Fatalf("second epoch bundle: %v", err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}
