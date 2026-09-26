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

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
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
	t.Parallel()

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
	at, last := st.Hash("friend:johnny")["at"], st.String("friend:johnny:last")
	if at != "2026-09-22T09:41:00Z" || last != at {
		t.Fatalf("at = %q, last = %q; want the beat and its memory", at, last)
	}
	if got := st.TTL("friend:johnny"); got != presence.DefaultTTL {
		t.Fatalf("friend:johnny ttl = %s; want %s", got, presence.DefaultTTL)
	}
}

// TestBeatWidthWritesTheHashAndPresencePrintsUpOrDown is #2673's DONE-WHEN
// through the verbs: `beat --width 8` puts width beside at in friend:<name>
// with the beat's TTL, `presence` over the friends SET prints the friend up
// with that width, and once the beat has lapsed the same line prints down.
func TestBeatWidthWritesTheHashAndPresencePrintsUpOrDown(t *testing.T) {
	t.Parallel()

	st := presence.NewFakeStore(beatAt)
	st.AddMembers(presence.FriendsSet, "emma", "stella", "rowan")
	clock := fakeStoreClock{st}
	var out, errb bytes.Buffer
	for _, as := range []string{"Emma", "stella"} {
		if code := cmdBeat([]string{"--as", as, "--store", "store.invalid:6380", "--once", "--width", "8"},
			&out, &errb, clock, fakeOpener(st)); code != 0 {
			t.Fatalf("beat exited %d: %s", code, errb.String())
		}
	}
	if h := st.Hash("friend:emma"); h["width"] != "8" || h["at"] != "2026-09-22T09:41:00Z" {
		t.Fatalf("friend:emma = %v; want at and width=8", h)
	}
	if got := st.TTL("friend:emma"); got != presence.DefaultTTL {
		t.Fatalf("ttl = %s; want %s", got, presence.DefaultTTL)
	}

	// Stella's width changes: she re-runs beat with the new number.
	clock.Sleep(60 * time.Second)
	if code := cmdBeat([]string{"--as", "stella", "--store", "store.invalid:6380", "--once", "--width", "3"},
		&out, &errb, clock, fakeOpener(st)); code != 0 {
		t.Fatalf("beat exited %d: %s", code, errb.String())
	}
	out.Reset()
	if code := cmdPresence([]string{"--store", "store.invalid:6380"}, &out, &errb, clock, fakeOpener(st)); code != 0 {
		t.Fatalf("presence exited %d: %s", code, errb.String())
	}
	if want := "friends: emma up 1m width=8 · stella up 0s width=3\n"; out.String() != want {
		t.Fatalf("line =\n\t%q\nwant\n\t%q", out.String(), want)
	}

	// Emma's beat stops; 31s later her 90s TTL has run out and she is down.
	clock.Sleep(31 * time.Second)
	out.Reset()
	if code := cmdPresence([]string{"--store", "store.invalid:6380"}, &out, &errb, clock, fakeOpener(st)); code != 0 {
		t.Fatalf("presence exited %d: %s", code, errb.String())
	}
	if want := "friends: emma down 1m (last 09:41Z) · stella up 31s width=3\n"; out.String() != want {
		t.Fatalf("line =\n\t%q\nwant\n\t%q", out.String(), want)
	}
}

