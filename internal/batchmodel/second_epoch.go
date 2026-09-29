package batchmodel

import (
	"context"
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/tablemodel"
)

// RunSecondEpoch captures one continuous, owned-Redis history matching the
// second-epoch model instance. No projected state is built from a receipt:
// every boundary is read independently from Redis, including the read-only
// writer refresh. The returned packet must still pass TLC to establish that
// the existing model actions explain the observations.
func RunSecondEpoch(ctx context.Context, r *tablemodel.Store, source []byte) ([]MixedStep, error) {
	if r == nil {
		return nil, fmt.Errorf("nil owned Redis store")
	}
	if err := seedSuiteRuntime(r, source, "second_epoch", false); err != nil {
		return nil, err
	}
	a := RedisCapture{Store: r}
	q := Request{Table: "t1", Epoch: "1"}
	initial, err := a.Capture(ctx, q)
	if err != nil {
		return nil, err
	}
	if initial.Epoch != "1" || initial.Definitions["1"]["_present"] != "1" || len(initial.Rows["1"]) != 2 || initial.Members["m3"].Exists {
		return nil, fmt.Errorf("second-epoch seed differs")
	}
	base := FiniteBaseline{TableRevision: initial.TableRevision, MemberRevision: map[string]string{"m1": initial.Members["m1"].Revision, "m2": "0", "m3": "0"}, Operations: initial.Operations, Receipts: initial.Receipts}
	initialModel, err := ProjectFinite(initial, base, ProjectionControl{SeenEpoch: "1", Outcome: "initial"})
	if err != nil {
		return nil, err
	}
	if got, err := replySignedInt(r.Cmd("HINCRBY", "replay:epoch", "n", 1)); err != nil || got != 2 {
		return nil, fmt.Errorf("epoch advance: %d %v", got, err)
	}
	advanced, err := a.Capture(ctx, q)
	if err != nil {
		return nil, err
	}
	if advanced.Epoch != "2" || len(advanced.Definitions["2"]) != 0 {
		return nil, fmt.Errorf("new epoch was not unshaped")
	}
	advancedModel, err := ProjectFinite(advanced, base, ProjectionControl{SeenEpoch: "1", Outcome: "advance"})
	if err != nil {
		return nil, err
	}
	seen, err := replyString(r.Cmd("HGET", "replay:epoch", "n"))
	if err != nil || seen != "2" {
		return nil, fmt.Errorf("writer epoch read: %q %v", seen, err)
	}
	refreshed, err := a.Capture(ctx, q)
	if err != nil {
		return nil, err
	}
	if !SameImage(advanced.Image, refreshed.Image) {
		return nil, fmt.Errorf("read-only refresh changed complete store")
	}
	refreshedModel, err := ProjectFinite(refreshed, base, ProjectionControl{SeenEpoch: "2", Outcome: "refresh"})
	if err != nil {
		return nil, err
	}
	bind := fmt.Sprintf(`{"fields":%s,"rows":[{"key":"r1"},{"key":"r2"}]}`, finiteFields)
	bindReply := r.Cmd("FCALL", "ns_table_bind", 0, "t1", bind, `{"epoch":"2"}`)
	if !strings.HasPrefix(fmt.Sprint(bindReply), "[OK ") {
		return nil, fmt.Errorf("second generation bind: %v", bindReply)
	}
	shaped, err := a.Capture(ctx, q)
	if err != nil {
		return nil, err
	}
	if shaped.Definitions["2"]["_present"] != "1" || len(shaped.Rows["2"]) != 2 || shaped.Members["m3"].Exists {
		return nil, fmt.Errorf("second generation shape differs")
	}
	if len(shaped.Events) != len(initial.Events)+1 || shaped.Events[len(initial.Events)].Fields["verb"] != "bind" {
		return nil, fmt.Errorf("bind lacks exactly one independent non-batch stream event")
	}
	bindEvent := shaped.Events[len(initial.Events)]
	shapeControl := ProjectionControl{SeenEpoch: "2", Outcome: "shape", NonBatchEvents: []StreamEvent{bindEvent}}
	shapeModel, err := ProjectFinite(shaped, base, shapeControl)
	if err != nil {
		return nil, err
	}
	canonical := []byte(fmt.Sprintf(`{"schema":1,"table":"t1","epoch":"2","expected_table_revision":%q,"operation_id":"op-epoch-two","actor":"w1","members":[{"id":"m3","expect":{"absent":true},"create":{"row":"r1","col":"c1","score":1},"set":{"status":"new"}}]}`, shaped.TableRevision))
	request, action, err := DecodeRequest(canonical, shaped, base.TableRevision, base.MemberRevision)
	if err != nil {
		return nil, err
	}
	reply := r.Cmd("FCALL", ntable.FnApply, 1, ntable.DefKey("t1"), "t1", string(canonical))
	receipt, err := DecodeAcceptedReply(reply, request, shaped)
	if err != nil {
		return nil, fmt.Errorf("epoch-two batch reply: %w", err)
	}
	final, err := a.Capture(ctx, Request{Table: "t1", Epoch: "2", OperationID: "op-epoch-two"})
	if err != nil {
		return nil, err
	}
	if final.LastEvent == nil || final.Recorded == nil {
		return nil, fmt.Errorf("epoch-two batch lacks durable operation or stream event")
	}
	streamReply := []any{"OK", []any{"RECEIPT", final.LastEvent.ID, final.LastEvent.Fields["epoch"], final.LastEvent.Fields["rev_before"], final.LastEvent.Fields["rev_after"], final.LastEvent.Fields["outcome"], final.LastEvent.Fields["batch_delta"]}}
	fromStream, err := DecodeAcceptedReply(streamReply, request, shaped)
	if err != nil {
		return nil, fmt.Errorf("epoch-two stream: %w", err)
	}
	final.LastReceipt = fromStream
	evidence := AcceptedEvidence{Action: action, Request: request, Before: shaped, Receipt: *receipt, Event: *final.LastEvent, Record: *final.Recorded}
	finalModel, err := ProjectFinite(final, base, ProjectionControl{SeenEpoch: "2", Outcome: "accepted", NonBatchEvents: []StreamEvent{bindEvent}, Accepted: []AcceptedEvidence{evidence}, Attempt: &action, Returned: receipt})
	if err != nil {
		return nil, err
	}
	batch := Step{Index: 3, Request: request, Result: Accepted, Before: shaped, After: final, Receipt: receipt, BeforeModel: shapeModel, AfterModel: finalModel, Action: action, TableBaseline: base.TableRevision, MemberBaseline: base.MemberRevision}
	if err := ValidateStep(batch); err != nil {
		return nil, err
	}
	return []MixedStep{
		{NonBatch: &NonBatchStep{Index: 0, Kind: AdvanceEpoch, Before: initial, After: advanced, BeforeModel: initialModel, AfterModel: advancedModel}},
		{NonBatch: &NonBatchStep{Index: 1, Kind: RefreshEpoch, Before: advanced, After: refreshed, BeforeModel: advancedModel, AfterModel: refreshedModel}},
		{NonBatch: &NonBatchStep{Index: 2, Kind: BindSecondEpoch, Before: refreshed, After: shaped, BeforeModel: refreshedModel, AfterModel: shapeModel}},
		{Batch: &batch},
	}, nil
}
