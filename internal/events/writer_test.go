package events

// The Writer's contract is a NEGATIVE one -- "this never fails its caller" -- so the tests
// that matter are the ones where everything goes wrong: no password, no address, a dial that
// refuses, a store that errors on every entry, and an entry Validate itself refuses. Each
// asserts the same two things: nothing panicked, and Send returned.
//
// No test here opens a connection. The store arrives through WriterOptions.Dial and the
// environment through WriterOptions.Lookup (AGENTS.md rule 2).

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

// env is a fake environment: exactly the variables a case says are set.
func env(pairs map[string]string) func(string) string {
	return func(name string) string { return pairs[name] }
}

// fakeDial hands the writer a store and records that it was asked for one.
func fakeDial(store Store, err error, seen *Dial) func(context.Context, Dial) (Store, error) {
	return func(_ context.Context, d Dial) (Store, error) {
		if seen != nil {
			*seen = d
		}
		return store, err
	}
}

// TestWriterSilentWithoutThePassword: a bench that was never given
// NOVA_REDIS_BENCH_PASSWORD emits nothing, says nothing, and dials nothing. This is the
// state most of the fleet is in, and a line per card about it would be noise on every
// machine the store has not reached yet.
func TestWriterSilentWithoutThePassword(t *testing.T) {
	t.Parallel()

	var log strings.Builder
	dialed := false
	w := OpenWriter(context.Background(), WriterOptions{
		Addr:   "store.invalid:6380",
		Log:    &log,
		Lookup: env(map[string]string{}),
		Dial: func(context.Context, Dial) (Store, error) {
			dialed = true
			return NewFakeStream(), nil
		},
	})
	defer w.Close()
	if dialed {
		t.Fatal("a writer with no password dialled the store; a bench without the password must not reach for it")
	}
	if w.Enabled() {
		t.Fatal("a writer with no password reports itself enabled")
	}
	w.Send(context.Background(), Event{Label: "c1", Kind: OK})
	if log.String() != "" {
		t.Fatalf("a writer with no password wrote to the log: %q", log.String())
	}
}

// TestWriterSilentWithoutAnAddress: the password alone is not a store. No address is
// resolved, nothing is dialled, and NO HOST IS GUESSED -- `nova-pulse event` refuses a
// missing --store rather than assume one, and this writer disables itself for the same
// reason.
func TestWriterSilentWithoutAnAddress(t *testing.T) {
	t.Parallel()

	var log strings.Builder
	w := OpenWriter(context.Background(), WriterOptions{
		Log:    &log,
		Lookup: env(map[string]string{DefaultPasswordEnv: "secret"}),
		Dial: func(context.Context, Dial) (Store, error) {
			t.Fatal("a writer with no address dialled something; no host may be guessed")
			return nil, nil
		},
	})
	defer w.Close()
	if w.Enabled() || log.String() != "" {
		t.Fatalf("a writer with no address is enabled=%v log=%q", w.Enabled(), log.String())
	}
}

// TestWriterResolvesTheAddressFromTheEnvironment: the flag wins, then NOVA_REDIS_ADDR, then
// NOVA_REDIS_HOST:NOVA_REDIS_PORT. A host with no port resolves to NOTHING rather than to a
// guessed port, which is the same refusal-to-guess in a third place.
func TestWriterResolvesTheAddressFromTheEnvironment(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		flag string
		vars map[string]string
		want string
	}{
		{"the flag wins", "flag.invalid:1", map[string]string{AddrEnv: "env.invalid:2", HostEnv: "h.invalid", PortEnv: "3"}, "flag.invalid:1"},
		{"then NOVA_REDIS_ADDR", "", map[string]string{AddrEnv: "env.invalid:2", HostEnv: "h.invalid", PortEnv: "3"}, "env.invalid:2"},
		{"then host and port", "", map[string]string{HostEnv: "h.invalid", PortEnv: "3"}, "h.invalid:3"},
		{"a host with no port is nothing", "", map[string]string{HostEnv: "h.invalid"}, ""},
		{"a port with no host is nothing", "", map[string]string{PortEnv: "3"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vars := map[string]string{DefaultPasswordEnv: "secret"}
			for k, v := range tc.vars {
				vars[k] = v
			}
			var seen Dial
			w := OpenWriter(context.Background(), WriterOptions{
				Addr: tc.flag, Lookup: env(vars),
				Dial: fakeDial(NewFakeStream(), nil, &seen),
			})
			defer w.Close()
			if tc.want == "" {
				if w.Enabled() {
					t.Fatalf("%s: the writer is enabled at addr %q", tc.name, seen.Addr)
				}
				return
			}
			if seen.Addr != tc.want {
				t.Errorf("addr is %q, want %q", seen.Addr, tc.want)
			}
			if seen.Password != "secret" {
				t.Errorf("the password did not reach the dial")
			}
			if seen.Username != DefaultUser {
				t.Errorf("user is %q, want the fleet's %q", seen.Username, DefaultUser)
			}
		})
	}
}

