package main

import (
	"context"
	"encoding/json"
	"errors"
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
	// actor and reads and writes no card (coordinator.go holds every verb to
	// one class).
	verbClasses["backup"] = classMachine
	notServed = append(notServed, "backup")
}

// secretPatterns are the shapes of a credential a backup must not hold: the
// kind names what was found, and the value is never printed (docs/SPEC-SPRINT.md,
// sprint-backup-verb).
var secretPatterns = []struct {
	kind string
	re   *regexp.Regexp
}{
	{"private key block", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"age secret key", regexp.MustCompile(`AGE-SECRET-KEY-1[0-9A-Z]{20,}`)},
	{"aws access key id", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"github token", regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{22,})`)},
	{"api key", regexp.MustCompile(`\bsk-(?:ant-)?[A-Za-z0-9_-]{20,}`)},
	{"slack token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`)},
	{"bearer credential", regexp.MustCompile(`(?i)\bauthorization:\s*bearer\s+[A-Za-z0-9._~+/=-]{16,}`)},
	{"password in a url", regexp.MustCompile(`://[^/\s:@]+:[^/\s:@]{3,}@`)},
}

// secretKinds counts the credential shapes the bytes hold, by kind. It reads
// the bytes as they are: a value the store compressed on disk is not seen
// (the sprint's twin document is plain text; an RDB's strings may not be).
func secretKinds(b []byte) map[string]int {
	found := map[string]int{}
	for _, p := range secretPatterns {
		if n := len(p.re.FindAll(b, -1)); n > 0 {
			found[p.kind] = n
		}
	}
	return found
}

func kindsText(found map[string]int) string {
	var parts []string
	for k, n := range found {
		parts = append(parts, fmt.Sprintf("%s x%d", k, n))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

// scanTwin is the snapshot's twin with the secrets scan after the load: the
// copy that is read back, loaded and compared is scanned too, so a copy that
// holds a credential fails the take as a copy that does not load does, is
// removed, and leaves the older backups unpruned (store.Snapshotter.Take).
type scanTwin struct {
	inner store.SnapshotTwin
	found map[string]int
}

func (s *scanTwin) Load(b []byte) (store.SnapshotCounts, error) {
	c, err := s.inner.Load(b)
	if err != nil {
		return c, err
	}
	if s.found = secretKinds(b); len(s.found) > 0 {
		return c, errors.New("holds a credential")
	}
	return c, nil
}

// cmdBackup is the sprint backup as one verb: the store written to a file with
// its checksum, the copy restored into a twin and its counts compared with the
// store's, the copy scanned for secrets, and the directory pruned to --keep
// only after the new copy passed all three. It is store.Snapshotter with the
// scan as part of the twin's load (docs/SPEC-SPRINT.md, sprint-backup-verb).
func (a *app) cmdBackup(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("backup")
	dir := fs.String("dir", "", "the directory the backup is written to (required)")
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
	now := time.Now
	if a.now != nil {
		now = a.now
	}
	var src store.SnapshotSource
	var inner store.SnapshotTwin = store.RDBTwin{}
	switch b := st.B.(type) {
	case *store.Redis:
		src = &redisSource{b: b}
	case *store.Mem:
		src, inner = store.MemSource{M: b}, store.MemTwin{}
	default:
		return refuse(stderr, "backup", "this store has no backup; run: nova-sprint backup --redis <a Redis address> --dir "+*dir)
	}
	twin := &scanTwin{inner: inner}
	sn := &store.Snapshotter{Dir: *dir, Keep: *keep, Source: src, Twin: twin, Now: now}
	got, err := sn.Take(context.Background())
	if len(twin.found) > 0 {
		fmt.Fprintf(stderr, "%s backup FAILED: the copy holds credentials (%s); the copy was removed, the older backups stay; remove them from the store (nova-sprint log shows where a text entered), then run: nova-sprint backup --dir %s\n", prog, kindsText(twin.found), *dir)
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s backup FAILED: %s\n", prog, err)
		return 1
	}
	if c.json {
		b, _ := json.Marshal(map[string]any{"file": got.File, "sha256": got.SHA256, "bytes": got.Bytes, "counts": got.Counts, "restored": true, "secrets": 0, "pruned": got.Pruned, "keep": *keep})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprintf(stdout, "BACKUP OK file=%s sha256=%s bytes=%d %s restored=twin compared=counts secrets=none pruned=%d keep=%d\n", got.File, got.SHA256, got.Bytes, countsText(got.Counts), len(got.Pruned), *keep)
	return 0
}
