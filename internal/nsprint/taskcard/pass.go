package taskcard

import (
	"context"
	"fmt"
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

// BeatKey is the hash whose at field is the consumer's beat, <kind>:<name>:beat
// for a bench and a friend alike (#4233: bench beat and friend beat each
// write their own; the friend:<f> row is ns_friend_row's and is never a
// liveness source).
func (c Consumer) BeatKey() string { return c.String() + ":beat" }

// MachineBeatKey is the hash the consumer's own beat writes about the
// machine it runs on: <consumer>:beat for a bench and a friend alike (host,
// load1, ncpu, cpu, and ci, the CI legs running there, nova-tools#4293).
func (c Consumer) MachineBeatKey() string { return c.String() + ":beat" }

// BeatAt is beatAt for another duty that measures room the way the deal
// pass does (the progress duty, #4319).
func BeatAt(at string) (time.Time, bool) { return beatAt(at) }

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

// DealPass is one pass of the deal duty at now: one call of ns_cm_pass
// (02_card_move.lua), which expires the lapsed copies, ensures the review
// column's reads, reads every consumer of the roster and deals each live,
// unpaused one with room the copies it needs, most room first. The Go
// pass made three to five round trips for the same work (2026-09-27,
// Glenn: "You always need to batch redis").
func DealPass(ctx context.Context, c redis.Cmdable, by string, now time.Time) (PassResult, error) {
	var res PassResult
	out, err := fcall(ctx, c, "ns_cm_pass", by, now.UnixMilli())
	if err != nil {
		return res, fmt.Errorf("deal pass: %w", err)
	}
	if len(out) > 0 && out[0] == "REFUSED" {
		return res, fmt.Errorf("deal pass: ns_cm_reads: %s", strings.Join(out[1:], " "))
	}
	arity := map[string]int{"expired": 2, "expire-refused": 2, "reads": 2, "skip": 2, "deal": 4, "deal-refused": 2}
	for i := 0; i < len(out); {
		n, ok := arity[out[i]]
		if !ok || i+n >= len(out) {
			return res, fmt.Errorf("deal pass: ns_cm_pass: unexpected reply at %d: %v", i, out[i:])
		}
		v := out[i+1 : i+1+n]
		switch out[i] {
		case "expired":
			res.Expired++
			res.Lines = append(res.Lines, fmt.Sprintf("EXPIRED %s to=%s why=lease-lapsed", v[0], v[1]))
		case "expire-refused":
			res.Lines = append(res.Lines, fmt.Sprintf("EXPIRE %s REFUSED why=%s", v[0], v[1]))
		case "reads":
			res.Lines = append(res.Lines, fmt.Sprintf("READS %s copies=%s", v[0], v[1]))
		case "skip":
			res.Lines = append(res.Lines, fmt.Sprintf("DEAL %s SKIP why=%s", v[0], v[1]))
		case "deal":
			dealt, _ := strconv.Atoi(v[1])
			res.Dealt += dealt
			res.Lines = append(res.Lines, fmt.Sprintf("DEAL %s dealt=%s free=%s ci=%s", v[0], v[1], v[2], v[3]))
		case "deal-refused":
			res.Lines = append(res.Lines, fmt.Sprintf("DEAL %s REFUSED why=%s", v[0], v[1]))
		}
		i += 1 + n
	}
	return res, nil
}

func valueAt(v []any, i int) any {
	if i < len(v) && v[i] != nil {
		return v[i]
	}
	return ""
}
