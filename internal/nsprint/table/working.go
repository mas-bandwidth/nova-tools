// working.go: the friend working column is the cards with a live child
// (#3892; Glenn 2026-09-25 4:40 PM ET: "The friends table must be accurate
// each second. It must not lie."). ZCARD friend:<f>:cards:working counts
// leases, and a lease is not work: emma showed 17 working with 5 live
// children and 11 finished cards that never left the set.
//
// A member of friend:<f>:cards:working is live when its record (task:<id>,
// or the card record s:<S>:card:<label> itself) says where=working, names
// f as its holder (friend, else owner) and carries a beat_at (written by
// `task beat` and `card beat`, ms) within BeatWindow of the tick's now.
// Every other member is stale: a lapsed or absent beat, or a record that is
// finished, someone else's or gone. The row prints the live count as
// working and the rest as stale=<n> beside its status; the reconciler's
// task-lease duty (ns_tcard_expire) moves a lapsed lease back to ready and
// unlinks a finished one, so stale is the lag of that duty, never hidden.
//
// The count is one read-only script in the tick's one pipeline: no second
// round trip, no KEYS, no SCAN; O(the friends' working sets).
package table

import (
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// BeatWindow is how old a card's beat may be for it to count as working:
// 90 s, a 60 s beat and half again (the issue's window; the card lease reap
// uses the same 90 s).
const BeatWindow = 90 * time.Second

// liveScript: ARGV now ms, window ms, then the friends; returns per friend
// the live count and the stale count of friend:<f>:cards:working.
const liveScript = `local now, window = tonumber(ARGV[1]), tonumber(ARGV[2])
local out = {}
for i = 3, #ARGV do
  local f = ARGV[i]
  local live, stale = 0, 0
  for _, id in ipairs(redis.call('ZRANGE', 'friend:' .. f .. ':cards:working', 0, -1)) do
    local key = 'task:' .. id
    if string.match(id, '^s:[-a-z0-9]+:card:') then key = id end
    local r = redis.call('HMGET', key, 'where', 'beat_at', 'friend', 'owner')
    local holder = r[3]
    if not holder or holder == '' then holder = r[4] end
    local beat = tonumber(r[2])
    if beat and beat < 100000000000 then beat = beat * 1000 end
    if r[1] == 'working' and holder == f and beat and now - beat <= window then
      live = live + 1
    else
      stale = stale + 1
    end
  end
  out[#out + 1] = live
  out[#out + 1] = stale
end
return out`

// beatWindow is the config's window, BeatWindow when unset.
func beatWindow(cfg SprintConfig) time.Duration {
	if cfg.BeatWindow > 0 {
		return cfg.BeatWindow
	}
	return BeatWindow
}

// liveArgs are liveScript's ARGV for the roster at now.
func liveArgs(now time.Time, window time.Duration, roster []string) []any {
	args := make([]any, 0, len(roster)+2)
	args = append(args, now.UnixMilli(), window.Milliseconds())
	for _, f := range roster {
		args = append(args, f)
	}
	return args
}

// liveCells are the working and stale cells of roster[i] from the script's
// reply; ok is false when the reply did not come back whole, and then both
// cells are "?" (never a false number).
func liveCells(cmd *redis.Cmd, n int) (working, stale []string, ok bool) {
	working, stale = make([]string, n), make([]string, n)
	vals, err := cmd.Int64Slice()
	ok = err == nil && len(vals) == 2*n
	for i := 0; i < n; i++ {
		working[i], stale[i] = "?", "?"
		if ok {
			working[i], stale[i] = strconv.FormatInt(vals[2*i], 10), strconv.FormatInt(vals[2*i+1], 10)
		}
	}
	return working, stale, ok
}
