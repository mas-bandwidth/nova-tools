package tset

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestFenceWireRequiresExactMarkerAndEmptyStepShape(t *testing.T) {
	t.Parallel()
	valid := fenceWireStep("fence-op", "stable-intent")
	raw, err := EncodeStep(valid)
	if err != nil {
		t.Fatalf("EncodeStep exact fence request: %v", err)
	}
	if !bytes.Contains(raw, []byte(`"fence":true`)) {
		t.Fatalf("EncodeStep omitted literal true fence marker: %s", raw)
	}
	if _, err := DecodeStep(raw); err != nil {
		t.Fatalf("DecodeStep exact fence request: %v", err)
	}

	for _, marker := range []string{"false", "null", `"true"`, "0"} {
		wire := []byte(`{"epoch":"0","space":"s","op":"fence-op","intent":"stable-intent","fence":` + marker + `,"entries":[]}`)
		if _, err := DecodeStep(wire); !fenceWireRefusal(err, "REQUEST") {
			t.Errorf("DecodeStep accepted non-true fence marker %s: %v", marker, err)
		}
	}
	ordinary := []byte(`{"epoch":"0","space":"s","op":"ordinary-op","intent":"ordinary-intent","entries":[]}`)
	if _, err := DecodeStep(ordinary); err != nil {
		t.Fatalf("ordinary named empty step without fence marker was refused: %v", err)
	}
	withEmptyResult := []byte(`{"epoch":"0","space":"s","op":"fence-op","intent":"stable-intent","result":"","fence":true,"entries":[]}`)
	if _, err := DecodeStep(withEmptyResult); err != nil {
		t.Fatalf("DecodeStep fence with explicitly empty result: %v", err)
	}

	badShapes := []Step{
		{Epoch: "0", Space: "s", Fence: true, Entries: []Entry{}},
		func() Step { s := fenceWireStep("op", "intent"); s.Intent = nil; return s }(),
		func() Step { s := fenceWireStep("op", "intent"); s.Entries = nil; return s }(),
		func() Step { s := fenceWireStep("op", "intent"); s.Result = "nonempty"; return s }(),
		func() Step {
			s := fenceWireStep("op", "intent")
			s.Entries = []Entry{{Kind: "rows", Table: "cards", Add: []string{"r"}}}
			return s
		}(),
		func() Step {
			s := fenceWireStep("op", "intent")
			s.Notes = []Note{{Line: NoteLine{Kind: "note", Meta: json.RawMessage(`{}`)}, About: []string{"primary"}}}
			return s
		}(),
	}
	for i, step := range badShapes {
		if _, err := EncodeStep(step); !fenceWireRefusal(err, "REQUEST") {
			t.Errorf("EncodeStep malformed fence shape %d returned %v; want REQUEST", i, err)
		}
	}
	for _, wire := range []string{
		`{"epoch":"0","space":"s","op":"op","intent":"i","fence":true,"entries":[{"kind":"rows","t":"cards","add":["r"]}]}`,
		`{"epoch":"0","space":"s","op":"op","intent":"i","fence":true,"entries":null}`,
		`{"epoch":"0","space":"s","op":"op","intent":"i","result":"x","fence":true,"entries":[]}`,
		`{"epoch":"0","space":"s","op":"op","intent":"i","fence":true,"notes":[{"line":{"kind":"note","meta":{}},"about":["p"]}],"entries":[]}`,
		`{"epoch":"0","space":"s","intent":"i","fence":true,"entries":[]}`,
	} {
		if _, err := DecodeStep([]byte(wire)); !fenceWireRefusal(err, "REQUEST") {
			t.Errorf("DecodeStep malformed fence request returned %v; want REQUEST: %s", err, wire)
		}
	}
}

func TestFenceWireDoesNotInferFenceFromOrdinaryEmptyStep(t *testing.T) {
	t.Parallel()
	step := fenceWireStep("ordinary-op", "ordinary-intent")
	step.Fence = false
	raw, err := EncodeStep(step)
	if err != nil {
		t.Fatalf("EncodeStep ordinary named empty step: %v", err)
	}
	if bytes.Contains(raw, []byte(`"fence"`)) {
		t.Fatalf("ordinary empty step unexpectedly acquired a fence marker: %s", raw)
	}
	fake := &fakeRedisClient{replies: []fakeRedisReply{{value: okWireReply("ordinary")}}}
	reply, err := newRedisWithClient(fake).Step(context.Background(), step)
	if err != nil || reply.Status != "ok" || reply.Replay {
		t.Fatalf("ordinary empty named step = (%+v, %v); want ordinary ok", reply, err)
	}
	if fake.fcallCalls != 1 {
		t.Fatalf("ordinary empty step dispatched %d calls; want one", fake.fcallCalls)
	}
}

