package ws

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// CheckpointKey holds the last checkpoint's receipt: utc, path, rows.
const CheckpointKey = "ws:checkpoint"

// CheckpointFields are the task hash fields a checkpoint row carries after
// stream, state, score and id.
var CheckpointFields = []string{"order", "blocked_on", "owner", "kind", "route", "est", "ref",
	"title", "pr", "head", "created_at", "state_at", "parked_from"}

// CheckpointResult is what Checkpoint wrote.
type CheckpointResult struct {
	Path    string
	Rows    int
	Streams int
}

// Streams is every stream name: ws:order by rank, then ws:names without a
// rank, by name. One pipeline, one round trip.
func Streams(ctx context.Context, c redis.Cmdable) ([]string, error) {
	pipe := c.Pipeline()
	order := pipe.ZRange(ctx, "ws:order", 0, -1)
	names := pipe.SMembers(ctx, "ws:names")
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("ws streams: %w", err)
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range order.Val() {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	var rest []string
	for _, s := range names.Val() {
		if !seen[s] {
			rest = append(rest, s)
		}
	}
	sort.Strings(rest)
	return append(out, rest...), nil
}

func cell(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return ' '
		}
		return r
	}, s)
}

// Checkpoint writes every stream's six sets, one TSV row per task with its
// hash fields, to path (atomically: a temporary file in the same directory,
// then rename), and records the receipt in ws:checkpoint. Four round trips:
// the stream names, the sets, the task hashes, the receipt.
func Checkpoint(ctx context.Context, c redis.Cmdable, path string, now time.Time) (CheckpointResult, error) {
	if path == "" {
		return CheckpointResult{}, fmt.Errorf("ws checkpoint: a path is required")
	}
	streams, err := Streams(ctx, c)
	if err != nil {
		return CheckpointResult{}, err
	}
	type set struct {
		stream, state string
		cmd           *redis.ZSliceCmd
	}
	var sets []set
	pipe := c.Pipeline()
	for _, s := range streams {
		for _, st := range States {
			sets = append(sets, set{s, st, pipe.ZRangeWithScores(ctx, Key(s, st), 0, -1)})
		}
	}
	if len(sets) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
			return CheckpointResult{}, fmt.Errorf("ws checkpoint sets: %w", err)
		}
	}
	type row struct {
		stream, state, id string
		score             float64
		hm                *redis.SliceCmd
	}
	var rows []row
	pipe = c.Pipeline()
	for _, s := range sets {
		for _, z := range s.cmd.Val() {
			id := fmt.Sprint(z.Member)
			rows = append(rows, row{s.stream, s.state, id, z.Score, pipe.HMGet(ctx, "task:"+id, CheckpointFields...)})
		}
	}
	if len(rows) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
			return CheckpointResult{}, fmt.Errorf("ws checkpoint tasks: %w", err)
		}
	}
	var b strings.Builder
	utc := now.UTC().Format(time.RFC3339)
	fmt.Fprintf(&b, "# ws checkpoint utc=%s streams=%d rows=%d\n", utc, len(streams), len(rows))
	b.WriteString("stream\tstate\tscore\tid\t" + strings.Join(CheckpointFields, "\t") + "\n")
	for _, r := range rows {
		b.WriteString(cell(r.stream) + "\t" + r.state + "\t" + fmt.Sprintf("%.0f", r.score) + "\t" + cell(r.id))
		for _, v := range r.hm.Val() {
			s := ""
			if v != nil {
				s = fmt.Sprint(v)
			}
			b.WriteString("\t" + cell(s))
		}
		b.WriteString("\n")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return CheckpointResult{}, fmt.Errorf("ws checkpoint: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ws-checkpoint-*")
	if err != nil {
		return CheckpointResult{}, fmt.Errorf("ws checkpoint: %w", err)
	}
	if _, err := tmp.WriteString(b.String()); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return CheckpointResult{}, fmt.Errorf("ws checkpoint: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return CheckpointResult{}, fmt.Errorf("ws checkpoint: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return CheckpointResult{}, fmt.Errorf("ws checkpoint: %w", err)
	}
	receipt := fmt.Sprintf("utc=%s path=%s rows=%d", utc, path, len(rows))
	if err := c.Set(ctx, CheckpointKey, receipt, 0).Err(); err != nil {
		return CheckpointResult{}, fmt.Errorf("ws checkpoint receipt: %w", err)
	}
	return CheckpointResult{Path: path, Rows: len(rows), Streams: len(streams)}, nil
}

// DefaultCheckpointDir is where scope park/keep write the checkpoint they
// take first when no --checkpoint path is given: $NOVA_SPRINT_CHECKPOINT_DIR,
// else nova-sprint/ws under the user's cache directory.
func DefaultCheckpointDir() (string, error) {
	if d := os.Getenv("NOVA_SPRINT_CHECKPOINT_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("no checkpoint directory: set NOVA_SPRINT_CHECKPOINT_DIR or pass --checkpoint: %w", err)
	}
	return filepath.Join(base, "nova-sprint", "ws"), nil
}

// KeepCheckpoints is how many default-directory checkpoints PruneCheckpoints
// leaves; each is one TSV of the index (about 200 KB at 1,000 tasks).
const KeepCheckpoints = 32

// DefaultCheckpointPath names a new checkpoint in dir for now.
func DefaultCheckpointPath(dir string, now time.Time) string {
	return filepath.Join(dir, "ws-"+now.UTC().Format("20060102T150405.000Z")+".tsv")
}

// PruneCheckpoints removes all but the newest keep ws-*.tsv files in dir (the
// names sort by time) and returns how many it removed.
func PruneCheckpoints(dir string, keep int) (int, error) {
	names, err := filepath.Glob(filepath.Join(dir, "ws-*.tsv"))
	if err != nil {
		return 0, err
	}
	sort.Strings(names)
	n := 0
	for len(names) > keep {
		if err := os.Remove(names[0]); err != nil {
			return n, err
		}
		names = names[1:]
		n++
	}
	return n, nil
}
