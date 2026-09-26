// The ws index verbs (nova-tools #3662, #3659, #3660; contract: rowan-new
// specs/ws-index.md): `ws counts|checkpoint|show`, `scope
// keep|park|unpark|ls` and `stream ls|order|rename`. Each is one call into
// internal/nsprint/ws, which is one FCALL of a fn/lua/ws.lua function (scope
// park/keep first write a checkpoint, a pipelined read). Each prints one
// receipt line with its measured ms (the list verbs print their rows first)
// and exits 0 done, 1 refused (REFUSED <why>, nothing written), 2 could not
// run. A verb over one second on 1,000 tasks is a bug (ws_test.go).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

func init() {
	register(Verb{Name: "ws", Summary: "the ws index: counts, checkpoint to TSV, show --order (every stream's cards with their DEPENDS-ON edges and sentinel)", Run: runWS})
	register(Verb{Name: "scope", Summary: "keep, park, unpark or list the streams in the sprint's scope", Run: runScope})
	register(Verb{Name: "stream", Summary: "list, order or rename the work streams; open, rebase, pr, status or close a stream branch", Run: runStream})
}

// countsNow is the instant ws counts and the table's one-shot read for (the
// ETA's hour of ws:log); a test pins it.
var countsNow = time.Now

// wsStdin is what `--ids @-` reads; a test replaces it.
var wsStdin io.Reader = os.Stdin

// wsCmd is one ws/scope/stream subverb's parsed common flags and its store.
type wsCmd struct {
	name  string
	fs    *flag.FlagSet
	redis *string
	by    *string
	why   *string
	out   io.Writer
	err   io.Writer
	start time.Time
}

func newWSCmd(name string, out, errOut io.Writer) *wsCmd {
	fs := verbflag.New(name)
	by := os.Getenv("USER")
	if by == "" {
		by = "nova-sprint"
	}
	return &wsCmd{
		name:  name,
		fs:    fs,
		redis: fs.String("redis", redisDefault("NOVA_SPRINT_REDIS"), ""),
		by:    fs.String("as", by, ""),
		why:   fs.String("why", "", ""),
		out:   out,
		err:   errOut,
	}
}

// parse parses args and wants exactly nargs positional arguments (-1: any).
func (w *wsCmd) parse(args []string, nargs int, want string) ([]string, int, bool) {
	if err := w.fs.Parse(args); err != nil {
		return nil, refuse(w.err, w.name, err.Error()+"; want "+want), false
	}
	rest := w.fs.Args()
	if nargs >= 0 && len(rest) != nargs {
		return nil, refuse(w.err, w.name, "want "+want+"; flags precede names"), false
	}
	if *w.redis == "" {
		return nil, refuse(w.err, w.name, "--redis <addr> is required (or NOVA_SPRINT_REDIS)"), false
	}
	return rest, 0, true
}

func (w *wsCmd) open(ctx context.Context) (*store.Store, int, bool) {
	st, err := store.Open(ctx, *w.redis)
	if err != nil {
		return nil, refuse(w.err, w.name, err.Error()), false
	}
	w.start = time.Now()
	return st, 0, true
}

func (w *wsCmd) ms() string {
	return strconv.FormatFloat(float64(time.Since(w.start).Microseconds())/1000, 'f', 1, 64)
}

// done prints the receipt for err: a REFUSED line and exit 1, a failure and
// exit 2, or line and exit 0.
func (w *wsCmd) done(err error, line string) int {
	var r *ws.Refused
	switch {
	case errors.As(err, &r):
		fmt.Fprintf(w.out, "REFUSED %s ms=%s\n", r.Why, w.ms())
		return 1
	case err != nil:
		return refuse(w.err, w.name, err.Error())
	}
	fmt.Fprintf(w.out, "%s ms=%s\n", line, w.ms())
	return 0
}

func subverb(args []string, verb, want string, errOut io.Writer) (string, []string, int, bool) {
	if len(args) == 0 {
		return "", nil, refuse(errOut, verb, "want "+want), false
	}
	return args[0], args[1:], 0, true
}

func runWS(ctx context.Context, args []string, out, errOut io.Writer) int {
	sub, rest, code, ok := subverb(args, "ws", "counts, checkpoint or show", errOut)
	if !ok {
		return code
	}
	switch sub {
	case "counts":
		return runWSCounts(ctx, rest, out, errOut)
	case "checkpoint":
		return runWSCheckpoint(ctx, rest, out, errOut)
	case "show":
		return runWSShow(ctx, rest, out, errOut)
	}
	return refuse(errOut, "ws", "unknown subverb "+sub+"; want counts, checkpoint or show")
}

