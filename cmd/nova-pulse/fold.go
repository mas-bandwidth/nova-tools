package main

// The fold verb's flags: the one consumer that turns cards:done into the SQLite record
// (nova-tools #2563). The queries live here and never in Redis (Johnny, section 8 of
// reports/redis-for-nova-tools-2026-09-21.md), so the hot store stays a queue.

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/events"
)

func cmdFold(args []string, stdout, stderr io.Writer) int {
	f := newFlags("fold")
	store := f.fs.String("store", "", "")
	user := f.fs.String("user", "", "")
	passwordEnv := f.fs.String("password-env", defaultPasswordEnv, "")
	stream := f.fs.String("stream", events.Stream, "")
	group := f.fs.String("group", events.Group, "")
	consumer := f.fs.String("consumer", "", "")
	db := f.fs.String("db", "", "")
	interval := f.fs.Duration("interval", time.Second, "")
	count := f.fs.Int("count", 100, "")
	timeout := f.fs.Int("timeout", 10, "")
	max := f.fs.Int("max", 20, "")
	once := f.fs.Bool("once", false, "")
	rebuild := f.fs.Bool("rebuild", false, "")
	initOnly := f.fs.Bool("init", false, "")
	report := f.fs.Bool("report", false, "")
	dump := f.fs.Bool("dump", false, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*db, "db", "the SQLite file the stream folds into, the record the views are read from")
	if *interval < time.Millisecond {
		f.add(fmt.Sprintf("--interval wants a duration such as 1s, got %s", *interval))
	}
	if *count < 1 {
		f.add(fmt.Sprintf("--count wants a whole number of entries per read, got %d", *count))
	}
	if *timeout < 1 {
		f.add(fmt.Sprintf("--timeout wants a whole number of seconds, got %d", *timeout))
	}
	if *max < 0 {
		f.add(fmt.Sprintf("--max wants a whole number of rows per block or 0 for no bound, got %d", *max))
	}
	if n := countTrue(*rebuild, *initOnly, *report, *dump); n > 1 {
		f.add("--rebuild, --init, --report and --dump are four different runs; name one")
	}
	if f.refused(stderr) {
		return 2
	}
	return events.FoldRun(events.FoldInput{
		Addr:     *store,
		Username: *user,
		Password: os.Getenv(*passwordEnv),
		Stream:   *stream,
		Group:    *group,
		Consumer: *consumer,
		DB:       *db,
		Interval: *interval,
		Count:    *count,
		Timeout:  time.Duration(*timeout) * time.Second,
		Max:      *max,
		Once:     *once,
		Rebuild:  *rebuild,
		Init:     *initOnly,
		Report:   *report,
		Dump:     *dump,
		Stdout:   stdout,
		Stderr:   stderr,
	})
}

func countTrue(flags ...bool) int {
	n := 0
	for _, b := range flags {
		if b {
			n++
		}
	}
	return n
}
