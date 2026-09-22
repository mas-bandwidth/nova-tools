package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/presence"
)

// The heartbeat's tests reach no store: the verbs take their opener as an
// argument, so a test hands in a fake whose clock it moves and the loop's
// sleeps move it too. Nothing here waits for a wall-clock second.

var beatAt = time.Date(2026, 9, 22, 9, 41, 0, 0, time.UTC)

// fakeStoreClock is one clock for the verb and the store: the verb's sleeps
// move the store's expiry, so `--every 30s` past a 90s TTL is three function
// calls and not ninety seconds of test.
type fakeStoreClock struct{ st *presence.FakeStore }

func (c fakeStoreClock) Now() time.Time        { return c.st.Now() }
func (c fakeStoreClock) Sleep(d time.Duration) { c.st.Advance(d) }

func fakeOpener(st presence.Store) storeOpener {
	return func(ctx context.Context, addr, user string) (presence.Store, func() error, error) {
		return st, func() error { return nil }, nil
	}
}

// countingOpener wraps an opener and counts how many times it dials. A
// refused --store must never reach it: cmdBeat and cmdPresence both check
// presence.Addr and print their refusal before they ever call open, so the
// count is the test's proof that a bad address never causes a network
// operation, not just that the CLI printed the right words.
func countingOpener(st presence.Store, dials *int) storeOpener {
	inner := fakeOpener(st)
	return func(ctx context.Context, addr, user string) (presence.Store, func() error, error) {
		*dials++
		return inner(ctx, addr, user)
	}
}

func TestBeatOnceWritesTheFriendsKey(t *testing.T) {
	st := presence.NewFakeStore(beatAt)
	var out, errb bytes.Buffer
	code := cmdBeat([]string{"--as", "Johnny", "--store", "store.invalid:6380", "--once"},
		&out, &errb, fakeStoreClock{st}, fakeOpener(st))
	if code != 0 {
		t.Fatalf("beat --once exited %d: %s", code, errb.String())
	}
	if got := out.String(); !strings.HasPrefix(got, "beat johnny key=friend:johnny every=30s ttl=1m30s") {
		t.Fatalf("startup line = %q", got)
	}
	vals, err := st.MGet(context.Background(), "friend:johnny", "friend:johnny:last")
	if err != nil {
		t.Fatalf("mget: %v", err)
	}
	if vals[0] != "2026-09-22T09:41:00Z" || vals[1] != vals[0] {
		t.Fatalf("keys = %q, %q; want the beat and its memory", vals[0], vals[1])
	}
}

func TestBeatRefusesWhatItCannotGuess(t *testing.T) {
	st := presence.NewFakeStore(beatAt)
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"no name", []string{"--store", "store.invalid:6380", "--once"}, "--as is required"},
		{"no store", []string{"--as", "johnny", "--once"}, "--store is required"},
		{"a bare host", []string{"--as", "johnny", "--store", "store.invalid", "--once"}, "store address refused"},
		{"a ttl inside the cadence", []string{"--as", "johnny", "--store", "store.invalid:6380", "--every", "30s", "--ttl", "20s", "--once"}, "is not longer than"},
		{"a cadence that is not a duration", []string{"--as", "johnny", "--store", "store.invalid:6380", "--every", "soon", "--once"}, "is not a duration"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := cmdBeat(c.args, &out, &errb, fakeStoreClock{st}, fakeOpener(st)); code != 2 {
				t.Fatalf("exit %d; want 2", code)
			}
			if !strings.Contains(errb.String(), c.want) {
				t.Fatalf("refusal = %q; want it to name %q", errb.String(), c.want)
			}
			if !strings.Contains(errb.String(), "run: nova-wake help") {
				t.Fatalf("refusal = %q; every refusal ends in the door to the usage", errb.String())
			}
		})
	}
}

// TestBeatStoreURLParsingRejectsTLSAndUserinfoWithoutLeakingTheSecret is the
// synthetic control for comment 5782441213 on #2612: a rediss:// --store used
// to be silently stripped to a plaintext host:port and dialled anyway, and a
// user:pass@ URL was never parsed at all -- the password rode along inside
// the address the beat then printed on its startup line. This drives the
// verb's own flag parsing (cmdBeat, --once, no network: the fake opener never
// dials) with all three shapes the comment named, and checks not just that
// the two bad ones are refused, but that the refusal -- and every other byte
// on stdout or stderr -- never contains the password, while the third,
// ordinary shape still works.
func TestBeatStoreURLParsingRejectsTLSAndUserinfoWithoutLeakingTheSecret(t *testing.T) {
	const secret = "hunter2"
	for _, c := range []struct {
		name       string
		store      string
		wantExit   int
		wantErr    string // substring the refusal must name; "" when it should succeed
		hasNetwork bool
	}{
		{"tls is refused", "rediss://store.invalid:6380", 2, "store address refused", false},
		{"userinfo is refused", "redis://johnny:" + secret + "@store.invalid:6380", 2, "store address refused", false},
		{"a plain URL still works", "redis://store.invalid:6380", 0, "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := presence.NewFakeStore(beatAt)
			var out, errb bytes.Buffer
			code := cmdBeat([]string{"--as", "johnny", "--store", c.store, "--once"},
				&out, &errb, fakeStoreClock{st}, fakeOpener(st))
			if code != c.wantExit {
				t.Fatalf("exit %d; want %d (stdout=%q stderr=%q)", code, c.wantExit, out.String(), errb.String())
			}
			if c.wantErr != "" && !strings.Contains(errb.String(), c.wantErr) {
				t.Fatalf("refusal = %q; want it to name %q", errb.String(), c.wantErr)
			}
			if strings.Contains(out.String(), secret) || strings.Contains(errb.String(), secret) {
				t.Fatalf("stdout=%q stderr=%q; the password leaked", out.String(), errb.String())
			}
		})
	}
}

