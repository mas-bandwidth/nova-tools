package taskcard

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/redis/go-redis/v9"
)

// TruthRow is one line of the GitHub truth file Rowan writes for a migrate
// (stream|where|id|kind|repo#n|state): state is the GitHub state of repo#n
// (open, closed, merged, or - for none). Migrate makes no GitHub call.
type TruthRow struct {
	Stream, Where, ID, Kind, Ref, State string
}

// ReadTruth reads the truth file; blank lines and # comments are skipped, a
// line that is not six |-separated fields is an error naming its number.
func ReadTruth(r io.Reader) ([]TruthRow, error) {
	var rows []TruthRow
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "|")
		if len(f) != 6 {
			return nil, fmt.Errorf("truth line %d: %d fields, want stream|where|id|kind|repo#n|state", n, len(f))
		}
		rows = append(rows, TruthRow{Stream: f[0], Where: f[1], ID: f[2], Kind: f[3], Ref: f[4], State: f[5]})
	}
	return rows, sc.Err()
}

// Place is where the truth puts a task, or "" when the record's own facts
// decide: a merged PR (or an issue closed by one) is landed; a closed one is
// done/ok.
func (t TruthRow) Place() (where, ok string) {
	switch t.State {
	case "merged":
		return "landed", "ok"
	case "closed":
		return "done", "ok"
	}
	return "", ""
}

// MigrateResult counts one migrate: the keys scanned, the task records
// placed per where ("" is null), and the keys skipped per reason.
type MigrateResult struct {
	Scanned int
	Placed  map[string]int
	Skipped map[string]int
}

// Migrate is the one-time walk of task:* (the only SCAN, run once): every
// task hash is placed in exactly one set by ns_tcard_place, in batches of
// batch ids per call: the truth row's place when it has one (merged ->
// landed, closed -> done), else the record's where, its one ws set, its
// friend-queue state and idx sets; a working task with no beat in the last
// ten minutes goes back to ready. It is idempotent: a second run places
// every record where it already is.
func Migrate(ctx context.Context, c redis.UniversalClient, sprint, by string, truth []TruthRow, batch int) (MigrateResult, error) {
	if batch < 1 {
		batch = 200
	}
	byID := map[string]TruthRow{}
	for _, t := range truth {
		byID[t.ID] = t
	}
	res := MigrateResult{Placed: map[string]int{}, Skipped: map[string]int{}}
	var pending []string
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		args := []any{by, sprint}
		for _, id := range pending {
			t := byID[id]
			where, ok := t.Place()
			args = append(args, id, where, ok, t.Stream)
		}
		reply, err := c.FCall(ctx, FnPlace, nil, args...).Result()
		if err != nil {
			return fmt.Errorf("%s: %w", FnPlace, err)
		}
		out, err := list(reply)
		if err != nil || len(out) != len(pending) {
			return fmt.Errorf("%s: %d replies for %d ids (%v)", FnPlace, len(out), len(pending), err)
		}
		for _, w := range out {
			if why, ok := strings.CutPrefix(w, "skip "); ok {
				res.Skipped[why]++
			} else {
				res.Placed[w]++
			}
		}
		pending = pending[:0]
		return nil
	}
	var cursor uint64
	for {
		keys, next, err := c.Scan(ctx, cursor, "task:*", 1000).Result()
		if err != nil {
			return res, fmt.Errorf("scan task:*: %w", err)
		}
		res.Scanned += len(keys)
		pipe := c.Pipeline()
		types := make([]*redis.StatusCmd, len(keys))
		for i, k := range keys {
			types[i] = pipe.Type(ctx, k)
		}
		if len(keys) > 0 {
			if _, err := pipe.Exec(ctx); err != nil {
				return res, fmt.Errorf("type task:*: %w", err)
			}
		}
		for i, k := range keys {
			id := strings.TrimPrefix(k, "task:")
			if types[i].Val() != "hash" || id == "" || strings.Contains(id, ":") {
				res.Skipped["not-a-task-hash"]++
				continue
			}
			pending = append(pending, id)
			if len(pending) >= batch {
				if err := flush(); err != nil {
					return res, err
				}
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return res, flush()
}
