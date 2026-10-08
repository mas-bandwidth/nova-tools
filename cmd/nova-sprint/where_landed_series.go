package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

func init() {
	for i := range verbs {
		if verbs[i].name == "where" {
			verbs[i].syntax = "[--watch] [--every <duration>] [--all] [--json [--cards] [--rows] [--archived] [--stale <duration>] [--at-epoch <n>]: includes landedSeries] [--release [<name>]]"
			orig := verbs[i].run
			verbs[i].run = func(a *app, args []string, stdout, stderr io.Writer) int {
				// where's own flags are parsed here once, every one of them, so --redis
				// and --at-epoch are read whatever comes before them (where --json --cards
				// --redis <addr>, the dashboard's call)
				c, atEpoch, _, err := whereSeriesFlags(a, args)
				if err != nil {
					if hasJSONWord(args) {
						// never a series from a store or epoch the caller did not name
						return refuse(stderr, "where", argErr("takes no words ", err))
					}
					return orig(a, args, stdout, stderr)
				}
				if !c.json {
					return orig(a, args, stdout, stderr)
				}
				w := &whereJSONWriter{a: a, c: *c, atEpoch: atEpoch, out: stdout, stderr: stderr}
				code := orig(a, args, w, stderr)
				w.flush()
				return code
			}
		}
	}
}

// whereSeriesFlags parses where's words on a flag set holding every flag of
// where (cmdWhere), and returns the store and epoch they name. Its list is
// pinned to where's own by TestWhereSeriesFlagsAreWheresFlags.
func whereSeriesFlags(a *app, args []string) (*common, int64, flagSet, error) {
	fs, c := a.verbSetup("where")
	fs.Bool("watch", false, "redraw in place every --every until interrupted")
	fs.Duration("every", time.Second, "the redraw interval with --watch, above 0")
	fs.Bool("all", false, "draw the readers and merge tables too")
	fs.Bool("cards", false, "with --json: also every dealt work card, judgment and lane")
	fs.Bool("archived", false, "with --json: the archived streams' rows of the work and merge tables in tables, and their primaries in --rows, beside the live ones (stream archive); their counts are in the footers and the summary either way")
	fs.Bool("rows", false, "with --json: also every primary's row of the work table")
	fs.Bool("fresh", false, "with --json: read the store now, never the sprint's server's last tick's document")
	fs.Duration("stale", defaultStale, "a stream with no progress for longer is shown stalled (--json)")
	atEpoch := fs.Int64("at-epoch", -1, "the sprint as it was at an earlier epoch (before a clear); the series is that epoch's")
	var rel releaseFlag
	fs.Var(&rel, "release", "show cards left per release, or for the named release")
	_, err := parse(fs, args) // words are where's to refuse, not ours
	return c, *atEpoch, fs, err
}

func hasJSONWord(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "--json" || arg == "-json" || strings.HasPrefix(arg, "--json=") || strings.HasPrefix(arg, "-json=") {
			return true
		}
	}
	return false
}

type whereJSONWriter struct {
	a       *app
	c       common
	atEpoch int64
	out     io.Writer
	stderr  io.Writer
	buf     bytes.Buffer
}

func (w *whereJSONWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		line, err := w.buf.ReadString('\n')
		if err != nil {
			w.buf.WriteString(line)
			break
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			fmt.Fprintln(w.out)
			continue
		}
		enriched := w.enrich(trimmed)
		fmt.Fprintln(w.out, enriched)
	}
	return len(p), nil
}

func (w *whereJSONWriter) flush() {
	if w.buf.Len() == 0 {
		return
	}
	trimmed := strings.TrimRight(w.buf.String(), "\r\n")
	w.buf.Reset()
	if trimmed == "" {
		return
	}
	enriched := w.enrich(trimmed)
	fmt.Fprintln(w.out, enriched)
}

// enrich adds landedSeries to a frame of where --json: the series of the store and
// epoch where's own flags named, at the frame's time. A frame that already carries
// one (drawn by the sprint's server) is left as it is.
func (w *whereJSONWriter) enrich(line string) string {
	var raw map[string]any
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return line
	}
	if _, ok := raw["tables"]; !ok {
		return line
	}
	if _, ok := raw["landedSeries"]; ok {
		return line
	}

	ctx := context.Background()
	st, err := w.a.storeAtCtx(ctx, w.c, w.atEpoch)
	if err != nil || st == nil {
		w.omitted(err)
		return line
	}

	now := w.a.now()
	if atStr, ok := raw["at"].(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, atStr); err == nil {
			now = t
		}
	}

	series, err := sprint.LandedSeriesFrom(ctx, st, now)
	if err != nil {
		w.omitted(err)
		return line
	}

	raw["landedSeries"] = series
	b, err := json.Marshal(raw)
	if err != nil {
		w.omitted(err)
		return line
	}
	return string(b)
}

// omitted says on stderr why a frame went out without landedSeries: the frame
// itself is where's and is never held back.
func (w *whereJSONWriter) omitted(err error) {
	if err == nil {
		err = fmt.Errorf("no store")
	}
	fmt.Fprintf(w.stderr, "%s where: landedSeries omitted: %s\n", prog, err)
}
