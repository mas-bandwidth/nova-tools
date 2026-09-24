package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/presence"
)

// #2200, docs/SPEC-STATE.md: the presence key "expires on its own; the beat
// renews it. A crashed line ages out with no tombstone", and `nova-wake awake`
// reads that same "I am here now" signal. The key is the one the production
// beat writes (#2610's friend:<name>, `nova-wake beat`), and the reader is the
// production awake verb with --store: nothing here calls a helper that only a
// test calls. One clock drives the verbs and the store, so a TTL lapse is a
// function call and not ninety seconds of test.
func TestPresenceKeyExpiresAndLeavesNoTombstone(t *testing.T) {
	t0 := beatAt
	st := presence.NewFakeStore(t0)
	clock := fakeStoreClock{st}
	open := fakeOpener(st)
	ctx := context.Background()

	// The bus says stella is asleep: her cursor is ten minutes old, twice the
	// window, and there is no BEAT file. Only the live key can wake her.
	bus := awakeBus(t)
	cursorCommit(t, bus, "stella", t0.Add(-600*time.Second))
	cfg := &wakeConfig{path: filepath.Join(t.TempDir(), "config"), values: map[string]string{}}

	beat := func() {
		t.Helper()
		var out, errb bytes.Buffer
		if code := cmdBeat([]string{"--as", "stella", "--store", "store.invalid:6380", "--once"},
			&out, &errb, clock, open); code != 0 {
			t.Fatalf("beat --once exited %d: %s", code, errb.String())
		}
	}
	keys := func() (live, last string) {
		t.Helper()
		v, err := st.MGet(ctx, presence.Key("stella"), presence.LastKey("stella"))
		if err != nil {
			t.Fatal(err)
		}
		return v[0], v[1]
	}
	awake := func() string {
		t.Helper()
		var out, errb bytes.Buffer
		if code := cmdAwake(cfg, []string{"--bus", bus, "--store", "store.invalid:6380"},
			&out, &errb, clock, open); code != 0 {
			t.Fatalf("awake --store exited %d: %s", code, errb.String())
		}
		return out.String()
	}

	// 1. The beat puts the key, holding the moment it was written.
	beat()
	if live, _ := keys(); live != t0.Format(presence.Stamp) {
		t.Fatalf("after the first beat friend:stella = %q, want %q", live, t0.Format(presence.Stamp))
	}
	if got := awake(); !strings.Contains(got, "FRIEND stella awake age=0 source=presence\n") {
		t.Fatalf("awake does not read the live key the beat wrote:\n%s", got)
	}

	// 2. The beat renews it: 60s on, a second beat rewrites the value AND the
	// TTL. 120s after the first beat (past its 90s TTL) the key is still
	// there, which it could only be if the second beat renewed the expiry.
	clock.Sleep(60 * time.Second)
	beat()
	renewed := t0.Add(60 * time.Second).Format(presence.Stamp)
	if live, _ := keys(); live != renewed {
		t.Fatalf("after the renewing beat friend:stella = %q, want %q", live, renewed)
	}
	clock.Sleep(60 * time.Second)
	if live, _ := keys(); live != renewed {
		t.Fatalf("120s after the first beat the renewed key is %q, want %q: the beat did not renew the TTL", live, renewed)
	}
	if got := awake(); !strings.Contains(got, "FRIEND stella awake age=60 source=presence\n") {
		t.Fatalf("awake does not read the renewed key:\n%s", got)
	}

	// 3. The line crashes: no more beats. Past the renewed TTL the key is
	// simply absent -- no write happened at the death, and the only other key
	// is the untimed :last, still holding the last beat's own stamp, not a
	// tombstone.
	sets := st.Sets
	clock.Sleep(presence.DefaultTTL)
	live, last := keys()
	if live != "" {
		t.Fatalf("a crashed line's key is still there: friend:stella = %q", live)
	}
	if last != renewed {
		t.Fatalf("friend:stella:last = %q, want the last beat's %q", last, renewed)
	}
	if st.Sets != sets {
		t.Fatalf("the store took %d writes after the line stopped beating; ageing out writes nothing", st.Sets-sets)
	}

	// 4. awake reads the absence: no live key, so the lane falls back to the
	// bus cursor, which says asleep.
	got := awake()
	if !strings.Contains(got, "FRIEND stella asleep age=810 source=bus-cursor\n") ||
		!strings.Contains(got, "AWAKE OK friends=1 awake=0 asleep=1 unknown=0 window=300\n") {
		t.Fatalf("awake after the key aged out:\n%s", got)
	}
}

// Without --store, awake is exactly the bus reading it was: the flag is the
// only door to the store, and a bad address is refused before any dial.
func TestAwakeStoreRefusesBadAddressWithoutDialing(t *testing.T) {
	st := presence.NewFakeStore(beatAt)
	dials := 0
	bus := awakeBus(t)
	cursorCommit(t, bus, "stella", beatAt.Add(-10*time.Second))
	cfg := &wakeConfig{path: filepath.Join(t.TempDir(), "config"), values: map[string]string{}}

	var out, errb bytes.Buffer
	if code := cmdAwake(cfg, []string{"--bus", bus}, &out, &errb, fakeStoreClock{st}, countingOpener(st, &dials)); code != 0 {
		t.Fatalf("awake without --store exited %d: %s", code, errb.String())
	}
	if dials != 0 || !strings.Contains(out.String(), "FRIEND stella awake age=10 source=bus-cursor\n") {
		t.Fatalf("awake without --store dialed %d times or changed its reading:\n%s", dials, out.String())
	}

	out.Reset()
	errb.Reset()
	if code := cmdAwake(cfg, []string{"--bus", bus, "--store", "no-port"}, &out, &errb, fakeStoreClock{st}, countingOpener(st, &dials)); code != 2 {
		t.Fatalf("awake --store no-port exited %d, want 2", code)
	}
	if dials != 0 || !strings.HasPrefix(errb.String(), "AWAKE REFUSED ") {
		t.Fatalf("a bad --store dialed %d times or refused in the wrong shape: %q", dials, errb.String())
	}
}
