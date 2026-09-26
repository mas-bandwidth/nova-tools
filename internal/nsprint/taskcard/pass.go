package taskcard

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// The deal duty's pass (nova-tools #3929, the table moves; it supersedes
// the friend-only deal of #3908 and is the one deal over every consumer,
// Glenn 12:55 PM ET): lapsed copies return to their primaries, the review
// column is kept (EnsureReads: a moved head re-headed, a review primary
// with no live copy given its read copies; #4094), then every
// enrolled consumer (the consumers SET) that is live, not down and not
// paused, ranked by free slots (most first, then by id), is dealt what it
// lacks and filled:
//
//	free = slots - |working|        need = free - |ready|
//	card deal --to <c> --n <need>   when need > 0 (read legs first, then work)
//	card work --as <c> --fill       when free > 0
//
// one FCALL each, no per-pass cap: a consumer with free slots and dealable
// cards is never idle past one pass. WHO, read_who, readers, tiers and
// kinds decide which consumer may take which card (TM.may). paused is the
// desired hash's flag, a bench's or a friend's alike (worker pause|resume,
// #4308): a paused consumer is dealt nothing and keeps what it holds.

// Live is how recent a consumer's beat must be for it to be dealt.
const Live = 90 * time.Second

// BeatKey is the hash whose at field is the consumer's beat: the bench's
// own beat (bench:<b>:beat) or the friend's row (friend:<f>).
func (c Consumer) BeatKey() string {
	if c.Kind == "bench" {
		return c.String() + ":beat"
	}
	return c.String()
}

// beatAt reads a beat's at: epoch ms or seconds, RFC 3339, or the
// friend-row form whose digits are YYYYMMDDHHMMSS.
func beatAt(at string) (time.Time, bool) {
	at = strings.TrimSpace(at)
	if n, err := strconv.ParseInt(at, 10, 64); err == nil && n > 0 {
		if n > 1e12 {
			return time.UnixMilli(n), true
		}
		if n < 1e11 {
			return time.Unix(n, 0), true
		}
	}
	if t, err := time.Parse(time.RFC3339, at); err == nil {
		return t, true
	}
	var d strings.Builder
	for _, r := range at {
		if r >= '0' && r <= '9' {
			d.WriteRune(r)
		}
	}
	if s := d.String(); len(s) >= 14 {
		if t, err := time.ParseInLocation("20060102150405", s[:14], time.UTC); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// PassResult is one deal pass.
type PassResult struct {
	Dealt, Worked, Expired int
	Lines                  []string
}

type passRow struct {
	k            Consumer
	free, need   int
	ready, slots int64
	ci           int64 // CI legs on the bench, from its beat (nova-tools#4293)
	down, paused bool
	live         bool
}

// FreeSlots is a consumer's free slots: its declared slots minus the CI
// legs running on it minus the copies working, never negative
// (nova-tools#4293: "a bench's free slots = declared slots minus the CI legs
// running on it"). ns_cm_work's fill computes the same in Redis.
func FreeSlots(slots, working, ci int64) int {
	if f := slots - ci - working; f > 0 {
		return int(f)
	}
	return 0
}

// DealPass is one pass of the deal duty at now.
func DealPass(ctx context.Context, c redis.Cmdable, by string, now time.Time) (PassResult, error) {
	var res PassResult
	x, err := ExpireCopies(ctx, c, by)
	if err != nil {
		return res, err
	}
	res.Expired = len(x)
	for _, e := range x {
		res.Lines = append(res.Lines, fmt.Sprintf("EXPIRED %s to=%s why=lease-lapsed", e.Copy, e.To))
	}
	reads, err := EnsureReads(ctx, c, by)
	if err != nil {
		return res, err
	}
	for _, r := range reads {
		res.Lines = append(res.Lines, fmt.Sprintf("READS %s copies=%s", r.Primary, strings.Join(r.Copies, ",")))
	}
	roster, err := Roster(ctx, c)
	if err != nil || len(roster) == 0 {
		return res, err
	}
	pipe := c.Pipeline()
	type cmds struct {
		desired        *redis.SliceCmd
		ready, working *redis.IntCmd
		down           *redis.IntCmd
		at, ci         *redis.StringCmd
	}
	cs := make([]cmds, len(roster))
	for i, k := range roster {
		cs[i] = cmds{
			desired: pipe.HMGet(ctx, k.DesiredKey(), "slots", "paused"),
			ready:   pipe.ZCard(ctx, k.Key("ready")),
			working: pipe.ZCard(ctx, k.Key("working")),
			down:    pipe.Exists(ctx, k.DownKey()),
			at:      pipe.HGet(ctx, k.BeatKey(), "at"),
			ci:      pipe.HGet(ctx, k.BeatKey(), "ci"),
		}
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return res, fmt.Errorf("deal pass: read: %w", err)
	}
	var rows []passRow
	for i, k := range roster {
		d := cs[i].desired.Val()
		slots, err := strconv.ParseInt(fmt.Sprint(valueAt(d, 0)), 10, 64)
		if err != nil {
			continue
		}
		t, ok := beatAt(cs[i].at.Val())
		r := passRow{k: k, slots: slots, ready: cs[i].ready.Val(), down: cs[i].down.Val() > 0,
			paused: fmt.Sprint(valueAt(d, 1)) == "1", live: ok && now.Sub(t) < Live && t.Sub(now) < Live}
		if k.Kind == "bench" {
			// A friend has no CI legs; only a bench's beat counts them.
			r.ci, _ = strconv.ParseInt(cs[i].ci.Val(), 10, 64)
		}
		r.free = FreeSlots(slots, cs[i].working.Val(), r.ci)
		r.need = r.free - int(r.ready)
		if r.live && !r.down && !r.paused && r.free > 0 {
			rows = append(rows, r)
		}
	}
	sort.SliceStable(rows, func(a, b int) bool {
		if rows[a].free != rows[b].free {
			return rows[a].free > rows[b].free
		}
		return rows[a].k.String() < rows[b].k.String()
	})
	for _, r := range rows {
		dealt := 0
		if r.need > 0 {
			d, err := Deal(ctx, c, DealRequest{To: r.k, N: r.need, By: by})
			if err != nil {
				res.Lines = append(res.Lines, fmt.Sprintf("DEAL %s REFUSED why=%s", r.k, err))
				continue
			}
			dealt = len(d)
			res.Dealt += dealt
		}
		// The dealer deals; it never takes a copy into working on a consumer's
		// behalf. A bench's beat session and a friend's serve call card work
		// themselves, then launch: a copy worked here by the reconciler has no
		// launcher, its lease lapses, and the primary lands in review for
		// nothing (the first copy-model quack, 2026-09-25 10:41 PM ET: copy
		// worked "by reconciler why work", no process on the bench).
		if dealt > 0 {
			res.Lines = append(res.Lines, fmt.Sprintf("DEAL %s dealt=%d free=%d ci=%d", r.k, dealt, r.free, r.ci))
		}
	}
	return res, nil
}

func valueAt(v []any, i int) any {
	if i < len(v) && v[i] != nil {
		return v[i]
	}
	return ""
}
