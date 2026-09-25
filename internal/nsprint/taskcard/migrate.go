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
// placed per where ("" is null), the keys skipped per reason, and the
// sprint store's records folded into the one store (Folded, per where; Held
// names the s:<S>:task:<id> keys left because task:<id> holds another task).
type MigrateResult struct {
	Scanned int
	Placed  map[string]int
	Skipped map[string]int
	Folded  map[string]int
	Held    []string
}

// FnFold is the one-time fold of s:<S>:task:<id> into task:<id>.
const FnFold = "ns_tcard_fold"

// fold moves every s:<S>:task:<id> record into the one store (the second
// SCAN of the one-time migrate, s:*:task:*), batch pairs per call.
func fold(ctx context.Context, c redis.UniversalClient, by string, batch int, res *MigrateResult) error {
	var cursor uint64
	var pending [][2]string
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		args := []any{by}
		for _, p := range pending {
			args = append(args, p[0], p[1])
		}
		reply, err := c.FCall(ctx, FnFold, nil, args...).Result()
		if err != nil {
			return fmt.Errorf("%s: %w", FnFold, err)
		}
		out, err := list(reply)
		if err != nil || len(out) != len(pending) {
			return fmt.Errorf("%s: %d replies for %d keys (%v)", FnFold, len(out), len(pending), err)
		}
		for i, w := range out {
			switch {
			case w == "held":
				res.Held = append(res.Held, "s:"+pending[i][0]+":task:"+pending[i][1])
			case strings.HasPrefix(w, "skip "):
				res.Skipped[strings.TrimPrefix(w, "skip ")]++
			default:
				res.Folded[w]++
			}
		}
		pending = pending[:0]
		return nil
	}
	for {
		keys, next, err := c.Scan(ctx, cursor, "s:*:task:*", 1000).Result()
		if err != nil {
			return fmt.Errorf("scan s:*:task:*: %w", err)
		}
		res.Scanned += len(keys)
		for _, k := range keys {
			rest := strings.TrimPrefix(k, "s:")
			S, id, ok := strings.Cut(rest, ":task:")
			if !ok || S == "" || id == "" || strings.Contains(S, ":") || strings.Contains(id, ":") {
				res.Skipped["not-a-sprint-task"]++
				continue
			}
			pending = append(pending, [2]string{S, id})
			if len(pending) >= batch {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return flush()
}

// Migrate is the one-time walk (the only SCANs, run once): the sprint
// store's s:<S>:task:<id> records fold into task:<id>, then every task:*
// hash is placed in exactly one set by ns_tcard_place, in batches of
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
	res := MigrateResult{Placed: map[string]int{}, Skipped: map[string]int{}, Folded: map[string]int{}}
	// First the sprint store's records move into the one store (one store,
	// ruling 2026-09-25 09:35 ET), then every task:* record is placed.
	if err := fold(ctx, c, by, batch, &res); err != nil {
		return res, err
	}
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
