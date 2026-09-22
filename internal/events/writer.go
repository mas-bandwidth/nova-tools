package events

// Writer is the CARD PATH's half of the stream, and its whole contract is one sentence:
// AN EMIT MAY NEVER FAIL THE WORK IT IS MEASURING (nova-tools #2563 item 1). A card that
// ran, a branch that was pushed and a pull request that was opened are facts on disk and on
// GitHub; the entry that says so is a measurement. When the store is down, the password is
// absent, the address is unset or the entry is refused at Validate's door, this writer says
// so in ONE line on the caller's own error stream and the caller carries on exactly as it
// did before the writer existed.
//
// That is why every method is safe on a nil *Writer: the call sites are `w.Send(ctx, e)`
// with no `if`, so there is no path where forgetting a guard turns a measurement into an
// outage.
//
// HOW IT IS TURNED ON. The store is named by the caller's `--events-store` flag, or, when
// that is empty, by the environment: NOVA_REDIS_ADDR, else NOVA_REDIS_HOST:NOVA_REDIS_PORT.
// The password is NEVER a flag and never an argument -- it is read from the variable
// `nova-secrets exec --only NOVA_REDIS_BENCH_PASSWORD` put it in, and nothing here prints
// it. No address or no password is SILENCE, not a warning: a bench that was never given the
// store's password must still run cards, and a line per card saying so is noise on every
// machine that does not have it yet.
//
// NO ADDRESS IS GUESSED. `nova-pulse event` refuses a missing --store rather than assume a
// host, and this writer holds the same line by disabling itself instead of dialling a
// default. The fleet's address reaches it from the environment the launcher and the ansible
// converge set, which is also AGENTS.md rule 2's shape: the host lives in configuration.

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The environment this writer reads. The password variable is the one
// `nova-secrets exec --only NOVA_REDIS_BENCH_PASSWORD` fills; the address is configuration,
// never a constant in this repository.
const (
	DefaultPasswordEnv = "NOVA_REDIS_BENCH_PASSWORD"
	AddrEnv            = "NOVA_REDIS_ADDR"
	HostEnv            = "NOVA_REDIS_HOST"
	PortEnv            = "NOVA_REDIS_PORT"
)

// DefaultUser is the fleet's ACL user for the card path: it holds XADD on `ev:*` and
// nothing else. The fold reads under its own user.
const DefaultUser = "bench"

// WriterOptions is how a verb turns the stream on. Every seam a test needs is here, so no
// test of a card path opens a connection (AGENTS.md rule 2).
type WriterOptions struct {
	Addr        string // --events-store; "" resolves from the environment
	Username    string // "" is DefaultUser
	PasswordEnv string // "" is DefaultPasswordEnv
	Stream      string // "" is Stream
	Timeout     time.Duration
	Log         io.Writer // where the one line per failure goes; nil discards it

	// Lookup is the environment. nil is os.Getenv, and a test passes a map.
	Lookup func(string) string
	// Dial opens the store. nil is the Redis one, and a test passes a fake -- including
	// a fake that ERRORS, which is the only way to prove the caller survives it.
	Dial func(ctx context.Context, d Dial) (Store, error)
}

// Writer holds an open store, or nothing at all. A disabled writer is not an error: it is
// the shape every bench without the store's password runs in.
type Writer struct {
	store  Store
	log    io.Writer
	stream string
}

// OpenWriter resolves the configuration and dials, and NEVER returns an error: a writer it
// could not open is a writer that emits nothing. A dial that failed says so once, here,
// rather than once per card.
func OpenWriter(ctx context.Context, opt WriterOptions) *Writer {
	lookup := opt.Lookup
	if lookup == nil {
		lookup = osGetenv
	}
	w := &Writer{log: opt.Log, stream: streamOr(opt.Stream)}

	passwordEnv := opt.PasswordEnv
	if passwordEnv == "" {
		passwordEnv = DefaultPasswordEnv
	}
	password := strings.TrimSpace(lookup(passwordEnv))
	addr := addrFrom(opt.Addr, lookup)
	// SILENCE, NOT A WARNING. A bench with no password or no address is the normal state
	// of a machine the store has not reached yet, and it must still run cards.
	if password == "" || addr == "" {
		return w
	}

	user := opt.Username
	if user == "" {
		user = DefaultUser
	}
	dial := opt.Dial
	if dial == nil {
		dial = dialRedis
	}
	timeout := timeoutOr(opt.Timeout)
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	store, err := dial(dialCtx, Dial{Addr: addr, Username: user, Password: password, Stream: opt.Stream})
	if err != nil {
		// The address is named and the password never is: Open's own error carries the
		// address alone, and this line adds nothing to it.
		w.note("EVENT STORE UNAVAILABLE %s; this run emits no events and is otherwise unchanged", err)
		return w
	}
	w.store = store
	return w
}

// Enabled reports whether an entry would actually be written. It exists for a receipt line,
// never as a guard a caller has to remember: Send is safe either way.
func (w *Writer) Enabled() bool { return w != nil && w.store != nil }

// StreamName is the stream entries land on, for a receipt.
func (w *Writer) StreamName() string {
	if w == nil {
		return Stream
	}
	return w.stream
}

// Send writes one entry and returns NOTHING. A refused entry, a store that is down and a
// context that was cancelled are all one line on the log and a return: the card, the
// harvest or the landing that called this is already done and is not undone by a
// measurement. There is no error to ignore, because there is no error to return.
func (w *Writer) Send(ctx context.Context, e Event) {
	if !w.Enabled() {
		return
	}
	if _, err := w.store.Emit(ctx, e); err != nil {
		w.note("EVENT SKIPPED label=%s event=%s: %s", e.Label, string(e.Kind), err)
		return
	}
}

// Close releases the store. It is safe on a nil or disabled writer.
func (w *Writer) Close() {
	if w == nil || w.store == nil {
		return
	}
	if err := w.store.Close(); err != nil {
		w.note("EVENT STORE CLOSE: %s", err)
	}
	w.store = nil
}

// note is the one line, and it is ONE LINE MECHANICALLY. Everything it renders comes from
// outside this process -- a store's error text, a label a card named itself -- so the whole
// rendered message goes through oneline.Escape before it is written. Without that, a Redis
// error carrying a newline would split one skip into two log lines and a reader counting
// them would count a failure twice.
//
// It never carries a password: nothing here holds one after OpenWriter returns, and Open's
// own error names the address alone.
func (w *Writer) note(format string, args ...any) {
	if w == nil || w.log == nil {
		return
	}
	fmt.Fprintln(w.log, oneline.Escape(fmt.Sprintf(format, args...)))
}

// addrFrom resolves the store's address: the flag, else NOVA_REDIS_ADDR, else
// NOVA_REDIS_HOST:NOVA_REDIS_PORT. A host with no port is incomplete and resolves to
// nothing rather than to a guessed port.
func addrFrom(flag string, lookup func(string) string) string {
	if v := strings.TrimSpace(flag); v != "" {
		return v
	}
	if v := strings.TrimSpace(lookup(AddrEnv)); v != "" {
		return v
	}
	host := strings.TrimSpace(lookup(HostEnv))
	port := strings.TrimSpace(lookup(PortEnv))
	if host == "" || port == "" {
		return ""
	}
	return host + ":" + port
}

func dialRedis(ctx context.Context, d Dial) (Store, error) { return Open(ctx, d) }

// osGetenv is the shipped environment, named so the default is visible beside the seam.
var osGetenv = func(name string) string { return os.Getenv(name) }