// TestBeatWindowAndWidthAreSetOnlyWhenTheFlagsArePassed is Rowan's rule of
// 2026-09-22 beside the #2612 beat: the window field is the cap's reset time
// and the width field is how many children are in use, each a flag the caller
// passes. The reset time is not this beat's clock. A missing flag writes no
// field and the beat still succeeds. presence prints the ones that are there.
func TestBeatWindowAndWidthAreSetOnlyWhenTheFlagsArePassed(t *testing.T) {
	t.Parallel()

	const (
		reset = "2026-09-23T04:00:00Z" // not the beat's own stamp
		stamp = "2026-09-22T09:41:00Z"
	)

	t.Run("both flags", func(t *testing.T) {
		st := presence.NewFakeStore(beatAt)
		var out, errb bytes.Buffer
		code := cmdBeat([]string{"--as", "Johnny", "--store", "store.invalid:6380", "--once",
			"--window", reset, "--width", "2"}, &out, &errb, fakeStoreClock{st}, fakeOpener(st))
		if code != 0 {
			t.Fatalf("beat --once exited %d: %s", code, errb.String())
		}
		vals := beatFields(st)
		if vals[0] != stamp || vals[1] != stamp {
			t.Fatalf("presence at/last = %q, %q; want the beat stamp %q on both", vals[0], vals[1], stamp)
		}
		if vals[2] != reset {
			t.Fatalf("window = %q; want the flag %q, not a clock this beat invented", vals[2], reset)
		}
		if vals[3] != "2" {
			t.Fatalf("width = %q; want 2", vals[3])
		}
		if st.Sets != 1 {
			t.Fatalf("writes = %d; want 1 (one MULTI: the hash, its TTL and its memory)", st.Sets)
		}

		out.Reset()
		errb.Reset()
		code = cmdPresence([]string{"--store", "store.invalid:6380", "--friends", "johnny"},
			&out, &errb, fakeStoreClock{st}, fakeOpener(st))
		if code != 0 {
			t.Fatalf("presence exited %d: %s", code, errb.String())
		}
		want := "friends: johnny up 0s window=" + reset + " width=2\n"
		if out.String() != want {
			t.Fatalf("line =\n\t%q\nwant\n\t%q", out.String(), want)
		}
	})

	t.Run("neither flag", func(t *testing.T) {
		st := presence.NewFakeStore(beatAt)
		var out, errb bytes.Buffer
		code := cmdBeat([]string{"--as", "Johnny", "--store", "store.invalid:6380", "--once"},
			&out, &errb, fakeStoreClock{st}, fakeOpener(st))
		if code != 0 {
			t.Fatalf("beat --once exited %d: %s; a missing flag must not fail the beat", code, errb.String())
		}
		vals := beatFields(st)
		if vals[0] != stamp || vals[1] != stamp {
			t.Fatalf("presence at/last = %q, %q; want the beat to have landed", vals[0], vals[1])
		}
		if vals[2] != "" || vals[3] != "" {
			t.Fatalf("window = %q, width = %q; a missing flag must not write the field", vals[2], vals[3])
		}
		if st.Sets != 1 {
			t.Fatalf("writes = %d; want 1", st.Sets)
		}

		out.Reset()
		errb.Reset()
		code = cmdPresence([]string{"--store", "store.invalid:6380", "--friends", "johnny"},
			&out, &errb, fakeStoreClock{st}, fakeOpener(st))
		if code != 0 {
			t.Fatalf("presence exited %d: %s", code, errb.String())
		}
		want := "friends: johnny up 0s\n"
		if out.String() != want {
			t.Fatalf("line =\n\t%q\nwant\n\t%q", out.String(), want)
		}
	})

	t.Run("one flag", func(t *testing.T) {
		st := presence.NewFakeStore(beatAt)
		var out, errb bytes.Buffer
		code := cmdBeat([]string{"--as", "johnny", "--store", "store.invalid:6380", "--once", "--window", reset},
			&out, &errb, fakeStoreClock{st}, fakeOpener(st))
		if code != 0 {
			t.Fatalf("window only exited %d: %s", code, errb.String())
		}
		vals := beatFields(st)
		if vals[2] != reset || vals[3] != "" {
			t.Fatalf("window only: window = %q, width = %q; want the reset and no width field", vals[2], vals[3])
		}

		st = presence.NewFakeStore(beatAt)
		out.Reset()
		errb.Reset()
		code = cmdBeat([]string{"--as", "johnny", "--store", "store.invalid:6380", "--once", "--width", "0"},
			&out, &errb, fakeStoreClock{st}, fakeOpener(st))
		if code != 0 {
			t.Fatalf("width 0 exited %d: %s; zero children is a count, not a missing flag", code, errb.String())
		}
		vals = beatFields(st)
		if vals[2] != "" || vals[3] != "0" {
			t.Fatalf("width 0: window = %q, width = %q; want no window field and width 0", vals[2], vals[3])
		}
	})

	t.Run("a width that is not a count writes nothing", func(t *testing.T) {
		st := presence.NewFakeStore(beatAt)
		var out, errb bytes.Buffer
		code := cmdBeat([]string{"--as", "johnny", "--store", "store.invalid:6380", "--once", "--width", "many"},
			&out, &errb, fakeStoreClock{st}, fakeOpener(st))
		if code != 2 {
			t.Fatalf("exit %d; want 2", code)
		}
		if !strings.Contains(errb.String(), "is not a count of children") {
			t.Fatalf("refusal = %q; want it to name the count", errb.String())
		}
		if st.Sets != 0 {
			t.Fatalf("writes = %d; a refused width must not write a beat", st.Sets)
		}
		for i, v := range beatFields(st) {
			if v != "" {
				t.Fatalf("value %d = %q; want nothing written", i, v)
			}
		}
	})
}

