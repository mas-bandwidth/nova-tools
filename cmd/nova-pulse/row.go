package main

// `nova-pulse row` at the command line (nova-tools #2561): this bench's swarm-table row,
// pushed into the fleet Redis every --interval.
//
// EVERY PATH IS A FLAG WITH A DEFAULT UNDER --home. bin/bench-row carried one session's
// queue directory in its source -- $HOME/rowan-working/tmp/session-0919b/queue/pull/bench/$H
// -- and a verb that did the same would freeze the scripts it exists to retire. The
// defaults are the bench standard's own layout; a bench laid out differently passes the
// flags and is not a special case in this file.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

func cmdRow(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("row")
	store := f.fs.String("store", "", "")
	user := f.fs.String("store-user", pulse.DefaultStoreUser, "")
	passEnv := f.fs.String("password-env", pulse.DefaultStorePasswordEnv, "")
	intervalRaw := f.fs.String("interval", "1s", "")
	host := f.fs.String("host", "", "")
	home := f.fs.String("home", "", "")
	queue := f.fs.String("queue", "", "")
	slots := f.fs.String("slots", "", "")
	results := f.fs.String("results", "", "")
	roots := f.fs.String("roots", "", "")
	since := f.fs.String("since", "", "")
	textfile := f.fs.String("textfile", "", "")
	once := f.fs.Bool("once", false, "")
	printOnly := f.fs.Bool("print", false, "")

	if !f.parse(args, stderr) {
		return 2
	}
	// --print measures and prints without a store, so --store is not wanted there: it is
	// the flag a bench operator reaches for when the table says 0 done and they want to
	// know which of the four readers answered nothing.
	if !*printOnly {
		f.want(*store, "store", "the fleet Redis this bench pushes its row into, as host:port")
	}

	interval, err := pulse.ParsePollInterval(*intervalRaw)
	if err != nil {
		f.add(fmt.Sprintf("--interval %s", err))
	}
	// A push interval at or past the row's own TTL is a bench that flickers on and off the
	// table. It is refused rather than clamped: a flag that silently means something else
	// is how a table nobody trusts gets built.
	if err == nil && interval >= pulse.BenchRowTTL {
		f.add(fmt.Sprintf("--interval %s is at or past the row's %s TTL, so this bench would vanish from the table between pushes",
			interval, pulse.BenchRowTTL))
	}

	h := strings.TrimSpace(*home)
	if h == "" {
		h, _ = os.UserHomeDir()
	}
	name := strings.ToLower(strings.TrimSpace(*host))
	if name == "" {
		hn, herr := os.Hostname()
		if herr != nil {
			f.add(fmt.Sprintf("--host was not given and this machine's hostname could not be read: %s", oneline.Err(herr)))
		}
		name = strings.ToLower(shortHostname(hn))
	}
	if f.refused(stderr) {
		return 2
	}

	nb := filepath.Join(h, "nova-bench")
	in := pulse.BenchRowInput{
		Store:    pulse.StoreOptions{Addr: *store, User: *user, PasswordEnv: *passEnv},
		Host:     name,
		Interval: interval,
		Once:     *once,
		Print:    *printOnly,
		Queue:    orDefault(*queue, ""),
		Slots:    orDefault(*slots, filepath.Join(nb, "slots")),
		Results:  orDefault(*results, filepath.Join(nb, "results")),
		Roots:    splitRoots(*roots, []string{filepath.Join(h, "rowan-working", "tmp"), filepath.Join(nb, "swarm-root")}),
		Since:    orDefault(*since, filepath.Join(nb, "SPRINT-START")),
		Textfile: *textfile,
		Stdout:   stdout,
		Stderr:   stderr,
		Now:      func() time.Time { return time.Now().UTC() },
	}
	_ = now // row reads the clock each tick; main's fixed instant would stamp every row alike
	return pulse.Row(in)
}

// shortHostname is `hostname -s`: the name up to the first dot.
func shortHostname(h string) string {
	if i := strings.IndexByte(h, '.'); i >= 0 {
		return h[:i]
	}
	return h
}

func orDefault(given, fallback string) string {
	if strings.TrimSpace(given) != "" {
		return given
	}
	return fallback
}

func splitRoots(given string, fallback []string) []string {
	if strings.TrimSpace(given) == "" {
		return fallback
	}
	var out []string
	for _, r := range strings.Split(given, ",") {
		if r = strings.TrimSpace(r); r != "" {
			out = append(out, r)
		}
	}
	return out
}
