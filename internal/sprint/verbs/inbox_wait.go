package verbs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// inbox --wait: the coordinator blocks on the sprint's notification stream
// until a judgment is raised (the owner's ask, 2026-09-30; section 3's inbox).
// The stream is the log, {p}sprint:log@<epoch> (L2 1.1): every note is a line
// of it, entry id <seq>-0, fields n and d, d the stored body whose `k` is "n"
// for a note and whose meta names the note's kind. The wait reads it with
// Redis XREAD BLOCK from the coordinator's cursor, the last seq it has seen,
// and returns the first JUDGMENT line after it: a line that needs the
// coordinator (sprint.Judgment), never the machine's DECIDED or HAPPENED
// lines, which it reads past.

// NoteStream is a blocking read of streams: Redis XREAD COUNT BLOCK.
type NoteStream interface {
	// Read returns the entries of each stream after its id, at most count
	// of each, the streams in the order given. With none there it blocks up
	// to block and returns none. A connection lost is a *StreamLost, and the
	// next Read, on a new connection, starts from the ids it is given.
	Read(ctx context.Context, from []StreamAt, count int, block time.Duration) ([]StreamEntry, error)
}

// StreamAt is a stream and the id after which to read it.
type StreamAt struct {
	Key, After string
}

// StreamEntry is one entry of a stream: its stream, its id and its fields.
type StreamEntry struct {
	Key    string
	ID     string
	Fields map[string]string
}

// StreamLost is a read whose connection was lost: it read nothing, and a read
// from the same ids reads what it would have.
type StreamLost struct{ Err error }

func (l *StreamLost) Error() string { return "the stream's connection was lost: " + l.Err.Error() }
func (l *StreamLost) Unwrap() error { return l.Err }

// InboxWait is inbox --wait: the stream it blocks on, how long, and the clock
// the deadline is read on.
type InboxWait struct {
	// Notes is the store's notification stream (RedisNotes on the store,
	// MemStream on the twin).
	Notes NoteStream
	// Timeout is --timeout: 0 is WaitTimeout.
	Timeout time.Duration
	// Now is the clock the deadline is read on; nil is the wall clock.
	Now func() time.Time
}

// WaitTimeout is how long inbox --wait blocks when no --timeout is given: a
// judgment's deadline (sprint.DeadlineJudgment), after which the tick marks
// it overdue.
const WaitTimeout = sprint.DeadlineJudgment

// WaitTimeoutMax is the longest --timeout: a day.
const WaitTimeoutMax = 24 * time.Hour

const (
	// waitSlice is the longest one blocked read lasts: the wait reads again
	// after it, so a connection that died without a word is found within it.
	waitSlice = 30 * time.Second
	// waitCount is the most entries one read returns of a stream.
	waitCount = 100
	// waitLost is how many reads in a row may lose their connection before
	// the wait gives up; each one resumes from the cursor.
	waitLost = 3
)

// logKey is the log of an epoch, the stream a step appends its lines to (L2
// 1.1: the deployment's prefix, then sprint:log@<epoch>).
func logKey(prefix string, epoch uint64) string {
	return prefix + "sprint:log@" + strconv.FormatUint(epoch, 10)
}

// streamSeq is the seq of a log entry's id, <seq>-0 (L2 2).
func streamSeq(id string) (uint64, bool) {
	s, rest, ok := strings.Cut(id, "-")
	if !ok || rest != "0" {
		return 0, false
	}
	n, err := strconv.ParseUint(s, 10, 64)
	return n, err == nil
}

// storedNote is a stored log body as far as the wait reads it (L2 1.1): the
// kind tag, the time, what the line is about, and its meta.
type storedNote struct {
	K     string                     `json:"k"`
	MS    string                     `json:"ms"`
	About []string                   `json:"about"`
	Meta  map[string]json.RawMessage `json:"meta"`
}

// judgmentOf reads a log entry: its line when it is a note that needs the
// coordinator (a JUDGMENT line: its meta's kind is sprint.Judgment), false
// for any other line.
func judgmentOf(en StreamEntry) (noteLine, bool, error) {
	d, ok := en.Fields["d"]
	if !ok {
		return noteLine{}, false, fmt.Errorf("inbox --wait: the log's entry %s of %s has no body", en.ID, en.Key)
	}
	var s storedNote
	if err := json.Unmarshal([]byte(d), &s); err != nil {
		return noteLine{}, false, fmt.Errorf("inbox --wait: the log's entry %s of %s: %w", en.ID, en.Key, err)
	}
	if s.K != "n" {
		return noteLine{}, false, nil
	}
	l := noteLine{Kind: "note", AtMS: s.MS, About: s.About, Meta: s.Meta}
	return l, l.meta("kind") == sprint.Judgment, nil
}

