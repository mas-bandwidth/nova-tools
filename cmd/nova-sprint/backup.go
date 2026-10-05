package main

import (
	"bytes"
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
	// backup is the schedule of the store's host, like snapshot: it needs no
	// actor and reads and writes no card. The class test holds every verb to
	// one class (coordinator.go).
	verbClasses["backup"] = classMachine
	// the file is written on the disk the verb is typed on
	notServed = append(notServed, "backup")
}

// secretKinds is what a backup is scanned for, by kind: the value of a match is
// never printed, only the kind and the count (SPEC-SPRINT, sprint-backup-verb).
var secretKinds = []struct {
	kind string
	re   *regexp.Regexp
}{
	{"private key block", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"age secret key", regexp.MustCompile(`AGE-SECRET-KEY-1[A-Z0-9]{20,}`)},
	{"GitHub token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{30,}`)},
	{"GitHub fine-grained token", regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{40,}`)},
	{"AWS access key id", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{"Slack token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`)},
	{"API key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{32,}`)},
	{"URL with a password", regexp.MustCompile(`[a-z][a-z0-9+.-]*://[^\s:/@"]*:[^\s@/"]+@`)},
}

// secretHits scans a backup's bytes and returns "kind xN" for each kind found,
// in kind order; none found is nil.
func secretHits(doc []byte) []string {
	var out []string
	for _, k := range secretKinds {
		if n := len(k.re.FindAll(doc, -1)); n > 0 {
			out = append(out, fmt.Sprintf("%s x%d", k.kind, n))
		}
	}
	sort.Strings(out)
	return out
}

// scanSource is a snapshot source whose bytes are scanned before they are
// written: a source holding a secret gives no snapshot, so nothing lands on
// disk and no older backup is pruned for it (store.Snapshotter.Take saves,
// then writes, then prunes).
type scanSource struct{ inner store.SnapshotSource }

func (s scanSource) Save(ctx context.Context) ([]byte, store.SnapshotCounts, error) {
	doc, counts, err := s.inner.Save(ctx)
	if err != nil {
		return nil, counts, err
	}
	if hits := secretHits(doc); len(hits) > 0 {
		return nil, counts, fmt.Errorf("the store holds secrets (%s); nothing was written, the older backups stay; remove them from the store, then run: nova-sprint backup --dir <dir>", strings.Join(hits, ", "))
	}
	return doc, counts, nil
}

// roundTripTwin restores a store document into a fresh store and holds the
// restore to the document: the twin must snapshot to the same bytes, which is
// the whole of the store and not its counts alone.
type roundTripTwin struct{}

func (roundTripTwin) Load(doc []byte) (store.SnapshotCounts, error) {
	none := store.SnapshotCounts{Keys: -1, Cards: -1}
	m := store.NewMem()
	if err := m.Restore(doc); err != nil {
		return none, err
	}
	again, err := m.Snapshot()
	if err != nil {
		return none, err
	}
	if !bytes.Equal(doc, again) {
		return none, fmt.Errorf("the restored twin differs from the backup (%d bytes, twin %d)", len(doc), len(again))
	}
	return store.MemTwin{}.Load(doc)
}

// cmdBackup is the sprint backup as one verb: the store is written to a file in
// --dir with its checksum, the file is restored into a twin and compared, and
// the bytes are scanned for secrets before anything is written; older backups
// are pruned to --keep only after the new one verifies (SPEC-SPRINT,
// sprint-backup-verb; the model is store.Snapshotter's keep law).
func (a *app) cmdBackup(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("backup")
	dir := fs.String("dir", "", "the directory the backup file is written to (required)")
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
	restored := "checksum+rdb-header"
	switch b := st.B.(type) {
	case *store.Redis:
		src = &redisSource{b: b}
	case *store.Mem:
		src, twin, restored = store.MemSource{M: b}, roundTripTwin{}, "equal"
	default:
		return refuse(stderr, "backup", "this store has no backup; run: nova-sprint backup --redis <a Redis address> --dir "+*dir)
	}
	sn := &store.Snapshotter{Dir: *dir, Keep: *keep, Source: scanSource{src}, Twin: twin, Now: now}
	got, err := sn.Take(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "%s backup FAILED: %s\n", prog, oneLine(err.Error()))
		return 1
	}
	if c.json {
		b, _ := json.Marshal(map[string]any{"file": got.File, "sha256": got.SHA256, "bytes": got.Bytes, "counts": got.Counts, "restored": restored, "secrets": "none", "pruned": got.Pruned})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprintf(stdout, "BACKUP OK file=%s sha256=%s bytes=%d %s restored=%s secrets=none pruned=%d keep=%d\n", got.File, got.SHA256, got.Bytes, countsText(got.Counts), restored, len(got.Pruned), *keep)
	return 0
}
