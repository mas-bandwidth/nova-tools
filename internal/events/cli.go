package events

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
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
	defer func() { _ = store.Close() }()
	store.SetClock(func() time.Time { return e.At })
	id, err := store.Emit(ctx, e)
	if err != nil {
		return fail(in.Stderr, "event", err)
	}
	fmt.Fprintln(in.Stdout, e.Line(store.StreamName(), id))
	return 0
}

// FoldInput is `nova-pulse fold`: the stream's one consumer, its replay and its two offline
// readers (--init and --report).
type FoldInput struct {
	Addr     string
	Username string
	Password string
	Stream   string
	Group    string
	Consumer string
	DB       string
	Interval time.Duration
	Count    int
	Timeout  time.Duration
	Max      int

	Once    bool // one pass, then exit; the CI-shaped form
	Rebuild bool // replay the whole stream into a file that does not exist yet
	Init    bool // apply the schema and exit, with no store
	Report  bool // print the views of an existing file, with no store
	Dump    bool // print every row, deterministically, with no store

	Stdout io.Writer
	Stderr io.Writer
}

// FoldRun is the verb. Exit 0 is a pass that ran, exit 2 is could not run.
func FoldRun(in FoldInput) int {
	if strings.TrimSpace(in.DB) == "" {
		return fail(in.Stderr, "fold", fmt.Errorf("--db is required; it wants the SQLite file the stream folds into; refusing to guess"))
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if in.Rebuild {
		if _, err := os.Stat(in.DB); err == nil {
			return fail(in.Stderr, "fold", fmt.Errorf("--rebuild replays the whole stream into a FRESH file and %s already exists; name a path that does not exist, or move the old file aside", in.DB))
		} else if !os.IsNotExist(err) {
			return fail(in.Stderr, "fold", err)
		}
	}

	db, err := OpenDB(ctx, in.DB)
	if err != nil {
		return fail(in.Stderr, "fold", err)
	}
	defer func() { _ = db.Close() }()

	switch {
	case in.Init:
		fmt.Fprintf(in.Stdout, "FOLD %s schema=%d tables=%s\n", db.Path(), SchemaVersion, Tables)
		return 0
	case in.Dump:
		if err := db.Dump(ctx, in.Stdout); err != nil {
			return fail(in.Stderr, "fold", err)
		}
		return 0
	case in.Report:
		if err := db.Report(ctx, in.Stdout, in.Max); err != nil {
			return fail(in.Stderr, "fold", err)
		}
		return 0
	}

	if strings.TrimSpace(in.Addr) == "" {
		return fail(in.Stderr, "fold", fmt.Errorf("--store is required; it wants the fleet Redis as host:port; refusing to guess (--init, --report and --dump read the file alone)"))
	}
	dialCtx, dialCancel := context.WithTimeout(ctx, timeoutOr(in.Timeout))
	store, err := Open(dialCtx, Dial{Addr: in.Addr, Username: in.Username, Password: in.Password, Stream: in.Stream})
	dialCancel()
	if err != nil {
		return fail(in.Stderr, "fold", err)
	}
	defer func() { _ = store.Close() }()

	if in.Rebuild {
		stats, err := Rebuild(ctx, store, db, in.Count)
		if err != nil {
			return fail(in.Stderr, "fold", err)
		}
		fmt.Fprintf(in.Stdout, "FOLD REBUILT %s stream=%s read=%d new=%d skipped=%d\n",
			db.Path(), store.StreamName(), stats.Read, stats.Inserted, stats.Skipped)
		return 0
	}

	folder := &Folder{
		Reader:   store,
		DB:       db,
		Group:    in.Group,
		Consumer: in.Consumer,
		Count:    in.Count,
		Block:    in.Interval,
	}
	if err := folder.Start(ctx); err != nil {
		return fail(in.Stderr, "fold", err)
	}
	if in.Once {
		stats, err := folder.Once(ctx)
		if err != nil {
			return fail(in.Stderr, "fold", err)
		}
		fmt.Fprintln(in.Stdout, stats.Line(db.Path(), store.StreamName(), folder.group()))
		return 0
	}
	stats, err := folder.Run(ctx, in.Interval, in.Stdout, store.StreamName())
	if err != nil {
		return fail(in.Stderr, "fold", err)
	}
	fmt.Fprintf(in.Stdout, "FOLD END %s stream=%s group=%s read=%d new=%d acked=%d skipped=%d\n",
		db.Path(), store.StreamName(), folder.group(), stats.Read, stats.Inserted, stats.Acked, stats.Skipped)
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