// TestWriterSurvivesADialThatRefuses: a store that cannot be reached is ONE line and a
// writer that emits nothing, never a refusal the caller has to handle.
func TestWriterSurvivesADialThatRefuses(t *testing.T) {
	t.Parallel()

	var log strings.Builder
	w := OpenWriter(context.Background(), WriterOptions{
		Log:    &log,
		Lookup: env(map[string]string{DefaultPasswordEnv: "secret", AddrEnv: "down.invalid:6380"}),
		Dial:   fakeDial(nil, errors.New("redis at down.invalid:6380: dial tcp: connection refused"), nil),
	})
	defer w.Close()
	if w.Enabled() {
		t.Fatal("a writer whose dial failed reports itself enabled")
	}
	w.Send(context.Background(), Event{Label: "c1", Kind: OK})
	if n := strings.Count(strings.TrimSpace(log.String()), "\n"); n != 0 {
		t.Fatalf("a dial that failed wrote %d extra lines; the contract is one:\n%s", n, log.String())
	}
	if !strings.Contains(log.String(), "EVENT STORE UNAVAILABLE") {
		t.Fatalf("the one line does not name the store being unavailable: %q", log.String())
	}
	if strings.Contains(log.String(), "secret") {
		t.Fatal("the log carried the password")
	}
}

// TestSendSurvivesAStoreThatErrors IS THE CONTRACT: a store that refuses every entry costs
// one line per entry and nothing else. Send returns no error BY CONSTRUCTION -- there is no
// value a caller could accidentally ignore -- and the caller's own work is untouched.
func TestSendSurvivesAStoreThatErrors(t *testing.T) {
	t.Parallel()

	fake := NewFakeStream()
	fake.FailEmit = errors.New("LOADING Redis is loading the dataset in memory")
	var log strings.Builder
	w := OpenWriter(context.Background(), WriterOptions{
		Log:    &log,
		Lookup: env(map[string]string{DefaultPasswordEnv: "secret", AddrEnv: "store.invalid:6380"}),
		Dial:   fakeDial(fake, nil, nil),
	})
	defer w.Close()
	if !w.Enabled() {
		t.Fatal("the writer did not open against the fake store")
	}
	for i := 0; i < 3; i++ {
		w.Send(context.Background(), Event{Label: "card-1", Kind: OK, TokensIn: Int64(10), TokensOut: Int64(2), USD: Float64(0.01)})
	}
	if fake.Len() != 0 {
		t.Fatalf("a store that errors kept %d entries", fake.Len())
	}
	lines := strings.Split(strings.TrimSpace(log.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("3 refused entries wrote %d lines, want one each:\n%s", len(lines), log.String())
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "EVENT SKIPPED label=card-1 event=ok") {
			t.Errorf("the line does not name the entry it skipped: %q", l)
		}
		if strings.Contains(l, "\n") {
			t.Errorf("a skip line is not one line: %q", l)
		}
	}
}

// TestSendSurvivesAnEntryTheDoorRefuses: Validate is a door (events.go), and an entry it
// refuses is the writer's problem, not the caller's. A field of payload size never reaches
// the store and never fails the card that produced it.
func TestSendSurvivesAnEntryTheDoorRefuses(t *testing.T) {
	t.Parallel()

	fake := NewFakeStream()
	var log strings.Builder
	w := OpenWriter(context.Background(), WriterOptions{
		Log:    &log,
		Lookup: env(map[string]string{DefaultPasswordEnv: "secret", AddrEnv: "store.invalid:6380"}),
		Dial:   fakeDial(fake, nil, nil),
	})
	defer w.Close()
	w.Send(context.Background(), Event{Label: "card-1", Kind: Kind("diff"), Model: strings.Repeat("x", maxFieldBytes+1)})
	if fake.Len() != 0 {
		t.Fatalf("a refused entry reached the store")
	}
	if !strings.Contains(log.String(), "EVENT SKIPPED label=card-1") {
		t.Fatalf("a refused entry wrote no line: %q", log.String())
	}
	// And a good entry after it still lands: one refusal does not disable the writer.
	w.Send(context.Background(), Event{Label: "card-2", Kind: OK})
	if fake.Len() != 1 {
		t.Fatalf("the store holds %d entries after a refusal then a good entry, want 1", fake.Len())
	}
}

// TestSendIsSafeOnANilWriter: the call sites write `w.Send(ctx, e)` with no guard, so a nil
// writer -- a caller that never opened one -- must be a no-op and not a panic. This is the
// property that makes "an emit never fails the card" mechanical rather than a convention
// every new call site has to remember.
func TestSendIsSafeOnANilWriter(t *testing.T) {
	t.Parallel()

	var w *Writer
	w.Send(context.Background(), Event{Label: "c1", Kind: OK})
	w.Close()
	if w.Enabled() {
		t.Fatal("a nil writer reports itself enabled")
	}
	if w.StreamName() != "cards:done" {
		t.Fatalf("a nil writer names stream %q, want cards:done", w.StreamName())
	}
}

