package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func init() {
	// snapshot is the schedule of the store's host, like promote and friend
	// clean: it needs no actor, and it reads and writes no card. The class
	// test holds every verb to one class (coordinator.go).
	verbClasses["snapshot"] = classMachine
	// the RDB is read from the disk the store's own host writes it to
	notServed = append(notServed, "snapshot")
}

const (
	snapshotKeepDefault = 7
	snapshotSaveWait    = 10 * time.Minute
)

// cmdSnapshot is the store-level snapshot (store.Snapshotter): the store is
// asked for an RDB, the copy is checksummed, loaded into a twin and compared,
// and the directory pruned to --keep; with --every it is a loop. With
// --restore-drill it loads a file into a twin and prints its counts, and
// never opens the live store (SPEC-SPRINT, store-snapshot-verb).
func (a *app) cmdSnapshot(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("snapshot")
	dir := fs.String("dir", "", "the directory the snapshots are written to (required unless --restore-drill)")
	keep := fs.Int("keep", snapshotKeepDefault, "how many verified snapshots stay; older ones are pruned after a newer one verifies")
	every := fs.Duration("every", 0, "take a snapshot now and again each time this passes, until interrupted (default: once)")
	drill := fs.String("restore-drill", "", "load this snapshot file into a twin and print its counts; the live store is never opened")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "snapshot", argErr("takes no words ", err, pos...))
	}
	if *drill != "" {
		if *dir != "" || *every != 0 {
			return refuse(stderr, "snapshot", "--restore-drill takes no --dir or --every; run: nova-sprint snapshot --restore-drill <file>")
		}
		counts, sum, err := store.RestoreDrill(*drill, store.RDBTwin{})
		if err != nil {
			return refuse(stderr, "snapshot", err.Error())
		}
		if c.json {
			b, _ := json.Marshal(map[string]any{"file": *drill, "sha256": sum, "counts": counts})
			fmt.Fprintln(stdout, string(b))
			return 0
		}
		fmt.Fprintf(stdout, "SNAPSHOT DRILL OK file=%s sha256=%s %s; the live store was not opened\n", *drill, sum, countsText(counts))
		return 0
	}
	if *dir == "" || *keep < 1 || *every < 0 {
		return refuse(stderr, "snapshot", "wants --dir <dir>, --keep of at least 1 and --every above zero when given; run: nova-sprint snapshot --dir snapshots --keep 7")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "snapshot", err.Error())
	}
	now := time.Now
	if a.now != nil {
		now = a.now
	}
	var src store.SnapshotSource
	var twin store.SnapshotTwin = store.RDBTwin{}
	switch b := st.B.(type) {
	case *store.Redis:
		src = &redisSource{b: b}
	case *store.Mem:
		src, twin = store.MemSource{M: b}, store.MemTwin{}
	default:
		return refuse(stderr, "snapshot", "this store has no snapshot; run: nova-sprint snapshot --redis <a Redis address> --dir "+*dir)
	}
	sn := &store.Snapshotter{Dir: *dir, Keep: *keep, Source: src, Twin: twin, Now: now}
	ctx := context.Background()
	if *every > 0 && a.notify != nil {
		var cancel context.CancelFunc
		ctx, cancel = a.notify(ctx)
		defer cancel()
	}
	code := 0
	take := func(ctx context.Context) error {
		got, err := sn.Take(ctx)
		if err != nil {
			code = 1
			return err
		}
		code = 0
		if c.json {
			b, _ := json.Marshal(got)
			fmt.Fprintln(stdout, string(b))
			return nil
		}
		fmt.Fprintf(stdout, "SNAPSHOT OK file=%s sha256=%s bytes=%d %s verified=checksum+twin pruned=%d keep=%d\n", got.File, got.SHA256, got.Bytes, countsText(got.Counts), len(got.Pruned), *keep)
		return nil
	}
	if *every == 0 {
		if err := take(ctx); err != nil {
			fmt.Fprintf(stderr, "%s snapshot FAILED: %s\n", prog, err)
		}
		return code
	}
	store.SnapshotEvery(ctx, take, func(ctx context.Context) error {
		t := time.NewTimer(*every)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			return nil
		}
	}, func(err error) {
		fmt.Fprintf(stderr, "%s snapshot FAILED: %s; the older snapshots stay, the next try is in %s\n", prog, err, *every)
	})
	return 0
}

func countsText(c store.SnapshotCounts) string {
	n := func(v int) string {
		if v < 0 {
			return "unknown"
		}
		return fmt.Sprint(v)
	}
	return "keys=" + n(c.Keys) + " cards=" + n(c.Cards)
}

// redisSource asks a Redis for its snapshot: BGSAVE, wait for LASTSAVE to
// move with the save reported ok, then copy the RDB file the server wrote
// (CONFIG GET dir, dbfilename), which is readable only on the store's host.
type redisSource struct{ b *store.Redis }

func (r *redisSource) Save(ctx context.Context) ([]byte, store.SnapshotCounts, error) {
	none := store.SnapshotCounts{Keys: -1, Cards: -1}
	c := r.b.C
	before, err := c.LastSave(ctx).Result()
	if err != nil {
		return nil, none, err
	}
	if err := c.BgSave(ctx).Err(); err != nil && !strings.Contains(err.Error(), "already in progress") {
		return nil, none, err
	}
	deadline := time.Now().Add(snapshotSaveWait)
	for {
		info, err := c.Info(ctx, "persistence").Result()
		if err != nil {
			return nil, none, err
		}
		if !strings.Contains(info, "rdb_bgsave_in_progress:1") {
			if strings.Contains(info, "rdb_last_bgsave_status:err") {
				return nil, none, errors.New("the store's BGSAVE failed (rdb_last_bgsave_status:err)")
			}
			if now, err := c.LastSave(ctx).Result(); err == nil && now > before {
				break
			}
		}
		if time.Now().After(deadline) {
			return nil, none, fmt.Errorf("the store's BGSAVE did not finish in %s", snapshotSaveWait)
		}
		select {
		case <-ctx.Done():
			return nil, none, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	cfg, err := c.ConfigGet(ctx, "dir").Result()
	if err != nil {
		return nil, none, err
	}
	name, err := c.ConfigGet(ctx, "dbfilename").Result()
	if err != nil {
		return nil, none, err
	}
	d, f := cfg["dir"], name["dbfilename"]
	b, err := os.ReadFile(filepath.Join(d, f))
	if err != nil {
		return nil, none, fmt.Errorf("the saved RDB %s cannot be read here: %v; run the snapshot on the store's host", filepath.Join(d, f), err)
	}
	return b, none, nil
}
