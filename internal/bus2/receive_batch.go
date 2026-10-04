package bus2

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"
)

// MaxReceiveBatch bounds both reads and pending recovery pages.
const MaxReceiveBatch = 1000

func receiveBounds(consumer, after string, count int) error {
	var problems []string
	if consumer == "" || len(consumer) > 128 || strings.ContainsAny(consumer, " \t\r\n\x00") {
		problems = append(problems, "consumer wants 1..128 bytes without whitespace")
	}
	if count < 1 || count > MaxReceiveBatch {
		problems = append(problems, "count wants 1..1000")
	}
	if after != "" {
		ms, seq, ok := strings.Cut(after, "-")
		_, e1 := strconv.ParseUint(ms, 10, 64)
		_, e2 := strconv.ParseUint(seq, 10, 64)
		if !ok || e1 != nil || e2 != nil {
			problems = append(problems, "pending cursor wants a numeric stream entry id (ms-seq)")
		}
	}
	if len(problems) > 0 {
		return &Refusal{problems}
	}
	return nil
}

func (b *Bus) receiveGroup(ctx context.Context, as string) error {
	if p := CheckName(as); p != "" {
		return &Refusal{[]string{p}}
	}
	names, _, err := b.Store.Roster(ctx)
	if err != nil {
		return err
	}
	if !slices.Contains(names, as) {
		return &Refusal{[]string{unknown(as)}}
	}
	return b.Store.EnsureGroup(ctx, StreamOf(as), as)
}

// RecvBatch uses a named consumer but keeps Recv's stale-claim-first rule
// (SPEC-BUS2.md, receive helpers; tla/Bus2.tla: PendingBeforeNew).
// Consumer labels distinguish ownership, not authorization boundaries.
func (b *Bus) RecvBatch(ctx context.Context, as, consumer string, block time.Duration, count int) ([]Entry, error) {
	if err := receiveBounds(consumer, "", count); err != nil {
		return nil, err
	}
	if err := b.receiveGroup(ctx, as); err != nil {
		return nil, err
	}
	got, err := b.Store.Claim(ctx, StreamOf(as), as, consumer, ClaimAfter, count)
	if err != nil || len(got) > 0 {
		return got, err
	}
	return b.Store.Read(ctx, StreamOf(as), as, consumer, block, count)
}

// PendingPage reads only this consumer's existing pending entries, in stream
// order strictly after the cursor, without claiming or acknowledging them
// (SPEC-BUS2.md, receive helpers). Ownership is observed at query time.
func (b *Bus) PendingPage(ctx context.Context, as, consumer, after string, count int) ([]Entry, string, error) {
	if err := receiveBounds(consumer, after, count); err != nil {
		return nil, "", err
	}
	if err := b.receiveGroup(ctx, as); err != nil {
		return nil, "", err
	}
	ids, err := b.Store.PendingPage(ctx, StreamOf(as), as, consumer, after, count)
	if err != nil || len(ids) == 0 {
		return nil, "", err
	}
	entries, err := b.Store.Get(ctx, StreamOf(as), ids)
	if err != nil {
		return nil, "", err
	}
	return entries, ids[len(ids)-1], nil
}