// runWSShow is `ws show --order [--stream <s>]` (nova-tools #4318): every
// stream's cards in order, one line each, `<where> <id> <- <edges>`, the
// sentinel last; then one receipt. A dependency that is not landed carries
// its set in parentheses, one with no record (no record). --stream shows
// one stream and refuses a name the index does not have, naming the ones it
// has. `stream order --show` is the same listing.
func runWSShow(ctx context.Context, args []string, out, errOut io.Writer) int {
	w := newWSCmd("ws show", out, errOut)
	order := w.fs.Bool("order", false, "")
	stream := w.fs.String("stream", "", "")
	if _, code, ok := w.parse(args, 0, "ws show --redis <addr> --order [--stream <s>]"); !ok {
		return code
	}
	if !*order {
		return refuse(errOut, w.name, "want --order: ws show --redis <addr> --order [--stream <s>] prints every stream's cards in order with their DEPENDS-ON edges")
	}
	st, code, ok := w.open(ctx)
	if !ok {
		return code
	}
	defer st.Close()
	return showOrder(ctx, w, st.Client(), *stream)
}

// showOrder prints the listing and the receipt for ws show --order and
// stream order --show.
func showOrder(ctx context.Context, w *wsCmd, c redis.Cmdable, only string) int {
	rows, err := ws.Show(ctx, c)
	if err != nil {
		return w.done(err, "")
	}
	if only != "" {
		var names []string
		var keep []ws.ShowStream
		for _, r := range rows {
			names = append(names, strconv.Quote(r.Stream))
			if r.Stream == only {
				keep = append(keep, r)
			}
		}
		if len(keep) == 0 {
			return w.done(&ws.Refused{Why: fmt.Sprintf("no stream %s; the index has %s: nova-sprint stream ls --redis <addr>", strconv.Quote(only), strings.Join(names, " "))}, "")
		}
		rows = keep
	}
	cards, edges := 0, 0
	for _, r := range rows {
		fmt.Fprintf(w.out, "STREAM %d %s cards=%d live=%d landed=%d sentinel=%s\n", r.Rank, strconv.Quote(r.Stream), len(r.Cards), r.Live, r.Landed, r.Sentinel)
		for _, card := range r.Cards {
			fmt.Fprintf(w.out, "  %s\n", card.Line(r.Live))
			cards++
			if card.Sentinel {
				edges += r.Live + r.Landed
			} else {
				edges += len(card.Deps)
			}
		}
	}
	return w.done(nil, fmt.Sprintf("SHOW streams=%d cards=%d edges=%d", len(rows), cards, edges))
}

func runWSCounts(ctx context.Context, args []string, out, errOut io.Writer) int {
	w := newWSCmd("ws counts", out, errOut)
	if _, code, ok := w.parse(args, 0, "ws counts --redis <addr>"); !ok {
		return code
	}
	st, code, ok := w.open(ctx)
	if !ok {
		return code
	}
	defer st.Close()
	// The one count (ws.Counts), the numbers sprint status and the table
	// print: the six stream sets per state, parked beside them, the total,
	// done=landed/total, left and the eta.
	c, err := (&ws.CountsReader{}).Read(ctx, st.Client(), countsNow())
	return w.done(err, c.Receipt())
}

func runWSCheckpoint(ctx context.Context, args []string, out, errOut io.Writer) int {
	w := newWSCmd("ws checkpoint", out, errOut)
	path := w.fs.String("out", "", "")
	if _, code, ok := w.parse(args, 0, "ws checkpoint --redis <addr> --out <path>"); !ok {
		return code
	}
	if *path == "" {
		return refuse(errOut, w.name, "--out <path> is required: the TSV the index is written to")
	}
	st, code, ok := w.open(ctx)
	if !ok {
		return code
	}
	defer st.Close()
	r, err := ws.Checkpoint(ctx, st.Client(), *path, time.Now())
	return w.done(err, fmt.Sprintf("CHECKPOINT path=%s streams=%d rows=%d", r.Path, r.Streams, r.Rows))
}

