package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func init() {
	// backup is the schedule of the store's host, like snapshot: it reads the
	// store and writes one file, and no card.
	verbClasses["backup"] = classMachine
	notServed = append(notServed, "backup")
}

// secretShape is the form a key takes, by name; a hit is reported by name and
// line and its text is never printed (docs/SPEC-SPRINT.md, sprint-backup-verb).
type secretShape struct {
	name string
	re   *regexp.Regexp
}

var backupShapes = []secretShape{
	{"pem-private-key", regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`)},
	{"age-secret-key", regexp.MustCompile(`AGE-SECRET-KEY-1[0-9A-Za-z]{10,}`)},
	{"forge-token", regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`)},
	{"forge-fine-grained-token", regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`)},
	{"provider-api-key", regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`)},
	{"cloud-access-key-id", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{"slack-token", regexp.MustCompile(`xox[abprs]-[A-Za-z0-9-]{10,}`)},
	{"password-in-address", regexp.MustCompile(`[a-z][a-z0-9+.-]*://[^\s/:@"]+:[^\s/@"]+@`)},
}

// secretHit is one shape found at one line of the file.
type secretHit struct {
	Shape string `json:"shape"`
	Line  int    `json:"line"`
}

// scanSecrets is every shape found in b, by line; it never returns the text.
func scanSecrets(b []byte) []secretHit {
	var hits []secretHit
	for i, ln := range bytes.Split(b, []byte("\n")) {
		for _, s := range backupShapes {
			if s.re.Match(ln) {
				hits = append(hits, secretHit{Shape: s.name, Line: i + 1})
			}
		}
	}
	return hits
}

// cmdBackup writes the store to --file, reads it back against its SHA-256,
// restores it into a twin and compares, and scans the file for secrets; a
// file that fails any check is removed (SPEC-SPRINT, sprint-backup-verb).
func (a *app) cmdBackup(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("backup")
	file := fs.String("file", "", "the file the store is written to (required)")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "backup", argErr("takes no words ", err, pos...))
	}
	if *file == "" {
		return refuse(stderr, "backup", "wants --file <path>; run: nova-sprint backup --file /tmp/nova-sprint-backup.json")
	}
	if _, err := os.Stat(*file); err == nil {
		return refuse(stderr, "backup", *file+" exists and is not overwritten; run: nova-sprint backup --file <a new path>")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "backup", err.Error())
	}
	var src store.SnapshotSource
	var twin store.SnapshotTwin = store.RDBTwin{}
	mem := false
	switch b := st.B.(type) {
	case *store.Redis:
		src = &redisSource{b: b}
	case *store.Mem:
		src, twin, mem = store.MemSource{M: b}, store.MemTwin{}, true
	default:
		return refuse(stderr, "backup", "this store has no backup; run: nova-sprint backup --redis <a Redis address> --file "+*file)
	}
	got, err := backupTo(context.Background(), *file, src, twin, mem)
	if err != nil {
		fmt.Fprintf(stderr, "%s backup FAILED: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	if c.json {
		b, _ := json.Marshal(got)
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprintf(stdout, "BACKUP OK file=%s sha256=%s bytes=%d %s verified=checksum+twin+secrets\n", got.File, got.SHA256, got.Bytes, countsText(got.Counts))
	return 0
}

// backupTo is the whole check, on a source and a twin so a test runs it with
// no socket: write, read back, restore, compare, scan. Any failure removes
// the file.
func backupTo(ctx context.Context, file string, src store.SnapshotSource, twin store.SnapshotTwin, mem bool) (store.SnapshotTaken, error) {
	var out store.SnapshotTaken
	doc, live, err := src.Save(ctx)
	if err != nil {
		return out, fmt.Errorf("the store gave no backup: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return out, fmt.Errorf("the directory of %s cannot be made: %v", file, err)
	}
	sum := sha256.Sum256(doc)
	if err := os.WriteFile(file, doc, 0o600); err != nil {
		return out, fmt.Errorf("%s cannot be written: %v", file, err)
	}
	fail := func(err error) (store.SnapshotTaken, error) {
		_ = os.Remove(file) // ignored: removing the failed file; the error that failed it is what is returned
		return out, err
	}
	back, err := os.ReadFile(file)
	if err != nil {
		return fail(fmt.Errorf("%s cannot be read back: %v", file, err))
	}
	if sha256.Sum256(back) != sum {
		return fail(fmt.Errorf("%s does not match its checksum on reading it back; the file was removed", file))
	}
	counts, err := twin.Load(back)
	if err != nil {
		return fail(fmt.Errorf("%s does not restore into a twin: %v; the file was removed", file, err))
	}
	if live.Keys >= 0 && counts.Keys >= 0 && live.Keys != counts.Keys || live.Cards >= 0 && counts.Cards >= 0 && live.Cards != counts.Cards {
		return fail(fmt.Errorf("%s restored with other counts than the store held (keys %d, cards %d; twin keys %d, cards %d); the file was removed", file, live.Keys, live.Cards, counts.Keys, counts.Cards))
	}
	if mem {
		// a restore normalises empty fields, so the twin is compared with
		// itself: restoring the twin's own document gives that document
		first, second := store.NewMem(), store.NewMem()
		if err := first.Restore(back); err != nil {
			return fail(err)
		}
		d1, err := first.Snapshot()
		if err != nil {
			return fail(err)
		}
		if err := second.Restore(d1); err != nil {
			return fail(err)
		}
		d2, err := second.Snapshot()
		if err != nil {
			return fail(err)
		}
		if !bytes.Equal(d1, d2) {
			return fail(fmt.Errorf("%s restored into a twin that does not hold what it was restored from; the file was removed", file))
		}
	}
	if hits := scanSecrets(back); len(hits) > 0 {
		var parts []string
		for _, h := range hits {
			parts = append(parts, fmt.Sprintf("%s at line %d", h.Shape, h.Line))
		}
		return fail(fmt.Errorf("%s holds what has the shape of a secret (%s); the file was removed and nothing is kept", file, strings.Join(parts, ", ")))
	}
	return store.SnapshotTaken{File: file, SHA256: hex.EncodeToString(sum[:]), Bytes: len(doc), Counts: counts}, nil
}
