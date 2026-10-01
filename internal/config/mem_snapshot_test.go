package config

import (
	"context"
	"strings"
	"testing"
)

func TestMemSnapshotAndRestoreRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	m := NewMem()
	machine, _ := Lookup(KindMachine)
	row, err := machine.NewRow("m1", map[string]string{
		"user":    "glenn",
		"seat":    "seat-a",
		"slots":   "2",
		"runners": "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Insert(ctx, KindMachine, row, "boss"); err != nil {
		t.Fatal(err)
	}

	friend, _ := Lookup(KindFriend)
	fRow, err := friend.NewRow("f1", map[string]string{
		"slots": "1",
		"tiers": "flash,pro",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Insert(ctx, KindFriend, fRow, "boss"); err != nil {
		t.Fatal(err)
	}

	doc, err := m.Snapshot(5)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	m2 := NewMem()
	schema, err := m2.Restore(doc)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if schema != 5 {
		t.Fatalf("schema = %d, want 5", schema)
	}

	gotM, ok, err := m2.Get(ctx, KindMachine, "m1")
	if err != nil || !ok {
		t.Fatalf("get m1: ok=%v, err=%v", ok, err)
	}
	if gotM.Fields["user"] != "glenn" || gotM.Fields["seat"] != "seat-a" || gotM.Fields["slots"] != "2" || gotM.Fields["runners"] != "1" {
		t.Fatalf("m1 fields mismatch: %v", gotM.Fields)
	}

	gotF, ok, err := m2.Get(ctx, KindFriend, "f1")
	if err != nil || !ok {
		t.Fatalf("get f1: ok=%v, err=%v", ok, err)
	}
	if gotF.Fields["slots"] != "1" || gotF.Fields["tiers"] != "flash,pro" {
		t.Fatalf("f1 fields mismatch: %v", gotF.Fields)
	}

	hist, err := m2.History(ctx, KindMachine, "m1")
	if err != nil || len(hist) != 1 {
		t.Fatalf("history m1: len=%d, err=%v", len(hist), err)
	}
	if hist[0].Actor != "boss" || hist[0].Op != OpAdd {
		t.Fatalf("history entry mismatch: %+v", hist[0])
	}
}

func TestMemRestoreRejections(t *testing.T) {
	t.Parallel()
	m := NewMem()

	// Not a twin snapshot (invalid JSON)
	if _, err := m.Restore([]byte("{invalid")); err == nil || !strings.Contains(err.Error(), "not a twin snapshot") {
		t.Fatalf("want not a twin snapshot error, got: %v", err)
	}

	// Unknown field rejected
	if _, err := m.Restore([]byte(`{"version":1,"schema":5,"rows":{},"unknown":123}`)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("want unknown field error, got: %v", err)
	}

	// Version mismatch
	if _, err := m.Restore([]byte(`{"version":99,"schema":5,"rows":{}}`)); err == nil || !strings.Contains(err.Error(), "a twin snapshot of version 99; this build reads version 1") {
		t.Fatalf("want version mismatch error, got: %v", err)
	}
}
