package tset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func receiptStep(epoch Decimal, op, intent, result string, entries ...Entry) Step {
	return Step{Epoch: epoch, Space: "receipt:", Op: &op, Intent: &intent,
		Result: result, Entries: append([]Entry{}, entries...)}
}

func receiptMem(t *testing.T) *Mem {
	t.Helper()
	m := NewMem()
	if err := m.DefineTable("receipt:", "work", TableDefinition{
		Columns: []string{"cards"}, MemberPrefix: "receipt:member:",
		EpochKey: "receipt:sprint:epoch", EpochField: "n",
	}); err != nil {
		t.Fatal(err)
	}
	return m
}

func requireReceiptCode(t *testing.T, err error, code string) {
	t.Helper()
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != code {
		t.Fatalf("wanted %s refusal, got %v", code, err)
	}
}

func TestAdvanceRows1024AndReplayAcrossTwoAdvances(t *testing.T) {
	t.Parallel()
	m := receiptMem(t)
	rows := make([]string, 1024)
	for i := range rows {
		rows[i] = fmt.Sprintf("row%04d", i)
	}
	first := receiptStep("0", "clear:0", "stable clear identity", "saved caller result",
		Entry{Kind: "advance", AdvanceFrom: "0"},
		Entry{Kind: "rows", Table: "work", Add: rows})
	reply, err := m.Step(context.Background(), first)
	if err != nil || reply.EpochAfter != "1" {
		t.Fatalf("advance with 1024 restored rows: reply=%+v err=%v", reply, err)
	}
	snapshot, err := m.Snapshot("receipt:")
	if err != nil {
		t.Fatal(err)
	}
	if got := len(snapshot.Epochs["1"].Tables["work"].Rows); got != 1024 {
		t.Fatalf("restored rows = %d, want 1024", got)
	}
	second := receiptStep("1", "clear:1", "second stable clear", "second result",
		Entry{Kind: "advance", AdvanceFrom: "1"})
	if _, err := m.Step(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	replanned := receiptStep("0", "clear:0", "stable clear identity", "different estimate")
	replay, err := m.Step(context.Background(), replanned)
	if err != nil || !replay.Replay || replay.EpochBefore != "0" ||
		replay.EpochAfter != "1" || replay.Result != "saved caller result" {
		t.Fatalf("original receipt after two advances: reply=%+v err=%v", replay, err)
	}
	snapshot, err = m.Snapshot("receipt:")
	if err != nil {
		t.Fatal(err)
	}
	if got := len(snapshot.Epochs["1"].Tables["work"].Rows); got != 1024 {
		t.Fatalf("historical restored rows changed on replay: %d", got)
	}
	tooMany := make([]string, 1025)
	copy(tooMany, rows)
	tooMany[1024] = "row1024"
	_, err = m.Step(context.Background(), receiptStep("2", "clear:2-too-many", "oversized restoration", "",
		Entry{Kind: "advance", AdvanceFrom: "2"},
		Entry{Kind: "rows", Table: "work", Add: tooMany}))
	requireReceiptCode(t, err, "LIMIT")
	afterLimit, err := m.Snapshot("receipt:")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot, afterLimit) {
		t.Fatal("over-limit restoration changed state")
	}
	newStale := receiptStep("0", "new stale operation", "fresh intent", "")
	_, err = m.Step(context.Background(), newStale)
	requireReceiptCode(t, err, "STALE")
	conflict := receiptStep("0", "clear:0", "changed semantic argument", "")
	_, err = m.Step(context.Background(), conflict)
	requireReceiptCode(t, err, "OPCONFLICT")
}

