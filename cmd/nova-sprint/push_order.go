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
// order=<ranked> order_rt=<round trips> order_ms=<ms>, order=- for a task
// in no stream; a DEPENDS-ON cycle prints its ORDER CYCLE line first and is
// order=cycle (the push stands; nothing of the order is written).
func pushReorder(ctx context.Context, c redis.Cmdable, stream, id, by string, out io.Writer) string {
	start := time.Now()
	rt := 0
	if stream == "" && id != "" {
		rt++
		stream, _ = c.HGet(ctx, "task:"+id, "stream").Result()
	}
	if stream == "" {
		return "order=-"
	}
	r, err := ws.Reorder(ctx, c, stream, by)
	rt += r.RoundTrips
	ms := strconv.FormatFloat(float64(time.Since(start).Microseconds())/1000, 'f', 1, 64)
	var ce *ws.CycleError
	switch {
	case errors.As(err, &ce):
		fmt.Fprintf(out, "ORDER CYCLE stream=%s %s\n", strconv.Quote(stream), ce)
		return fmt.Sprintf("order=cycle order_rt=%d order_ms=%s", rt, ms)
	case err != nil:
		return fmt.Sprintf("order=error:%s order_rt=%d order_ms=%s", quoteField(err.Error()), rt, ms)
	}
	return fmt.Sprintf("order=%d order_rt=%d order_ms=%s", r.Ranked, rt, ms)
}