// TestBeatStoreURLQueryParametersAreRefusedWithoutDialing is the CLI-level
// control for comment 5783425783 on #2612, and the two comments before it on
// the same issue. The latest hole: a query VALUE containing "@" --
// redis://store.invalid:6380?password=prefix@SYNTHETIC_SECRET, the exact
// string from the comment -- still became a printed host, because the prior
// repair's addrProblems took strings.LastIndex(s, "@") before it had found
// the query boundary, mistook the query value's own "@" for the userinfo
// delimiter, and printed everything after it as the host. This drives the
// verb's own flag parsing (cmdBeat, --once) for that exact shape plus every
// other shape comment 5783425783 asked for, through a counting opener that
// must stay at zero: the refusal has to happen before cmdBeat ever calls
// open, or the "no network operation occurs on a refused address" half of
// the control is not proven. It also checks the refusal is refusedAddr byte
// for byte, and that the two accepted forms in the same list -- a bare
// host:port and an [ipv6]:port -- still parse and reach the opener.
func TestBeatStoreURLQueryParametersAreRefusedWithoutDialing(t *testing.T) {
	const secret = "SYNTHETIC_SECRET"
	// Mirrors presence.refusedAddr, unexported on purpose: the CLI proves
	// the refusal it prints against the same literal the package's own test
	// checks Addr's error against byte for byte.
	const wantRefusal = "store address refused: only host:port or redis://host:port is supported"
	for _, c := range []struct {
		name     string
		store    string
		accepted bool
	}{
		// The exact synthetic URL from comment 5783425783.
		{"query value containing @", "redis://store.invalid:6380?password=prefix@" + secret, false},
		{"query value containing @, short host", "redis://h:6380?x=a@b", false},
		{"userinfo before the host", "redis://a@b:6380", false},
		{"port zero", "redis://h:0", false},
		{"port above 65535", "redis://h:70000", false},
		{"no port at all", "redis://h", false},
		{"bare host:port", "h:6380", true},
		{"redis:// host:port", "redis://h:6380", true},
		{"bracketed ipv6 host:port", "[::1]:6380", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := presence.NewFakeStore(beatAt)
			var dials int
			var out, errb bytes.Buffer
			code := cmdBeat([]string{"--as", "johnny", "--store", c.store, "--once"},
				&out, &errb, fakeStoreClock{st}, countingOpener(st, &dials))
			if c.accepted {
				if code != 0 {
					t.Fatalf("exit %d; want 0 -- %q is a valid store address (stdout=%q stderr=%q)", code, c.store, out.String(), errb.String())
				}
				if dials != 1 {
					t.Fatalf("dials = %d; want 1 -- an accepted --store must reach the opener", dials)
				}
				return
			}
			if code != 2 {
				t.Fatalf("exit %d; want 2 (stdout=%q stderr=%q)", code, out.String(), errb.String())
			}
			if !strings.Contains(errb.String(), wantRefusal) {
				t.Fatalf("refusal = %q; want the constant refusal %q", errb.String(), wantRefusal)
			}
			if strings.Contains(out.String(), secret) || strings.Contains(errb.String(), secret) {
				t.Fatalf("stdout=%q stderr=%q; the secret leaked", out.String(), errb.String())
			}
			if strings.Contains(out.String(), "store.invalid") || strings.Contains(errb.String(), "store.invalid") {
				t.Fatalf("stdout=%q stderr=%q; the synthetic host leaked", out.String(), errb.String())
			}
			if dials != 0 {
				t.Fatalf("dials = %d; want 0 -- a refused --store must never reach the opener", dials)
			}
		})
	}
}

