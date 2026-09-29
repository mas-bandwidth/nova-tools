package batchmodel

import (
	"context"
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/tablemodel"
)

// RunOrdinaryRemove captures an actual ordinary table writer followed by a
// batch that omits the changed member's revision guard. The ordinary remove
// leaves the member record and its status intact while advancing both table
// and member revisions. The model must explain both transitions continuously.
func RunOrdinaryRemove(ctx context.Context, r *tablemodel.Store, source []byte) ([]MixedStep, error) {
	if r == nil {
		return nil, fmt.Errorf("nil owned Redis store")
	}
	if err := seedSuiteRuntime(r, source, "ordinary_remove", true); err != nil {
		return nil, err
	}
	a := RedisCapture{Store: r}
	identity := Request{Table: "t1", Epoch: "1"}
	initial, err := a.Capture(ctx, identity)
	if err != nil {
		return nil, err
	}
	base := FiniteBaseline{TableRevision: initial.TableRevision, MemberRevision: map[string]string{"m1": initial.Members["m1"].Revision, "m2": "0", "m3": initial.Members["m3"].Revision}, Operations: initial.Operations, Receipts: initial.Receipts}
	initialModel, err := ProjectFinite(initial, base, ProjectionControl{SeenEpoch: "1", Outcome: "initial"})
	if err != nil {
		return nil, err
	}
	reply := r.Cmd("FCALL", "ns_table_cell_remove", 0, "t1", "r1", "c1", "m1", `{"epoch":"1"}`)
	if !strings.HasPrefix(fmt.Sprint(reply), "[OK ") {
		return nil, fmt.Errorf("ordinary cell_remove: %v", reply)
	}
	removed, err := a.Capture(ctx, identity)
	if err != nil {
		return nil, err
	}
	if !removed.Members["m1"].Exists || removed.Members["m1"].Place != nil || removed.Members["m1"].Fields["status"] != "ready" {
		return nil, fmt.Errorf("ordinary remove lost retained member record")
	}
	if len(removed.Events) != len(initial.Events)+1 || removed.Events[len(initial.Events)].Fields["verb"] != "cell_remove" {
		return nil, fmt.Errorf("ordinary writer lacks independent cell_remove event")
	}
	event := removed.Events[len(initial.Events)]
	ordinaryControl := ProjectionControl{SeenEpoch: "1", Outcome: "ordinary", NonBatchEvents: []StreamEvent{event}}
	removedModel, err := ProjectFinite(removed, base, ordinaryControl)
	if err != nil {
		return nil, err
	}
	canonical := []byte(fmt.Sprintf(`{"schema":1,"table":"t1","epoch":"1","expected_table_revision":%q,"operation_id":"op-after-ordinary","actor":"w1","members":[{"id":"m1","expect":{"fields":{"status":{"equals":"ready"}}}}]}`, removed.TableRevision))
	request, action, err := DecodeRequest(canonical, removed, base.TableRevision, base.MemberRevision)
	if err != nil {
		return nil, err
	}
	applyReply := r.Cmd("FCALL", ntable.FnApply, 1, ntable.DefKey("t1"), "t1", string(canonical))
	receipt, err := DecodeAcceptedReply(applyReply, request, removed)
	if err != nil {
		return nil, fmt.Errorf("omitted-guard reply: %w", err)
	}
	final, err := a.Capture(ctx, Request{Table: "t1", Epoch: "1", OperationID: "op-after-ordinary"})
	if err != nil {
		return nil, err
	}
	if final.LastEvent == nil || final.Recorded == nil {
		return nil, fmt.Errorf("omitted-guard batch lacks durable evidence")
	}
	streamReply := []any{"OK", []any{"RECEIPT", final.LastEvent.ID, final.LastEvent.Fields["epoch"], final.LastEvent.Fields["rev_before"], final.LastEvent.Fields["rev_after"], final.LastEvent.Fields["outcome"], final.LastEvent.Fields["batch_delta"]}}
	fromStream, err := DecodeAcceptedReply(streamReply, request, removed)
	if err != nil {
		return nil, fmt.Errorf("omitted-guard stream: %w", err)
	}
	final.LastReceipt = fromStream
	evidence := AcceptedEvidence{Action: action, Request: request, Before: removed, Receipt: *receipt, Event: *final.LastEvent, Record: *final.Recorded}
	finalModel, err := ProjectFinite(final, base, ProjectionControl{SeenEpoch: "1", Outcome: "accepted", NonBatchEvents: []StreamEvent{event}, Accepted: []AcceptedEvidence{evidence}, Attempt: &action, Returned: receipt})
	if err != nil {
		return nil, err
	}
	batch := Step{Index: 1, Request: request, Result: Accepted, Before: removed, After: final, Receipt: receipt, BeforeModel: removedModel, AfterModel: finalModel, Action: action, TableBaseline: base.TableRevision, MemberBaseline: base.MemberRevision}
	if err := ValidateStep(batch); err != nil {
		return nil, err
	}
	return []MixedStep{
		{NonBatch: &NonBatchStep{Index: 0, Kind: OrdinaryRemoveMember, Before: initial, After: removed, BeforeModel: initialModel, AfterModel: removedModel}},
		{Batch: &batch},
	}, nil
}
