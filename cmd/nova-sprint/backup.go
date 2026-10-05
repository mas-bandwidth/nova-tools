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

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func init() {
	// backup is the schedule of the store's host, as snapshot is: it needs no
	// actor and reads and writes no card.
	verbClasses["backup"] = classMachine
	// the RDB is read from the disk the store's own host writes it to
	notServed = append(notServed, "backup")
}

// backupShapes are the forms a secret takes, by name; a refusal prints the
// names and never the text matched. They follow the shapes of
// internal/hygiene/keyshapes.txt that can appear in a store's bytes.
var backupShapes = []struct {
	name string
	re   *regexp.Regexp
}{
	{"pem-private-key", regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`)},
	{"age-secret-key", regexp.MustCompile(`AGE-SECRET-KEY-1[0-9A-Za-z]{10,}`)},
	{"forge-token", regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`)},
	{"forge-fine-grained-token", regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`)},
	{"api-key", regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`)},
	{"cloud-key-id", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{"chat-token", regexp.MustCompile(`xox[abprs]-[A-Za-z0-9-]{10,}`)},
	{"password-in-address", regexp.MustCompile(`[a-z][a-z0-9+.-]*://[^\s/:@"]+:[^\s/@"]+@`)},
}

// backupSecretKinds is the names of the shapes b holds, sorted and each once.
func backupSecretKinds(b []byte) []string {
	var kinds []string
	for _, s := range backupShapes {
		if s.re.Match(b) {
			kinds = append(kinds, s.name)
		}
	}
	sort.Strings(kinds)
	return kinds
}

// scanTwin is the twin the copy is proved on, with the secrets scan in front
// of the load: a copy that holds a secret is refused, so store.Snapshotter
// removes it and keeps the older ones.
type scanTwin struct{ next store.SnapshotTwin }

func (t scanTwin) Load(b []byte) (store.SnapshotCounts, error) {
	if kinds := backupSecretKinds(b); len(kinds) > 0 {
		return store.SnapshotCounts{Keys: -1, Cards: -1}, fmt.Errorf("the copy holds %s; the values are not printed. Remove the secret from the store, then run: nova-sprint backup --dir <dir>", strings.Join(kinds, ", "))
	}
	return t.next.Load(b)
}

// cmdBackup is the store's backup: the store is written to a checksummed file,
// read back, restored into a twin and compared, and the file is scanned for
// secrets, all of it one verb where it was a hand procedure (SPEC-SPRINT,
// sprint-backup-verb).
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
	sn := &store.Snapshotter{Dir: *dir, Keep: *keep, Source: src, Twin: scanTwin{next: twin}, Now: now}
	got, err := sn.Take(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "%s backup FAILED: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	if c.json {
		b, _ := json.Marshal(got)
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprintf(stdout, "BACKUP OK file=%s sha256=%s bytes=%d %s verified=checksum+twin secrets=none pruned=%d keep=%d\n", got.File, got.SHA256, got.Bytes, countsText(got.Counts), len(got.Pruned), *keep)
	return 0
}