// checkpointFirst is what scope park and keep run before they write: the
// index to --checkpoint, or to a new file in the default directory, whose
// oldest files beyond ws.KeepCheckpoints it then prunes.
func checkpointFirst(ctx context.Context, st *store.Store, path string) (ws.CheckpointResult, error) {
	now := time.Now()
	dir := ""
	if path == "" {
		d, err := ws.DefaultCheckpointDir()
		if err != nil {
			return ws.CheckpointResult{}, err
		}
		dir, path = d, ws.DefaultCheckpointPath(d, now)
	}
	r, err := ws.Checkpoint(ctx, st.Client(), path, now)
	if err != nil {
		return r, err
	}
	if dir != "" {
		if _, err := ws.PruneCheckpoints(dir, ws.KeepCheckpoints); err != nil {
			return r, fmt.Errorf("prune checkpoints in %s: %w", dir, err)
		}
	}
	return r, nil
}

func runScope(ctx context.Context, args []string, out, errOut io.Writer) int {
	sub, rest, code, ok := subverb(args, "scope", "keep, park, unpark or ls", errOut)
	if !ok {
		return code
	}
	switch sub {
	case "keep":
		return runScopeKeep(ctx, rest, out, errOut)
	case "park":
		return runScopePark(ctx, rest, out, errOut)
	case "unpark":
		return runScopeUnpark(ctx, rest, out, errOut)
	case "ls":
		return runScopeLs(ctx, rest, out, errOut)
	}
	return refuse(errOut, "scope", "unknown subverb "+sub+"; want keep, park, unpark or ls")
}

func runScopeKeep(ctx context.Context, args []string, out, errOut io.Writer) int {
	w := newWSCmd("scope keep", out, errOut)
	streams := w.fs.String("streams", "", "")
	cp := w.fs.String("checkpoint", "", "")
	if _, code, ok := w.parse(args, 0, `scope keep --redis <addr> --streams "<a>|<b>" [--checkpoint <path>]`); !ok {
		return code
	}
	names := ws.ParseStreams(*streams)
	if len(names) == 0 {
		return refuse(errOut, w.name, `--streams "<a>|<b>" names the streams to keep; every other stream is parked`)
	}
	st, code, ok := w.open(ctx)
	if !ok {
		return code
	}
	defer st.Close()
	c, err := checkpointFirst(ctx, st, *cp)
	if err != nil {
		return refuse(errOut, w.name, "checkpoint first: "+err.Error())
	}
	r, err := ws.Keep(ctx, st.Client(), *w.by, whyOr(*w.why, "scope keep"), names)
	return w.done(err, fmt.Sprintf("KEPT streams=%d parked_streams=%d parked=%d checkpoint=%s rows=%d",
		r.Kept, r.ParkedStreams, r.ParkedTasks, c.Path, c.Rows))
}

func whyOr(why, def string) string {
	if why == "" {
		return def
	}
	return why
}

func runScopePark(ctx context.Context, args []string, out, errOut io.Writer) int {
	w := newWSCmd("scope park", out, errOut)
	stream := w.fs.String("stream", "", "")
	idsArg := w.fs.String("ids", "", "")
	cp := w.fs.String("checkpoint", "", "")
	if _, code, ok := w.parse(args, 0, "scope park --redis <addr> --stream <s> [--ids @file] [--checkpoint <path>]"); !ok {
		return code
	}
	if *stream == "" {
		return refuse(errOut, w.name, "--stream <s> is required")
	}
	var ids []string
	if *idsArg != "" {
		var err error
		if ids, err = ws.ReadIDs(*idsArg, wsStdin); err != nil {
			return refuse(errOut, w.name, err.Error())
		}
	}
	st, code, ok := w.open(ctx)
	if !ok {
		return code
	}
	defer st.Close()
	c, err := checkpointFirst(ctx, st, *cp)
	if err != nil {
		return refuse(errOut, w.name, "checkpoint first: "+err.Error())
	}
	why := whyOr(*w.why, "scope park")
	if ids == nil {
		n, err := ws.ParkStream(ctx, st.Client(), *stream, *w.by, why)
		return w.done(err, fmt.Sprintf("PARKED stream=%s parked=%d checkpoint=%s rows=%d", strconv.Quote(*stream), n, c.Path, c.Rows))
	}
	// --ids parks those tasks only; each must be in --stream (one pipelined
	// read of their stream fields), else nothing moves.
	pipe := st.Client().Pipeline()
	got := make([]*redis.StringCmd, len(ids))
	for i, id := range ids {
		got[i] = pipe.HGet(ctx, "task:"+id, "stream")
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return refuse(errOut, w.name, err.Error())
	}
	var other []string
	for i, cmd := range got {
		if cmd.Val() != *stream {
			other = append(other, ids[i])
		}
	}
	if len(other) > 0 {
		return w.done(&ws.Refused{Why: fmt.Sprintf("%d of %d ids are not in stream %s (first %s)", len(other), len(ids), strconv.Quote(*stream), other[0])}, "")
	}
	r, err := ws.MoveMany(ctx, st.Client(), "parked", *w.by, why, ids)
	if err == nil && len(r.Refused) > 0 {
		fmt.Fprintf(out, "PARKED stream=%s parked=%d same=%d refused=%d first=%s:%s checkpoint=%s ms=%s\n",
			strconv.Quote(*stream), r.Moved, r.Same, len(r.Refused), r.Refused[0].ID, strconv.Quote(r.Refused[0].Why), c.Path, w.ms())
		return 1
	}
	return w.done(err, fmt.Sprintf("PARKED stream=%s parked=%d same=%d checkpoint=%s rows=%d", strconv.Quote(*stream), r.Moved, r.Same, c.Path, c.Rows))
}

