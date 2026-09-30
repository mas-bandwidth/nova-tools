package tset

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestMemReadMissingHistoricalEpochReportsActive(t *testing.T) {
	t.Parallel()
	m := readFixture(t)
	if err := m.SetActiveEpoch("read:", "1"); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	delete(m.spaces["read:"].epochs, "0")
	m.mu.Unlock()
	before, err := m.Snapshot("read:")
	if err != nil {
		t.Fatal(err)
	}
	reply, readErr := m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "read:", Queries: []ReadQuery{{Kind: "rows", Table: "work"}}})
	var refusal *Refusal
	if !errors.As(readErr, &refusal) || refusal.Code != "EPOCHGONE" || refusal.Detail.ActiveEpoch != "1" || refusal.Detail.QueryIndex != nil {
		t.Fatalf("missing epoch 0 at active 1: reply=%+v err=%v", reply, readErr)
	}
	if reply.Status != "" || len(reply.Answers) != 0 {
		t.Fatalf("missing epoch returned answers: %+v", reply)
	}
	after, err := m.Snapshot("read:")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("read changed state: err=%v", err)
	}
}

func TestMemReadMissingHistoricalTableReportsActive(t *testing.T) {
	t.Parallel()
	m := readFixture(t)
	if err := m.SetActiveEpoch("read:", "1"); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	delete(m.spaces["read:"].epochs["0"].tables, "work")
	m.mu.Unlock()
	before, err := m.Snapshot("read:")
	if err != nil {
		t.Fatal(err)
	}
	reply, readErr := m.Read(context.Background(), ReadPlan{Epoch: "0", Space: "read:", Queries: []ReadQuery{{Kind: "rows", Table: "work"}}})
	var refusal *Refusal
	if !errors.As(readErr, &refusal) || refusal.Code != "EPOCHGONE" || refusal.Detail.ActiveEpoch != "1" ||
		refusal.Detail.Table != "work" || refusal.Detail.QueryIndex == nil || *refusal.Detail.QueryIndex != 0 {
		t.Fatalf("missing table snapshot at epoch 0, active 1: reply=%+v err=%v", reply, readErr)
	}
	if reply.Status != "" || len(reply.Answers) != 0 {
		t.Fatalf("missing table returned answers: %+v", reply)
	}
	after, err := m.Snapshot("read:")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("read changed state: err=%v", err)
	}
}
