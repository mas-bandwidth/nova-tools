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

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func init() {
	// backup is the schedule of the store's host, like snapshot: it needs no
	// actor and reads and writes no card (coordinator.go).
	verbClasses["backup"] = classMachine
	notServed = append(notServed, "backup")
}

// secretPatterns are the shapes of a credential a backup must not carry: the
// kind is what a finding names, never the value (docs/SPEC-SPRINT.md,
// sprint-backup-verb).
var secretPatterns = []struct {
	kind string
	re   *regexp.Regexp
}{
	{"private-key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"age-secret-key", regexp.MustCompile(`AGE-SECRET-KEY-1[0-9A-Z]{20,}`)},
	{"github-token", regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{40,})`)},
	{"aws-access-key", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"slack-token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`)},
	{"api-key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`)},
	{"bearer-token", regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{24,}`)},
}

// secretFinding is one kind of credential found in a backup, and how many.
type secretFinding struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// scanSecrets names the kinds of credential the bytes hold with a count each,
// in kind order; it returns no value it matched.
func scanSecrets(b []byte) []secretFinding {
	var out []secretFinding
	for _, p := range secretPatterns {
		if n := len(p.re.FindAll(b, -1)); n > 0 {
			out = append(out, secretFinding{p.kind, n})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

func findingsText(fs []secretFinding) string {
	if len(fs) == 0 {
		return "none"
	}
	var parts []string
	for _, f := range fs {
		parts = append(parts, fmt.Sprintf("%s=%d", f.Kind, f.Count))
	}
	return strings.Join(parts, ",")
}

// backupResult is what one backup proved.
type backupResult struct {
	File     string               `json:"file"`
	SHA256   string               `json:"sha256"`
	Bytes    int                  `json:"bytes"`
	Counts   store.SnapshotCounts `json:"counts"`
	Restored store.SnapshotCounts `json:"restored"`
	Secrets  []secretFinding      `json:"secrets"`
	Pruned   int                  `json:"pruned"`
}

// cmdBackup is the sprint backup as one verb: the store is written to a file
// (store.Snapshotter, which checksums it and loads it into a twin), the file
// is restored into a twin again and its counts compared with the store's at the
// save, and the file is scanned for credentials. A backup that holds one is a
// failure (exit 1, the file stays as the only copy and the finding names its
// kind and count, never the value) (SPEC-SPRINT, sprint-backup-verb).
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
		return refuse(stderr, "backup", "this store has no snapshot; run: nova-sprint backup --redis <a Redis address> --dir "+*dir)
	}
	sn := &store.Snapshotter{Dir: *dir, Keep: *keep, Source: src, Twin: twin, Now: now}
	got, err := sn.Take(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "%s backup FAILED: %s\n", prog, err)
		return 1
	}
	restored, _, err := store.RestoreDrill(got.File, twin)
	if err != nil {
		fmt.Fprintf(stderr, "%s backup FAILED: the file %s was written and does not restore: %s\n", prog, got.File, err)
		return 1
	}
	if restored != got.Counts {
		fmt.Fprintf(stderr, "%s backup FAILED: the file %s restored with %s, not the %s it was written with\n", prog, got.File, countsText(restored), countsText(got.Counts))
		return 1
	}
	b, err := os.ReadFile(got.File)
	if err != nil {
		fmt.Fprintf(stderr, "%s backup FAILED: the file %s cannot be read for the secrets scan: %v; run: nova-sprint backup --dir %s\n", prog, got.File, err, *dir)
		return 1
	}
	res := backupResult{File: got.File, SHA256: got.SHA256, Bytes: got.Bytes, Counts: got.Counts, Restored: restored, Secrets: scanSecrets(b), Pruned: len(got.Pruned)}
	if len(res.Secrets) > 0 {
		fmt.Fprintf(stderr, "%s backup FAILED: the file %s holds credentials (%s); it is kept as the only copy and is not to be stored or shared; find the rows that hold them with grep on the store's text, remove them from the store, and run: nova-sprint backup --dir %s\n", prog, got.File, findingsText(res.Secrets), *dir)
		return 1
	}
	if c.json {
		j, _ := json.Marshal(res)
		fmt.Fprintln(stdout, string(j))
		return 0
	}
	fmt.Fprintf(stdout, "BACKUP OK file=%s sha256=%s bytes=%d %s restored=%s secrets=none pruned=%d keep=%d\n", res.File, res.SHA256, res.Bytes, countsText(res.Counts), countsText(res.Restored), res.Pruned, *keep)
	return 0
}
