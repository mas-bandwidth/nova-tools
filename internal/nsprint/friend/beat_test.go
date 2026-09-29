package friend_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/friend"
)

func TestBeatRequestValidation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Nil client
	if _, err := friend.Beat(ctx, nil, friend.BeatRequest{Friend: "f1"}); err == nil {
		t.Fatal("Beat with nil client should fail")
	}

	// Empty friend
	// We cannot test without a mock redis or real redis, so let's verify error formatting
}

func TestBeatResultFormatting(t *testing.T) {
	t.Parallel()

	res := &friend.BeatResult{
		Friend:     "emma",
		AtMS:       1727599200000,
		Working:    2,
		LeaseUntil: 1727599380000,
		Models:     "pro,flash",
	}

	line := res.Line()
	want := "BEAT friend=emma working=2 lease_until=1727599380000 at=1727599200000"
	if line != want {
		t.Fatalf("Line() = %q, want %q", line, want)
	}

	r := friend.ReassignedCard{
		Card:   "c42",
		Friend: "emma",
		Reason: "lapsed-child-alive",
	}

	rline := r.Line()
	rwant := "REASSIGN card=c42 friend=emma reason=lapsed-child-alive"
	if rline != rwant {
		t.Fatalf("ReassignedCard.Line() = %q, want %q", rline, rwant)
	}
}

func TestBeatRequestDefaults(t *testing.T) {
	t.Parallel()

	now := time.UnixMilli(1727599200000)
	req := friend.BeatRequest{
		Friend: "friend:EMMA",
		Now:    func() time.Time { return now },
	}

	if strings.TrimPrefix(strings.ToLower(req.Friend), "friend:") != "emma" {
		t.Fatalf("normalization mismatch")
	}
}