// beatFields is johnny's beat as the store holds it: at, :last, window, width.
func beatFields(st *presence.FakeStore) [4]string {
	h := st.Hash("friend:johnny")
	return [4]string{h["at"], st.String("friend:johnny:last"), h["window"], h["width"]}
}

func TestBeatRefusesWhatItCannotGuess(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

	st := presence.NewFakeStore(beatAt)
	// One good beat (call 1), then a store that refuses four calls -- four
	// beats -- and then a store that answers again. A beat that exited on the first error would have
	// reported its friend as gone for the rest of the day.
	st.Hook = func(call int) error {
		if call >= 2 && call <= 5 {
			return errStoreDown
		}
		return nil
	}
	var errb bytes.Buffer
	beatLoop(context.Background(), st, "johnny", presence.DefaultEvery, presence.DefaultTTL, fakeStoreClock{st}, &errb, presence.Side{}, 6)

	if got := st.Sets; got != 2 {
		t.Fatalf("writes = %d; want 2 (the two beats the store took)", got)
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
	t.Parallel()

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
	want := "friends: johnny up 12s · emma down 1h12m (last 09:41Z) · freddy down\n"
	if out.String() != want {
		t.Fatalf("line =\n\t%q\nwant\n\t%q", out.String(), want)
	}
}

func TestPresenceReadsTheRosterFromTheBusCheckout(t *testing.T) {
	t.Parallel()

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
	if out.String() != "friends: stella down\n" {
		t.Fatalf("line = %q; want the roster minus Glenn and Rowan", out.String())
	}
}

func TestPresenceRefusesARosterItWouldHaveToInvent(t *testing.T) {
	t.Parallel()

	st := presence.NewFakeStore(beatAt)
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"no roster flag and an empty friends set", []string{"--store", "store.invalid:6380"}, "the friends set names no friends"},
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
	t.Parallel()

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
	t.Parallel()

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

// TestTheCommandReferencePresenceExampleMatchesWhatTheToolPrints is the
// comparator test SPEC-TOOLWORK §7 rule 7 asks for: `docs/CLI.md` pastes one
// `nova-wake presence` example ahead of the `### First run` heading -- the
// prose that introduces the verb, not a first-run transcript -- so it is read
// straight out of the document here rather than through onboarding.FirstRun,
// and its command and output block are compared with the one comparator every
// firstrun_test.go in this repo uses (onboarding.Compare, #2218): same number
// of lines, same lines, same order. The doc's `--store 100.115.99.19:6380` is
// illustrative -- CI-NET means nothing here dials it -- so the store is a fake
// built to the documented scenario (johnny up 12s width 8, stella up 4s, emma
// down 1h12m last 09:41Z, freddy never beaten, all four in the friends SET).
func TestTheCommandReferencePresenceExampleMatchesWhatTheToolPrints(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "CLI.md"))
	if err != nil {
		t.Fatal(err)
	}
	cmdLine, want := presenceExampleFromCLI(t, string(raw))

	fields := strings.Fields(cmdLine)
	if len(fields) < 3 || fields[0] != "$" || fields[1] != "nova-wake" || fields[2] != "presence" {
		t.Fatalf("docs/CLI.md's presence example is %q; want it to start `$ nova-wake presence`", cmdLine)
	}
	args := append([]string(nil), fields[3:]...)

	st := presence.NewFakeStore(beatAt)
	st.AddMembers(presence.FriendsSet, "johnny", "stella", "emma", "freddy")
	ctx := context.Background()
	if err := presence.Beat(ctx, st, "emma", st.Now(), presence.DefaultTTL); err != nil {
		t.Fatalf("beat emma: %v", err)
	}
	st.Advance(72 * time.Minute) // emma's window is long gone
	if err := presence.Beat(ctx, st, "stella", st.Now().Add(-4*time.Second), presence.DefaultTTL); err != nil {
		t.Fatalf("beat stella: %v", err)
	}
	eight := int64(8)
	if err := presence.BeatSide(ctx, st, "johnny", st.Now().Add(-12*time.Second), presence.DefaultTTL, presence.Side{Width: &eight}); err != nil {
		t.Fatalf("beat johnny: %v", err)
	}

	var out, errb bytes.Buffer
	code := cmdPresence(args, &out, &errb, fakeStoreClock{st}, fakeOpener(st))

	step := onboarding.Step{Line: cmdLine, Want: want}
	res := onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}
	for _, p := range onboarding.Compare(step, res, nil) {
		t.Errorf("docs/CLI.md drift: %s", p)
	}
}

