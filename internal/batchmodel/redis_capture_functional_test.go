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

func TestImage(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	err := tablemodel.WithStore(ctx, tablemodel.ServerOptions{RedisServer: testutil.Program(t), TmpDir: t.TempDir()}, func(r *tablemodel.Store) {
		r.Cmd("HSET", "replay:epoch", "n", "1")
		r.Cmd("HSET", ntable.RevisionKey("t1"), "n", "12")
		r.Cmd("HSET", ntable.MemberKey("m1"), "epoch", "1", "revision", "7", "place:t1", "r1:c1", "status", "")
		r.Cmd("ZADD", ntable.CellKeyAt("t1", "r1", "c1", 1), 1, "m1")
		r.Cmd("SADD", "foreign:set", "sentinel")
		r.Cmd("XADD", ntable.ChangesKey("t1"), "*", "verb", "seed")
		capture := RedisCapture{Store: r}
		q := Request{Table: "t1", Epoch: "1", OperationID: "op1"}
		before, err := capture.Capture(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		m1 := before.Members["m1"]
		if before.TableRevision != "12" || before.Epoch != "1" || !m1.Exists || m1.Epoch != "1" || m1.Revision != "7" || m1.Place == nil || m1.Place.Score != "1" {
			t.Fatalf("member/table observation lost: %+v %+v", before, m1)
		}
		if v, ok := m1.Fields["status"]; !ok || v != "" {
			t.Fatalf("present empty field lost: %+v", m1.Fields)
		}
		if before.Members["m2"].Exists || before.Receipts != 1 || before.LastEvent == nil || before.LastEvent.Fields["verb"] != "seed" {
			t.Fatalf("missing member or stream observation lost: %+v", before)
		}
		image := cloneSnapshot(before).Image
		stable, err := capture.ScanImage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !SameImage(image, stable) {
			t.Fatal("unchanged store image was not stable")
		}
		streamBefore := append([]byte(nil), stable[ntable.ChangesKey("t1")]...)
		added := r.Cmd("XADD", ntable.ChangesKey("t1"), "*", "verb", "transient")
		r.Cmd("XDEL", ntable.ChangesKey("t1"), added)
		streamAfter, err := capture.ScanImage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if SameBytes(streamBefore, streamAfter[ntable.ChangesKey("t1")]) {
			t.Fatal("stream metadata change was invisible")
		}
		r.Cmd("SADD", "foreign:set", "second")
		after, err := capture.Capture(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if SameImage(image, after.Image) {
			t.Fatal("complete image missed unrelated set change")
		}
		r.Cmd("SET", "foreign:bytes", string([]byte{0xff}))
		invalidA, err := capture.ScanImage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		r.Cmd("SET", "foreign:bytes", string([]byte{0xfe}))
		invalidB, err := capture.ScanImage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if SameImage(invalidA, invalidB) {
			t.Fatal("distinct invalid UTF-8 values collapsed in complete image")
		}
		if _, err := capture.Capture(ctx, Request{Table: "t1", Epoch: "invalid", OperationID: "op1"}); err == nil {
			t.Fatal("invalid capture epoch silently became zero")
		}
		r.Cmd("HSET", ntable.MemberKey("m1"), "place:foreign", "r1:c1")
		if _, err := capture.Capture(ctx, q); err == nil || !strings.Contains(err.Error(), "foreign placement") {
			t.Fatalf("foreign member placement escaped finite capture: %v", err)
		}
		r.Cmd("HDEL", ntable.MemberKey("m1"), "place:foreign")
		r.Cmd("XGROUP", "CREATE", ntable.ChangesKey("t1"), "hidden-group", "$")
		if _, err := capture.Capture(ctx, q); err == nil || !strings.Contains(err.Error(), "consumer groups") {
			t.Fatalf("change-stream group mutation escaped capture: %v", err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRecord(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	source, err := os.ReadFile(filepath.Join("..", "nsprint", "fn", "lua", "table.lua"))
	if err != nil {
		t.Fatal(err)
	}
	err = tablemodel.WithStore(ctx, tablemodel.ServerOptions{RedisServer: testutil.Program(t), TmpDir: t.TempDir()}, func(r *tablemodel.Store) {
		r.Cmd("FUNCTION", "LOAD", "REPLACE", "#!lua name=batch_capture\n"+string(source))
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
				t.Fatalf("row_add: %v", got)
			}
		}
		if got := call("cell_add", "t1", "r1", "c1", 1, "m1", `{"epoch":"1"}`); !strings.HasPrefix(fmt.Sprint(got), "[OK ") {
			t.Fatalf("cell_add: %v", got)
		}
		if got := call("cell_add", "t1", "r1", "c2", 2, "m3", `{"epoch":"1"}`); !strings.HasPrefix(fmt.Sprint(got), "[OK ") {
			t.Fatalf("guard cell_add: %v", got)
		}
		r.Cmd("HSET", ntable.MemberKey("m1"), "status", "ready")
		r.Cmd("HSET", ntable.MemberKey("m3"), "token", "permit")
		tableRev, err := replyString(r.Cmd("HGET", ntable.RevisionKey("t1"), "n"))
		if err != nil {
			t.Fatal(err)
		}
		memberRev, err := replyString(r.Cmd("HGET", ntable.MemberKey("m1"), "revision"))
		if err != nil {
			t.Fatal(err)
		}
		canonical := []byte(fmt.Sprintf(`{"schema":1,"table":"t1","epoch":"1","expected_table_revision":%q,"operation_id":"op-capture","actor":"w1","members":[{"id":"m1","expect":{"revision":%q}}]}`, tableRev, memberRev))
		q := Request{Table: "t1", Epoch: "1", OperationID: "op-capture", Canonical: canonical}
		capture := RedisCapture{Store: r}
		before, err := capture.Capture(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		base := FiniteBaseline{TableRevision: tableRev, MemberRevision: map[string]string{"m1": memberRev, "m2": "0", "m3": before.Members["m3"].Revision}, Operations: before.Operations, Receipts: before.Receipts}
		initial, err := ProjectFinite(before, base, ProjectionControl{SeenEpoch: "1", Outcome: "initial"})
		if err != nil {
			t.Fatal(err)
		}
		initialText, err := initial.TLA()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(initialText, `"ready"`) || !strings.Contains(initialText, `"permit"`) {
			t.Fatal("finite seed application fields absent from projection")
		}
		decoded, action, err := DecodeRequest(canonical, before, tableRev, base.MemberRevision)
		if err != nil {
			t.Fatal(err)
		}
		got := r.Cmd("FCALL", "ns_table_apply", 1, ntable.DefKey("t1"), "t1", string(canonical))
		if !strings.HasPrefix(fmt.Sprint(got), "[OK [RECEIPT ") {
			t.Fatalf("apply: %v", got)
		}
		receipt, err := DecodeAcceptedReply(got, decoded, before)
		if err != nil {
			t.Fatalf("actual reply decode: %v", err)
		}
		after, err := capture.Capture(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if before.Recorded != nil || after.Recorded == nil || after.Recorded.OperationID != q.OperationID || !SameBytes(after.Recorded.Canonical, canonical) || after.Recorded.ReceiptID == "" || len(after.Recorded.ResultJSON) == 0 || after.Operations != before.Operations+1 || after.Receipts != before.Receipts+1 {
			t.Fatalf("batch record/stream capture failed: before=%+v after=%+v", before, after)
		}
		event := after.LastEvent
		if event == nil || event.Fields["verb"] != "apply" {
			t.Fatal("accepted batch has no independent apply event")
		}
		evidence := AcceptedEvidence{Action: action, Request: decoded, Before: before, Receipt: *receipt, Event: *event, Record: *after.Recorded}
		afterModel, err := ProjectFinite(after, base, ProjectionControl{SeenEpoch: "1", Accepted: []AcceptedEvidence{evidence}, Outcome: "accepted", Attempt: &action, Returned: receipt})
		if err != nil {
			t.Fatalf("accepted physical projection: %v", err)
		}
		streamReply := []any{"OK", []any{"RECEIPT", event.ID, event.Fields["epoch"], event.Fields["rev_before"], event.Fields["rev_after"], event.Fields["outcome"], event.Fields["batch_delta"]}}
		streamReceipt, err := DecodeAcceptedReply(streamReply, decoded, before)
		if err != nil {
			t.Fatalf("independent stream decode: %v", err)
		}
		after.LastReceipt = streamReceipt
		step := Step{Request: decoded, Result: Accepted, Before: before, After: after, Receipt: receipt, BeforeModel: initial, AfterModel: afterModel,
			TableBaseline: tableRev, MemberBaseline: base.MemberRevision, Action: action}
		if err := ValidateStep(step); err != nil {
			t.Fatalf("actual reply/stream/record vs physical image: %v", err)
		}
		corrupt := step
		corrupt.After = cloneSnapshot(corrupt.After)
		corrupt.After.Recorded.BeforeRevision = "0"
		if err := ValidateStep(corrupt); err == nil || !strings.Contains(err.Error(), "durable operation record") {
			t.Fatalf("durable rev_before corruption escaped: %v", err)
		}
		corrupt = step
		corrupt.After = cloneSnapshot(corrupt.After)
		corrupt.After.StreamInfo.MaxDeletedID = "9-0"
		if err := ValidateStep(corrupt); err == nil || !strings.Contains(err.Error(), "hidden stream metadata") {
			t.Fatalf("hidden stream metadata corruption escaped: %v", err)
		}
		out := t.TempDir()
		bundle, err := WriteBundle(filepath.Join("..", "..", "tla"), out, []Step{step})
		if err != nil {
			t.Fatalf("real Apply bundle: %v", err)
		}
		if bundle.SourceSHA256 == "" || bundle.ConfigSHA256 == "" {
			t.Fatal("generated bundle lacks input hashes")
		}
		r.Cmd("HSET", operationHashKey("t1"), "hidden", "changed")
		if _, err := capture.Capture(ctx, q); err == nil || !strings.Contains(err.Error(), "field identity") {
			t.Fatalf("unknown durable operation hash field escaped capture: %v", err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTraceA(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	source, err := os.ReadFile(filepath.Join("..", "nsprint", "fn", "lua", "table.lua"))
	if err != nil {
		t.Fatal(err)
	}
	err = tablemodel.WithStore(ctx, tablemodel.ServerOptions{RedisServer: testutil.Program(t), TmpDir: t.TempDir()}, func(r *tablemodel.Store) {
		r.Cmd("FUNCTION", "LOAD", "REPLACE", "#!lua name=batch_trace\n"+string(source))
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
				t.Fatalf("row add: %v", got)
			}
		}
		for _, seed := range []struct {
			col, id string
			score   int
		}{{"c1", "m1", 1}, {"c2", "m3", 2}} {
			if got := call("cell_add", "t1", "r1", seed.col, seed.score, seed.id, `{"epoch":"1"}`); !strings.HasPrefix(fmt.Sprint(got), "[OK ") {
				t.Fatalf("cell add: %v", got)
			}
		}
		r.Cmd("HSET", ntable.MemberKey("m1"), "status", "ready")
		r.Cmd("HSET", ntable.MemberKey("m3"), "token", "permit")
		var original []byte
		builders := []RequestBuilder{
			func(s Snapshot, _ []AcceptedEvidence) ([]byte, error) {
				original = []byte(fmt.Sprintf(`{"schema":1,"table":"t1","epoch":"1","expected_table_revision":%q,"operation_id":"op-move","actor":"w1","members":[{"id":"m1","expect":{"revision":%q,"place":{"row":"r1","col":"c1"},"fields":{"status":{"equals":"ready"}}},"move":{"row":"r2","col":"c1"},"set":{"status":"done"}},{"id":"m3","expect":{"revision":%q,"fields":{"token":{"equals":"permit"}}}}]}`, s.TableRevision, s.Members["m1"].Revision, s.Members["m3"].Revision))
				return original, nil
			},
			func(Snapshot, []AcceptedEvidence) ([]byte, error) { return append([]byte(nil), original...), nil },
			func(Snapshot, []AcceptedEvidence) ([]byte, error) {
				return append(append([]byte(nil), original...), ' '), nil
			},
		}
		steps, _, err := RunFiniteTrace(ctx, RedisCapture{Store: r}, builders)
		if err != nil {
			t.Fatal(err)
		}
		if len(steps) != 3 || steps[0].Result != Accepted || steps[1].Result != Replayed || steps[2].Result != Refused {
			t.Fatalf("wrong trace outcomes: %+v", steps)
		}
		corrupt := steps[0]
		corrupt.After = cloneSnapshot(corrupt.After)
		corrupt.After.Image["foreign:unrelated"] = []byte("injected")
		if err := ValidateStep(corrupt); err == nil || !strings.Contains(err.Error(), "unrelated key") {
			t.Fatalf("accepted extra foreign write escaped complete-image check: %v", err)
		}
		corrupt = steps[0]
		corrupt.After = cloneSnapshot(corrupt.After)
		corrupt.After.RevisionFields["hidden"] = "changed"
		key := ntable.RevisionKey("t1")
		corrupt.After.Image[key] = append(corrupt.After.Image[key], 0xff)
		if err := ValidateStep(corrupt); err == nil || !strings.Contains(err.Error(), "revision metadata") {
			t.Fatalf("accepted same-key revision corruption escaped: %v", err)
		}
		corrupt = steps[0]
		corrupt.After = cloneSnapshot(corrupt.After)
		if len(corrupt.Before.Events) == 0 {
			t.Fatal("fixture lacks initial stream prefix")
		}
		corrupt.After.Events[0].Fields["verb"] = "rewritten"
		streamKey := ntable.ChangesKey("t1")
		corrupt.After.Image[streamKey] = append(corrupt.After.Image[streamKey], 0xfc)
		if err := ValidateStep(corrupt); err == nil || !strings.Contains(err.Error(), "event prefix") {
			t.Fatalf("accepted rewrite of initial stream event escaped: %v", err)
		}
		corrupt = steps[0]
		corrupt.After = cloneSnapshot(corrupt.After)
		raw := corrupt.After.Events[0].RawFields
		if len(raw) < 4 {
			t.Fatal("fixture lacks ordered stream fields")
		}
		raw[0], raw[1], raw[2], raw[3] = raw[2], raw[3], raw[0], raw[1]
		corrupt.After.Image[streamKey] = append(corrupt.After.Image[streamKey], 0xfa)
		if err := ValidateStep(corrupt); err == nil || !strings.Contains(err.Error(), "event prefix") {
			t.Fatalf("accepted reordering of prior stream fields escaped: %v", err)
		}
		out := t.TempDir()
		if _, err := WriteBundle(filepath.Join("..", "..", "tla"), out, steps); err != nil {
			t.Fatalf("continuous real trace bundle: %v", err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTraceB(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	source, err := os.ReadFile(filepath.Join("..", "nsprint", "fn", "lua", "table.lua"))
	if err != nil {
		t.Fatal(err)
	}
	err = tablemodel.WithStore(ctx, tablemodel.ServerOptions{RedisServer: testutil.Program(t), TmpDir: t.TempDir()}, func(r *tablemodel.Store) {
		r.Cmd("FUNCTION", "LOAD", "REPLACE", "#!lua name=batch_trace_noop\n"+string(source))
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
				t.Fatalf("row add: %v", got)
			}
		}
		for _, seed := range []struct {
			col, id string
			score   int
		}{{"c1", "m1", 1}, {"c2", "m3", 2}} {
			if got := call("cell_add", "t1", "r1", seed.col, seed.score, seed.id, `{"epoch":"1"}`); !strings.HasPrefix(fmt.Sprint(got), "[OK ") {
				t.Fatalf("cell add: %v", got)
			}
		}
		r.Cmd("HSET", ntable.MemberKey("m1"), "status", "ready")
		r.Cmd("HSET", ntable.MemberKey("m3"), "token", "permit")
		builders := []RequestBuilder{
			func(s Snapshot, _ []AcceptedEvidence) ([]byte, error) {
				return []byte(fmt.Sprintf(`{"schema":1,"table":"t1","epoch":"1","expected_table_revision":%q,"operation_id":"op-same","actor":"w1","members":[{"id":"m1","expect":{"place":{"row":"r1","col":"c1"}},"move":{"row":"r1","col":"c1","score":1}}]}`, s.TableRevision)), nil
			},
			func(s Snapshot, _ []AcceptedEvidence) ([]byte, error) {
				return []byte(fmt.Sprintf(`{"schema":1,"table":"t1","epoch":"1","expected_table_revision":%q,"operation_id":"op-remove","actor":"w1","members":[{"id":"m1","expect":{"revision":%q,"place":{"row":"r1","col":"c1"}},"remove":true}]}`, s.TableRevision, s.Members["m1"].Revision)), nil
			},
			func(s Snapshot, _ []AcceptedEvidence) ([]byte, error) {
				return []byte(fmt.Sprintf(`{"schema":1,"table":"t1","epoch":"1","expected_table_revision":%q,"operation_id":"op-unset","actor":"w1","members":[{"id":"m1","expect":{"revision":%q,"fields":{"status":{"equals":"ready"}}},"unset":["status"]}]}`, s.TableRevision, s.Members["m1"].Revision)), nil
			},
		}
		steps, _, err := RunFiniteTrace(ctx, RedisCapture{Store: r}, builders)
		if err != nil {
			t.Fatal(err)
		}
		if len(steps) != 3 || steps[0].Receipt.Kind != "noop" || steps[0].Receipt.ChangedCount != 0 || steps[1].Receipt.Kind != "changed" || steps[2].Receipt.Kind != "changed" || steps[2].After.Members["m1"].Place != nil {
			t.Fatalf("no-op/remove/unset outcomes incorrect")
		}
		if _, exists := steps[2].After.Members["m1"].Fields["status"]; exists {
			t.Fatal("unset left field present")
		}
		out := t.TempDir()
		if _, err := WriteBundle(filepath.Join("..", "..", "tla"), out, steps); err != nil {
			t.Fatalf("continuous no-op/remove/unset bundle: %v", err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTraceC(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	source, err := os.ReadFile(filepath.Join("..", "nsprint", "fn", "lua", "table.lua"))
	if err != nil {
		t.Fatal(err)
	}
	err = tablemodel.WithStore(ctx, tablemodel.ServerOptions{RedisServer: testutil.Program(t), TmpDir: t.TempDir()}, func(r *tablemodel.Store) {
		seedFiniteRuntime(t, r, source, "batch_trace_create")
		var initialRev string
		builders := []RequestBuilder{
			func(s Snapshot, _ []AcceptedEvidence) ([]byte, error) {
				initialRev = s.TableRevision
				return []byte(fmt.Sprintf(`{"schema":1,"table":"t1","epoch":"1","expected_table_revision":%q,"operation_id":"op-create","actor":"w1","members":[{"id":"m2","expect":{"absent":true},"create":{"row":"r2","col":"c2","score":1},"set":{"status":"new"}},{"id":"m3","expect":{"revision":%q,"fields":{"token":{"equals":"permit"}}}}]}`, s.TableRevision, s.Members["m3"].Revision)), nil
			},
			func(_ Snapshot, _ []AcceptedEvidence) ([]byte, error) {
				return []byte(fmt.Sprintf(`{"schema":1,"table":"t1","epoch":"1","expected_table_revision":%q,"operation_id":"op-stale","actor":"w1","members":[{"id":"m3","expect":{"fields":{"token":{"equals":"permit"}}}}]}`, initialRev)), nil
			},
			func(s Snapshot, _ []AcceptedEvidence) ([]byte, error) {
				return []byte(fmt.Sprintf(`{"schema":1,"table":"t1","epoch":"1","expected_table_revision":%q,"operation_id":"op-guard-fail","actor":"w1","members":[{"id":"m3","expect":{"fields":{"token":{"equals":"deny"}}}}]}`, s.TableRevision)), nil
			},
		}
		steps, _, err := RunFiniteTrace(ctx, RedisCapture{Store: r}, builders)
		if err != nil {
			t.Fatal(err)
		}
		if len(steps) != 3 || steps[0].Result != Accepted || steps[1].Result != Refused || steps[2].Result != Refused || !steps[0].After.Members["m2"].Exists {
			t.Fatalf("create/refusal trace outcomes incorrect")
		}
		if !SameImage(steps[0].After.Image, steps[1].After.Image) || !SameImage(steps[1].After.Image, steps[2].After.Image) {
			t.Fatal("preventive refusal changed complete store")
		}
		out := t.TempDir()
		if _, err := WriteBundle(filepath.Join("..", "..", "tla"), out, steps); err != nil {
			t.Fatalf("create/refusal bundle: %v", err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

func seedFiniteRuntime(t *testing.T, r *tablemodel.Store, source []byte, name string) {
	t.Helper()
	r.Cmd("FUNCTION", "LOAD", "REPLACE", "#!lua name="+name+"\n"+string(source))
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
			t.Fatalf("row add: %v", got)
		}
	}
	for _, seed := range []struct {
		col, id string
		score   int
	}{{"c1", "m1", 1}, {"c2", "m3", 2}} {
		if got := call("cell_add", "t1", "r1", seed.col, seed.score, seed.id, `{"epoch":"1"}`); !strings.HasPrefix(fmt.Sprint(got), "[OK ") {
			t.Fatalf("cell add: %v", got)
		}
	}
	r.Cmd("HSET", ntable.MemberKey("m1"), "status", "ready")
	r.Cmd("HSET", ntable.MemberKey("m3"), "token", "permit")
}
