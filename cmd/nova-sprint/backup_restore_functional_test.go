//go:build functional

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/testutil"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// incompleteRedisTwin loses one record field after RESTORE, preserving every key and
// placement, so only the logical state comparison can reject it (sprint-backup-out).
type incompleteRedisTwin struct{ *serverTwin }

func (t *incompleteRedisTwin) Restore(ctx context.Context, keys []store.DumpKey) (store.BackupCounts, error) {
	counts, err := t.serverTwin.Restore(ctx, keys)
	if err != nil {
		return counts, err
	}
	records, err := t.c.Keys(ctx, "*s1-1*").Result()
	if err != nil {
		return counts, err
	}
	for _, key := range records {
		kind, err := t.c.Type(ctx, key).Result()
		if err != nil {
			return counts, err
		}
		if kind != "hash" {
			continue
		}
		present, err := t.c.HExists(ctx, key, "brief").Result()
		if err != nil {
			return counts, err
		}
		if present {
			return counts, t.c.HDel(ctx, key, "brief").Err()
		}
	}
	return counts, errors.New("the restore holds no brief field to remove")
}

// Backup's real Redis path compares state, not just key/member counts, through the
// --out pipeline: dump, compress, split, load this library, restore, compare and scan.
// A restored record missing its brief keeps the counts and fails without publishing --out.
func TestRedisBackupOutIsSemantic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: testutil.Start(t)})
	t.Cleanup(func() { _ = c.Close() }) // ignored: the owned test server is cleaned up by its fixture
	require.NoError(t, fn.Load(ctx, c))
	names := sprint.Names{Prefix: "r-"}
	b := &store.Redis{C: c, Names: names, Now: time.Now}
	st := &store.Store{B: b, Names: names, Actor: "tester"}
	require.NoError(t, st.Init(ctx))
	res, err := st.Run(ctx, store.AddStep(sprint.AddReq{Stream: "s1", Count: 1, Brief: "c: restore one card\nREPO: mas-bandwidth/nova-tools\n\nThe task."}))
	require.NoError(t, err)
	require.Empty(t, res.Refused)
	src := redisBackup{b: b, names: names}
	source, ok := any(src).(stateSource)
	require.True(t, ok, "the Redis backup source must expose logical state")
	want, err := source.State(ctx)
	require.NoError(t, err)
	_, keys, cols, err := src.Take(ctx)
	require.NoError(t, err)
	twin, err := startServerTwin(ctx, "redis-server", t.TempDir(), names)
	require.NoError(t, err)
	t.Cleanup(twin.Close)
	got, err := twin.Restore(ctx, keys)
	require.NoError(t, err)
	require.Empty(t, (store.BackupCounts{Keys: len(keys), Columns: cols}).Differ(got))
	semantic, ok := any(twin).(stateTwin)
	require.True(t, ok, "the isolated Redis must expose restored logical state")
	restored, err := semantic.State(ctx)
	require.NoError(t, err)
	require.Empty(t, sprintStateDiff(want, restored))
	self, err := os.Executable()
	require.NoError(t, err)
	root := t.TempDir()

	for _, damaged := range []bool{false, true} {
		name := "complete"
		if damaged {
			name = "missing field with unchanged counts"
		}
		t.Run(name, func(t *testing.T) {
			// Each row owns its output and Redis, and shares only the read-only source.
			t.Parallel()
			out := filepath.Join(root, name)
			backup := backupOut{out: out, partBytes: 600, src: src, xz: "xz", split: "split",
				twin: func(ctx context.Context, work string) (backupTwin, error) {
					twin, err := startServerTwin(ctx, "redis-server", work, names)
					if err != nil {
						return nil, err
					}
					if damaged {
						return &incompleteRedisTwin{twin}, nil
					}
					return twin, nil
				},
				scan: backupScanner{bin: fakeSecrets(t, "nsv_FAKE_FOR_RESTORE_TEST"), store: t.TempDir(), as: "tester", key: "unused", sops: "/bin/true", self: self}}
			result, err := backup.run(ctx)
			if damaged {
				require.ErrorContains(t, err, "record work s1-1 field brief")
				require.Empty(t, result.line)
				_, err = os.Stat(out)
				require.True(t, os.IsNotExist(err), "a semantic failure publishes no --out")
			} else {
				require.NoError(t, err)
				require.Contains(t, result.line, "restore=semantic compared=state+counts")
				require.NotContains(t, result.line, "integrity only")
				readme, err := os.ReadFile(filepath.Join(out, "README.md"))
				require.NoError(t, err)
				require.Contains(t, string(readme), "same counts and the same sprint state")
			}
		})
	}
}