// presenceExampleFromCLI reads the `$ nova-wake presence ...` example out of
// docs/CLI.md's `## nova-wake` section and returns its command line and the
// block written under it. It is not under `### First run` -- onboarding.FirstRun
// and onboarding.Transcript both require a `### ` heading immediately before
// the fence, and this example sits in the prose that introduces the verb
// instead -- so this reads the section the same way fencedLines does, anchored
// on the command's own marker rather than a heading.
func presenceExampleFromCLI(t *testing.T, cli string) (cmdLine string, want []string) {
	t.Helper()
	section, ok := onboarding.Section(cli, "nova-wake")
	if !ok {
		t.Fatal("docs/CLI.md has no `## nova-wake` section")
	}
	const marker = "$ nova-wake presence "
	i := strings.Index(section, marker)
	if i < 0 {
		t.Fatalf("docs/CLI.md's `## nova-wake` section carries no %q example", marker)
	}
	block := section[i:]
	end := strings.Index(block, "\n```")
	if end < 0 {
		t.Fatal("the presence example's fenced block never closes")
	}
	lines := strings.Split(block[:end], "\n")
	if len(lines) < 2 {
		t.Fatalf("the presence example has no output block under it:\n%s", block[:end])
	}
	return lines[0], lines[1:]
}

// errStoreDown is what a store that is not answering looks like to the verb.
var errStoreDown = errors.New("dial tcp: connection refused")

// TestPresenceReadsFriendRowHash is #3447's first half: on the fleet store
// friend:<name> is the friend row, a HASH with no TTL written every second by
// the row loop (at, up, ready, working, width, done, slots). presence reads it
// as that row: its up field says up or down and its at dates the reading. A
// row with up=1 and a fresh at is up, never "AWAY 1s"; a fresh row with up=0 is
// down with no age (the row does not say since when); a row whose at has gone
// stale is a silent row loop, down and dated by that at.
func TestPresenceReadsFriendRowHash(t *testing.T) {
	t.Parallel()

	st := presence.NewFakeStore(beatAt)
	st.AddMembers(presence.FriendsSet, "stella", "johnny", "emma", "rowan")
	stamp := func(ago time.Duration) string { return beatAt.Add(-ago).Format(presence.Stamp) }
	st.SetHash("friend:stella", 0, "at", stamp(time.Second), "up", "1", "ready", "0", "working", "1", "width", "15", "done", "5", "slots", "32")
	st.SetHash("friend:johnny", 0, "at", stamp(time.Second), "up", "0", "ready", "2", "working", "0", "width", "0", "done", "3")
	st.SetHash("friend:emma", 0, "at", stamp(40*time.Second), "up", "1", "ready", "1", "working", "4", "width", "4", "done", "0")
	for _, n := range []string{"stella", "johnny", "emma"} {
		st.SetString("friend:"+n+":last", stamp(time.Second))
	}

	var out, errb bytes.Buffer
	if code := cmdPresence([]string{"--store", "store.invalid:6380"}, &out, &errb, fakeStoreClock{st}, fakeOpener(st)); code != 0 {
		t.Fatalf("presence exited %d: %s", code, errb.String())
	}
	if want := "friends: emma down 40s (last 09:40Z) · johnny down · stella up 1s width=15\n"; out.String() != want {
		t.Fatalf("line =\n\t%q\nwant\n\t%q", out.String(), want)
	}
	if st.Sets != 0 {
		t.Fatalf("writes = %d; presence is a read", st.Sets)
	}
}