// noteIDAt is a note's id at an epoch: n and its seq, with the epoch suffix
// from epoch 1 (1.3.4).
func noteIDAt(seq, epoch uint64) string {
	id := "n" + strconv.FormatUint(seq, 10)
	if epoch > 0 {
		id += "~" + strconv.FormatUint(epoch, 10)
	}
	return id
}

// waited is what a wait found: the judgment's line and id, or none; and the
// epoch and seq the cursor stands at after it.
type waited struct {
	ID    string
	Line  noteLine
	Found bool
	Epoch uint64
	Last  uint64
}

// waitJudgment blocks on the log from the cursor until the first JUDGMENT line
// after it, or until the deadline. It reads the epoch's log after the cursor
// and the next epoch's from its start, so a clear during the wait moves it to
// the new epoch's log and nothing raised there is missed. Every line read, of
// any kind, moves the cursor; a read whose connection is lost is made again
// from the cursor, at most waitLost times in a row.
func waitJudgment(ctx context.Context, notes NoteStream, prefix string, epoch, after uint64, deadline time.Time, now func() time.Time) (waited, error) {
	cur := StreamAt{Key: logKey(prefix, epoch), After: strconv.FormatUint(after, 10) + "-0"}
	next := StreamAt{Key: logKey(prefix, epoch+1), After: "0-0"}
	last := after
	lost := 0
	for {
		left := deadline.Sub(now())
		if left <= 0 {
			return waited{Epoch: epoch, Last: last}, nil
		}
		block := min(left, waitSlice)
		if block < time.Millisecond {
			block = time.Millisecond // XREAD BLOCK 0 blocks for ever
		}
		es, err := notes.Read(ctx, []StreamAt{cur, next}, waitCount, block)
		if err != nil {
			var sl *StreamLost
			if errors.As(err, &sl) && ctx.Err() == nil && lost < waitLost {
				lost++
				continue // a new connection, from the same cursor
			}
			return waited{Epoch: epoch, Last: last}, err
		}
		lost = 0
		for _, en := range es {
			switch en.Key {
			case cur.Key:
			case next.Key:
				// The epoch moved (a clear, 1.5.2): the next epoch's log from its
				// start, and the one after it beside.
				epoch++
				cur, next = next, StreamAt{Key: logKey(prefix, epoch+1), After: "0-0"}
			default:
				return waited{Epoch: epoch, Last: last}, fmt.Errorf("inbox --wait: a read of %s and %s returned an entry of %s", cur.Key, next.Key, en.Key)
			}
			seq, ok := streamSeq(en.ID)
			if !ok {
				return waited{Epoch: epoch, Last: last}, fmt.Errorf("inbox --wait: the log's entry id %s of %s is not <seq>-0", en.ID, en.Key)
			}
			cur.After, last = en.ID, seq
			l, ok, err := judgmentOf(en)
			if err != nil {
				return waited{Epoch: epoch, Last: last}, err
			}
			if ok {
				return waited{ID: noteIDAt(seq, epoch), Line: l, Found: true, Epoch: epoch, Last: seq}, nil
			}
		}
	}
}

