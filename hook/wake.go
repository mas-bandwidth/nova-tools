package hook

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// What one daily drill did to one name (tla/WakeDrill.tla, Drill).
const (
	WakeLeft    = 0 // not a hooked friend: not drilled
	WakePassed  = 1 // drilled today, a pass: no judgment
	WakeMissed  = 2 // a miss, not yet recorded: the caller sends one judgment, then marks the day
	WakeAlready = 3 // this day was already drilled: no judgment
)

// Wake is one name's outcome of the daily drill.
type Wake struct {
	Name   string
	Did    int // WakeLeft, WakePassed, WakeMissed or WakeAlready
	Judged int // judgments recorded for this day: 0 or 1
	Day    string
}

// Miss is the once-key and the marks of a miss, the day and judged written in
// the same atomic step as the judgment (tla/WakeDrill.tla, the one write).
// ok is false when w is not a miss.
func (w Wake) Miss() (once string, marks []ToldMark, ok bool) {
	if w.Did != WakeMissed {
		return "", nil, false
	}
	return prefix + "wake:" + w.Name + ":" + w.Day, []ToldMark{
		{Field: "day", Value: w.Day},
		{Field: "judged", Value: "1"},
	}, true
}

// wakeLua is one daily drill over every name, in one atomic step
// (tla/WakeDrill.tla, Drill). now is the caller's clock in ms, the store's
// TIME when the server calls. A name that is not a hooked friend is left
// (code 0). A day already recorded is left (code 3). A hooked friend whose
// epoch is younger than the window, or who has answered within the window
// and not before this epoch's armed, is a pass: the day is recorded with
// judged 0 and nothing is emitted (code 1). Otherwise it is a miss (code 2)
// and this script writes nothing, so a send that fails is tried again and a
// second pass the same day, after the mark, emits nothing. The challenge,
// the miss count and the hook state are not touched.
// KEYS: per name, its record then its drill key.
// ARGV: now ms, the UTC day, the window ms.
var wakeLua = redis.NewScript(`
local now = tonumber(ARGV[1])
local day = ARGV[2]
local window = tonumber(ARGV[3])
local out = {}
local i = 1
while i < #KEYS do
  local rec, drill = KEYS[i], KEYS[i + 1]
  local code, judged = 0, 0
  if redis.call('HGET', rec, 'state') == 'hooked' and redis.call('HGET', rec, 'role') == 'friend' then
    local cur = redis.call('HMGET', drill, 'day', 'judged')
    if cur[1] == day then
      code = 3
      judged = tonumber(cur[2] or '0')
    else
      local armed = tonumber(redis.call('HGET', rec, 'armed') or '0')
      local ans = tonumber(redis.call('HGET', rec, 'answered_at') or '0')
      local young = armed > 0 and (now - armed) < window
      local fresh = ans > 0 and ans >= armed and (now - ans) <= window
      if young or fresh then
        redis.call('HSET', drill, 'day', day, 'judged', '0')
        code = 1
      else
        code = 2
      end
    end
  end
  table.insert(out, {code, judged})
  i = i + 2
end
return out
`)

// Wake drills each name at now (tla/WakeDrill.tla). now is an argument: the
// server passes the store's TIME, and a test passes the instant it means.
// One round trip. A miss is not recorded here; Wake.Miss carries the mark
// the judgment's send writes.
func (s Store) Wake(ctx context.Context, names []string, now time.Time) ([]Wake, error) {
	if len(names) == 0 {
		return nil, nil
	}
	day := now.UTC().Format("2006-01-02")
	keys := make([]string, 0, len(names)*2)
	out := make([]Wake, len(names))
	for i, n := range names {
		out[i] = Wake{Name: n, Day: day}
		keys = append(keys, RecordKey(n), DrillKey(n))
	}
	ctx, cancel := s.call(ctx)
	defer cancel()
	got, err := wakeLua.Run(ctx, s.C, keys, now.UnixMilli(), day, WakeEvery.Milliseconds()).Slice()
	if err != nil {
		return nil, err
	}
	if len(got) != len(names) {
		return nil, fmt.Errorf("the wake script answered %d names, not %d", len(got), len(names))
	}
	for i, row := range got {
		v, ok := row.([]any)
		if !ok || len(v) != 2 {
			return nil, fmt.Errorf("the wake script answered %v for %s, not a code and a count", row, names[i])
		}
		did, _ := v[0].(int64)    // ignored: a code that is no number reads as left
		judged, _ := v[1].(int64) // ignored: a count that is no number reads as none
		out[i].Did, out[i].Judged = int(did), int(judged)
	}
	return out, nil
}
