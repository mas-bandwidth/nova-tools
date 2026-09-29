package batchmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// RequestBuilder receives the last independent Redis snapshot and the
// validated accepted history. It returns the exact bytes to send to one FCALL.
// A retry may return the original bytes; a conflict may retain the operation
// ID with changed bytes. The runner never re-encodes the request.
type RequestBuilder func(Snapshot, []AcceptedEvidence) ([]byte, error)

// RunFiniteTrace captures one to three consecutive batch FCALLs on an already
// prepared, quiescent owned Redis fixture. It proves wire, durable record,
// stream, complete image and finite-state projection consistency before the
// caller writes a TLC bundle. TLC must still validate Apply(q) for every step.
// Non-batch epoch/ordinary-writer transitions need a separate typed harness.
func RunFiniteTrace(ctx context.Context, a RedisCapture, builders []RequestBuilder) ([]Step, FiniteBaseline, error) {
	var zero FiniteBaseline
	if len(builders) < 1 || len(builders) > 3 {
		return nil, zero, fmt.Errorf("finite trace needs one to three batch calls")
	}
	if a.Store == nil {
		return nil, zero, fmt.Errorf("nil owned Redis store")
	}
	current, err := a.Capture(ctx, Request{Table: "t1", Epoch: "1"})
	if err != nil {
		return nil, zero, err
	}
	base := FiniteBaseline{TableRevision: current.TableRevision, MemberRevision: map[string]string{}, Operations: current.Operations, Receipts: current.Receipts}
	for _, id := range []string{"m1", "m2", "m3"} {
		base.MemberRevision[id] = current.Members[id].Revision
	}
	control := ProjectionControl{SeenEpoch: current.Epoch, Outcome: "initial"}
	steps := make([]Step, 0, len(builders))
	for i, build := range builders {
		if err := ctx.Err(); err != nil {
			return nil, base, err
		}
		raw, err := build(cloneSnapshot(current), cloneAcceptedHistory(control.Accepted))
		if err != nil {
			return nil, base, fmt.Errorf("build step %d: %w", i, err)
		}
		qPeek, err := peekFiniteRequest(raw)
		if err != nil {
			return nil, base, fmt.Errorf("identity step %d: %w", i, err)
		}
		before := cloneSnapshot(current)
		before.Recorded = recordFor(before, qPeek)
		decodeBefore := before
		if before.Recorded != nil && SameBytes(before.Recorded.Canonical, raw) {
			prior := acceptedFor(control.Accepted, qPeek)
			if prior == nil {
				return nil, base, fmt.Errorf("retry has no validated original receipt")
			}
			decodeBefore = prior.Before
		}
		q, action, err := DecodeRequest(raw, decodeBefore, base.TableRevision, base.MemberRevision)
		if err != nil {
			return nil, base, fmt.Errorf("decode step %d: %w", i, err)
		}
		beforeModel, err := ProjectFinite(before, base, control)
		if err != nil {
			return nil, base, fmt.Errorf("project before %d: %w", i, err)
		}
		reply := a.Store.Cmd("FCALL", ntable.FnApply, 1, ntable.DefKey(q.Table), q.Table, string(q.Canonical))
		wire, err := replyList(reply)
		if err != nil || len(wire) == 0 {
			return nil, base, fmt.Errorf("malformed FCALL reply: %v", err)
		}
		var result Result
		var receipt, priorReceipt *Receipt
		switch wire[0] {
		case "REFUSED":
			result = Refused
			if before.Recorded != nil && len(wire) >= 2 && wire[1] != "OPCONFLICT" {
				return nil, base, fmt.Errorf("recorded conflicting operation did not refuse OPCONFLICT")
			}
		case "OK":
			if before.Recorded == nil {
				result = Accepted
				receipt, err = DecodeAcceptedReply(reply, q, before)
				if err != nil {
					return nil, base, fmt.Errorf("accepted reply %d: %w", i, err)
				}
			} else {
				if !SameBytes(before.Recorded.Canonical, raw) {
					return nil, base, fmt.Errorf("conflicting bytes returned success")
				}
				result = Replayed
				prior := acceptedFor(control.Accepted, q)
				if prior == nil {
					return nil, base, fmt.Errorf("replay lacks validated original")
				}
				decoded, err := decodeReplayedReply(wire, *before.Recorded, *prior)
				if err != nil {
					return nil, base, fmt.Errorf("replay lost original receipt: %v", err)
				}
				receipt = decoded
				copy := prior.Receipt
				priorReceipt = &copy
			}
		default:
			return nil, base, fmt.Errorf("unknown FCALL reply %v", wire[0])
		}
		after, err := a.Capture(ctx, q)
		if err != nil {
			return nil, base, fmt.Errorf("capture after %d: %w", i, err)
		}
		next := control
		next.Attempt = &action
		next.Returned = nil
		switch result {
		case Accepted:
			event := after.LastEvent
			if event == nil || after.Recorded == nil {
				return nil, base, fmt.Errorf("accepted call lacks event/record")
			}
			streamReply := []any{"OK", []any{"RECEIPT", event.ID, event.Fields["epoch"], event.Fields["rev_before"], event.Fields["rev_after"], event.Fields["outcome"], event.Fields["batch_delta"]}}
			fromStream, err := DecodeAcceptedReply(streamReply, q, before)
			if err != nil {
				return nil, base, fmt.Errorf("stream receipt: %w", err)
			}
			after.LastReceipt = fromStream
			evidence := AcceptedEvidence{Action: action, Request: q, Before: before, Receipt: *receipt, Event: *event, Record: *after.Recorded}
			next.Accepted = append(append([]AcceptedEvidence(nil), control.Accepted...), evidence)
			next.Outcome = "accepted"
			next.Returned = receipt
		case Replayed:
			next.Outcome = "replay"
			next.Returned = receipt
		case Refused:
			if before.Recorded != nil {
				next.Outcome = "conflict"
			} else {
				next.Outcome = "stale-or-invalid"
			}
		}
		afterModel, err := ProjectFinite(after, base, next)
		if err != nil {
			return nil, base, fmt.Errorf("project after %d: %w", i, err)
		}
		step := Step{Index: uint64(i), Request: q, Result: result, Before: before, After: after, Receipt: receipt, PriorReceipt: priorReceipt,
			BeforeModel: beforeModel, AfterModel: afterModel, Action: action, TableBaseline: base.TableRevision, MemberBaseline: base.MemberRevision}
		if err := ValidateStep(step); err != nil {
			return nil, base, fmt.Errorf("validate step %d: %w", i, err)
		}
		steps = append(steps, step)
		current = after
		control = next
	}
	return steps, base, nil
}

