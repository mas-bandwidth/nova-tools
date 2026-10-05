package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func init() {
	// backup is snapshot's schedule on the store's host: it needs no actor, reads
	// and writes no card, and is served to nobody (the RDB is read from the disk
	// the store's own host writes it to).
	verbClasses["backup"] = classMachine
	notServed = append(notServed, "backup")
}

// secretShapes are the shapes of a credential a backup file must not hold, by
// the kind a refusal names. A value is never printed, only its kind.
var secretShapes = []struct {
	kind string
	re   *regexp.Regexp
}{
	{"private key block", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"age secret key", regexp.MustCompile(`AGE-SECRET-KEY-1[0-9A-Z]{20,}`)},
	{"github token", regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{30,})`)},
	{"api key", regexp.MustCompile(`\bsk-(?:ant-)?[A-Za-z0-9_-]{30,}`)},
	{"cloud access key", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"slack token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`)},
	{"password in an address", regexp.MustCompile(`\b[a-z][a-z0-9+.-]*://[^\s/:@]*:[^\s/@]+@`)},
}

// secretKinds is the kinds of secret the bytes hold, each once, in name order.
func secretKinds(b []byte) []string {
	var kinds []string
	for _, s := range secretShapes {
		if s.re.Match(b) {
			kinds = append(kinds, s.kind)
		}
	}
	slices.Sort(kinds)
	return kinds
}

// scanTwin is the twin a backup proves its file on: it scans the bytes read
// back from the written file for secrets, and only a clean file is loaded into
// the inner twin and counted. Take removes a file the twin refuses.
type scanTwin struct{ inner store.SnapshotTwin }

func (t scanTwin) Load(file []byte) (store.SnapshotCounts, error) {
	if kinds := secretKinds(file); len(kinds) > 0 {
		return store.SnapshotCounts{Keys: -1, Cards: -1}, fmt.Errorf("the file holds %s; no value is shown", strings.Join(kinds, ", "))
	}
	return t.inner.Load(file)
}

// cmdBackup is the sprint's backup in one step (docs/SPEC-SPRINT.md,
// "backup"): the store is written to a file in --dir, the file is read back
// and checksummed, restored into a twin and its counts compared with the
// store's, and scanned for secrets. A file that fails any check is removed and
// the older ones stay.
func (a *app) cmdBackup(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("backup")
	dir := fs.String("dir", "", "the directory the backups are written to")
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
	sn := &store.Snapshotter{Dir: *dir, Keep: *keep, Source: src, Twin: scanTwin{twin}, Now: now}
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
	fmt.Fprintf(stdout, "BACKUP OK file=%s sha256=%s bytes=%d %s verified=checksum+twin+secrets pruned=%d keep=%d\n", got.File, got.SHA256, got.Bytes, countsText(got.Counts), len(got.Pruned), *keep)
	return 0
}
