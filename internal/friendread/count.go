// Package friendread folds kind=read entries on the cards:done stream into the
// per-friend done count.
//
// nova-tools #2678. The table used to count typed DISPOSITION lines by scanning
// GitHub comments. A friend's typed line is one cards:done entry. The stream
// names a transition with the field event, not kind: kind on a decide entry is
// the unit's kind, so a decide entry whose kind is the word read is not a
// friend's read. This package folds event=read. It does not call gh, and it
// does not read a comment body.
//
// Glenn, 2026-09-22: the friend columns are queue, working, done and status.
// Per-friend ok, fail and the calibration column are not answers this fold
// gives. The verdict stays on the read — APPROVE or HOLD, the word the lander
// and Jev's calibration join on — and is not turned into a score of the friend.
package friendread

import (
	"fmt"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const (
	// Stream is the one stream a friend's read is written to.
	Stream = "cards:done"
	// EventRead is the event field's value for one typed read.
	EventRead = "read"
)

// Entry is one cards:done stream entry: its id and the field map XREAD returns.
// The id is how a redelivery is told from a second read.
type Entry struct {
	ID     string
	Fields map[string]string
}

// Friend is one friend's done count for a single UTC day. Done is the number
// of kind=read events. There is no ok, no fail and no calibration on it.
type Friend struct {
	Who  string
	Done int
}

// Line is the one scannable line of a friend's done count.
func (f Friend) Line() string {
	return fmt.Sprintf("READS who=%s done=%d", oneline.Field(f.Who), f.Done)
}

// Read is one counted read. Verdict and score are carried so a later join can
// see what the friend wrote. They do not change Done.
type Read struct {
	ID      string
	Who     string
	Verdict string
	Score   string
	Head    string
	PR      string
	At      time.Time
}

// Result is the fold of one day.
type Result struct {
	Day     string
	Friends []Friend
	Reads   []Read
}

// Parse reads a fixture stream: one entry per line, `<ms>-<seq>` then
// key=value fields. A blank line and a line whose first non-space character
// is # are skipped. A DISPOSITION comment is refused — this is not a comment
// scanner.
func Parse(text string) ([]Entry, error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var out []Entry
	for i, line := range strings.Split(text, "\n") {
		n := i + 1
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		toks := strings.Fields(trim)
		id := toks[0]
		if !streamID(id) {
			return nil, fmt.Errorf("line %d: %q is not a cards:done stream id; a fixture entry is <ms>-<seq> and key=value fields, not a comment", n, id)
		}
		e := Entry{ID: id, Fields: map[string]string{}}
		for _, tok := range toks[1:] {
			key, val, ok := strings.Cut(tok, "=")
			if !ok || key == "" {
				return nil, fmt.Errorf("line %d: %q is not key=value; cards:done carries stream fields, not a DISPOSITION comment", n, tok)
			}
			key = strings.ToLower(key)
			if !fieldKey(key) {
				return nil, fmt.Errorf("line %d: field %q is not a stream field name", n, key)
			}
			e.Fields[key] = val
		}
		out = append(out, e)
	}
	return out, nil
}

// Count folds entries into per-friend done counts for one UTC day.
//
// day is YYYY-MM-DD. The fold does not read the clock: the caller names the
// day, and an entry counts when the UTC date of its at field is that day.
// roster is the friends to report, in that order, including a friend with
// nothing to count. A friend who appears only on the stream is reported after
// the roster. The first delivery of a stream id wins; a redelivery is not a
// second read and does not rewrite the first.
func Count(entries []Entry, day string, roster []string) (Result, error) {
	if _, err := time.Parse("2006-01-02", day); err != nil {
		return Result{}, fmt.Errorf("day %q is not YYYY-MM-DD; the fold counts one UTC day and does not assume today", day)
	}
	res := Result{Day: day}
	index := map[string]int{}
	for _, name := range roster {
		name = strings.TrimSpace(name)
		if name == "" || strings.ContainsAny(name, " \t=") {
			return Result{}, fmt.Errorf("roster name %q is not one token; a friend is a single who= word", name)
		}
		key := strings.ToLower(name)
		if _, ok := index[key]; ok {
			continue
		}
		index[key] = len(res.Friends)
		res.Friends = append(res.Friends, Friend{Who: name})
	}
	seen := map[string]struct{}{}
	for _, e := range entries {
		if e.ID == "" {
			return Result{}, fmt.Errorf("a cards:done entry has no stream id; the fold cannot tell a redelivery from a second read")
		}
		if _, ok := seen[e.ID]; ok {
			continue
		}
		seen[e.ID] = struct{}{}
		read, ok := countedRead(e, day)
		if !ok {
			continue
		}
		res.Reads = append(res.Reads, read)
		i, known := index[read.Who]
		if !known {
			i = len(res.Friends)
			index[read.Who] = i
			res.Friends = append(res.Friends, Friend{Who: read.Who})
		}
		res.Friends[i].Done++
	}
	return res, nil
}

// countedRead reports whether this entry is one friend's read on day.
// The transition is the event field. kind is not consulted: on a decide entry
// that field is the unit's kind, and the word read there is not this event.
func countedRead(e Entry, day string) (Read, bool) {
	f := e.Fields
	if f == nil || !strings.EqualFold(strings.TrimSpace(f["event"]), EventRead) {
		return Read{}, false
	}
	who := strings.ToLower(strings.TrimSpace(f["who"]))
	if who == "" || strings.ContainsAny(who, " \t=") {
		return Read{}, false
	}
	verdict, ok := countedVerdict(f["verdict"])
	if !ok {
		return Read{}, false
	}
	head := strings.ToLower(strings.TrimSpace(f["head"]))
	if !hexHead(head) {
		return Read{}, false
	}
	at, ok := parseAt(f["at"])
	if !ok || at.UTC().Format("2006-01-02") != day {
		return Read{}, false
	}
	return Read{
		ID:      e.ID,
		Who:     who,
		Verdict: verdict,
		Score:   strings.TrimSpace(f["score"]),
		Head:    head,
		PR:      strings.TrimSpace(f["pr"]),
		At:      at.UTC(),
	}, true
}

func countedVerdict(s string) (string, bool) {
	v := strings.ToUpper(strings.TrimSpace(s))
	if v == "APPROVE" || v == "HOLD" {
		return v, true
	}
	return "", false
}

func parseAt(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func hexHead(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}

func streamID(id string) bool {
	ms, seq, ok := strings.Cut(id, "-")
	if !ok || strings.Contains(seq, "-") {
		return false
	}
	return allDigits(ms) && allDigits(seq)
}

func fieldKey(key string) bool {
	if key == "" {
		return false
	}
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_':
		default:
			return false
		}
	}
	return true
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
