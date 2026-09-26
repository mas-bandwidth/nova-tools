package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// pushReorder writes the stream's work order after a push onto it
// (nova-tools #4322: `task push` and `card push` onto a stream call
// ws.Reorder, the one function `ws reorder` calls, so the stored order is
// always current). stream is the push's; empty, it is read from the task's
// record (one more round trip, counted). It returns the receipt fields:
// order=<ranked> order_rt=<round trips> order_ms=<ms> (and order_stale=<n>
// when a concurrent push or move made the write read again), order=- for a
// task in no stream; a DEPENDS-ON cycle prints its ORDER CYCLE line first
// and is order=cycle (nothing of the order is written: the stream already
// held the cycle, since the push's own FCALL refuses a push that closes one
// through the card it writes).
func pushReorder(ctx context.Context, c redis.Cmdable, stream, id, by string, out io.Writer) string {
	_, fields := streamReorder(ctx, c, stream, id, by, out)
	return fields
}

// streamReorder is pushReorder that also returns the stream it ordered ("" none).
func streamReorder(ctx context.Context, c redis.Cmdable, stream, id, by string, out io.Writer) (string, string) {
	start := time.Now()
	rt := 0
	if stream == "" && id != "" {
		rt++
		stream, _ = c.HGet(ctx, "task:"+id, "stream").Result()
	}
	if stream == "" {
		return "", "order=-"
	}
	r, err := ws.Reorder(ctx, c, stream, by)
	rt += r.RoundTrips
	ms := strconv.FormatFloat(float64(time.Since(start).Microseconds())/1000, 'f', 1, 64)
	stale := ""
	if r.Stale > 0 {
		stale = fmt.Sprintf(" order_stale=%d", r.Stale)
	}
	var ce *ws.CycleError
	switch {
	case errors.As(err, &ce):
		fmt.Fprintf(out, "ORDER CYCLE stream=%s %s\n", strconv.Quote(stream), ce)
		return stream, fmt.Sprintf("order=cycle order_rt=%d order_ms=%s", rt, ms)
	case err != nil:
		return stream, fmt.Sprintf("order=error:%s order_rt=%d%s order_ms=%s", quoteField(err.Error()), rt, stale, ms)
	}
	return stream, fmt.Sprintf("order=%d order_rt=%d%s order_ms=%s", r.Ranked, rt, stale, ms)
}

// reorderLines is a door's receipt (the #4322 fix round: every path that
// inserts or moves a card in a stream's waiting, ready or merging set
// recomputes the order): one `ORDER stream=<s> order=...` line per distinct
// named stream, in the order given, each through ws.Reorder.
func reorderLines(ctx context.Context, c redis.Cmdable, streams []string, by string, out io.Writer) {
	seen := map[string]bool{}
	for _, s := range streams {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		fmt.Fprintf(out, "ORDER stream=%s %s\n", quoteField(s), pushReorder(ctx, c, s, "", by, out))
	}
}

// orderGrant is a push door's check before its write (the #4322 fix
// round): a push onto a named stream is refused, nothing written, when the
// seat cannot write the stream's work order (ws.ProbeReorder: FCALL
// ns_ws_reorder PROBE, refused NOPERM without +fcall|ns_ws_reorder). The
// refusal text `ORDER GRANT <err>; ...remedy`, or "" when the seat holds
// the grant or the push names no stream. A DEPENDS-ON cycle is not checked
// here: the push's own FCALL refuses one through the card it writes
// (cm_order_cycle), on the live edge set at that instant.
func orderGrant(ctx context.Context, c redis.Cmdable, streams ...string) string {
	for _, s := range streams {
		if s == "" {
			continue
		}
		if err := ws.ProbeReorder(ctx, c); err != nil {
			return "ORDER GRANT " + err.Error() + "; this seat cannot write stream " + strconv.Quote(s) +
				"'s work order, so the push is refused and nothing is written: grant +fcall|" + ws.FnReorder +
				" (store.ACLRules; the fleet's redis-acl.rules)"
		}
		return ""
	}
	return ""
}

// batchCycle is card push's check of its own batch before any write (pure:
// no store read): `ORDER CYCLE stream=<s> DEPENDS-ON cycle <a -> b -> a>`
// when the batch's cards for one stream close a cycle among themselves, "".
func batchCycle(stream string, adds []ws.PushCard) string {
	var ce *ws.CycleError
	if err := ws.BatchCycle(stream, adds); errors.As(err, &ce) {
		return "ORDER CYCLE stream=" + strconv.Quote(stream) + " " + ce.Error()
	} else if err != nil {
		return "ORDER " + err.Error()
	}
	return ""
}
