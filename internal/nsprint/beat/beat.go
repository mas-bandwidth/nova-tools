// Package beat is the one judgement of a friend's presence (nova-tools
// #4233): a friend is up when its beat, friend:<f>:beat, carries an at
// under Window old. The beat has no TTL (`friend beat` PERSISTs it; keys do
// not expire, at is reader-judged, Glenn 2026-09-23), so a reader that asks
// EXISTS would see a friend that beat once as live forever. Every Go reader
// of a friend's liveness reads the at through Read and judges it with Up;
// task_claim.lua and review.lua apply the same window in Lua.
package beat

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Window is how old a beat's at may be for the friend to read up: the
// consumer table's minute (table.hostBeatStale) and ns_friend_row's.
const Window = 60 * time.Second

// Key is the friend's beat hash.
func Key(friend string) string { return "friend:" + friend + ":beat" }

// Read queues the beat's at on pipe; UpCmd judges the answer.
func Read(ctx context.Context, pipe redis.Cmdable, friend string) *redis.StringCmd {
	return pipe.HGet(ctx, Key(friend), "at")
}

// UpCmd is Up over a queued Read; an absent beat (redis.Nil) or an
// unanswered read is down.
func UpCmd(cmd *redis.StringCmd, now time.Time) bool {
	if cmd == nil {
		return false
	}
	at, err := cmd.Result()
	if err != nil {
		return false
	}
	return Up(at, now)
}

// Up reports whether at, a beat's stamp, is within Window of now on either
// side. at is epoch ms (ns_friend_beat, friend beat) or seconds, RFC 3339,
// or the row form whose digits are YYYYMMDDHHMMSS; anything else is down.
func Up(at string, now time.Time) bool {
	t, ok := At(at)
	if !ok {
		return false
	}
	d := now.Sub(t)
	if d < 0 {
		d = -d
	}
	return d <= Window
}

// At parses a beat's at.
func At(at string) (time.Time, bool) {
	at = strings.TrimSpace(at)
	if at == "" {
		return time.Time{}, false
	}
	if n, err := strconv.ParseInt(at, 10, 64); err == nil && n > 0 {
		if n > 1e12 {
			return time.UnixMilli(n), true
		}
		if n < 1e11 {
			return time.Unix(n, 0), true
		}
		return time.Time{}, false
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