// inboxWait is Inbox with --wait (the owner's ask, 2026-09-30; section 3's
// inbox): one read through the one read path finds the active epoch and the
// log's last seq, and then the wait blocks on the log from the cursor
// (InboxReq.After, the last seq the coordinator has seen) until the first
// JUDGMENT line after it, or --timeout. A judgment raised before the wait
// began, after the cursor, is returned at once. The view is the inbox's shape
// with the one judgment as its one group, Last its seq; at the timeout it has
// no group and Last is the last seq read, which the next wait takes as its
// cursor.
//
// The read is one round trip (Result.Trips). The blocked reads are not: a
// blocked XREAD is no round trip in the tick's budget (1.4.2), and the store
// answers it the moment a step appends a line (Redis wakes a blocked XREAD
// on the XADD), so a judgment reaches the waiting coordinator as its step
// commits, with no poll.
func inboxWait(ctx context.Context, e *Env, req InboxReq) (Result, error) {
	const verb = "inbox"
	w := req.Wait
	if w.Notes == nil {
		return Result{Verb: verb}, refuseLocal(verb, sprintfn.CodeRequest, "inbox --wait has no notification stream to block on")
	}
	timeout := w.Timeout
	if timeout == 0 {
		timeout = WaitTimeout
	}
	if timeout < 0 || timeout > WaitTimeoutMax {
		return Result{Verb: verb}, refuseLocal(verb, sprintfn.CodeRequest, "inbox --wait --timeout %s is outside 0 to %s", timeout, WaitTimeoutMax)
	}
	now := w.Now
	if now == nil {
		now = time.Now
	}
	res, err := e.Do(ctx, Planned{Verb: verb, Read: func(epoch tset.Decimal) *sprintfn.ReadRequest {
		return &sprintfn.ReadRequest{Epoch: epoch, Tset: []tset.ReadQuery{{Kind: "last"}}}
	}})
	if err != nil {
		return res, err
	}
	if len(res.Read.Tset) != 1 {
		return res, errors.New("inbox --wait: the read answered the wrong number of queries")
	}
	epoch, ok := undec(res.Read.Epoch)
	last, ok2 := undec(res.Read.Tset[0].LastSeq)
	if !ok || !ok2 {
		return res, fmt.Errorf("inbox --wait: the read's epoch %q or last seq %q is not a number", res.Read.Epoch, res.Read.Tset[0].LastSeq)
	}
	if req.After > last {
		return res, refuseLocal(verb, sprintfn.CodeRequest,
			"the cursor %d is past the log's last line %d at epoch %d (a cursor of another epoch?): run inbox for the cursor", req.After, last, epoch)
	}
	got, err := waitJudgment(ctx, w.Notes, e.Names.Prefix, epoch, req.After, now().Add(timeout), now)
	if err != nil {
		return res, err
	}
	res.Epoch = got.Epoch
	v := InboxView{Groups: []sprint.Group{}, Cursor: strconv.FormatUint(req.After, 10), Last: strconv.FormatUint(got.Last, 10),
		At: now().UTC()}
	if got.Found {
		v.Groups = append(v.Groups, groupOfLine(got.ID, got.Line))
		res.Said = groupText(v.Groups[0]) + fmt.Sprintf("INBOX WAIT judgment=%s last=%s\n", got.ID, v.Last)
	} else {
		res.Said = fmt.Sprintf("INBOX WAIT nothing: no judgment after %s in %s; last=%s\n", v.Cursor, timeout, v.Last)
	}
	if req.Out != nil {
		*req.Out = v
	}
	return res, nil
}

// ---- the store's stream

// RedisNotes is the store's NoteStream: XREAD COUNT BLOCK on a go-redis
// client. A blocked read holds one of the client's connections for its block;
// go-redis gives a blocking command its block and ten seconds more to answer
// before it drops the connection. A reply that is not the server's (a
// connection reset, closed or timed out) is a *StreamLost, and the next read
// takes a new connection from the pool.
type RedisNotes struct {
	C redis.Cmdable
}

// Read is XREAD COUNT count BLOCK block STREAMS <keys> <ids>.
func (r RedisNotes) Read(ctx context.Context, from []StreamAt, count int, block time.Duration) ([]StreamEntry, error) {
	if block < time.Millisecond {
		block = time.Millisecond // BLOCK 0 blocks for ever
	}
	streams := make([]string, 0, 2*len(from))
	for _, f := range from {
		streams = append(streams, f.Key)
	}
	for _, f := range from {
		streams = append(streams, f.After)
	}
	res, err := r.C.XRead(ctx, &redis.XReadArgs{Streams: streams, Count: int64(count), Block: block}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil // the block ran out with nothing
	}
	if err != nil {
		var server redis.Error
		if errors.As(err, &server) || ctx.Err() != nil {
			return nil, err
		}
		return nil, &StreamLost{Err: err}
	}
	var out []StreamEntry
	for _, s := range res {
		for _, m := range s.Messages {
			en := StreamEntry{Key: s.Stream, ID: m.ID, Fields: make(map[string]string, len(m.Values))}
			for k, v := range m.Values {
				en.Fields[k] = fmt.Sprint(v)
			}
			out = append(out, en)
		}
	}
	return out, nil
}

