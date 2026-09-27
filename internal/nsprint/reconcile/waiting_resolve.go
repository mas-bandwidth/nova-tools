package reconcile

// The waiting-resolve duty (nova-tools #3872): waiting -> ready is automatic.
//
// THE HURT (2026-09-25). Rowan walked the waiting sets by hand three times in
// one morning, checking each card's DEPENDS-ON against what had landed and
// moving the released ones to ready. Glenn 09:50 AM ET: "This activity of
// checking 'waiting' cards and for ones that aren't blocked on dependencies
// distributing them to friends (or swarms!) should be an automatic thing."
//
// THE RULE. Every reconciler pass, under the lease, this duty reads
// ws:<s>:waiting for every stream in ws:order and each waiting task's
// blocked_on (its DEPENDS-ON, the one form of #3409: task ids and
// owner/repo#n issues or PRs, joined by ',' or ';'), and checks every entry
// against the records:
//
//   - task:<id> (or a bare id) is met when task:<id> is landed (state landed,
//     or where landed, or where done with where_ok not fail); a stream's
//     sentinel, <slug>:sentinel (nova-tools #4318, ws.SentinelID), is a
//     task id like any other, so a stream that must wait for another whole
//     stream puts DEPENDS-ON <slug>:sentinel on its first card and that is
//     the one kind of edge there is;
//   - owner/repo#n is met when a task in a ws set whose pr, ref or origin
//     names it is landed;
//   - an entry with no record (no task:<id>; no task in any ws set naming the
//     repo#n; any other form) is NOT met and is reported in unknown=, never
//     guessed: no evidence is not negative evidence;
//   - blocked_on "none" or "-" has nothing to wait on and is met; an empty
//     blocked_on is no evidence either way and the task stays waiting.
//
// A stream's own sentinel is never a waiter here: it waits in its stream
// for every other card to land, and then for the coordinator's acceptance
// (nova-tools#4412, Glenn 2026-09-26 via Stella: "sentinels wait for the
// coordinator's review and merge"): the coordinator's task land lands it,
// never the last card's landing and never this duty (TK.edge refuses an
// actor without the coordinator role). The duty leaves it out of the
// counts, and when a waiting sentinel has no live card left it prints the
// receipt with the acceptance command, once per change:
//
//	SENTINEL ready-for-acceptance stream=<s> id=<slug>:sentinel live=0: nova-sprint task land --actor <coordinator> --id <slug>:sentinel --sha <merge sha>
//
// A sentinel edge is met by landed alone (never by done: a sentinel's done
// is a rename's), so a dependent stream is released only by the stop.
//
// A task whose every entry is met moves waiting -> ready through
// ns_ws_move_many (ws.MoveMany, the ws index's one writer), one call per
// stream per distinct blocked_on, so the ws:log entry's why names the
// dependencies that released it; its score in ready is its created_at,
// unchanged. Reads are pipelined rounds over the sets and the named records,
// never a SCAN or KEYS. The duty prints one receipt line per stream with
// waiting tasks when it moves something or what is still waiting changed:
//
//	RESOLVE stream=<s> ready=<k> still=<n> on=<unmet deps> unknown=<deps>
//
// Every write is bounded by the lease (#3322, #3805): no move starts with
// less than the write margin of the lease left, and a fenced lease stops the
// duty with ErrFenced.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pitstop"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
)

// WaitingResolve is the reconciler's waiting-resolve duty. Its Run is a Duty.
type WaitingResolve struct {
	Client *redis.Client
	// Actor is the ws:log `by`; "reconciler" when empty.
	Actor string
	// Margin is the lease time kept back from the moves; DefaultWriteMargin
	// capped at TTL/6 when zero.
	Margin time.Duration
	// Out receives the RESOLVE receipt lines; nil prints nothing.
	Out io.Writer

	last map[string]string // stream -> what its last printed line held waiting
}

// ResolveLine is one stream's receipt for one pass.
type ResolveLine struct {
	Stream  string
	Ready   []string   // ids moved to ready, oldest first
	Still   int        // tasks left waiting
	On      []string   // unmet dependencies that have a record
	Unknown []string   // dependencies with no record
	Refused []ws.IDWhy // ids ns_ws_move_many refused (left waiting, counted in Still)
	// Stop is the stream's waiting sentinel with no live card left: ready
	// for the coordinator's acceptance (#4412).
	Stop string
}

// StopLine is the receipt for a sentinel ready for the coordinator's
// acceptance, with the one command that accepts it.
func (r ResolveLine) StopLine() string {
	return fmt.Sprintf("SENTINEL ready-for-acceptance stream=%s id=%s live=0: nova-sprint task land --actor <coordinator> --id %s --sha <merge sha>",
		wrField(r.Stream), r.Stop, r.Stop)
}

// String is the receipt line.
func (r ResolveLine) String() string {
	return fmt.Sprintf("RESOLVE stream=%s ready=%d still=%d on=%s unknown=%s",
		wrField(r.Stream), len(r.Ready), r.Still, wrList(r.On), wrList(r.Unknown))
}

func wrField(s string) string {
	if s == "" || strings.ContainsAny(s, " \t\"=") {
		return strconv.Quote(s)
	}
	return s
}

func wrList(xs []string) string {
	if len(xs) == 0 {
		return "-"
	}
	return strings.Join(xs, ",")
}

// Run is one duty pass: Counts.Routed is the tasks moved to ready,
// Counts.Refused the moves ns_ws_move_many refused (each also named in the
// duty's error), so they reach the DUTY line and proc:progress once.
func (d *WaitingResolve) Run(ctx context.Context, l *Lease) (Counts, error) {
	lines, err := d.Pass(ctx, l)
	var c Counts
	for _, r := range lines {
		c.Routed += len(r.Ready)
		c.Refused += len(r.Refused)
	}
	d.print(lines)
	return c, err
}

