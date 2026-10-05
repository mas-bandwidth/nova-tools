package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func init() {
	// backup is the schedule of the store's host, as snapshot is, and reads the
	// RDB from that host's disk
	verbClasses["backup"] = classMachine
	notServed = append(notServed, "backup")
}

// backupShapes are the key shapes a backup is scanned for: the rows of
// internal/hygiene/keyshapes.txt (the shapes the card gate scans diffs by),
// by name and expression. A finding prints the name and a count, never the text.
var backupShapes = []struct {
	name string
	re   *regexp.Regexp
}{
	{"pem-private-key", regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`)},
	{"age-secret-key", regexp.MustCompile(`AGE-SECRET-KEY-1[0-9A-Za-z]{10,}`)},
	{"forge-token", regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`)},
	{"forge-fine-grained-token", regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`)},
	{"anthropic-api-key", regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{20,}`)},
	{"openai-api-key", regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`)},
	{"xai-api-key", regexp.MustCompile(`xai-[A-Za-z0-9-]{20,}`)},
	{"google-api-key", regexp.MustCompile(`AIza[0-9A-Za-z_-]{30,}`)},
	{"slack-token", regexp.MustCompile(`xox[abprs]-[0-9A-Za-z-]{10,}`)},
	{"aws-access-key-id", regexp.MustCompile(`(A3T[A-Z0-9]|AKIA|ASIA|ABIA|ACCA)[A-Z0-9]{16}`)},
	{"json-web-token", regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
}

// scanKeys is the names of the key shapes b holds, each with its count.
func scanKeys(b []byte) []string {
	var out []string
	for _, s := range backupShapes {
		if n := len(s.re.FindAll(b, -1)); n > 0 {
			out = append(out, fmt.Sprintf("%s x%d", s.name, n))
		}
	}
	sort.Strings(out)
	return out
}

// backupSource scans what its source saved before the snapshotter writes it:
// bytes that hold a key shape are an error, so the file is never written and
// the directory keeps only backups that are clean (the keep law of
// store.Snapshotter: a failed take leaves the older ones).
type backupSource struct {
	src     store.SnapshotSource
	scanned int
}

func (b *backupSource) Save(ctx context.Context) ([]byte, store.SnapshotCounts, error) {
	doc, counts, err := b.src.Save(ctx)
	if err != nil {
		return nil, counts, err
	}
	b.scanned = len(doc)
	if found := scanKeys(doc); len(found) > 0 {
		return nil, counts, fmt.Errorf("the store holds key shapes (%s) and no backup was written; find them with nova-sprint where --json and remove them from the cards, then run: nova-sprint backup --dir <dir>", strings.Join(found, ", "))
	}
	return doc, counts, nil
}

// runBackup is one backup: the store's bytes scanned, written with their
// SHA-256, read back, restored into a twin and compared with the counts the
// store held (store.Snapshotter.Take), the directory pruned to keep.
func runBackup(ctx context.Context, dir string, keep int, src *backupSource, twin store.SnapshotTwin, now func() time.Time) (store.SnapshotTaken, error) {
	sn := &store.Snapshotter{Dir: dir, Keep: keep, Source: src, Twin: twin, Now: now}
	return sn.Take(ctx)
}

// cmdBackup is the sprint backup as one verb: it writes the store to a file in
// --dir, restores the file into a twin and compares its counts with the
// store's, and scans what it wrote for key shapes (SPEC-SPRINT,
// sprint-backup-verb). It replaces the hand procedure of a restore test and a
// secrets scan.
func (a *app) cmdBackup(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("backup")
	dir := fs.String("dir", "", "the directory the backups are written to (required)")
	keep := fs.Int("keep", snapshotKeepDefault, "how many verified, scanned backups stay; older ones are pruned after a newer one verifies")
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
		return refuse(stderr, "backup", "this store has no snapshot; run: nova-sprint backup --redis <a Redis address> --dir "+*dir)
	}
	bs := &backupSource{src: src}
	got, err := runBackup(context.Background(), *dir, *keep, bs, twin, now)
	if err != nil {
		fmt.Fprintf(stderr, "%s backup FAILED: %s\n", prog, err)
		return 1
	}
	if c.json {
		b, _ := json.Marshal(map[string]any{"file": got.File, "sha256": got.SHA256, "bytes": got.Bytes, "counts": got.Counts, "scanned": bs.scanned, "secrets": 0, "pruned": got.Pruned})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprintf(stdout, "BACKUP OK file=%s sha256=%s bytes=%d %s restored=twin compared=counts scanned=%d secrets=0 pruned=%d keep=%d\n", got.File, got.SHA256, got.Bytes, countsText(got.Counts), bs.scanned, len(got.Pruned), *keep)
	return 0
}
