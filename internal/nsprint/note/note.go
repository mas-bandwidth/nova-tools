// Package note is the MERGE-NOTE record (nova-tools #4324, part 4): the
// typed lines a merge card ends with (a conflict it resolved, a wrong
// pattern it found, what the tree now expects), written on the stream's or
// the sprint's record so the deal pass renders them into every copy's brief
// at deal. A waiting or ready card gets them when it is dealt, a friend's
// copies at its next pull, a recut carries the note that caused it. A
// stream's notes expire when the landing that carried its merge card lands
// (stream.Merge deletes the key); a sprint's notes stay until `note drop`.
//
// Keys:
//
//	ws:<stream>:notes     list  MERGE-NOTE by=<who> at=<ms> <text>, oldest first
//	sprint:<S>:notes      list  the same, the sprint record's (the coordinator one level up)
//
// Every read is one pipeline; the lines are data for a brief, never a verb.
package note

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Kind is the typed line's first word.
const Kind = "MERGE-NOTE"

// StreamKey is a stream's notes list.
func StreamKey(stream string) string { return "ws:" + stream + ":notes" }

// SprintKey is a sprint's notes list.
func SprintKey(sprint string) string { return "sprint:" + sprint + ":notes" }

// Line is one typed note line: MERGE-NOTE by=<who> at=<ms> <text>. The text
// is one line (inner whitespace folded); by has no spaces.
func Line(by string, at time.Time, text string) (string, error) {
	by = strings.TrimSpace(by)
	text = strings.Join(strings.Fields(text), " ")
	switch {
	case by == "" || strings.ContainsAny(by, " \t"):
		return "", errors.New("a note names one --by word")
	case text == "":
		return "", errors.New("a note is one non-empty line")
	}
	return fmt.Sprintf("%s by=%s at=%d %s", Kind, by, at.UnixMilli(), text), nil
}

// Post appends one note to key and returns the list's length.
func Post(ctx context.Context, c redis.Cmdable, key, by, text string, at time.Time) (int64, error) {
	line, err := Line(by, at, text)
	if err != nil {
		return 0, err
	}
	return c.RPush(ctx, key, line).Result()
}

// Load reads every note of the keys, in key order then oldest first, in one
// round trip. A missing key contributes nothing.
func Load(ctx context.Context, c redis.Cmdable, keys ...string) ([]string, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	pipe := c.Pipeline()
	cmds := make([]*redis.StringSliceCmd, len(keys))
	for i, k := range keys {
		cmds[i] = pipe.LRange(ctx, k, 0, -1)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	var out []string
	for _, cmd := range cmds {
		for _, l := range cmd.Val() {
			if l = strings.TrimSpace(l); l != "" {
				out = append(out, l)
			}
		}
	}
	return out, nil
}

// Drop deletes a notes list and returns how many notes it held.
func Drop(ctx context.Context, c redis.Cmdable, key string) (int64, error) {
	pipe := c.Pipeline()
	n := pipe.LLen(ctx, key)
	pipe.Del(ctx, key)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return 0, err
	}
	return n.Val(), nil
}

// ForCopy is the notes a copy's brief carries: its stream's, then the
// sprint's. An empty sprint reads the default sprint (the lowest score of
// sprint:order); an empty stream reads no stream notes.
func ForCopy(ctx context.Context, c redis.Cmdable, stream, sprint string) ([]string, error) {
	if sprint == "" {
		s, err := c.ZRange(ctx, "sprint:order", 0, 0).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
		if len(s) > 0 {
			sprint = s[0]
		}
	}
	var keys []string
	if stream != "" {
		keys = append(keys, StreamKey(stream))
	}
	if sprint != "" {
		keys = append(keys, SprintKey(sprint))
	}
	return Load(ctx, c, keys...)
}

// Parse reads a note line's by, at and text; ok is false for another line.
func Parse(line string) (by string, at time.Time, text string, ok bool) {
	f := strings.Fields(line)
	if len(f) < 4 || f[0] != Kind || !strings.HasPrefix(f[1], "by=") || !strings.HasPrefix(f[2], "at=") {
		return "", time.Time{}, "", false
	}
	ms, err := strconv.ParseInt(strings.TrimPrefix(f[2], "at="), 10, 64)
	if err != nil {
		return "", time.Time{}, "", false
	}
	return strings.TrimPrefix(f[1], "by="), time.UnixMilli(ms), strings.Join(f[3:], " "), true
}