func TestTheLoopKeepsBeatingThroughAStoreThatBlinked(t *testing.T) {
	st := presence.NewFakeStore(beatAt)
	// One good beat (calls 1 and 2), then a store that refuses four calls
	// -- four beats, each giving up on its first write -- and then a store
	// that answers again. A beat that exited on the first error would have
	// reported its friend as gone for the rest of the day.
	st.Hook = func(call int) error {
		if call >= 3 && call <= 6 {
			return errStoreDown
		}
		return nil
	}
	var errb bytes.Buffer
	beatLoop(context.Background(), st, "johnny", presence.DefaultEvery, presence.DefaultTTL, fakeStoreClock{st}, &errb, 6)

	if got := st.Sets; got != 4 {
		t.Fatalf("writes = %d; want 4 (two keys each for the two beats the store took)", got)
	}
	lines := strings.Count(strings.TrimSpace(errb.String()), "\n") + 1
	if lines != 2 {
		t.Fatalf("stderr =\n%s\nwant two lines: the failure once and the recovery once, never one a beat", errb.String())
	}
	if !strings.Contains(errb.String(), "still beating every 30s") || !strings.Contains(errb.String(), "answering again") {
		t.Fatalf("stderr = %q; want the failure and the recovery named", errb.String())
	}

	// The friend is up at the end, from the beat that landed after the
	// store came back.
	sts, err := presence.Read(context.Background(), st, []string{"johnny"}, st.Now())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if sts[0].State != presence.Up {
		t.Fatalf("after the store came back: %v; want Up", sts[0].State)
	}
}

func TestPresencePrintsTheOneLine(t *testing.T) {
	st := presence.NewFakeStore(beatAt)
	ctx := context.Background()
	if err := presence.Beat(ctx, st, "emma", st.Now(), presence.DefaultTTL); err != nil {
		t.Fatalf("beat emma: %v", err)
	}
	st.Advance(72 * time.Minute) // emma's window is long gone
	if err := presence.Beat(ctx, st, "johnny", st.Now().Add(-12*time.Second), presence.DefaultTTL); err != nil {
		t.Fatalf("beat johnny: %v", err)
	}

	var out, errb bytes.Buffer
	code := cmdPresence([]string{"--store", "store.invalid:6380", "--friends", "johnny,emma,freddy"},
		&out, &errb, fakeStoreClock{st}, fakeOpener(st))
	if code != 0 {
		t.Fatalf("presence exited %d: %s", code, errb.String())
	}
	want := "friends: johnny up 12s · emma AWAY 1h12m (last 09:41Z) · freddy none\n"
	if out.String() != want {
		t.Fatalf("line =\n\t%q\nwant\n\t%q", out.String(), want)
	}
}

func TestPresenceReadsTheRosterFromTheBusCheckout(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "participants.json"), []byte(
		`{"participants":[{"name":"Rowan"},{"name":"Stella"},{"name":"Glenn"}]}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	st := presence.NewFakeStore(beatAt)
	var out, errb bytes.Buffer
	code := cmdPresence([]string{"--store", "store.invalid:6380", "--bus", dir},
		&out, &errb, fakeStoreClock{st}, fakeOpener(st))
	if code != 0 {
		t.Fatalf("presence exited %d: %s", code, errb.String())
	}
	if out.String() != "friends: stella none\n" {
		t.Fatalf("line = %q; want the roster minus Glenn and Rowan", out.String())
	}
}

func TestPresenceRefusesARosterItWouldHaveToInvent(t *testing.T) {
	st := presence.NewFakeStore(beatAt)
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"no roster at all", []string{"--store", "store.invalid:6380"}, "name the friends"},
		{"two rosters", []string{"--store", "store.invalid:6380", "--friends", "a", "--bus", "."}, "three spellings of one roster"},
		{"no store", []string{"--friends", "a"}, "--store is required"},
		{"a bus with no participants file", []string{"--store", "store.invalid:6380", "--bus", t.TempDir()}, "cannot read"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := cmdPresence(c.args, &out, &errb, fakeStoreClock{st}, fakeOpener(st)); code != 2 {
				t.Fatalf("exit %d; want 2", code)
			}
			if !strings.Contains(errb.String(), c.want) {
				t.Fatalf("refusal = %q; want it to name %q", errb.String(), c.want)
			}
			if out.Len() != 0 {
				t.Fatalf("a refusal printed a line on stdout: %q; an empty friends line reads as good news", out.String())
			}
		})
	}
}

func TestPresenceSaysNothingRatherThanAnEmptyRoomWhenTheStoreIsDown(t *testing.T) {
	st := presence.NewFakeStore(beatAt)
	st.Err = errStoreDown
	var out, errb bytes.Buffer
	code := cmdPresence([]string{"--store", "store.invalid:6380", "--friends", "johnny"},
		&out, &errb, fakeStoreClock{st}, fakeOpener(st))
	if code != 2 {
		t.Fatalf("exit %d; want 2", code)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q; a store that cannot be read is not four friends who are away", out.String())
	}
}

func TestTheUsageNamesBothVerbs(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, &out, &errb); code != 0 {
		t.Fatalf("help exited %d", code)
	}
	for _, want := range []string{"nova-wake beat --as <name> --store <host:port>", "nova-wake presence --store <host:port>"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("the usage banner does not name %q", want)
		}
	}
}

// errStoreDown is what a store that is not answering looks like to the verb.
var errStoreDown = errors.New("dial tcp: connection refused")
