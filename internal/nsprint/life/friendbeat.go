package life

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// A friend's beat (nova-tools #4233; Glenn 2026-09-26 8:58 AM ET: friends
// may run on any bench, 9:03 AM: "friends are running themselves"): the
// friend's own harness ticks `nova-sprint friend beat --as friend:<f>` at
// zero tokens, and that one tick, ONE round trip, is the friend's presence
// on every table and the lease of every copy it holds:
//
//	friend:<f>:beat   host, at (ms), load1, ncpu, cpu: the consumer table's
//	                  status (up under a minute old) and load, measured on
//	                  the machine the friend's session runs on; the deal
//	                  duty's liveness (taskcard.Consumer.BeatKey) and the
//	                  reader pool's up (review.lua) read the same at
//	ns_cm_beat        friend:<f> with no id: every copy in
//	                  friend:<f>:cards:working gets a fresh lease, in the
//	                  same pipeline, so no copy can end between a read of
//	                  the set and its beat
//	friend:<f>:beat   models: the distinct models of the copies it holds in
//	models            working (task:<copy> model, what card work --model
//	                  and friend pull --model record), comma joined, in the
//	                  same pipeline; removed when none names one. The
//	                  table's friend row prints it beside the name.
//
// No TTL is set and any TTL a `friend hello` loop left is removed: keys do
// not expire, at is reader-judged (Glenn 2026-09-23). The friend:<f> row is
// not written here: ns_friend_row (`friend row`) is its one writer, and a
// beat that wrote up=1 there would never write the 0. The session field of
// the beat is left alone, so a hello loop of the same friend keeps beating
// beside this one.

// FriendBeatRequest is one friend beat.
type FriendBeatRequest struct {
	Friend string
	Host   string
	// Load1, NCPU and CPU are the machine's measurements (Load1Now,
	// runtime.NumCPU, CPUBusyNow); an empty one is not written.
	Load1 string
	NCPU  int
	CPU   string
	// At is the beat's clock; zero means now.
	At time.Time
}

// FriendBeatResult is what one beat wrote.
type FriendBeatResult struct {
	Friend string
	AtMS   int64
	// Working is how many copies' leases the beat renewed, LeaseUntil the
	// lease they now hold (ms; 0 with none).
	Working    int
	LeaseUntil int64
	// Models is the models field the beat wrote ("" when it removed it).
	Models string
}

// FriendBeatHarness is the harness field a friend beat writes, naming its
// producer beside a hello loop's harness.
const FriendBeatHarness = "friend beat"

// FriendBeat writes the friend's beat and renews its working copies' leases
// in one pipeline. A refused lease renewal (ns_cm_beat REFUSED) is the
// error, so the verb's loop backs off and says why.
func FriendBeat(ctx context.Context, st *store.Store, req FriendBeatRequest) (FriendBeatResult, error) {
	friend := strings.ToLower(strings.TrimSpace(req.Friend))
	host := strings.TrimSpace(req.Host)
	if st == nil || st.Client() == nil || friend == "" || host == "" || strings.ContainsAny(friend+host, " \t\r\n:") {
		return FriendBeatResult{}, fmt.Errorf("friend beat: store, friend and host are required")
	}
	at := req.At
	if at.IsZero() {
		at = time.Now()
	}
	ms := at.UnixMilli()
	fields := []any{"host", host, "at", strconv.FormatInt(ms, 10), "harness", FriendBeatHarness}
	if v := strings.TrimSpace(req.Load1); v != "" {
		fields = append(fields, "load1", v)
	}
	if req.NCPU > 0 {
		fields = append(fields, "ncpu", strconv.Itoa(req.NCPU))
	}
	if v := strings.TrimSpace(req.CPU); v != "" {
		fields = append(fields, "cpu", v)
	}
	beat := "friend:" + friend + ":beat"
	pipe := st.Client().Pipeline()
	pipe.HSet(ctx, beat, fields...)
	pipe.Persist(ctx, beat)
	leases := pipe.FCall(ctx, "ns_cm_beat", nil, "friend:"+friend)
	models := pipe.Eval(ctx, modelsScript, []string{"friend:" + friend + ":cards:working", beat})
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return FriendBeatResult{}, fmt.Errorf("friend beat %s: %w", friend, err)
	}
	reply, err := leases.Slice()
	if err != nil {
		return FriendBeatResult{}, fmt.Errorf("friend beat %s: leases: %w", friend, err)
	}
	words := make([]string, len(reply))
	for i, v := range reply {
		words[i] = fmt.Sprint(v)
	}
	if len(words) == 2 && words[0] == "REFUSED" {
		return FriendBeatResult{}, fmt.Errorf("friend beat %s: leases: %s", friend, words[1])
	}
	if len(words) != 3 || words[0] != "BEAT" {
		return FriendBeatResult{}, fmt.Errorf("friend beat %s: leases: unexpected reply %v", friend, words)
	}
	n, _ := strconv.Atoi(words[1])
	until, _ := strconv.ParseInt(words[2], 10, 64)
	if n == 0 {
		until = 0
	}
	m, err := models.Text()
	if err != nil && !errors.Is(err, redis.Nil) {
		return FriendBeatResult{}, fmt.Errorf("friend beat %s: models: %w", friend, err)
	}
	return FriendBeatResult{Friend: friend, AtMS: ms, Working: n, LeaseUntil: until, Models: m}, nil
}

// modelsScript writes the beat's models field (KEYS[2]) from the models of the
// copies in the working set (KEYS[1]): each distinct model once, in the
// set's order, comma joined; the field is removed when none names one.
const modelsScript = `local seen, out = {}, {}
for _, id in ipairs(redis.call('ZRANGE', KEYS[1], 0, -1)) do
  local m = redis.call('HGET', 'task:' .. id, 'model')
  if m and m ~= '' and not seen[m] then seen[m] = true; out[#out + 1] = m end
end
if #out == 0 then redis.call('HDEL', KEYS[2], 'models'); return '' end
local models = table.concat(out, ',')
redis.call('HSET', KEYS[2], 'models', models)
return models`
