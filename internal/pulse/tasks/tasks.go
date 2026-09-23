// Package tasks is the friend task queue.
//
// THE RULING (next-sprint 2026-09-22): assignments by bus note -> q:friend:<name>.
// Work lives in Redis; the bus stays what chat is. A friend's assignment is dealt
// onto the stream q:friend:emma exactly the way the dealer (#2624) deals a card
// onto q:<bench> -- one XADD, the same entry fields -- and a note is a letter,
// never a queue entry.
//
// THE LIFE OF A TASK, the four legs a window walks it through:
//
//	q:friend:emma       served. The stream is the queue. A task XADDed here -- fields
//	                       task, kind, ref, the dealer's Place fields -- is served to
//	                       Emma's window by nova-wake: one XREADGROUP over the window
//	                       group, claim-before-new like every pull queue in this repo.
//	TAKE                leased. The window's take mints THE ONE LEASE, SET NX on
//	                       friend:<name>:take:<task>, and writes the record task:<id>
//	                       state=working. "Working means leased with a live
//	                       heartbeat": the lease carries no TTL of its own -- its
//	                       heartbeat is the beat key friend:<name> (#2612), and no
//	                       other clock.
//	friend:emma expires returned. The beat lapses with the window and the reap reads
//	                       it: a live key means every lease is a live window's and
//	                       nothing returns; a missing key means the friend is gone,
//	                       and the leased task comes back to the stream -- the old
//	                       delivery is acked, the entry's own fields are XADDed back,
//	                       the record reads state=open again.
//	the PR-merged event done.  The record flips state=done on one trigger only: the
//	                       landed entry on cards:done (#2587), the lander's merge,
//	                       naming this task -- by label, the id the whole stream
//	                       joins on, or by pr, the task's own ref. An ok attempt is
//	                       an attempt that succeeded, a pr is a pull request opened,
//	                       a read is a friend's read: none of them is a merge, and
//	                       none of them flips it.
//
// THE KEYS. The store holds ids, counts and event ids and NOTHING ELSE (the division
// every Redis package here keeps): q:friend:<name> the stream, friend:<name> the beat
// key, friend:<name>:take:<task> the lease -- inside the beat's own friend:* namespace,
// because ~friend:* is what a window's own seat holds on the store -- and task:<id> the
// record, the sprint store's own shape. Every take key ends at the flip or at the reap
// and never anywhere else, so no key outlives the task it names.
//
// THE SEAMS. The window's serve poll (nova-wake serve) runs EnsureGroup once at start
// and Serve each interval; the window's TAKE runs Take; the reap is run by the friends
// who are still here -- a window whose own beat has lapsed cannot be the one reaping
// -- and the fold over cards:done runs Done. Nothing but those four reaches these
// keys, and no second system reads the stream behind them.
package tasks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/events"
	"github.com/redis/go-redis/v9"
)

// StreamPrefix is the queue's namespace: a friend's queue is q:friend:<name>, the
// ruling's own spelling of the dealer's q:<consumer> with the consumer being the
// friend.
const StreamPrefix = "q:friend:"

// PresencePrefix is the beat's namespace (#2612): friend:<name>, a string key with a
// TTL, alive exactly while the window beats. Presence is read from this key and from
// nothing else -- friend:<name>:last is a memory, never a presence (#2675).
const PresencePrefix = "friend:"

// TakeInfix is the lease's segment, inside the beat's own namespace so the window's
// own seat can mint it: friend:<name>:take:<task>.
const TakeInfix = ":take:"

// RecordPrefix is the task's record: task:<id>, the sprint store's own shape, so the
// dealer's table and this queue read one row.
const RecordPrefix = "task:"

// Group is the window's consumer group, the one group per stream: the group is the
// work's and the consumer is the friend's, so a task reaches exactly one window, and
// a second group over the same stream would duplicate the whole queue.
const Group = "window"

// MaxLen is the approximate cap a return XADD trims to: the dealer's own cap.
const MaxLen = 100_000

// LeaseIdle is how long a delivery may sit untaken before the next serve claims it
// back -- the silence a window that died between its serve and its take leaves behind
// it. It is redisq's ConsumerLease minute: the lease whose heartbeat lapses.
const LeaseIdle = time.Minute

// The task states. open is a task on the stream nobody holds; working is the take's
// word, leased with a live heartbeat; done is the one flip the PR-merged event owns.
const (
	StateOpen    = "open"
	StateWorking = "working"
	StateDone    = "done"
)

