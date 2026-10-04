package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/redis/go-redis/v9"
)

// logCardName is the sprint key of one card's log lines.
func logCardName(id string) string { return "logcard:" + id }

// logCardIDs is every id Line.About would accept for the line: each name, each
// dotted prefix of a name (a primary of a work or read card), and the line's
// primary when it is not a note. Empty names are left out. Each id is once.
func logCardIDs(l sprint.Line) []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, n := range l.Names() {
		if n == "" {
			continue
		}
		parts := strings.Split(n, ".")
		cur := parts[0]
		add(cur)
		for _, p := range parts[1:] {
			cur += "." + p
			add(cur)
		}
	}
	if l.Note == nil {
		add(l.Primary)
	}
	return out
}

// logCardID is the card id of a key named logcard:<id> at epoch, or "" when
// k is not that key. The caller holds no lock; the function only reads k.
func logCardID(k string, epoch uint64) string {
	mark := "sprint:" + logCardName("")
	i := strings.LastIndex(k, mark)
	if i < 0 {
		return ""
	}
	tail := k[i+len(mark):]
	suf := epochSuffix(epoch)
	if suf == "" {
		if strings.Contains(tail, "@") {
			return ""
		}
		return tail
	}
	if !strings.HasSuffix(tail, suf) {
		return ""
	}
	id := strings.TrimSuffix(tail, suf)
	if id == "" || strings.Contains(id, "@") {
		return ""
	}
	return id
}

// appendLocked appends one log line and, when the index already holds every
// earlier line (or this is the first), the cards the line is about. The
// caller holds m.mu.
func (m *Mem) appendLocked(l *memLog, line sprint.Line) {
	m.seq++
	l.lines = append(l.lines, memLine{fmt.Sprintf("%d-0", m.seq), line})
	if len(l.lines) != 1 && !l.indexed {
		return
	}
	if l.byCard == nil {
		l.byCard = map[string][]sprint.Line{}
	}
	for _, id := range logCardIDs(line) {
		l.byCard[id] = append(l.byCard[id], line)
	}
	l.indexed = true
}

// LogCard is the lines indexed under id. An empty log is indexed. A log
// written before the index is not, and the lines are not returned.
func (m *Mem) LogCard(_ context.Context, id string) ([]sprint.Line, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.log()
	if len(l.lines) > 0 && !l.indexed {
		return nil, false, nil
	}
	return append([]sprint.Line(nil), l.byCard[id]...), true, nil
}

// LogIndex rebuilds the card index from the lines the log holds. An old
// backend is a read of an earlier epoch and is not written: the catch-up
// is skipped, which is not an error.
func (m *Mem) LogIndex(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.old {
		return nil
	}
	l := m.log()
	l.byCard = map[string][]sprint.Line{}
	for _, x := range l.lines {
		for _, id := range logCardIDs(x.line) {
			l.byCard[id] = append(l.byCard[id], x.line)
		}
	}
	l.indexed = true
	return nil
}

// DropLogIndex forgets the card index and keeps the lines: a log written
// before the index. For tests.
func (m *Mem) DropLogIndex() {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.log()
	l.byCard = nil
	l.indexed = false
}

// LogCards is the card ids the index names. It does not create an empty log.
func (m *Mem) LogCards(context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.logs[m.epoch]
	if l == nil || len(l.byCard) == 0 {
		return nil, nil
	}
	return slicesSorted(l.byCard), nil
}