// TestSendWritesTheEntryWhenTheStoreIsUp is the positive control this file owes: without it
// every assertion above would pass on a writer that silently does nothing at all.
func TestSendWritesTheEntryWhenTheStoreIsUp(t *testing.T) {
	t.Parallel()

	fake := NewFakeStream()
	fake.Now = func() time.Time { return time.Date(2026, 9, 22, 14, 0, 0, 0, time.UTC) }
	w := OpenWriter(context.Background(), WriterOptions{
		Lookup: env(map[string]string{DefaultPasswordEnv: "secret", AddrEnv: "store.invalid:6380"}),
		Dial:   fakeDial(fake, nil, nil),
	})
	defer w.Close()
	w.Send(context.Background(), Event{
		Label: "card-1", Bench: "hulk", Model: "opencode/deepseek-v4-flash", Route: "opencode",
		Kind: OK, TokensIn: Int64(1200), TokensOut: Int64(340), USD: Float64(0.07), Attempt: 2,
	})
	if fake.Len() != 1 {
		t.Fatalf("the store holds %d entries, want 1", fake.Len())
	}
	got, err := fake.Range(context.Background(), "-", 10)
	if err != nil {
		t.Fatal(err)
	}
	e, err := FromFields(got[0].Fields)
	if err != nil {
		t.Fatalf("the entry does not read back: %v", err)
	}
	if e.Label != "card-1" || e.Kind != OK || e.TokensIn == nil || *e.TokensIn != 1200 ||
		e.TokensOut == nil || *e.TokensOut != 340 || e.USD == nil || *e.USD != 0.07 {
		t.Fatalf("the entry read back as %+v", e)
	}
	if e.Bench != "hulk" || e.Route != "opencode" || e.Attempt != 2 {
		t.Fatalf("the entry lost a field: %+v", e)
	}
	if e.At.IsZero() {
		t.Fatal("the entry carries no stamp")
	}
}

// TestTheSkipLineIsOneLineEvenWhenTheStoreIsNot: everything `note` renders comes from
// outside this process -- a store's error text, a label a card named itself. A Redis error
// carrying a newline would otherwise split ONE skipped entry into two log lines, and a
// reader counting EVENT SKIPPED lines would count one failure twice.
func TestTheSkipLineIsOneLineEvenWhenTheStoreIsNot(t *testing.T) {
	t.Parallel()

	fake := NewFakeStream()
	fake.FailEmit = errors.New("ERR the store said\nsomething\twith\nnewlines in it")
	var log strings.Builder
	w := OpenWriter(context.Background(), WriterOptions{
		Log:    &log,
		Lookup: env(map[string]string{DefaultPasswordEnv: "secret", AddrEnv: "store.invalid:6380"}),
		Dial:   fakeDial(fake, nil, nil),
	})
	defer w.Close()
	w.Send(context.Background(), Event{Label: "card-1", Kind: OK})
	got := strings.TrimSuffix(log.String(), "\n")
	if strings.ContainsAny(got, "\n\t") {
		t.Fatalf("one skipped entry wrote more than one line: %q", got)
	}
	if !strings.HasPrefix(got, "EVENT SKIPPED label=card-1 event=ok") {
		t.Fatalf("the escaped line lost its shape: %q", got)
	}
}

// TestTheWritersWriteTheCardsDoneKey locks in the KEY the card path writes (Johnny's HOLD on
// #2619). Neither cmdNative nor cmdHarvest sets WriterOptions.Stream, so the key is the
// default Open resolves. This drives the real RedisStore -- Open, then XADD -- against
// miniredis, and asserts the entry is on `cards:done` and `ev:cards` was never created.
func TestTheWritersWriteTheCardsDoneKey(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	var log strings.Builder
	w := OpenWriter(context.Background(), WriterOptions{
		Addr:   mr.Addr(),
		Log:    &log,
		Lookup: env(map[string]string{DefaultPasswordEnv: "unused"}),
		Dial: func(ctx context.Context, d Dial) (Store, error) {
			d.Username, d.Password = "", "" // miniredis runs without an ACL
			return Open(ctx, d)
		},
	})
	defer w.Close()
	if w.StreamName() != "cards:done" {
		t.Fatalf("the writer names stream %q, want cards:done", w.StreamName())
	}
	w.Send(context.Background(), Event{Label: "card-1", Kind: OK})
	if log.String() != "" {
		t.Fatalf("the send said: %s", log.String())
	}
	if !mr.Exists("cards:done") {
		t.Fatalf("no cards:done key after a send; keys are %v", mr.Keys())
	}
	if mr.Exists("ev:cards") {
		t.Fatal("a send created ev:cards; there is one stream and it is cards:done")
	}
	entries, err := mr.Stream("cards:done")
	if err != nil || len(entries) != 1 {
		t.Fatalf("cards:done holds %d entries (%v), want 1", len(entries), err)
	}
	for i := 0; i+1 < len(entries[0].Values); i += 2 {
		if name := entries[0].Values[i]; name == "usd" || name == "tokens_in" || name == "tokens_out" {
			t.Errorf("an event with no cost wrote %s=%s; an absent cost is no field", name, entries[0].Values[i+1])
		}
	}
}