func TestFenceRepliesDecodeThroughStepAndSteps(t *testing.T) {
	t.Parallel()
	fullFenced := `{"status":"fenced","epoch_before":"0","epoch_after":"0","changed":0,"guarded":0,"changed_per_entry":[],"first_seq":"0","last_seq":"0","lines":0,"result":"","replay":false,"counters":{}}`
	compactFenced := `{"status":"fenced","epoch_before":"0","epoch_after":"0","changed":0,"first_seq":"0","last_seq":"0","result":"","replay":true}`
	compactOK := `{"status":"ok","epoch_before":"0","epoch_after":"0","changed":1,"first_seq":"1","last_seq":"1","result":"applied","replay":true}`

	freshClient := &fakeRedisClient{replies: []fakeRedisReply{{value: fullFenced}}}
	fresh, err := newRedisWithClient(freshClient).Step(context.Background(), fenceWireStep("fresh-fence", "intent-fresh"))
	if err != nil || fresh.Status != "fenced" || fresh.Replay || fresh.Changed != 0 || fresh.Guarded != 0 || fresh.Lines != 0 || fresh.FirstSeq != "0" || fresh.LastSeq != "0" {
		t.Fatalf("Step fresh fenced reply = (%+v, %v)", fresh, err)
	}

	fencedRequest := fenceWireStep("replayed-fence", "intent-fence")
	ordinaryRequest := fenceWireStep("replayed-ok", "intent-ok")
	ordinaryRequest.Fence = false
	batchClient := &fakeRedisClient{replies: []fakeRedisReply{
		{value: fullFenced},
		{value: compactFenced},
		{value: compactOK},
	}}
	results, err := newRedisWithClient(batchClient).Steps(context.Background(), []Step{fencedRequest, fencedRequest, ordinaryRequest})
	if err != nil || len(results) != 3 {
		t.Fatalf("Steps fenced/replay replies = (%+v, %v)", results, err)
	}
	want := []struct {
		status string
		replay bool
	}{{"fenced", false}, {"fenced", true}, {"ok", true}}
	for i := range results {
		if results[i].Err != nil || results[i].Reply.Status != want[i].status || results[i].Reply.Replay != want[i].replay {
			t.Errorf("Steps slot %d = (%+v, %v); want status=%s replay=%t", i, results[i].Reply, results[i].Err, want[i].status, want[i].replay)
		}
	}
	if batchClient.pipelineCalls != 1 || batchClient.flushCalls != 1 || len(batchClient.capturedArgs) != 3 {
		t.Errorf("Steps pipeline=%d flush=%d dispatched=%d; want one flush and three aligned requests", batchClient.pipelineCalls, batchClient.flushCalls, len(batchClient.capturedArgs))
	}
}

func TestFenceStatusSurvivesDoneReceiptDecode(t *testing.T) {
	t.Parallel()
	const raw = `{"status":"read","epoch":"0","active_epoch":"0","time_ms":"1","answers":[{"kind":"done","slots":[{"status":"match","intent_digest":"digest","receipt":{"status":"fenced","epoch_before":"0","epoch_after":"0","first_seq":"0","last_seq":"0","changed":0,"result":""}}]}],"complete":true,"counters":{}}`
	reply, err := DecodeReadReply([]byte(raw))
	if err != nil {
		t.Fatalf("DecodeReadReply saved fenced receipt: %v", err)
	}
	if len(reply.Answers) != 1 || len(reply.Answers[0].Done) != 1 || reply.Answers[0].Done[0].Status != "match" || reply.Answers[0].Done[0].Receipt == nil || reply.Answers[0].Done[0].Receipt.Status != "fenced" {
		t.Fatalf("done answer lost saved fenced status: %+v", reply.Answers)
	}
}

