package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/log"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func init() {
	// backup is the schedule of the store's host, like snapshot: it needs no
	// actor, reads and writes no card, and the RDB it copies is on that disk.
	verbClasses["backup"] = classMachine
	notServed = append(notServed, "backup")
}

// cmdBackup is the sprint backup (SPEC-SPRINT, sprint-backup-verb): the store
// is written to a file in --dir with its SHA-256, read back, restored into a
// twin and compared with the store, and the file scanned for secret-shaped
// values; a file that fails any step is removed and the older ones stay, and
// the directory is pruned to --keep only after a copy passes everything.
func (a *app) cmdBackup(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("backup")
	dir := fs.String("dir", "", "the directory the backups are written to (required)")
	keep := fs.Int("keep", snapshotKeepDefault, "how many verified backups stay; older ones are pruned after a newer one passes")
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
	sn := &store.Snapshotter{Dir: *dir, Keep: *keep, Source: src, Twin: scanTwin{twin}, Now: a.now}
	got, err := sn.Take(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "%s backup FAILED: %s\n", prog, err)
		return 1
	}
	if c.json {
		b, _ := json.Marshal(got)
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprintf(stdout, "BACKUP OK file=%s sha256=%s bytes=%d %s restored=twin compared=counts scanned=secrets found=0 pruned=%d keep=%d\n", got.File, got.SHA256, got.Bytes, countsText(got.Counts), len(got.Pruned), *keep)
	return 0
}

// scanTwin is a twin that also scans the file it is loaded from: the copy
// restores into the inner twin first, then its bytes are held to the log
// redaction rules (log.Redact); a secret-shaped value fails the load, which
// removes the file. Only the count is reported, never a value.
type scanTwin struct{ inner store.SnapshotTwin }

func (s scanTwin) Load(b []byte) (store.SnapshotCounts, error) {
	counts, err := s.inner.Load(b)
	if err != nil {
		return counts, err
	}
	text := string(b)
	n := strings.Count(log.Redact(text), log.Redacted) - strings.Count(text, log.Redacted)
	if n > 0 {
		return counts, fmt.Errorf("%d secret-shaped value(s) found in the backup (not shown); the store holds a secret that must be removed before it is backed up", n)
	}
	return counts, nil
}