// Task is one dealt task, served to a window: the entry's id, the dealer's three
// fields, and every field the entry carried, so a returned task is dealt again with
// everything it was dealt with the first time.
type Task struct {
	ID     string
	Kind   string
	Ref    string
	Entry  string
	Fields map[string]string
}

// Record is the task's own row, task:<id>: what was dealt, who holds it, and where its
// life stands. It is the observable of every leg -- the take writes working, the reap
// writes open, the merge writes done.
type Record struct {
	ID       string
	Kind     string
	Ref      string
	Owner    string
	State    string
	LeasedAt time.Time
	DoneAt   time.Time
}

// Lease is one take's receipt: which friend, which task, which served entry, and the
// fencing token the flip and the reap compare before they end it.
type Lease struct {
	Friend string
	Task   string
	Entry  string
	Token  string
	At     time.Time
}

// Queue is the friend task queue over one Redis. The client is the seam: the fleet
// store in production, a miniredis in a check. The clock is injected so a stamp is
// never the wall's guess.
type Queue struct {
	rdb redis.Cmdable
	now func() time.Time
}

// New holds the queue over rdb.
func New(rdb redis.Cmdable) *Queue {
	return &Queue{rdb: rdb, now: func() time.Time { return time.Now().UTC() }}
}

// SetClock installs the clock the take and the flip stamp with.
func (q *Queue) SetClock(now func() time.Time) {
	if now != nil {
		q.now = now
	}
}

// Stream is the friend's queue, q:friend:<name>.
func Stream(friend string) (string, error) {
	name, err := slug(friend)
	if err != nil {
		return "", err
	}
	return StreamPrefix + name, nil
}

// PresenceKey is the friend's beat key, friend:<name>.
func PresenceKey(friend string) (string, error) {
	name, err := slug(friend)
	if err != nil {
		return "", err
	}
	return PresencePrefix + name, nil
}

// TakeKey is the lease, friend:<name>:take:<task>.
func TakeKey(friend, task string) (string, error) {
	name, err := slug(friend)
	if err != nil {
		return "", err
	}
	id, err := slug(task)
	if err != nil {
		return "", err
	}
	return PresencePrefix + name + TakeInfix + id, nil
}

// RecordKey is the task's record, task:<id>.
func RecordKey(task string) (string, error) {
	id, err := slug(task)
	if err != nil {
		return "", err
	}
	return RecordPrefix + id, nil
}

// EnsureGroup makes the window group on the friend's stream, making the stream if it is
// absent, at entry 0: a window that starts after the dealer has dealt reads the whole
// standing queue, exactly the fold's rule. A group that already exists is not an error
// -- every window start runs this.
func (q *Queue) EnsureGroup(ctx context.Context, friend string) error {
	stream, err := Stream(friend)
	if err != nil {
		return err
	}
	err = q.rdb.XGroupCreateMkStream(ctx, stream, Group, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return fmt.Errorf("the window group on %s: %w", stream, err)
	}
	return nil
}

// Serve hands the friend's next task to the window that calls it: one task per call,
// and a queue with nothing new costs one read. A delivery this group holds untaken
// past LeaseIdle -- a window that died between its serve and its take -- is claimed
// back first, the redisq pull's own shape; then the new entries are read. An entry
// with no task field is not a dealt task: its delivery is ended and it is refused by
// name, so one malformed entry cannot stall a queue behind it forever.
func (q *Queue) Serve(ctx context.Context, friend string) (*Task, error) {
	name, err := slug(friend)
	if err != nil {
		return nil, err
	}
	stream, err := Stream(name)
	if err != nil {
		return nil, err
	}
	msgs, _, err := q.rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream: stream, Group: Group, Consumer: name,
		MinIdle: LeaseIdle, Start: "0-0", Count: 1,
	}).Result()
	if err != nil {
		if strings.Contains(err.Error(), "NOGROUP") {
			return nil, fmt.Errorf("no %s group on %s; the window's start makes it -- tasks.EnsureGroup once (nova-wake serve runs it at start)", Group, stream)
		}
		return nil, err
	}
	var msg *redis.XMessage
	if len(msgs) > 0 {
		msg = &msgs[0]
	} else {
		res, rerr := q.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group: Group, Consumer: name,
			Streams: []string{stream, ">"},
			Count:   1,
			Block:   -1,
		}).Result()
		if errors.Is(rerr, redis.Nil) {
			return nil, nil
		}
		if rerr != nil {
			if strings.Contains(rerr.Error(), "NOGROUP") {
				return nil, fmt.Errorf("no %s group on %s; the window's start makes it -- tasks.EnsureGroup once (nova-wake serve runs it at start)", Group, stream)
			}
			return nil, rerr
		}
		for i := range res {
			if len(res[i].Messages) == 0 {
				continue
			}
			served := res[i].Messages[0]
			msg = &served
			break
		}
		if msg == nil {
			return nil, nil
		}
	}
	t, terr := q.task(stream, *msg)
	if terr != nil {
		// A malformed entry is ended, not skipped around: leaving it pending would
		// hand the same refusal to every serve until the end of the sprint.
		_ = q.ack(ctx, stream, msg.ID)
		return nil, terr
	}
	return t, nil
}