func runScopeUnpark(ctx context.Context, args []string, out, errOut io.Writer) int {
	w := newWSCmd("scope unpark", out, errOut)
	stream := w.fs.String("stream", "", "")
	if _, code, ok := w.parse(args, 0, "scope unpark --redis <addr> --stream <s>"); !ok {
		return code
	}
	if *stream == "" {
		return refuse(errOut, w.name, "--stream <s> is required")
	}
	st, code, ok := w.open(ctx)
	if !ok {
		return code
	}
	defer st.Close()
	n, err := ws.UnparkStream(ctx, st.Client(), *stream, *w.by, whyOr(*w.why, "scope unpark"))
	return w.done(err, fmt.Sprintf("UNPARKED stream=%s unparked=%d", strconv.Quote(*stream), n))
}

// scopeOf is a stream's place in the scope: parked (everything not yet
// started is parked), kept (nothing parked) or partial.
func scopeOf(r ws.StreamCounts) string {
	switch {
	case r.Parked == 0:
		return "kept"
	case r.Cell(ws.Waiting)+r.Cell(ws.Ready) == 0:
		return "parked"
	}
	return "partial"
}

// active is a stream's cards not parked, landed or closed.
func active(r ws.StreamCounts) int64 { return r.Sum() - r.Cell(ws.Landed) }

func runScopeLs(ctx context.Context, args []string, out, errOut io.Writer) int {
	w := newWSCmd("scope ls", out, errOut)
	if _, code, ok := w.parse(args, 0, "scope ls --redis <addr>"); !ok {
		return code
	}
	st, code, ok := w.open(ctx)
	if !ok {
		return code
	}
	defer st.Close()
	c, err := ws.Counts(ctx, st.Client(), "")
	n := map[string]int{}
	for _, r := range c.Streams {
		s := scopeOf(r)
		n[s]++
		fmt.Fprintf(out, "%s %s active=%d parked=%d\n", s, strconv.Quote(r.Stream), active(r), r.Parked)
	}
	return w.done(err, fmt.Sprintf("SCOPE streams=%d kept=%d parked=%d partial=%d", len(c.Streams), n["kept"], n["parked"], n["partial"]))
}

func runStream(ctx context.Context, args []string, out, errOut io.Writer) int {
	sub, rest, code, ok := subverb(args, "stream", "ls, order, rename, open, rebase, pr, status or close", errOut)
	if !ok {
		return code
	}
	if code, handled := runStreamLife(ctx, sub, rest, out, errOut); handled {
		return code
	}
	switch sub {
	case "ls":
		return runStreamLs(ctx, rest, out, errOut)
	case "order":
		return runStreamOrder(ctx, rest, out, errOut)
	case "rename":
		return runStreamRename(ctx, rest, out, errOut)
	}
	return refuse(errOut, "stream", "unknown subverb "+sub+"; want ls, order, rename, open, rebase, pr, status or close")
}

