package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func init() {
	// backup is the schedule of the store's host, like snapshot: it needs no
	// actor and reads and writes no card (coordinator.go holds every verb to
	// one class)
	verbClasses["backup"] = classMachine
	// the RDB is read from the disk the store's own host writes it to
	notServed = append(notServed, "backup")
}

// secretKinds are the shapes a credential has in a file. The scan names a kind
// and a count and never the match itself, so a report cannot leak what it
// found (SPEC-SPRINT, backup).
var secretKinds = []struct {
	name string
	re   *regexp.Regexp
}{
	{"private-key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"github-token", regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{22,})`)},
	{"api-key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`)},
	{"aws-key", regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`)},
	{"slack-token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`)},
	{"bearer", regexp.MustCompile(`(?i)\bauthorization["':= ]+bearer +[A-Za-z0-9._~+/=-]{20,}`)},
}

// scanSecrets counts the secrets of each kind in b; a kind with none is not in
// the map.
func scanSecrets(b []byte) map[string]int {
	found := map[string]int{}
	for _, k := range secretKinds {
		if n := len(k.re.FindAllIndex(b, -1)); n > 0 {
			found[k.name] = n
		}
	}
	return found
}

// secretsText is the found secrets as `kind=n` words, in name order.
func secretsText(found map[string]int) string {
	var w []string
	for k, n := range found {
		w = append(w, fmt.Sprintf("%s=%d", k, n))
	}
	sort.Strings(w)
	return strings.Join(w, " ")
}

// backupResult is the one value a backup builds, rendered as a line or as JSON.
type backupResult struct {
	store.SnapshotTaken
	Secrets map[string]int `json:"secrets"`
}

// cmdBackup is the sprint's backup in one verb (SPEC-SPRINT, backup): the
// store is written to a verified file (store.Snapshotter: saved, checksummed,
// restored into a twin, counts compared), and the file is scanned for secrets.
// It replaces the hand procedure of a restore test and a secrets scan.
func (a *app) cmdBackup(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("backup")
	dir := fs.String("dir", "", "the directory the backup file is written to (required)")
	keep := fs.Int("keep", snapshotKeepDefault, "how many verified backups stay; older ones are pruned after a newer one verifies")
	dry := fs.Bool("dry-run", false, "print the plan and write nothing: the store is not asked for a backup")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "backup", argErr("takes no words ", err, pos...))
	}
	if *dir == "" || *keep < 1 {
		return refuse(stderr, "backup", "wants --dir <dir> and --keep of at least 1 when given; run: nova-sprint backup --dir backups --keep 7")
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
	if *dry {
		fmt.Fprintf(stdout, "BACKUP OK dry-run dir=%s keep=%d steps=save,restore-twin,compare-counts,scan-secrets; nothing was written\n", *dir, *keep)
		return 0
	}
	sn := &store.Snapshotter{Dir: *dir, Keep: *keep, Source: src, Twin: twin, Now: now}
	got, err := sn.Take(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "%s backup FAILED: %s; the older backups stay; run: nova-sprint backup --dir %s\n", prog, oneline.Escape(err.Error()), *dir)
		return 1
	}
	b, err := os.ReadFile(got.File)
	if err != nil {
		fmt.Fprintf(stderr, "%s backup FAILED: %s was written and cannot be read back to scan: %s; run: nova-sprint backup --dir %s\n", prog, got.File, oneline.Escape(err.Error()), *dir)
		return 1
	}
	res := backupResult{SnapshotTaken: got, Secrets: scanSecrets(b)}
	if len(res.Secrets) > 0 {
		fmt.Fprintf(stderr, "%s backup FAILED: %s holds secrets (%s); the file is kept; run: remove the secrets from the store, then nova-sprint backup --dir %s\n", prog, got.File, secretsText(res.Secrets), *dir)
		return 1
	}
	if c.json {
		j, _ := json.Marshal(res) // ignored: a struct of strings, numbers and a map of counts always marshals
		fmt.Fprintln(stdout, string(j))
		return 0
	}
	fmt.Fprintf(stdout, "BACKUP OK file=%s sha256=%s bytes=%d %s restored=twin compared=counts secrets=0 pruned=%d keep=%d\n", got.File, got.SHA256, got.Bytes, countsText(got.Counts), len(got.Pruned), *keep)
	return 0
}