func TestFenceReplyDecoderRequiresFreshAndCompactSchemas(t *testing.T) {
	t.Parallel()
	fresh := `{"status":"fenced","epoch_before":"0","epoch_after":"0","changed":0,"guarded":0,"changed_per_entry":[],"first_seq":"0","last_seq":"0","lines":0,"result":"","replay":false,"counters":{}}`
	compactFence := `{"status":"fenced","epoch_before":"0","epoch_after":"0","changed":0,"first_seq":"0","last_seq":"0","result":"","replay":true}`
	compactOK := `{"status":"ok","epoch_before":"0","epoch_after":"0","changed":1,"first_seq":"1","last_seq":"1","result":"applied","replay":true}`
	for _, valid := range []string{fresh, compactFence, compactOK} {
		if _, err := DecodeReply([]byte(valid)); err != nil {
			t.Fatalf("DecodeReply refused valid fenced/compact schema %s: %v", valid, err)
		}
	}

	freshRequired := []string{"status", "epoch_before", "epoch_after", "changed", "guarded", "changed_per_entry", "first_seq", "last_seq", "lines", "result", "replay", "counters"}
	for _, key := range freshRequired {
		bad := fenceWireEditObject(t, fresh, func(object map[string]json.RawMessage) { delete(object, key) })
		if _, err := DecodeReply(bad); err == nil {
			t.Errorf("DecodeReply accepted fresh fenced reply missing %q", key)
		}
	}
	freshWrongTypes := map[string]json.RawMessage{
		"status": json.RawMessage(`0`), "epoch_before": json.RawMessage(`0`), "epoch_after": json.RawMessage(`null`),
		"changed": json.RawMessage(`null`), "guarded": json.RawMessage(`null`), "changed_per_entry": json.RawMessage(`null`),
		"first_seq": json.RawMessage(`0`), "last_seq": json.RawMessage(`null`), "lines": json.RawMessage(`null`),
		"result": json.RawMessage(`null`), "replay": json.RawMessage(`null`), "counters": json.RawMessage(`[]`),
	}
	for key, value := range freshWrongTypes {
		bad := fenceWireEditObject(t, fresh, func(object map[string]json.RawMessage) { object[key] = value })
		if _, err := DecodeReply(bad); err == nil {
			t.Errorf("DecodeReply accepted fresh fenced reply with invalid %q type", key)
		}
	}
	freshNonzero := map[string]json.RawMessage{
		"changed": json.RawMessage(`1`), "guarded": json.RawMessage(`1`), "changed_per_entry": json.RawMessage(`[1]`),
		"first_seq": json.RawMessage(`"1"`), "last_seq": json.RawMessage(`"1"`), "lines": json.RawMessage(`1`),
		"result": json.RawMessage(`"unexpected"`), "epoch_after": json.RawMessage(`"1"`), "replay": json.RawMessage(`true`),
	}
	for key, value := range freshNonzero {
		bad := fenceWireEditObject(t, fresh, func(object map[string]json.RawMessage) { object[key] = value })
		if _, err := DecodeReply(bad); err == nil {
			t.Errorf("DecodeReply accepted fresh fenced reply with invalid no-effect field %q", key)
		}
	}

	for _, compact := range []string{compactFence, compactOK} {
		for _, key := range []string{"status", "epoch_before", "epoch_after", "changed", "first_seq", "last_seq", "result", "replay"} {
			bad := fenceWireEditObject(t, compact, func(object map[string]json.RawMessage) { delete(object, key) })
			if _, err := DecodeReply(bad); err == nil {
				t.Errorf("DecodeReply accepted compact replay missing %q: %s", key, compact)
			}
		}
		for _, key := range []string{"guarded", "changed_per_entry", "lines", "counters"} {
			bad := fenceWireEditObject(t, compact, func(object map[string]json.RawMessage) { object[key] = json.RawMessage(`0`) })
			if _, err := DecodeReply(bad); err == nil {
				t.Errorf("DecodeReply accepted compact replay with fresh-only field %q", key)
			}
		}
	}
	compactWrongTypes := map[string]json.RawMessage{
		"status": json.RawMessage(`false`), "epoch_before": json.RawMessage(`0`), "epoch_after": json.RawMessage(`null`),
		"changed": json.RawMessage(`null`), "first_seq": json.RawMessage(`[]`), "last_seq": json.RawMessage(`null`),
		"result": json.RawMessage(`null`), "replay": json.RawMessage(`0`),
	}
	for key, value := range compactWrongTypes {
		bad := fenceWireEditObject(t, compactFence, func(object map[string]json.RawMessage) { object[key] = value })
		if _, err := DecodeReply(bad); err == nil {
			t.Errorf("DecodeReply accepted compact fenced replay with invalid %q type", key)
		}
	}
	for key, value := range map[string]json.RawMessage{
		"changed": json.RawMessage(`1`), "first_seq": json.RawMessage(`"1"`), "last_seq": json.RawMessage(`"1"`),
		"result": json.RawMessage(`"unexpected"`), "epoch_after": json.RawMessage(`"1"`),
	} {
		bad := fenceWireEditObject(t, compactFence, func(object map[string]json.RawMessage) { object[key] = value })
		if _, err := DecodeReply(bad); err == nil {
			t.Errorf("DecodeReply accepted compact fenced replay with nonzero %q", key)
		}
	}
}

func fenceWireStep(op, intent string) Step {
	return Step{Epoch: "0", Space: "s", Op: &op, Intent: &intent, Entries: []Entry{}, Fence: true}
}

func fenceWireRefusal(err error, code string) bool {
	var refusal *Refusal
	return errors.As(err, &refusal) && refusal.Code == code
}

func fenceWireEditObject(t *testing.T, raw string, edit func(map[string]json.RawMessage)) []byte {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &object); err != nil {
		t.Fatalf("decode response fixture: %v", err)
	}
	edit(object)
	encoded, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("encode edited response fixture: %v", err)
	}
	return encoded
}
