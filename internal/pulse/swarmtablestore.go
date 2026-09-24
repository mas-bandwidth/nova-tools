package pulse

// The store side of `nova-pulse status --store`: the SwarmStoreReader over the fleet Redis,
// and the verb's loop.
//
// SCAN, NOT KEYS. bin/sprint-table-redis used `redis-cli --scan`, which is SCAN under the
// hood, and this keeps that: KEYS blocks the whole instance, and the instance is the
// fleet's coordination state with seven benches writing a row a second into it.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

// redisSwarmReader is the production SwarmStoreReader.
type redisSwarmReader struct{ rdb *redis.Client }

func (r redisSwarmReader) Keys(ctx context.Context, pattern string) ([]string, error) {
	var out []string
	iter := r.rdb.Scan(ctx, 0, pattern, 200).Iterator()
	for iter.Next(ctx) {
		out = append(out, iter.Val())
	}
	return out, iter.Err()
}

func (r redisSwarmReader) Hash(ctx context.Context, key string) (map[string]string, error) {
	h, err := r.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		// A key of the wrong type is not a hash this table can show, and it is not a
		// reason to print no table at all.
		if isWrongType(err) {
			return nil, nil
		}
		return nil, err
	}
	return h, nil
}

func (r redisSwarmReader) Get(ctx context.Context, key string) (string, bool, error) {
	v, err := r.rdb.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", false, nil
		}
		if isWrongType(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return v, true, nil
}

func isWrongType(err error) bool {
	return err != nil && len(err.Error()) >= 9 && err.Error()[:9] == "WRONGTYPE"
}

// SwarmStatusInput is one `nova-pulse status --store` invocation.
type SwarmStatusInput struct {
	Store    StoreOptions
	Friends  []string      // the roster a `friends:` line must name even when a key is absent
	Interval time.Duration // zero prints once and returns
	Out      string        // optional file the table is written to, atomically, each tick
	Stdout   io.Writer
	Stderr   io.Writer
	Now      func() time.Time
	Sleep    func(time.Duration)

	// Reader is the seam: a test hands in a fake store and opens no socket.
	Reader SwarmStoreReader
}

// SwarmStatus prints the swarm table, once or on an interval.
func SwarmStatus(in SwarmStatusInput) int {
	stdout, stderr := in.Stdout, in.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	now := in.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	sleep := in.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}

	reader := in.Reader
	if reader == nil {
		rdb, err := DialStore(context.Background(), in.Store)
		if err != nil {
			fmt.Fprintf(stderr, "SWARM TABLE REFUSED store: %s\n", oneline.Err(err))
			return 2
		}
		defer func() { _ = rdb.Close() }()
		reader = redisSwarmReader{rdb: rdb}
	}

	for {
		st, err := ReadSwarmState(context.Background(), reader, in.Friends, now())
		if err != nil {
			fmt.Fprintf(stderr, "SWARM TABLE REFUSED read: %s\n", oneline.Err(err))
			if in.Interval <= 0 {
				return 3
			}
			sleep(in.Interval)
			continue
		}
		table := RenderSwarmTable(st, now())
		if in.Out != "" {
			if err := writeTableFile(in.Out, table); err != nil {
				fmt.Fprintf(stderr, "SWARM TABLE OUT %s: %s\n", oneline.Field(in.Out), oneline.Err(err))
			}
		} else {
			fmt.Fprint(stdout, table)
		}
		if in.Interval <= 0 {
			return 0
		}
		sleep(in.Interval)
	}
}

// writeTableFile writes the table to a path atomically: everybody watching the file with
// `watch cat` must see a whole table or the previous one, never half of one. It is the same
// tmp-then-rename bin/sprint-table-redis did with its .new file.
func writeTableFile(path, body string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".swarm-table-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}