func TestDoneBatchMixedOriginalEpochs(t *testing.T) {
	t.Parallel()
	m := receiptMem(t)
	first := receiptStep("0", "part:0", "same semantic part zero", "result zero",
		Entry{Kind: "advance", AdvanceFrom: "0"})
	if _, err := m.Step(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	second := receiptStep("1", "part:1", "same semantic part one", "result one",
		Entry{Kind: "advance", AdvanceFrom: "1"})
	if _, err := m.Step(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	identities := []DoneIdentity{
		{Epoch: "1", Op: "part:1", IntentDigest: intentDigest("same semantic part one")},
		{Epoch: "0", Op: "part:0", IntentDigest: intentDigest("same semantic part zero")},
		{Epoch: "0", Op: "missing", IntentDigest: intentDigest("missing")},
		{Epoch: "1", Op: "part:1", IntentDigest: intentDigest("different")},
	}
	read, err := m.Read(context.Background(), ReadPlan{Epoch: "2", Space: "receipt:",
		Queries: []ReadQuery{{Kind: "done", Ops: identities}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Answers) != 1 || len(read.Answers[0].Done) != 4 {
		t.Fatalf("done alignment: %+v", read.Answers)
	}
	slots := read.Answers[0].Done
	if slots[0].Status != "match" || slots[0].Receipt.Result != "result one" ||
		slots[0].IntentDigest != intentDigest("same semantic part one") ||
		slots[1].Status != "match" || slots[1].Receipt.Result != "result zero" ||
		slots[1].IntentDigest != intentDigest("same semantic part zero") ||
		slots[2].Status != "absent" || slots[3].Status != "conflict" ||
		slots[3].IntentDigest != intentDigest("same semantic part one") {
		t.Fatalf("mixed epoch slots: %+v", slots)
	}
	if slots[0].Receipt.EpochAfter != "2" || slots[1].Receipt.EpochAfter != "1" {
		t.Fatalf("wrong original advance results: %+v", slots)
	}
}

func TestReceiptSizeAndCompactReplay(t *testing.T) {
	t.Parallel()
	m := receiptMem(t)
	// Every control byte expands to six JSON bytes, exercising a receipt near
	// its encoded cap while staying within the legal 4 KiB caller result.
	result := strings.Repeat("\x01", MaxResultBytes)
	step := receiptStep("0", "max-result", "intent", result)
	fresh, err := m.Step(context.Background(), step)
	if err != nil {
		t.Fatal(err)
	}
	var work struct {
		PlannedCommands int `json:"planned_commands"`
		PlannedBytes    int `json:"planned_argv_bytes"`
	}
	if err := json.Unmarshal(fresh.Counters, &work); err != nil {
		t.Fatal(err)
	}
	stored := encodeMemReceipt(m.space("receipt:").receipts["0"]["max-result"])
	wantBytes := len("HSET") + len("receipt:sprint:done@0") + len("max-result") + len(stored)
	if work.PlannedCommands != 1 || work.PlannedBytes != wantBytes {
		t.Fatalf("receipt HSET budget = %d commands/%d bytes, want 1/%d",
			work.PlannedCommands, work.PlannedBytes, wantBytes)
	}
	if len(stored) > maxReceiptBytes {
		t.Fatalf("persisted receipt is %d bytes, cap %d", len(stored), maxReceiptBytes)
	}
	read, err := m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "receipt:",
		Queries: []ReadQuery{{Kind: "done", Ops: []DoneIdentity{{Epoch: "0", Op: "max-result",
			IntentDigest: intentDigest("intent")}}}}})
	if err != nil || len(read.Answers) != 1 || len(read.Answers[0].Done) != 1 ||
		read.Answers[0].Done[0].Receipt == nil || read.Answers[0].Done[0].Receipt.Result != result {
		t.Fatalf("read back maximum encoded receipt: reply=%+v err=%v", read, err)
	}
	var readWork struct {
		FetchedBytes int `json:"fetched_bytes"`
	}
	if err := json.Unmarshal(read.Counters, &readWork); err != nil {
		t.Fatal(err)
	}
	if readWork.FetchedBytes < len(stored) {
		t.Fatalf("done read charged %d raw bytes for %d-byte receipt", readWork.FetchedBytes, len(stored))
	}
	replay, err := m.Step(context.Background(), step)
	if err != nil || !replay.Replay || replay.Result != result {
		t.Fatalf("maximum result replay: reply=%+v err=%v", replay, err)
	}
	encoded, err := replay.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"guarded", "changed_per_entry", "lines", "counters"} {
		if strings.Contains(string(encoded), `"`+forbidden+`"`) {
			t.Fatalf("compact replay invented %s: %s", forbidden, encoded)
		}
	}
	space := m.space("receipt:")
	boundary := receiptStep("0", "at-cap", "intent", "")
	base := memReceipt{IntentDigest: intentDigest("intent"), Status: "ok",
		EpochBefore: "0", EpochAfter: "0", FirstSeq: "0", LastSeq: "0"}
	baseWire := encodeMemReceipt(base)
	exactResult := strings.Repeat("x", maxReceiptBytes-len(baseWire))
	err = m.saveReceipt(space, boundary, Reply{Status: "ok", EpochBefore: "0", EpochAfter: "0",
		FirstSeq: "0", LastSeq: "0", Result: exactResult})
	if err != nil {
		t.Fatalf("exact 32 KiB internal receipt refused: %v", err)
	}
	exactWire := encodeMemReceipt(space.receipts["0"]["at-cap"])
	if len(exactWire) != maxReceiptBytes {
		t.Fatalf("exact receipt bytes=%d, want %d", len(exactWire), maxReceiptBytes)
	}
	tooLarge := receiptStep("0", "oversized-receipt", "intent", "")
	err = m.saveReceipt(space, tooLarge, Reply{Status: "ok", EpochBefore: "0", EpochAfter: "0",
		FirstSeq: "0", LastSeq: "0", Result: strings.Repeat("x", maxReceiptBytes)})
	requireReceiptCode(t, err, "LIMIT")
	if _, ok := space.receipts["0"]["oversized-receipt"]; ok {
		t.Fatal("oversized receipt was stored")
	}
}

func TestReceiptReplayChecksEngineBeforeLookup(t *testing.T) {
	t.Parallel()
	m := receiptMem(t)
	original := receiptStep("0", "engine-replay", "stable intent", "recorded result")
	if _, err := m.Step(context.Background(), original); err != nil {
		t.Fatal(err)
	}
	if err := m.SetEngine("receipt:", "legacy"); err != nil {
		t.Fatal(err)
	}
	before, err := m.Snapshot("receipt:")
	if err != nil {
		t.Fatal(err)
	}
	// This request would match the saved identity. Its new advance plan and
	// result are deliberately different; neither may run or mask ENGINE.
	replanned := receiptStep("0", "engine-replay", "stable intent", "new estimate",
		Entry{Kind: "advance", AdvanceFrom: "0"})
	_, err = m.Step(context.Background(), replanned)
	requireReceiptCode(t, err, "ENGINE")
	after, err := m.Snapshot("receipt:")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || m.spaces["receipt:"].engine != "legacy" {
		t.Fatal("ENGINE refusal changed the receipt, table, epoch, or engine state")
	}
	if err := m.SetEngine("receipt:", Version); err != nil {
		t.Fatal(err)
	}
	replay, err := m.Step(context.Background(), replanned)
	if err != nil || !replay.Replay || replay.Result != "recorded result" {
		t.Fatalf("saved identity did not replay after engine repair: reply=%+v err=%v", replay, err)
	}
}

// lostReplyFake commits the step, then hides the successful reply from its
// caller. Recovery must use the original part identity and done query.
type lostReplyFake struct {
	mem  *Mem
	lost bool
}

func (f *lostReplyFake) Step(ctx context.Context, step Step) (Reply, error) {
	reply, err := f.mem.Step(ctx, step)
	if err != nil {
		return Reply{}, err
	}
	if !f.lost {
		f.lost = true
		return Reply{}, ErrOutcomeUnknown
	}
	return reply, nil
}

func TestUnknownOutcomeRecoveredByOriginalDoneIdentity(t *testing.T) {
	t.Parallel()
	m := receiptMem(t)
	fake := &lostReplyFake{mem: m}
	part := receiptStep("0", "batch/part-0007", "fixed part number and arguments", "recorded",
		Entry{Kind: "advance", AdvanceFrom: "0"})
	_, err := fake.Step(context.Background(), part)
	if !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("lost reply classified as %v", err)
	}
	read, err := m.Read(context.Background(), ReadPlan{Epoch: "1", Space: "receipt:",
		Queries: []ReadQuery{{Kind: "done", Ops: []DoneIdentity{{Epoch: "0", Op: "batch/part-0007",
			IntentDigest: intentDigest("fixed part number and arguments")}}}}})
	if err != nil {
		t.Fatal(err)
	}
	slot := read.Answers[0].Done[0]
	if slot.Status != "match" || slot.Receipt == nil || slot.Receipt.Result != "recorded" {
		t.Fatalf("lost reply was not recoverable: %+v", slot)
	}
	before, err := m.Snapshot("receipt:")
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := fake.Step(context.Background(), receiptStep("0", "batch/part-0007",
		"fixed part number and arguments", "new estimate"))
	if err != nil || !replayed.Replay || replayed.Result != "recorded" {
		t.Fatalf("same part retry: reply=%+v err=%v", replayed, err)
	}
	after, err := m.Snapshot("receipt:")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("retry changed table or epoch state")
	}
}