// A replay carries the saved result plus a third marker. Decode the durable
// original as a new receipt against its original prestate, then require the
// returned payload to match it exactly. The replay's current prestate cannot
// explain the original receipt.
func decodeReplayedReply(wire []any, record OperationRecord, prior AcceptedEvidence) (*Receipt, error) {
	if len(wire) != 3 || wire[0] != "OK" || wire[2] != "REPLAY" {
		return nil, fmt.Errorf("replay marker missing or malformed")
	}
	if !reflect.DeepEqual(record, prior.Record) || !sameOperation(record, prior.Request, prior.Receipt) {
		return nil, fmt.Errorf("durable replay record differs from validated original")
	}
	var stored any
	if err := json.Unmarshal(record.ResultJSON, &stored); err != nil {
		return nil, fmt.Errorf("durable result decode: %w", err)
	}
	storedWire, err := replyList(stored)
	if err != nil || !reflect.DeepEqual(wire[:2], storedWire) {
		return nil, fmt.Errorf("replay payload differs from durable result")
	}
	decoded, err := DecodeAcceptedReply(stored, prior.Request, prior.Before)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(*decoded, prior.Receipt) {
		return nil, fmt.Errorf("durable result differs from validated receipt")
	}
	return decoded, nil
}

func peekFiniteRequest(raw []byte) (Request, error) {
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return Request{}, err
	}
	if err := validateManifestShape(raw); err != nil {
		return Request{}, err
	}
	var w wireManifest
	if err := json.Unmarshal(raw, &w); err != nil {
		return Request{}, err
	}
	if w.Table != "t1" || (w.Epoch != "1" && w.Epoch != "2") || w.OperationID == "" {
		return Request{}, fmt.Errorf("request identity outside finite model")
	}
	return Request{Table: w.Table, Epoch: w.Epoch, OperationID: w.OperationID}, nil
}
func recordFor(s Snapshot, q Request) *OperationRecord {
	key := operationField(q.Epoch, q.OperationID)
	record, ok := s.OperationRecords[key]
	if !ok {
		return nil
	}
	return &record
}
func acceptedFor(history []AcceptedEvidence, q Request) *AcceptedEvidence {
	for i := range history {
		if history[i].Request.Table == q.Table && history[i].Request.Epoch == q.Epoch && history[i].Request.OperationID == q.OperationID {
			return &history[i]
		}
	}
	return nil
}

func cloneAcceptedHistory(history []AcceptedEvidence) []AcceptedEvidence {
	out := make([]AcceptedEvidence, len(history))
	for i, e := range history {
		out[i] = e
		out[i].Action = cloneAction(e.Action)
		out[i].Request = cloneRequest(e.Request)
		out[i].Before = cloneSnapshot(e.Before)
		r := cloneReceipt(&e.Receipt)
		out[i].Receipt = *r
		out[i].Event.Fields = make(map[string]string, len(e.Event.Fields))
		out[i].Event.RawFields = append([]string(nil), e.Event.RawFields...)
		for k, v := range e.Event.Fields {
			out[i].Event.Fields[k] = v
		}
		out[i].Record.Canonical = append([]byte(nil), e.Record.Canonical...)
		out[i].Record.ResultJSON = append([]byte(nil), e.Record.ResultJSON...)
	}
	return out
}