// runStreamLs prints each stream's rank and counts; --tree adds every plan
// of the stream under it (nova-tools#4317), collapsed: one line per parent
// with its derived state and its children's counts folded in; --expand
// lists each child and the stitch under the parent.
func runStreamLs(ctx context.Context, args []string, out, errOut io.Writer) int {
	w := newWSCmd("stream ls", out, errOut)
	tree := w.fs.Bool("tree", false, "")
	expand := w.fs.Bool("expand", false, "")
	if _, code, ok := w.parse(args, 0, "stream ls --redis <addr> [--tree [--expand]]"); !ok {
		return code
	}
	if *expand && !*tree {
		return refuse(errOut, w.name, "--expand goes with --tree")
	}
	st, code, ok := w.open(ctx)
	if !ok {
		return code
	}
	defer st.Close()
	c, err := ws.Counts(ctx, st.Client(), "")
	plans := map[string][]taskcard.Plan{}
	n := 0
	if err == nil && *tree {
		names := make([]string, len(c.Streams))
		for i, r := range c.Streams {
			names[i] = r.Stream
		}
		var ps []taskcard.Plan
		if ps, err = taskcard.Plans(ctx, st.Client(), names); err == nil {
			for _, p := range ps {
				plans[p.Stream] = append(plans[p.Stream], p)
				n++
			}
		}
	}
	for i, r := range c.Streams {
		fmt.Fprintf(out, "%d %s waiting=%d ready=%d working=%d review=%d merging=%d landed=%d parked=%d\n",
			i+1, strconv.Quote(r.Stream), r.Cells[0], r.Cells[1], r.Cells[2], r.Cells[3], r.Cells[4], r.Cells[5], r.Parked)
		for _, p := range plans[r.Stream] {
			fmt.Fprintf(out, "  %s\n", p.Line())
			if !*expand {
				continue
			}
			for _, ch := range p.Children {
				fmt.Fprintf(out, "    child %s %s pr=%s score=%s\n", ch.ID, orDash(ch.Where), orDash(ch.PRRef()), orDash(ch.Score))
			}
			if p.Stitch.ID != "" {
				fmt.Fprintf(out, "    stitch %s %s pr=%s\n", p.Stitch.ID, orDash(p.Stitch.Where), orDash(p.Stitch.PRRef()))
			}
		}
	}
	if *tree {
		return w.done(err, fmt.Sprintf("STREAMS n=%d plans=%d", len(c.Streams), n))
	}
	return w.done(err, fmt.Sprintf("STREAMS n=%d", len(c.Streams)))
}

func runStreamOrder(ctx context.Context, args []string, out, errOut io.Writer) int {
	w := newWSCmd("stream order", out, errOut)
	show := w.fs.Bool("show", false, "")
	names, code, ok := w.parse(args, -1, "stream order --redis <addr> <stream> [<stream>...] | stream order --redis <addr> --show")
	if !ok {
		return code
	}
	if *show {
		// the listing of ws show --order: every stream's cards with edges
		if len(names) > 0 {
			return refuse(errOut, w.name, "--show lists the streams; it ranks nothing: stream order --redis <addr> --show")
		}
		st, code, ok := w.open(ctx)
		if !ok {
			return code
		}
		defer st.Close()
		return showOrder(ctx, w, st.Client(), "")
	}
	if len(names) == 0 {
		return refuse(errOut, w.name, "name the streams in priority order, first is rank 1; --show lists the cards of every stream with their edges")
	}
	st, code, ok := w.open(ctx)
	if !ok {
		return code
	}
	defer st.Close()
	n, err := ws.Order(ctx, st.Client(), names)
	return w.done(err, fmt.Sprintf("ORDERED streams=%d first=%s", n, strconv.Quote(names[0])))
}

func runStreamRename(ctx context.Context, args []string, out, errOut io.Writer) int {
	w := newWSCmd("stream rename", out, errOut)
	names, code, ok := w.parse(args, 2, "stream rename --redis <addr> <old> <new>")
	if !ok {
		return code
	}
	if strings.TrimSpace(names[1]) != names[1] {
		return refuse(errOut, w.name, "the new name has leading or trailing space")
	}
	st, code, ok := w.open(ctx)
	if !ok {
		return code
	}
	defer st.Close()
	n, err := ws.Rename(ctx, st.Client(), names[0], names[1], *w.by)
	return w.done(err, fmt.Sprintf("RENAMED from=%s to=%s members=%d", strconv.Quote(names[0]), strconv.Quote(names[1]), n))
}