// Take leases a served task to the friend's window. It mints the one lease, SET NX
// with a fencing token, and writes the record: state=working, an owner, a leased_at.
// A task already leased is refused and this delivery is ended -- two windows of one
// friend share a queue, and the second take of one task is the first take's echo, not a
// turn. A task whose record already reads done is refused the same way: the merge
// ended it, and a stale redelivery is not a second life.
func (q *Queue) Take(ctx context.Context, friend string, t Task) (*Lease, error) {
	name, err := slug(friend)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(t.ID) == "" {
		return nil, fmt.Errorf("a take wants the served task; run tasks.Serve first, the take names the task it was handed")
	}
	id, err := slug(t.ID)
	if err != nil {
		return nil, err
	}
	stream, err := Stream(name)
	if err != nil {
		return nil, err
	}
	rec, ok, err := q.record(ctx, id)
	if err != nil {
		return nil, err
	}
	if ok && rec.State == StateDone {
		_ = q.ack(ctx, stream, t.Entry)
		return nil, fmt.Errorf("task %s is done -- the PR-merged event flipped it; this delivery is ended, not taken", id)
	}
	token, err := mintToken()
	if err != nil {
		return nil, err
	}
	key, err := TakeKey(name, id)
	if err != nil {
		return nil, err
	}
	// The value is the served entry's id and the fencing token: the flip and the reap
	// read the entry out of it to end the delivery, and compare the whole of it before
	// they end the lease, so a lease is never taken out from under a live one.
	won, err := q.rdb.SetNX(ctx, key, strings.TrimSpace(t.Entry)+" "+token, 0).Result()
	if err != nil {
		return nil, err
	}
	if !won {
		_ = q.ack(ctx, stream, t.Entry)
		return nil, fmt.Errorf("task %s is already leased to %s's window; one lease per task -- the take key %s", id, name, key)
	}
	at := q.now()
	if err := q.rdb.HSet(ctx, RecordPrefix+id, map[string]any{
		"kind":      strings.TrimSpace(t.Kind),
		"ref":       strings.TrimSpace(t.Ref),
		"owner":     name,
		"state":     StateWorking,
		"leased_at": stamp(at),
	}).Err(); err != nil {
		return nil, err
	}
	return &Lease{Friend: name, Task: id, Entry: strings.TrimSpace(t.Entry), Token: token, At: at}, nil
}

