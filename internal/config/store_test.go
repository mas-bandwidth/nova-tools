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
		id, err := st.Insert(ctx, KindMachine, mk(machine, "studio", map[string]string{"user": "glenn", "seat": "studio", "slots": "64"}), "rowan")
		if err != nil || id != 1 {
			t.Fatalf("add machine: id %d err %v", id, err)
		}
		id, err = st.Insert(ctx, KindFriend, mk(friend, "rowan", map[string]string{"slots": "32", "tiers": "frontier,pro", "roles": "builder"}), "rowan")
		if err != nil || id != 2 {
			t.Fatalf("add friend: id %d err %v", id, err)
		}
		row, found, err := st.Get(ctx, KindFriend, "rowan")
		if err != nil || !found || row.Fields["slots"] != "32" || row.Fields["tiers"] != "frontier,pro" || row.Fields["roles"] != "builder" || row.CreatedAt == "" || row.UpdatedAt == "" {
			t.Fatalf("get: %+v %v %v", row, found, err)
		}
		if _, found, err := st.Get(ctx, KindFriend, "nobody"); err != nil || found {
			t.Fatalf("get nobody: %v %v", found, err)
		}
		rows, err := st.List(ctx, KindFriend)
		if err != nil || len(rows) != 1 || rows[0].Name != "rowan" {
			t.Fatalf("list: %+v %v", rows, err)
		}
		after, id, err := st.Update(ctx, KindFriend, "rowan", map[string]string{"slots": "64", "roles": "builder,reader"}, "stella")
		if err != nil || id != 3 || after.Fields["slots"] != "64" || after.Fields["roles"] != "builder,reader" || after.Fields["tiers"] != "frontier,pro" {
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
		for _, one := range []string{KindFleet, KindSprint} {
			if _, counted := counts[one]; counted {
				t.Fatalf("counts %v: %s is one row and is not counted", counts, one)
			}
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
		if hist[1].Op != OpSet || hist[1].Before["slots"] != "32" || hist[1].After["slots"] != "64" || hist[1].After["roles"] != "builder,reader" || hist[1].Actor != "stella" {
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
		if _, err := st.Insert(ctx, KindMachine, mk(machine, "studio", map[string]string{"user": "glenn", "seat": "studio", "slots": "64"}), "rowan"); err != nil {
			t.Fatal(err)
		}
		_, err := st.Insert(ctx, KindMachine, mk(machine, "studio", map[string]string{"user": "glenn", "seat": "studio", "slots": "64"}), "rowan")
		refusal(err, ErrExists, "machine studio exists")
		if _, err := st.Insert(ctx, KindFriend, mk(friend, "rowan", map[string]string{"slots": "1", "tiers": "frontier"}), "rowan"); err != nil {
			t.Fatal(err)
		}
		_, err = st.Insert(ctx, KindFriend, mk(friend, "rowan", map[string]string{"slots": "1", "tiers": "frontier"}), "rowan")
		refusal(err, ErrExists, "friend rowan exists")
		_, _, err = st.Update(ctx, KindFriend, "nobody", map[string]string{"slots": "2"}, "rowan")
		refusal(err, ErrNotFound, "friend nobody not found")
		_, _, err = st.Update(ctx, KindSprint, KindSprint, map[string]string{"coordinator": "nobody"}, "rowan")
		refusal(err, ErrNoRef, "--coordinator nobody names no friend row")
		if _, _, err := st.Update(ctx, KindSprint, KindSprint, map[string]string{"coordinator": "rowan"}, "rowan"); err != nil {
			t.Fatal(err)
		}
		_, err = st.Delete(ctx, KindFriend, "rowan", "rowan")
		refusal(err, ErrReferenced, "friend rowan is the --coordinator of the sprint")
		_, err = st.Delete(ctx, KindFriend, "nobody", "rowan")
		refusal(err, ErrNotFound, "friend nobody not found")
		// A refused write leaves no history and moves no revision.
		if rev, _ := st.Rev(ctx, KindFriend); rev != 2 {
			t.Fatalf("rev after refusals %d, want 2", rev)
		}
		if hist, _ := st.History(ctx, KindFriend, "nobody"); len(hist) != 0 {
			t.Fatalf("a refused write left history: %+v", hist)
		}
		// The handover frees the old coordinator's row.
		if _, _, err := st.Update(ctx, KindSprint, KindSprint, map[string]string{"coordinator": ""}, "rowan"); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Delete(ctx, KindFriend, "rowan", "rowan"); err != nil {
			t.Fatalf("remove the freed friend: %v", err)
		}
	})

	t.Run("the fleet row", func(t *testing.T) {
		t.Parallel()
		st := open(t)
		// The row is there before anything is set, both fields empty, and
		// has no history yet: migrate made it, nobody added it.
		row, found, err := st.Get(ctx, KindFleet, KindFleet)
		if err != nil || !found || row.Fields["store"] != "" || row.Fields["coordinator"] != "" || row.CreatedAt == "" {
			t.Fatalf("fresh fleet row: %+v %v %v", row, found, err)
		}
		if hist, err := st.History(ctx, KindFleet, KindFleet); err != nil || len(hist) != 0 {
			t.Fatalf("fresh fleet history %v %v", hist, err)
		}
		if rev, err := st.Rev(ctx, KindFleet); err != nil || rev != 0 {
			t.Fatalf("fresh fleet rev %d %v", rev, err)
		}
		rows, err := st.List(ctx, KindFleet)
		if err != nil || len(rows) != 1 || rows[0].Name != KindFleet {
			t.Fatalf("list fleet: %+v %v", rows, err)
		}
		// A store or coordinator must be a machine row.
		_, _, err = st.Update(ctx, KindFleet, KindFleet, map[string]string{"store": "space"}, "rowan")
		if err == nil || !errors.Is(err, ErrNoRef) || !strings.Contains(err.Error(), "--store space names no machine row") {
			t.Fatalf("store naming no machine: %v", err)
		}
		if _, err := st.Insert(ctx, KindMachine, mk(machine, "space", map[string]string{"user": "nova", "seat": "space", "slots": "0"}), "rowan"); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Insert(ctx, KindMachine, mk(machine, "studio", map[string]string{"user": "glenn", "seat": "studio", "slots": "64"}), "rowan"); err != nil {
			t.Fatal(err)
		}
		after, id, err := st.Update(ctx, KindFleet, KindFleet, map[string]string{"store": "space", "coordinator": "studio"}, "rowan")
		if err != nil || id != 3 || after.Fields["store"] != "space" || after.Fields["coordinator"] != "studio" {
			t.Fatalf("set the fleet: %+v id %d err %v", after.Fields, id, err)
		}
		if rev, _ := st.Rev(ctx, KindFleet); rev != 3 {
			t.Fatalf("fleet rev %d, want 3", rev)
		}
		// A machine the fleet names cannot be removed (a foreign key; the
		// tool names it).
		_, err = st.Delete(ctx, KindMachine, "space", "rowan")
		if err == nil || !errors.Is(err, ErrReferenced) || !strings.Contains(err.Error(), "machine space is the --store of the fleet") {
			t.Fatalf("remove the store machine: %v", err)
		}
		// Clearing a field is an empty value; the machine is then free.
		after, _, err = st.Update(ctx, KindFleet, KindFleet, map[string]string{"store": ""}, "rowan")
		if err != nil || after.Fields["store"] != "" || after.Fields["coordinator"] != "studio" {
			t.Fatalf("clear the store: %+v %v", after.Fields, err)
		}
		if _, err := st.Delete(ctx, KindMachine, "space", "rowan"); err != nil {
			t.Fatalf("remove the freed machine: %v", err)
		}
		hist, err := st.History(ctx, KindFleet, KindFleet)
		if err != nil || len(hist) != 2 || hist[0].Op != OpSet || hist[0].Before["store"] != "" || hist[0].After["store"] != "space" || hist[1].After["store"] != "" {
			t.Fatalf("fleet history %+v %v", hist, err)
		}
	})

	t.Run("the route kind", func(t *testing.T) {
		t.Parallel()
		st := open(t)
		route, _ := Lookup(KindRoute)
		id, err := st.Insert(ctx, KindRoute, mk(route, "deepseek-flash", map[string]string{"provider": "deepseek", "model": "deepseek-chat", "seat": "worker", "tier": "flash"}), "rowan")
		if err != nil || id != 1 {
			t.Fatalf("add route: id %d err %v", id, err)
		}
		row, found, err := st.Get(ctx, KindRoute, "deepseek-flash")
		if err != nil || !found || row.Fields["provider"] != "deepseek" || row.Fields["model"] != "deepseek-chat" || row.Fields["seat"] != "worker" || row.Fields["tier"] != "flash" {
			t.Fatalf("get route: %+v %v %v", row, found, err)
		}
		rows, err := st.List(ctx, KindRoute)
		if err != nil || len(rows) != 1 || rows[0].Name != "deepseek-flash" {
			t.Fatalf("list route: %+v %v", rows, err)
		}
		after, id, err := st.Update(ctx, KindRoute, "deepseek-flash", map[string]string{"tier": "pro"}, "stella")
		if err != nil || id != 2 || after.Fields["tier"] != "pro" {
			t.Fatalf("set route: %+v id %d err %v", after, id, err)
		}
		rev, err := st.Rev(ctx, KindRoute)
		if err != nil || rev != 2 {
			t.Fatalf("rev route: %d %v", rev, err)
		}
		id, err = st.Delete(ctx, KindRoute, "deepseek-flash", "rowan")
		if err != nil || id != 3 {
			t.Fatalf("remove route: id %d err %v", id, err)
		}
		hist, err := st.History(ctx, KindRoute, "deepseek-flash")
		if err != nil || len(hist) != 3 {
			t.Fatalf("history route: %+v %v", hist, err)
		}
		if hist[1].Op != OpSet || hist[1].Before["tier"] != "flash" || hist[1].After["tier"] != "pro" {
			t.Errorf("history set route %+v", hist[1])
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
	row, _ := machine.NewRow("studio", map[string]string{"user": "glenn", "seat": "studio", "slots": "64"})
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
