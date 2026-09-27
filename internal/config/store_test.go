package config

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// storeTests is the contract every Store keeps, run against Mem here and
// against Postgres in pg_functional_test.go, so the fake is held to the real
// one's refusals (docs/CONTRIBUTING.md: a fake is strict like the real tool).
func storeTests(t *testing.T, open func(t *testing.T) Store) {
	t.Helper()
	ctx := context.Background()
	friend, _ := Lookup(KindFriend)
	machine, _ := Lookup(KindMachine)
	mk := func(k *Kind, name string, raw map[string]string) Row {
		t.Helper()
		row, err := k.NewRow(name, raw)
		if err != nil {
			t.Fatal(err)
		}
		return row
	}

	t.Run("add, get, list, set, history, remove", func(t *testing.T) {
		t.Parallel()
		st := open(t)
		id, err := st.Insert(ctx, KindMachine, mk(machine, "studio", map[string]string{"ssh": "studio", "os_arch": "darwin/arm64", "slots": "64", "roles": "bench,coordination"}), "rowan")
		if err != nil || id != 1 {
			t.Fatalf("add machine: id %d err %v", id, err)
		}
		id, err = st.Insert(ctx, KindFriend, mk(friend, "rowan", map[string]string{"machine": "studio", "slots": "32", "roles": "coordinator", "logins": "rowan-claude"}), "rowan")
		if err != nil || id != 2 {
			t.Fatalf("add friend: id %d err %v", id, err)
		}
		row, found, err := st.Get(ctx, KindFriend, "rowan")
		if err != nil || !found || row.Fields["slots"] != "32" || row.Fields["roles"] != "coordinator" || row.CreatedAt == "" || row.UpdatedAt == "" {
			t.Fatalf("get: %+v %v %v", row, found, err)
		}
		if _, found, err := st.Get(ctx, KindFriend, "nobody"); err != nil || found {
			t.Fatalf("get nobody: %v %v", found, err)
		}
		rows, err := st.List(ctx, KindFriend)
		if err != nil || len(rows) != 1 || rows[0].Name != "rowan" {
			t.Fatalf("list: %+v %v", rows, err)
		}
		after, id, err := st.Update(ctx, KindFriend, "rowan", map[string]string{"slots": "64", "note": "wider"}, "stella")
		if err != nil || id != 3 || after.Fields["slots"] != "64" || after.Fields["note"] != "wider" || after.Fields["roles"] != "coordinator" {
			t.Fatalf("set: %+v id %d err %v", after, id, err)
		}
		rev, err := st.Rev(ctx, KindFriend)
		if err != nil || rev != 3 {
			t.Fatalf("rev friend: %d %v", rev, err)
		}
		rev, err = st.Rev(ctx, KindMachine)
		if err != nil || rev != 1 {
			t.Fatalf("rev machine: %d %v", rev, err)
		}
		if rev, err := st.Rev(ctx, "nothing"); err != nil || rev != 0 {
			t.Fatalf("rev of an unknown kind: %d %v", rev, err)
		}
		counts, err := st.Counts(ctx)
		if err != nil || counts[KindFriend] != 1 || counts[KindMachine] != 1 {
			t.Fatalf("counts %v %v", counts, err)
		}
		id, err = st.Delete(ctx, KindFriend, "rowan", "rowan")
		if err != nil || id != 4 {
			t.Fatalf("remove: id %d err %v", id, err)
		}
		if _, found, _ := st.Get(ctx, KindFriend, "rowan"); found {
			t.Fatal("removed row is still there")
		}
		hist, err := st.History(ctx, KindFriend, "rowan")
		if err != nil || len(hist) != 3 {
			t.Fatalf("history: %+v %v", hist, err)
		}
		if hist[0].Op != OpAdd || hist[0].Before != nil || hist[0].After["slots"] != "32" || hist[0].Actor != "rowan" || hist[0].At == "" || hist[0].ID != 2 {
			t.Errorf("history add row %+v", hist[0])
		}
		if hist[1].Op != OpSet || hist[1].Before["slots"] != "32" || hist[1].After["slots"] != "64" || hist[1].After["note"] != "wider" || hist[1].Actor != "stella" {
			t.Errorf("history set row %+v", hist[1])
		}
		if hist[2].Op != OpRemove || hist[2].Before["slots"] != "64" || hist[2].After != nil {
			t.Errorf("history remove row %+v", hist[2])
		}
		if hist, err := st.History(ctx, KindFriend, "nobody"); err != nil || len(hist) != 0 {
			t.Errorf("history of nobody: %v %v", hist, err)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		t.Parallel()
		st := open(t)
		refusal := func(err error, want error, detail string) {
			t.Helper()
			if err == nil {
				t.Fatalf("accepted; want a refusal %v saying %q", want, detail)
			}
			if !Refused(err) || !errors.Is(err, want) {
				t.Fatalf("error %v is not the refusal %v", err, want)
			}
			if !strings.Contains(err.Error(), detail) {
				t.Fatalf("refusal %q does not say %q", err, detail)
			}
		}
		_, err := st.Insert(ctx, KindFriend, mk(friend, "rowan", map[string]string{"machine": "studio", "slots": "1"}), "rowan")
		refusal(err, ErrNoRef, "--machine studio names no machine row")
		if _, err := st.Insert(ctx, KindMachine, mk(machine, "studio", map[string]string{"ssh": "studio", "os_arch": "darwin/arm64", "slots": "64"}), "rowan"); err != nil {
			t.Fatal(err)
		}
		_, err = st.Insert(ctx, KindMachine, mk(machine, "studio", map[string]string{"ssh": "studio", "os_arch": "darwin/arm64", "slots": "64"}), "rowan")
		refusal(err, ErrExists, "machine studio exists")
		if _, err := st.Insert(ctx, KindFriend, mk(friend, "rowan", map[string]string{"machine": "studio", "slots": "1", "logins": "rowan-claude"}), "rowan"); err != nil {
			t.Fatal(err)
		}
		_, err = st.Insert(ctx, KindFriend, mk(friend, "stella", map[string]string{"machine": "studio", "slots": "1", "logins": "rowan-claude"}), "rowan")
		refusal(err, ErrLoginTaken, "--logins rowan-claude is friend rowan's login")
		_, err = st.Insert(ctx, KindFriend, mk(friend, "stella", map[string]string{"machine": "studio", "slots": "1", "logins": "rowan"}), "rowan")
		refusal(err, ErrLoginTaken, "--logins rowan is a friend's name")
		_, _, err = st.Update(ctx, KindFriend, "nobody", map[string]string{"slots": "2"}, "rowan")
		refusal(err, ErrNotFound, "friend nobody not found")
		_, _, err = st.Update(ctx, KindFriend, "rowan", map[string]string{"machine": "hulk"}, "rowan")
		refusal(err, ErrNoRef, "--machine hulk names no machine row")
		_, err = st.Delete(ctx, KindMachine, "studio", "rowan")
		refusal(err, ErrReferenced, "machine studio is the --machine of friend rowan")
		_, err = st.Delete(ctx, KindFriend, "nobody", "rowan")
		refusal(err, ErrNotFound, "friend nobody not found")
		// A refused write leaves no history and moves no revision.
		if rev, _ := st.Rev(ctx, KindFriend); rev != 2 {
			t.Fatalf("rev after refusals %d, want 2", rev)
		}
		if hist, _ := st.History(ctx, KindFriend, "stella"); len(hist) != 0 {
			t.Fatalf("a refused add left history: %+v", hist)
		}
		// The same login on the same friend is fine on a set.
		if _, _, err := st.Update(ctx, KindFriend, "rowan", map[string]string{"logins": "rowan-claude,rowan-bot"}, "rowan"); err != nil {
			t.Fatal(err)
		}
	})
}

func TestMemStoreKeepsTheContract(t *testing.T) {
	t.Parallel()
	storeTests(t, func(t *testing.T) Store { return NewMem() })
}

func TestMemStoreHandsOutCopies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()
	machine, _ := Lookup(KindMachine)
	row, _ := machine.NewRow("studio", map[string]string{"ssh": "studio", "os_arch": "darwin/arm64", "slots": "64"})
	if _, err := st.Insert(ctx, KindMachine, row, "rowan"); err != nil {
		t.Fatal(err)
	}
	row.Fields["slots"] = "1"
	got, _, _ := st.Get(ctx, KindMachine, "studio")
	if got.Fields["slots"] != "64" {
		t.Fatal("the store shares its row with the caller")
	}
	got.Fields["slots"] = "2"
	again, _, _ := st.Get(ctx, KindMachine, "studio")
	if again.Fields["slots"] != "64" {
		t.Fatal("a row read from the store is the store's own")
	}
}