// Reap returns a friend's leased tasks to the stream once the friend's beat has
// lapsed. Presence is the lease's whole clock: while friend:<name> is alive, every
// lease is a live window's and the reap returns nothing; once the key is gone -- a
// window that exited, ran out of credit or was killed stops writing and the key lapses
// within its TTL, and the lapse IS the signal -- the friend's tasks come home.
//
// The return is written so a crash can never lose a friend's work: the task is XADDed
// back onto the stream BEFORE the lease is dropped, and only the winner of the lease
// compares flips the record. A duplicate the crash leaves behind is served and refused
// at take; a loss would be work thrown away.
func (q *Queue) Reap(ctx context.Context, friend string) ([]string, error) {
	name, err := slug(friend)
	if err != nil {
		return nil, err
	}
	presence, err := PresenceKey(name)
	if err != nil {
		return nil, err
	}
	if _, perr := q.rdb.Get(ctx, presence).Result(); perr == nil {
		return nil, nil // present: every lease is a live window's
	} else if !errors.Is(perr, redis.Nil) {
		return nil, perr
	}
	stream, err := Stream(name)
	if err != nil {
		return nil, err
	}
	prefix := PresencePrefix + name + TakeInfix
	keys, err := q.scan(ctx, prefix+"*")
	if err != nil {
		return nil, err
	}
	sort.Strings(keys)
	var returned []string
	for _, key := range keys {
		taskID, err := slug(strings.TrimPrefix(key, prefix))
		if err != nil {
			continue // not a lease this package minted; not ours to read
		}
		value, err := q.rdb.Get(ctx, key).Result()
		if errors.Is(err, redis.Nil) {
			continue // ended between the scan and this read
		}
		if err != nil {
			return nil, err
		}
		entry, _, _ := strings.Cut(value, " ")
		rec, ok, err := q.record(ctx, taskID)
		if err != nil {
			return nil, err
		}
		if ok && rec.State == StateDone {
			// done work never returns; a lease the flip never finished clearing ends here.
			_, _ = releaseScript.Run(ctx, q.rdb, []string{key}, value).Int64()
			continue
		}
		fields := q.returnFields(ctx, stream, entry, rec, ok)
		if fields == nil {
			// a lease that names no entry and no record spells no task anyone can
			// deal again; it is ended rather than returned as junk.
			_, _ = releaseScript.Run(ctx, q.rdb, []string{key}, value).Int64()
			continue
		}
		newID, err := q.rdb.XAdd(ctx, &redis.XAddArgs{
			Stream: stream, MaxLen: MaxLen, Approx: true, Values: fields,
		}).Result()
		if err != nil {
			return nil, err
		}
		_ = q.ack(ctx, stream, entry)
		won, err := releaseScript.Run(ctx, q.rdb, []string{key}, value).Int64()
		if err != nil {
			return nil, err
		}
		if won != 1 {
			// The PR-merged event flipped the task between the scan and the lease:
			// the work is done and nothing returns. The entry this reap just wrote
			// is this reap's own, and goes.
			_ = q.rdb.XDel(ctx, stream, newID).Err()
			continue
		}
		if ok {
			_ = q.rdb.HSet(ctx, RecordPrefix+taskID, "state", StateOpen).Err()
			_ = q.rdb.HDel(ctx, RecordPrefix+taskID, "leased_at").Err()
		}
		returned = append(returned, taskID)
	}
	return returned, nil
}

// Done flips a task's record to done, and it accepts one trigger: the PR-merged event
// -- a landed entry on cards:done, the lander's merge, naming this task, by label (the
// id the whole stream joins on) or by pr (the task's own ref). Every other event
// returns false and flips nothing: an ok attempt, a pr opened, a read, a landed entry
// for somebody else's task. A landed event delivered twice flips once.
//
// The record is written before the lease ends: a crash between the two leaves a done
// task with a stale lease -- the next reap clears it, or this same event redelivered
// does -- and never a working task with no lease and no queue entry, which is the
// lost-task order.
func (q *Queue) Done(ctx context.Context, taskID string, ev events.Event) (bool, error) {
	id, err := slug(taskID)
	if err != nil {
		return false, err
	}
	if ev.Kind != events.Landed {
		return false, nil // not the PR-merged event; it flips nothing
	}
	rec, ok, err := q.record(ctx, id)
	if err != nil || !ok {
		return false, err // never taken: there is no record to flip
	}
	if ev.Label != id && (ev.PR == "" || ev.PR != rec.Ref) {
		return false, nil // the merge of another task
	}
	if rec.State == StateDone {
		// The flip already happened; a redelivery flips nothing. A lease the flip
		// never finished clearing is ended here, because every take key ends at the
		// flip or at the reap and never anywhere else.
		q.endLease(ctx, rec.Owner, id)
		return false, nil
	}
	if err := q.rdb.HSet(ctx, RecordPrefix+id, map[string]any{
		"state":   StateDone,
		"done_at": stamp(q.now()),
	}).Err(); err != nil {
		return false, err
	}
	q.endLease(ctx, rec.Owner, id)
	return true, nil
}

// Record reads one task's row. ok is false when the task has no record -- a task that
// was dealt and never taken.
func (q *Queue) Record(ctx context.Context, taskID string) (Record, bool, error) {
	id, err := slug(taskID)
	if err != nil {
		return Record{}, false, err
	}
	return q.record(ctx, id)
}

// task reads one stream entry into a served Task.
func (q *Queue) task(stream string, m redis.XMessage) (*Task, error) {
	fields := stringFields(m.Values)
	id := strings.TrimSpace(fields["task"])
	if id == "" {
		return nil, fmt.Errorf("stream %s entry %s carries no task field; the dealer writes task, kind, ref -- this is not a dealt task", stream, m.ID)
	}
	return &Task{
		ID:     id,
		Kind:   strings.TrimSpace(fields["kind"]),
		Ref:    strings.TrimSpace(fields["ref"]),
		Entry:  m.ID,
		Fields: fields,
	}, nil
}