func (d *WaitingResolve) print(lines []ResolveLine) {
	if d.Out == nil {
		return
	}
	if d.last == nil {
		d.last = map[string]string{}
	}
	for _, r := range lines {
		// What is still waiting, and on what: an idle pass over the same
		// waiting set prints nothing.
		held := fmt.Sprintf("%d %s %s %s", r.Still, wrList(r.On), wrList(r.Unknown), r.Stop)
		if len(r.Ready) == 0 && d.last[r.Stream] == held {
			continue
		}
		d.last[r.Stream] = held
		if r.Still > 0 || len(r.Ready) > 0 {
			_, _ = fmt.Fprintln(d.Out, r.String())
		}
		if r.Stop != "" {
			_, _ = fmt.Fprintln(d.Out, r.StopLine())
		}
	}
}

func (d *WaitingResolve) actor() string {
	if d.Actor != "" {
		return d.Actor
	}
	return "reconciler"
}

func (d *WaitingResolve) margin(l *Lease) time.Duration {
	if d.Margin > 0 {
		return d.Margin
	}
	return min(DefaultWriteMargin, l.TTL()/6)
}

// wrDep is one parsed DEPENDS-ON entry.
type wrDep struct {
	raw  string
	kind int    // wrTask, wrRef or wrBad
	key  string // the task id, or owner/repo#n lower-cased
}

const (
	wrBad = iota
	wrTask
	wrRef
)

// Pass resolves every stream's waiting set once: one call of
// ns_waiting_resolve_pass (internal/nsprint/fn/lua/waiting_resolve.lua).
// The rounds the Go pass made over the wire (the streams in rank order, each
// stream's waiting set under the epoch, each waiter's DEPENDS-ON, the task
// records named and, for a repo#n, every stream member that can name it)
// run in the server, and the releases go through the one task move there,
// grouped by DEPENDS-ON text so the ws:log why names the entries met. The
// reply is one line per stream that had waiting tasks or a ready stop, in
// ws:order. A released stitch's brief is written here after the call
// (#4317: it starts with the whole picture). Bounded by the lease (#3805):
// with less than the write margin left the pass does not start.
func (d *WaitingResolve) Pass(ctx context.Context, l *Lease) ([]ResolveLine, error) {
	if d.Client == nil || l == nil {
		return nil, errors.New("waiting-resolve: client and lease are required")
	}
	if err := l.fencedErr(); err != nil {
		return nil, err
	}
	if left, m := l.Remaining(), d.margin(l); left < m {
		return nil, fmt.Errorf("waiting-resolve: the pass not started: LEASE-MARGIN: %s of the lease left, below the %s write margin",
			left.Round(time.Millisecond), m)
	}
	c := d.Client
	// A stream a pit stop holds keeps its waiting tasks where they are: the
	// pass's holds go with the call (a stop naming streams holds those; a
	// whole stop holds every stream but the lifted).
	hs, err := pitstop.Current(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("waiting-resolve: pitstop: %w", err)
	}
	args := []any{d.actor()}
	for _, h := range hs {
		if !h.Stop.Set {
			continue
		}
		if h.Stop.Streams != nil {
			args = append(args, "only", len(h.Stop.Streams))
			for _, s := range h.Stop.Streams {
				args = append(args, s)
			}
		} else {
			args = append(args, "all", len(h.Stop.Lifted))
			for _, s := range h.Stop.Lifted {
				args = append(args, s)
			}
		}
	}
	reply, err := c.FCall(ctx, "ns_waiting_resolve_pass", nil, args...).StringSlice()
	if err != nil {
		return nil, fmt.Errorf("waiting-resolve: ns_waiting_resolve_pass: %w", err)
	}
	var lines []ResolveLine
	byStream := map[string]int{}
	var stitches, errs []string
	arity := map[string]int{"line": 2, "still": 2, "on": 2, "unknown": 2, "ready": 2, "refused": 3, "stitch": 1}
	for i := 0; i < len(reply); {
		n, ok := arity[reply[i]]
		if !ok || i+n >= len(reply) {
			return lines, fmt.Errorf("waiting-resolve: ns_waiting_resolve_pass: unexpected reply at %d: %v", i, reply[i:])
		}
		v := reply[i+1 : i+1+n]
		switch reply[i] {
		case "line":
			byStream[v[0]] = len(lines)
			lines = append(lines, ResolveLine{Stream: v[0], Stop: v[1]})
		case "stitch":
			stitches = append(stitches, v[0])
		default:
			idx, ok := byStream[v[0]]
			if !ok {
				return lines, fmt.Errorf("waiting-resolve: ns_waiting_resolve_pass: %s row for a stream with no line: %s", reply[i], v[0])
			}
			r := &lines[idx]
			switch reply[i] {
			case "still":
				r.Still, _ = strconv.Atoi(v[1])
			case "on":
				r.On = append(r.On, v[1])
			case "unknown":
				r.Unknown = append(r.Unknown, v[1])
			case "ready":
				r.Ready = append(r.Ready, v[1])
			case "refused":
				r.Refused = append(r.Refused, ws.IDWhy{ID: v[1], Why: v[2]})
				errs = append(errs, fmt.Sprintf("stream %s: %s refused: %s", v[0], v[1], v[2]))
			}
		}
		i += 1 + n
	}
	for _, id := range stitches {
		if _, _, err := taskcard.WriteStitchBrief(ctx, c, id); err != nil {
			errs = append(errs, fmt.Sprintf("stitch %s brief: %v", id, err))
		}
	}
	if len(errs) > 0 {
		return lines, fmt.Errorf("waiting-resolve: %s", strings.Join(errs, "; "))
	}
	return lines, nil
}