func slicesSorted(by map[string][]sprint.Line) []string {
	out := make([]string, 0, len(by))
	for id := range by {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// LogAbout is the epoch's log lines about id, in order. It reads that card's
// index when the index holds every line. When it is behind, including a log
// written before the index, the lines are that card's lines of the epoch log
// (Line.About). A read writes nothing: not the per-card streams, not the
// card set, not the index. A lost catch-up is not this call's error. Release
// indexes the lines it appends. Catch-up of a pre-index log is LogIndex,
// for a writer, best-effort, and it does not write an old backend. This
// read does not call it.
func (st *Store) LogAbout(ctx context.Context, id string) ([]sprint.Line, error) {
	st, err := st.pin(ctx)
	if err != nil {
		return nil, err
	}
	lines, indexed, err := st.B.LogCard(ctx, id)
	if err != nil {
		return nil, err
	}
	if indexed {
		return lines, nil
	}
	all, err := st.logUpTo(ctx, "")
	if err != nil {
		return nil, err
	}
	var about []sprint.Line
	for _, line := range all {
		if line.About(id) {
			about = append(about, line)
		}
	}
	return about, nil
}

// writeLogLine appends one line to the epoch log and to each card's own
// stream, in the caller's pipeline. ids collects the cards named.
func (r *Redis) writeLogLine(ctx context.Context, p redis.Pipeliner, line sprint.Line, body []byte, ids map[string]struct{}) {
	p.XAdd(ctx, &redis.XAddArgs{Stream: r.key(keyLog), Values: []any{"line", string(body)}})
	for _, id := range logCardIDs(line) {
		ids[id] = struct{}{}
		p.XAdd(ctx, &redis.XAddArgs{Stream: r.key(logCardName(id)), Values: []any{"line", string(body)}})
	}
}

// LogCard reads the card's lines and whether the index count matches the log's
// length, in one transaction. A missing index on an empty log matches.
func (r *Redis) LogCard(ctx context.Context, id string) ([]sprint.Line, bool, error) {
	var mark *redis.StringCmd
	var length *redis.IntCmd
	var msgs *redis.XMessageSliceCmd
	_, err := r.C.TxPipelined(ctx, func(p redis.Pipeliner) error {
		mark = p.Get(ctx, r.key(keyLogIndex))
		length = p.XLen(ctx, r.key(keyLog))
		msgs = p.XRange(ctx, r.key(logCardName(id)), "-", "+")
		return nil
	})
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, false, err
	}
	indexedN := int64(0)
	if s, gerr := mark.Result(); gerr == nil {
		indexedN, _ = strconv.ParseInt(s, 10, 64)
	}
	xlen, xerr := length.Result()
	if xerr != nil {
		return nil, false, xerr
	}
	if indexedN != xlen {
		return nil, false, nil
	}
	got, merr := msgs.Result()
	if merr != nil && !errors.Is(merr, redis.Nil) {
		return nil, false, merr
	}
	var lines []sprint.Line
	for _, msg := range got {
		s, _ := msg.Values["line"].(string)
		var line sprint.Line
		if s != "" && json.Unmarshal([]byte(s), &line) == nil {
			lines = append(lines, line)
		}
	}
	return lines, true, nil
}

// LogCards is the card ids named in the index set.
func (r *Redis) LogCards(ctx context.Context) ([]string, error) {
	ids, err := r.C.SMembers(ctx, r.key(keyLogCards)).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	sort.Strings(ids)
	return ids, nil
}

// LogIndex rebuilds the card index from the log and records the log's length.
// A line appended while it reads fails the transaction, and it reads again.
// An old backend is not written. A read does not call this: a lost write
// must not fail the read, and under load the watch may never land.
func (r *Redis) LogIndex(ctx context.Context) error {
	if r.Old {
		return nil
	}
	logKey := r.key(keyLog)
	idxKey := r.key(keyLogIndex)
	setKey := r.key(keyLogCards)
	var err error
	for i := 0; i < 8; i++ {
		err = r.C.Watch(ctx, func(tx *redis.Tx) error {
			mark, gerr := tx.Get(ctx, idxKey).Result()
			if gerr != nil && !errors.Is(gerr, redis.Nil) {
				return gerr
			}
			xlen, xerr := tx.XLen(ctx, logKey).Result()
			if xerr != nil {
				return xerr
			}
			if n, _ := strconv.ParseInt(mark, 10, 64); mark != "" && n == xlen {
				return nil
			}
			var bodies []string
			var lines []sprint.Line
			start := "-"
			for {
				msgs, rerr := tx.XRangeN(ctx, logKey, start, "+", int64(logPage)).Result()
				if rerr != nil {
					return rerr
				}
				for _, msg := range msgs {
					s, _ := msg.Values["line"].(string)
					var line sprint.Line
					if s == "" || json.Unmarshal([]byte(s), &line) != nil {
						continue
					}
					bodies = append(bodies, s)
					lines = append(lines, line)
				}
				if len(msgs) < logPage {
					break
				}
				start = "(" + msgs[len(msgs)-1].ID
			}
			old, serr := tx.SMembers(ctx, setKey).Result()
			if serr != nil && !errors.Is(serr, redis.Nil) {
				return serr
			}
			_, perr := tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
				var del []string
				for _, id := range old {
					del = append(del, r.key(logCardName(id)))
				}
				for from := 0; from < len(del); from += deleteChunk {
					p.Del(ctx, del[from:min(from+deleteChunk, len(del))]...)
				}
				if len(old) > 0 {
					p.Del(ctx, setKey)
				}
				ids := map[string]struct{}{}
				for i, line := range lines {
					for _, id := range logCardIDs(line) {
						ids[id] = struct{}{}
						p.XAdd(ctx, &redis.XAddArgs{Stream: r.key(logCardName(id)), Values: []any{"line", bodies[i]}})
					}
				}
				if len(ids) > 0 {
					members := make([]any, 0, len(ids))
					for id := range ids {
						members = append(members, id)
					}
					p.SAdd(ctx, setKey, members...)
				}
				p.Set(ctx, idxKey, strconv.FormatInt(xlen, 10), 0)
				return nil
			})
			return perr
		}, logKey, idxKey)
		if !errors.Is(err, redis.TxFailedErr) {
			return err
		}
	}
	return fmt.Errorf("the log index was not written: the log kept changing")
}
