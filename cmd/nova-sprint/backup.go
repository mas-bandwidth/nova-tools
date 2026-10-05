package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/log"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func init() {
	// backup is the schedule of the store's host, like snapshot: it needs no
	// actor, and it reads and writes no card (coordinator.go).
	verbClasses["backup"] = classMachine
	// the RDB is read from the disk the store's own host writes it to
	notServed = append(notServed, "backup")
}

// cmdBackup is the sprint backup as one verb (SPEC-SPRINT, sprint-backup-verb):
// the store is written to a file, the file is restored into a twin and its
// counts compared (store.Snapshotter), and the file is scanned for secrets. A
// file that fails any of the three is removed before the directory is pruned,
// so the directory holds only backups that restore and hold no secret.
func (a *app) cmdBackup(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("backup")
	dir := fs.String("dir", "", "the directory the backup is written to (required)")
	keep := fs.Int("keep", snapshotKeepDefault, "how many verified backups stay; older ones are pruned after a newer one verifies")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "backup", argErr("takes no words ", err, pos...))
	}
	if *dir == "" || *keep < 1 {
		return refuse(stderr, "backup", "wants --dir <dir> and --keep of at least 1; run: nova-sprint backup --dir backups --keep 7")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "backup", err.Error())
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
		return refuse(stderr, "backup", "this store has no backup; run: nova-sprint backup --redis <a Redis address> --dir "+*dir)
	}
	scan := &scanTwin{inner: twin}
	sn := &store.Snapshotter{Dir: *dir, Keep: *keep, Source: src, Twin: scan, Now: now}
	got, err := sn.Take(context.Background())
	if scan.hits > 0 {
		fmt.Fprintf(stderr, "%s backup FAILED: the copy holds %d secret-shaped value(s); the backup was removed and the older ones stay; find them with: nova-sprint card <id>, and remove the secret from the card\n", prog, scan.hits)
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s backup FAILED: %s\n", prog, err)
		return 1
	}
	if c.json {
		b, _ := json.Marshal(map[string]any{"file": got.File, "sha256": got.SHA256, "bytes": got.Bytes, "counts": got.Counts, "secrets": 0, "pruned": got.Pruned})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprintf(stdout, "BACKUP OK file=%s sha256=%s bytes=%d %s restored=twin compared=counts secrets=0 pruned=%d keep=%d\n", got.File, got.SHA256, got.Bytes, countsText(got.Counts), len(got.Pruned), *keep)
	return 0
}

// scanTwin is a twin that also scans what it loads: a file that loads is read
// for secret-shaped values (log.Redact's rules, the ones the fleet's logs are
// held to), and the count is kept for the verb. It runs inside the snapshot's
// checks, so a failing file is removed before anything is pruned. The scan is
// over the bytes as written: a value an RDB compresses is not seen, so the
// scan is the twin store's (JSON) check in full and a best effort on an RDB.
type scanTwin struct {
	inner store.SnapshotTwin
	hits  int
}

func (s *scanTwin) Load(rdb []byte) (store.SnapshotCounts, error) {
	c, err := s.inner.Load(rdb)
	if err != nil {
		return c, err
	}
	text := string(rdb)
	red := log.Redact(text)
	if red != text {
		s.hits = max(1, strings.Count(red, log.Redacted)-strings.Count(text, log.Redacted))
		return c, fmt.Errorf("the copy holds %d secret-shaped value(s)", s.hits)
	}
	return c, nil
}