// record reads the task's hash. An absent hash is (zero, false, nil): it is a task
// nobody has taken, not an error.
func (q *Queue) record(ctx context.Context, id string) (Record, bool, error) {
	m, err := q.rdb.HGetAll(ctx, RecordPrefix+id).Result()
	if err != nil {
		return Record{}, false, err
	}
	if len(m) == 0 {
		return Record{}, false, nil
	}
	return Record{
		ID:       id,
		Kind:     m["kind"],
		Ref:      m["ref"],
		Owner:    m["owner"],
		State:    m["state"],
		LeasedAt: unstamp(m["leased_at"]),
		DoneAt:   unstamp(m["done_at"]),
	}, true, nil
}

// returnFields are the fields the reap deals a task back with: the entry's own, read
// from the stream where the dealer left them; the record's three when the entry has
// been trimmed away; and nil when neither spells a task -- a lease minted on nothing.
func (q *Queue) returnFields(ctx context.Context, stream, entry string, rec Record, ok bool) map[string]any {
	if entry != "" {
		msgs, err := q.rdb.XRange(ctx, stream, entry, entry).Result()
		if err == nil && len(msgs) == 1 {
			return anyFields(msgs[0].Values)
		}
	}
	if ok {
		return map[string]any{"task": rec.ID, "kind": rec.Kind, "ref": rec.Ref}
	}
	return nil
}

// endLease drops the task's take key and ends the delivery it named. The compare is
// the fencing token's: a lease is ended by the flip or the reap that read it, and
// never by a hand that held a different one.
func (q *Queue) endLease(ctx context.Context, friend, task string) {
	key, err := TakeKey(friend, task)
	if err != nil {
		return
	}
	value, err := q.rdb.Get(ctx, key).Result()
	if err != nil {
		return // no lease: nothing to end
	}
	won, err := releaseScript.Run(ctx, q.rdb, []string{key}, value).Int64()
	if err != nil || won != 1 {
		return // somebody ended it first
	}
	stream, err := Stream(friend)
	if err != nil {
		return
	}
	entry, _, _ := strings.Cut(value, " ")
	_ = q.ack(ctx, stream, entry)
}

// ack ends one delivery in the window group. The pending list is the delivery record,
// never the lease -- the take key is the lease -- so an ack that ends another window's
// delivery takes nothing from it.
func (q *Queue) ack(ctx context.Context, stream, entry string) error {
	if entry == "" {
		return nil
	}
	return q.rdb.XAck(ctx, stream, Group, entry).Err()
}

// releaseScript is the compare-and-del every lease here ends under: the stored value
// must be the one the caller read, or the script is a no-op. It is redisq's own atom,
// the same fencing rule over the same shape of key.
var releaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

// scan walks a match without ever running KEYS over the fleet store, in bounded pages.
func (q *Queue) scan(ctx context.Context, match string) ([]string, error) {
	var (
		cursor uint64
		out    []string
	)
	for {
		keys, next, err := q.rdb.Scan(ctx, cursor, match, 128).Result()
		if err != nil {
			return nil, err
		}
		out = append(out, keys...)
		if next == 0 {
			return out, nil
		}
		cursor = next
	}
}

// slug is the one normalizer every key here goes through: one token of letters, digits,
// '-' and '_', in lower case -- Emma and emma are one friend, and a task id is the
// label the whole stream joins on, in the spelling the stream writes it. A name that
// cannot be a key segment is refused by name rather than guessed: a ':' in a name would
// forge key segments, and the refusal is cheaper than the forgery.
func slug(name string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(name))
	if s == "" {
		return "", fmt.Errorf("a name is required; it wants one token of letters, digits, '-' and '_', got nothing")
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return "", fmt.Errorf("%q is not a name this queue keys on; it wants one token of letters, digits, '-' and '_'", name)
		}
	}
	return s, nil
}

// mintToken mints the lease's fencing token, redisq's own 16 random bytes.
func mintToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("the lease's token could not be minted: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// stamp writes a time the way the beat and the sprint store both write one.
func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// unstamp reads one back; unreadable is the zero time, never a guess.
func unstamp(s string) time.Time {
	if strings.TrimSpace(s) == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// stringFields reads a stream entry's values the way every reader here does.
func stringFields(values map[string]any) map[string]string {
	out := make(map[string]string, len(values))
	for k, v := range values {
		out[k] = asString(v)
	}
	return out
}

// anyFields writes a stream entry's values back the way the dealer wrote them.
func anyFields(values map[string]any) map[string]any {
	out := make(map[string]any, len(values))
	for k, v := range values {
		out[k] = asString(v)
	}
	return out
}

// asString carries a field's value across Redis's loose typing.
func asString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return fmt.Sprint(t)
	}
}