// ---- the twin's stream

// MemStream is a NoteStream in memory, the twin's: Append adds an entry, and
// a Read blocked on the stream wakes at once, as Redis wakes a blocked XREAD
// on an XADD. Its block is timed by After, an injected clock (nil: the wall
// clock). OnBlock, when set, is called each time a read blocks. Lose makes the
// reads blocked now, and the next ones, lose their connection.
type MemStream struct {
	After   func(time.Duration) <-chan time.Time
	OnBlock func()

	mu      sync.Mutex
	streams map[string][]StreamEntry
	wake    chan struct{}
	lose    int
}

// NewMemStream is an empty MemStream.
func NewMemStream() *MemStream {
	return &MemStream{streams: map[string][]StreamEntry{}, wake: make(chan struct{})}
}

// Append adds an entry to a stream, as XADD with an explicit id: the id must
// be above the stream's last.
func (m *MemStream) Append(key, id string, fields map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.streams[key]
	if len(s) > 0 && compareStreamID(id, s[len(s)-1].ID) <= 0 {
		return fmt.Errorf("the id %s is not above %s's last, %s", id, key, s[len(s)-1].ID)
	}
	f := make(map[string]string, len(fields))
	for k, v := range fields {
		f[k] = v
	}
	m.streams[key] = append(s, StreamEntry{Key: key, ID: id, Fields: f})
	close(m.wake)
	m.wake = make(chan struct{})
	return nil
}

// Lose makes the next n reads lose their connection, a read blocked now the
// first of them.
func (m *MemStream) Lose(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lose = n
	close(m.wake)
	m.wake = make(chan struct{})
}

// Read is XREAD COUNT BLOCK on the streams in memory.
func (m *MemStream) Read(ctx context.Context, from []StreamAt, count int, block time.Duration) ([]StreamEntry, error) {
	m.mu.Lock()
	if m.lose > 0 {
		m.lose--
		m.mu.Unlock()
		return nil, &StreamLost{Err: errors.New("the twin's stream dropped the connection")}
	}
	out, wake := m.after(from, count), m.wake
	m.mu.Unlock()
	if len(out) > 0 || block <= 0 {
		return out, nil
	}
	var timeout <-chan time.Time
	if m.After != nil {
		timeout = m.After(block)
	} else {
		t := time.NewTimer(block)
		defer t.Stop()
		timeout = t.C
	}
	if m.OnBlock != nil {
		m.OnBlock()
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout:
			return nil, nil
		case <-wake:
		}
		m.mu.Lock()
		if m.lose > 0 {
			m.lose--
			m.mu.Unlock()
			return nil, &StreamLost{Err: errors.New("the twin's stream dropped the connection")}
		}
		out, wake = m.after(from, count), m.wake
		m.mu.Unlock()
		if len(out) > 0 {
			return out, nil
		}
	}
}

// after is the entries of each stream after its id, at most count of each.
func (m *MemStream) after(from []StreamAt, count int) []StreamEntry {
	var out []StreamEntry
	for _, f := range from {
		n := 0
		for _, en := range m.streams[f.Key] {
			if n == count {
				break
			}
			if compareStreamID(en.ID, f.After) > 0 {
				out = append(out, StreamEntry{Key: en.Key, ID: en.ID, Fields: en.Fields})
				n++
			}
		}
	}
	return out
}

// compareStreamID orders two stream ids <ms>-<seq> as Redis does; an id that
// does not parse orders below every one that does.
func compareStreamID(a, b string) int {
	parse := func(id string) (uint64, uint64, bool) {
		x, y, ok := strings.Cut(id, "-")
		if !ok {
			return 0, 0, false
		}
		p, err1 := strconv.ParseUint(x, 10, 64)
		q, err2 := strconv.ParseUint(y, 10, 64)
		return p, q, err1 == nil && err2 == nil
	}
	a1, a2, aok := parse(a)
	b1, b2, bok := parse(b)
	switch {
	case aok != bok:
		if aok {
			return 1
		}
		return -1
	case a1 != b1:
		if a1 < b1 {
			return -1
		}
		return 1
	case a2 != b2:
		if a2 < b2 {
			return -1
		}
		return 1
	}
	return 0
}
