package config

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// hostsFake is a HostReader over a map: where each friend's beat says she runs.
type hostsFake map[string]string

func (h hostsFake) FriendHosts(_ context.Context, names []string) (map[string]string, error) {
	out := map[string]string{}
	for _, n := range names {
		out[n] = h[n]
	}
	return out, nil
}

type hostsErr struct{}

func (hostsErr) FriendHosts(context.Context, []string) (map[string]string, error) {
	return nil, errors.New("store down")
}

func seedWidths(t *testing.T) *Mem {
	t.Helper()
	ctx := context.Background()
	m := NewMem()
	for _, r := range []struct {
		name  string
		slots string
	}{{"m1", "8"}, {"m2", "4"}, {"m3", "0"}, {"m4", "2"}} {
		if _, err := m.Insert(ctx, KindMachine, Row{Name: r.name, Fields: map[string]string{"user": "u", "seat": "s", "slots": r.slots, "runners": "0"}}, "t"); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

func addFriend(t *testing.T, m *Mem, name, slots string) {
	t.Helper()
	if _, err := m.Insert(context.Background(), KindFriend, Row{Name: name, Fields: map[string]string{"slots": slots, "tiers": "flash", "roles": "builder"}}, "t"); err != nil {
		t.Fatal(err)
	}
}

// TestWidthIsSlotsLessTheFriendsChargedThere: decision 1, the width of the
// sprint's member is the machine's ceiling less the desired slots of the
// friends charged to it, and a machine with no room is no member.
func TestWidthIsSlotsLessTheFriendsChargedThere(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := seedWidths(t)
	// no friend: the width is the ceiling, and no Redis is needed
	ws, err := Widths(ctx, m, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]MachineWidth{}
	for _, w := range ws {
		got[w.Machine] = w
	}
	if got["m1"].Width != 8 || !got["m1"].Member() || got["m3"].Width != 0 || got["m3"].Member() {
		t.Fatalf("no friends: %+v", ws)
	}
	for i := 1; i < len(ws); i++ {
		if ws[i-1].Machine >= ws[i].Machine {
			t.Fatalf("widths not in name order: %+v", ws)
		}
	}

	// two friends on m1 by their beats, one on m4 taking all of it, one with
	// slots 0 that is charged nowhere
	addFriend(t, m, "f1", "3")
	addFriend(t, m, "f2", "2")
	addFriend(t, m, "f3", "2")
	addFriend(t, m, "f4", "0")
	ws, err = Widths(ctx, m, hostsFake{"f1": "m1", "f2": "m1", "f3": "m4"})
	if err != nil {
		t.Fatal(err)
	}
	got = map[string]MachineWidth{}
	for _, w := range ws {
		got[w.Machine] = w
	}
	if w := got["m1"]; w.Slots != 8 || w.Charged != 5 || w.Width != 3 || !w.Member() {
		t.Fatalf("m1: %+v", w)
	}
	if w := got["m2"]; w.Charged != 0 || w.Width != 4 {
		t.Fatalf("m2: %+v", w)
	}
	if w := got["m4"]; w.Charged != 2 || w.Width != 0 || w.Member() {
		t.Fatalf("m4 is full of its friend and is no member: %+v", w)
	}
}

// TestAFriendWithNoBeatIsChargedToTheCoordinatorMachine: the default charge
// is the one apply makes (RedisApplier.charge), and with no coordinator
// machine either the width refuses naming the fix.
func TestAFriendWithNoBeatIsChargedToTheCoordinatorMachine(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := seedWidths(t)
	addFriend(t, m, "f1", "3")
	if _, err := Widths(ctx, m, hostsFake{}); err == nil || !strings.Contains(err.Error(), "nova-config fleet set --coordinator") {
		t.Fatalf("no beat, no coordinator: %v", err)
	}
	if _, _, err := m.Update(ctx, KindFleet, KindFleet, map[string]string{"coordinator": "m2"}, "t"); err != nil {
		t.Fatal(err)
	}
	ws, err := Widths(ctx, m, hostsFake{})
	if err != nil {
		t.Fatal(err)
	}
	if w, _ := WidthOf(ws, "m2"); w.Charged != 3 || w.Width != 1 {
		t.Fatalf("m2 carries the friend: %+v", w)
	}
}

// TestWidthsNeedARedisOnlyWhenAFriendCarriesSlots: a store with friends and
// no host reader refuses (never a silently wrong width), and a host reader
// that fails is the error.
func TestWidthsNeedARedisOnlyWhenAFriendCarriesSlots(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := seedWidths(t)
	addFriend(t, m, "f1", "0")
	if _, err := Widths(ctx, m, nil); err != nil {
		t.Fatalf("a friend with no slots needs no Redis: %v", err)
	}
	addFriend(t, m, "f2", "1")
	if _, err := Widths(ctx, m, nil); err == nil || !strings.Contains(err.Error(), "Redis address is needed") {
		t.Fatalf("no host reader: %v", err)
	}
	if _, err := Widths(ctx, m, hostsErr{}); err == nil || !strings.Contains(err.Error(), "store down") {
		t.Fatalf("host reader down: %v", err)
	}
}

// TestWidthIsDerivedNotStored: the machine kind declares no width or member
// field; the width is always the rows' arithmetic (decision 1, no new field).
func TestWidthIsDerivedNotStored(t *testing.T) {
	t.Parallel()
	k, _ := Lookup(KindMachine)
	for _, f := range k.Fields {
		if f.Name == "width" || f.Name == "member" {
			t.Fatalf("the machine kind declares %s; the width is derived from slots and the friends' charges", f.Name)
		}
	}
}
