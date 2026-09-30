package events

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// EmitInput is `nova-pulse event`: one XADD, one line, for the bash writers -- the
// launcher, harvest and the lander -- until they are Go.
type EmitInput struct {
	Addr     string
	Username string
	Password string // read from the environment by the caller; never a flag, never printed
	Stream   string
	Event    Event
	Print    bool // render the entry and write nothing, so a writer can be watched offline
	Now      time.Time
	Timeout  time.Duration
	Stdout   io.Writer
	Stderr   io.Writer
}

// EmitOne validates the event, writes it and prints the receipt. Exit 0 wrote one entry,
// exit 2 could not run.
func EmitOne(in EmitInput) int {
	e := in.Event.Stamp(in.Now)
	if err := e.Validate(); err != nil {
		return fail(in.Stderr, "event", err)
	}
	if in.Print {
		fields := e.Fields()
		for _, name := range fieldNames {
			if v, ok := fields[name]; ok {
				fmt.Fprintf(in.Stdout, "%s\t%s\n", name, v)
			}
		}
		fmt.Fprintln(in.Stdout, e.Line(streamOr(in.Stream), "(not written: --print)"))
		return 0
	}
	if strings.TrimSpace(in.Addr) == "" {
		return fail(in.Stderr, "event", fmt.Errorf("--store is required; it wants the fleet Redis as host:port; refusing to guess"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeoutOr(in.Timeout))
	defer cancel()
	store, err := Open(ctx, Dial{Addr: in.Addr, Username: in.Username, Password: in.Password, Stream: in.Stream})
	if err != nil {
		return fail(in.Stderr, "event", err)
	}
	// ignored: a deferred close after the event is written and its line printed
	defer func() { _ = store.Close() }()
	store.SetClock(func() time.Time { return e.At })
	id, err := store.Emit(ctx, e)
	if err != nil {
		return fail(in.Stderr, "event", err)
	}
	fmt.Fprintln(in.Stdout, e.Line(store.StreamName(), id))
	return 0
}

func streamOr(s string) string {
	if strings.TrimSpace(s) == "" {
		return Stream
	}
	return s
}

func timeoutOr(d time.Duration) time.Duration {
	if d <= 0 {
		return 10 * time.Second
	}
	return d
}

// fail is the one refusal shape: one line naming what was wrong and the door to the usage,
// exit 2 (docs/SPEC.md Conventions).
func fail(stderr io.Writer, verb string, err error) int {
	fmt.Fprintf(stderr, "nova-pulse %s: %s; run: nova-pulse help\n", verb, oneline.Escape(err.Error()))
	return 2
}