// TestBeatRefusesNonStringFriendKey is #3447's second half: beat never writes
// into a friend:<name> it does not own. The friend row (a hash carrying the up
// field) has one writer, the row loop, and any other type is somebody else's
// key: beat exits 2 naming the type and leaves the key exactly as it was. A
// plain string (a beat older than #2673) is the beat's own and is replaced.
func TestBeatRefusesNonStringFriendKey(t *testing.T) {
	t.Parallel()

	rowAt := beatAt.Add(-time.Second).Format(presence.Stamp)
	t.Run("the friend row", func(t *testing.T) {
		st := presence.NewFakeStore(beatAt)
		st.SetHash("friend:stella", 0, "at", rowAt, "up", "1", "ready", "4", "working", "1", "width", "1")
		var out, errb bytes.Buffer
		code := cmdBeat([]string{"--as", "stella", "--store", "store.invalid:6380", "--once", "--width", "8"},
			&out, &errb, fakeStoreClock{st}, fakeOpener(st))
		if code != 2 {
			t.Fatalf("exit %d; want 2 (stderr %q)", code, errb.String())
		}
		if !strings.Contains(errb.String(), "friend:stella is a hash") || !strings.Contains(errb.String(), "friend row") {
			t.Fatalf("refusal = %q; want it to name the type and the row", errb.String())
		}
		h := st.Hash("friend:stella")
		if h["at"] != rowAt || h["width"] != "1" || h["ready"] != "4" {
			t.Fatalf("friend:stella = %v; the refused beat changed the row", h)
		}
		if got := st.TTL("friend:stella"); got != 0 {
			t.Fatalf("ttl = %s; the refused beat put an expiry on the row", got)
		}
		if st.Sets != 0 || st.String("friend:stella:last") != "" {
			t.Fatalf("writes = %d, :last = %q; a refused beat writes nothing", st.Sets, st.String("friend:stella:last"))
		}

		// The loop stops on the refusal instead of retrying it forever.
		errb.Reset()
		err := beatLoop(context.Background(), st, "stella", presence.DefaultEvery, presence.DefaultTTL, fakeStoreClock{st}, &errb, presence.Side{}, 5)
		var kt *presence.KeyTypeError
		if !errors.As(err, &kt) || kt.Type != "hash" {
			t.Fatalf("beatLoop = %v; want the KeyTypeError naming hash", err)
		}
		if st.Sets != 0 {
			t.Fatalf("writes = %d; want none", st.Sets)
		}
	})
	t.Run("another type", func(t *testing.T) {
		st := presence.NewFakeStore(beatAt)
		st.AddMembers("friend:johnny", "x")
		var out, errb bytes.Buffer
		code := cmdBeat([]string{"--as", "johnny", "--store", "store.invalid:6380", "--once"},
			&out, &errb, fakeStoreClock{st}, fakeOpener(st))
		if code != 2 || !strings.Contains(errb.String(), "friend:johnny is a set") {
			t.Fatalf("exit %d, stderr %q; want 2 naming the set", code, errb.String())
		}
		if st.Sets != 0 {
			t.Fatalf("writes = %d; want none", st.Sets)
		}
	})
	t.Run("a legacy string is the beat's own", func(t *testing.T) {
		st := presence.NewFakeStore(beatAt)
		st.SetString("friend:johnny", rowAt)
		var out, errb bytes.Buffer
		if code := cmdBeat([]string{"--as", "johnny", "--store", "store.invalid:6380", "--once"},
			&out, &errb, fakeStoreClock{st}, fakeOpener(st)); code != 0 {
			t.Fatalf("exit %d: %s", code, errb.String())
		}
		if h := st.Hash("friend:johnny"); h["at"] != beatAt.Format(presence.Stamp) {
			t.Fatalf("friend:johnny = %v; want the beat hash in place of the string", h)
		}
	})
}
