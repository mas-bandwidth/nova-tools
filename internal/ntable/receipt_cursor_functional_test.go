//go:build functional

package ntable_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

func receiptCursorTable(t *testing.T) (*redis.Client, context.Context) {
	t.Helper()
	_, c := live(t)
	ctx := context.Background()
	if err := ntable.Create(ctx, c, demo(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	return c, ctx
}

func TestReceiptCursorPagesAndOpaqueIDs(t *testing.T) {
	t.Parallel()
	c, ctx := receiptCursorTable(t) // create and row-add each emit a receipt
	first, err := ntable.ReadReceiptPage(ctx, c, "demo", ntable.ReceiptCursor{}, 1)
	if err != nil || len(first.Events) != 1 || !first.HasMore || first.Next.Revision != 1 {
		t.Fatalf("first page: %+v %v", first, err)
	}
	second, err := ntable.ReadReceiptPage(ctx, c, "demo", first.Next, 1)
	if err != nil || len(second.Events) != 1 || second.HasMore || second.Next.Revision != 2 {
		t.Fatalf("second page: %+v %v", second, err)
	}
	if second.Events[0].Before != first.Next.Revision {
		t.Fatalf("broken page link: %+v", second.Events[0])
	}
	empty, err := ntable.ReadReceiptPage(ctx, c, "demo", second.Next, 1)
	if err != nil || len(empty.Events) != 0 || empty.Next != second.Next {
		t.Fatalf("empty page: %+v %v", empty, err)
	}

	// A valid writer can use a nonconsecutive stream ID. The receipt chain is
	// consecutive in table revisions, not in Redis stream IDs.
	id := "9999999999999-42"
	if err := c.XAdd(ctx, &redis.XAddArgs{Stream: ntable.ChangesKey("demo"), ID: id,
		Values: map[string]any{"rev_before": "2", "rev_after": "3", "epoch": "0", "verb": "synthetic"}}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, ntable.RevisionKey("demo"), "n", "3").Err(); err != nil {
		t.Fatal(err)
	}
	third, err := ntable.ReadReceiptPage(ctx, c, "demo", second.Next, 1)
	if err != nil || len(third.Events) != 1 || third.Events[0].ID != id || third.Next.Revision != 3 {
		t.Fatalf("nonconsecutive ID: %+v %v", third, err)
	}
}

func TestReceiptCursorRefusesDeletedAnchorAndUnreadGaps(t *testing.T) {
	t.Parallel()
	for _, which := range []string{"anchor", "middle", "tail", "trim"} {
		t.Run(which, func(t *testing.T) {
			t.Parallel()
			c, ctx := probeTable(t)
			if _, err := ntable.RowAdd(ctx, c, "demo", "other", ntable.RowSpec{}); err != nil {
				t.Fatal(err)
			}
			page, err := ntable.ReadReceiptPage(ctx, c, "demo", ntable.ReceiptCursor{}, 1)
			if err != nil {
				t.Fatal(err)
			}
			ids := c.XRange(ctx, ntable.ChangesKey("demo"), "-", "+").Val()
			switch which {
			case "anchor":
				err = c.XDel(ctx, ntable.ChangesKey("demo"), ids[0].ID).Err()
			case "middle":
				err = c.XDel(ctx, ntable.ChangesKey("demo"), ids[1].ID).Err()
			case "tail":
				err = c.XDel(ctx, ntable.ChangesKey("demo"), ids[len(ids)-1].ID).Err()
			case "trim":
				err = c.XTrimMaxLen(ctx, ntable.ChangesKey("demo"), 1).Err()
			}
			if err != nil {
				t.Fatal(err)
			}
			if got, err := ntable.ReadReceiptPage(ctx, c, "demo", page.Next, 10); !errors.Is(err, ntable.ErrReceiptCursorGap) || len(got.Events) != 0 {
				t.Fatalf("after %s: %+v %v", which, got, err)
			}
		})
	}
}

func TestReceiptCursorRefusesTrimmedPrefixAndMissingStream(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	key := ntable.ChangesKey("demo")
	if err := c.XTrimMaxLen(ctx, key, 1).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.ReadReceiptPage(ctx, c, "demo", ntable.ReceiptCursor{}, 10); !errors.Is(err, ntable.ErrReceiptCursorGap) {
		t.Fatalf("trimmed prefix: %v", err)
	}
	if err := c.Del(ctx, key).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.ReadReceiptPage(ctx, c, "demo", ntable.ReceiptCursor{}, 10); !errors.Is(err, ntable.ErrReceiptCursorGap) {
		t.Fatalf("missing stream with nonzero revision: %v", err)
	}
}

func TestReceiptCursorKeepsDeliveredHistoryOutOfTheGap(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	page, err := ntable.ReadReceiptPage(ctx, c, "demo", ntable.ReceiptCursor{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.XTrimMaxLen(ctx, ntable.ChangesKey("demo"), 1).Err(); err != nil {
		t.Fatal(err)
	}
	got, err := ntable.ReadReceiptPage(ctx, c, "demo", page.Next, 10)
	if err != nil || len(got.Events) != 0 || got.Next != page.Next {
		t.Fatalf("trim before retained cursor: %+v %v", got, err)
	}
}

func TestReceiptCursorCarriesEpochTransition(t *testing.T) {
	t.Parallel()
	c, tb := epochFixture(t)
	ctx := context.Background()
	page, err := ntable.ReadReceiptPage(ctx, c, tb.Name, ntable.ReceiptCursor{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, tb.EpochKey, "n", 1).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.Clear(ctx, c, tb.Name, ntable.WriteOptions{Epoch: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, tb.Name, "fresh", ntable.RowSpec{}, ntable.WriteOptions{Epoch: 1}); err != nil {
		t.Fatal(err)
	}
	more, err := ntable.ReadReceiptPage(ctx, c, tb.Name, page.Next, 10)
	if err != nil || len(more.Events) != 2 || more.Events[0].Epoch != 1 || more.Events[1].Epoch != 1 {
		t.Fatalf("epoch transition: %+v %v", more, err)
	}
}

func TestReceiptCursorShowsDropAndRecreate(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	page, err := ntable.ReadReceiptPage(ctx, c, "demo", ntable.ReceiptCursor{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.DropDefinition(ctx, c, "demo"); err != nil {
		t.Fatal(err)
	}
	if err := ntable.Create(ctx, c, demo(), now); err != nil {
		t.Fatal(err)
	}
	more, err := ntable.ReadReceiptPage(ctx, c, "demo", page.Next, 10)
	if err != nil || len(more.Events) != 2 || more.Events[0].Verb != "drop_definition" || more.Events[1].Verb != "create" {
		t.Fatalf("drop/recreate: %+v %v", more, err)
	}
	if more.Next.Revision != page.Next.Revision+2 {
		t.Fatalf("revision reset across recreate: %+v", more)
	}
}
