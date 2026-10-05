package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/log"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func init() {
	// backup is the schedule of the store's host, like snapshot: it needs no
	// actor and reads and writes no card. The class test holds every verb to
	// one class (coordinator.go).
	verbClasses["backup"] = classMachine
	// the RDB is read from the disk the store's own host writes it to
	notServed = append(notServed, "backup")
}

// backupResult is the one value a backup renders, as a line or as JSON.
type backupResult struct {
	File    string `json:"file"`
	SHA256  string `json:"sha256"`
	Bytes   int    `json:"bytes"`
	Keys    int    `json:"keys"`
	Cards   int    `json:"cards"`
	Secrets int    `json:"secrets"`
}

// cmdBackup is the sprint backup as one verb (docs/SPEC-SPRINT.md,
// sprint-backup-verb): the store is written to --out with its SHA-256 beside
// it, the file is read back and restored into a twin, the twin's counts are
// compared with the store's at the save, and the file is scanned for
// secret-shaped values. A file that fails any of the three is removed.
func (a *app) cmdBackup(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("backup")
	out := fs.String("out", "", "the file the backup is written to (required; refused when it exists)")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "backup", argErr("takes no words ", err, pos...))
	}
	if *out == "" {
		return refuse(stderr, "backup", "wants --out <file>; run: nova-sprint backup --out sprint.backup")
	}
	path := filepath.Clean(*out)
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
		return refuse(stderr, "backup", "this store has no backup; run: nova-sprint backup --redis <a Redis address> --out "+path)
	}
	got, err := takeBackup(context.Background(), src, twin, path)
	if err != nil {
		var refused backupRefusal
		if errors.As(err, &refused) {
			return refuse(stderr, "backup", refused.Error())
		}
		fmt.Fprintf(stderr, "BACKUP FAILED: %s\n", err)
		return 1
	}
	if c.json {
		b, _ := json.Marshal(got)
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprintf(stdout, "BACKUP OK file=%s sha256=%s bytes=%d %s restored=twin compared=counts secrets=0\n", got.File, got.SHA256, got.Bytes, countsText(store.SnapshotCounts{Keys: got.Keys, Cards: got.Cards}))
	return 0
}

// backupRefusal is a failure of the request, before any file is written.
type backupRefusal string

func (r backupRefusal) Error() string { return string(r) }

// takeBackup is the three checks over one file: written, restored into the
// twin with its counts compared with the store's at the save, and scanned. A
// file that fails after it was written is removed, with its checksum.
func takeBackup(ctx context.Context, src store.SnapshotSource, twin store.SnapshotTwin, path string) (backupResult, error) {
	var out backupResult
	if _, err := os.Lstat(path); err == nil {
		return out, backupRefusal(path + " already exists and is never overwritten; run: nova-sprint backup --out <a file that is not there>")
	}
	rdb, live, err := src.Save(ctx)
	if err != nil {
		return out, fmt.Errorf("the store gave no backup: %w", err)
	}
	sum := sha256.Sum256(rdb)
	hexsum := hex.EncodeToString(sum[:])
	if err := atomicfile.WriteFile(path, rdb, 0o600, atomicfile.ExactMode(), atomicfile.NoReplace()); err != nil {
		return out, backupRefusal(fmt.Sprintf("%s cannot be written: %v; run: nova-sprint backup --out <a file in a directory that is there>", path, err))
	}
	side := path + ".sha256"
	if err := atomicfile.WriteFile(side, []byte(hexsum+"  "+filepath.Base(path)+"\n"), 0o600, atomicfile.ExactMode()); err != nil {
		return out, errors.Join(err, removeBackup(path))
	}
	fail := func(err error) (backupResult, error) { return out, errors.Join(err, removeBackup(path)) }
	counts, gotsum, err := store.RestoreDrill(path, twin)
	if err != nil {
		return fail(fmt.Errorf("the file does not restore: %v; the backup was removed", err))
	}
	if gotsum != hexsum {
		return fail(fmt.Errorf("the file %s does not match its checksum on reading it back; the backup was removed", path))
	}
	if why := backupCountsDiffer(live, counts); why != "" {
		return fail(fmt.Errorf("the file restored with other counts than the store held at the save (%s); the backup was removed", why))
	}
	if n := secretShapes(rdb); n > 0 {
		return fail(fmt.Errorf("the file holds secret-shaped values: secrets=%d; the backup was removed (a card brief or a note carries a credential: remove it from the store, then run: nova-sprint backup --out <file>)", n))
	}
	return backupResult{File: path, SHA256: hexsum, Bytes: len(rdb), Keys: counts.Keys, Cards: counts.Cards}, nil
}

// removeBackup removes a failed backup and its checksum; an absent file is the goal.
func removeBackup(path string) error {
	var errs []error
	for _, p := range []string{path, path + ".sha256"} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// backupCountsDiffer is what the twin counted against what the store held; a
// count of -1 is unknown and is not compared.
func backupCountsDiffer(want, got store.SnapshotCounts) string {
	var why []string
	if want.Keys >= 0 && got.Keys >= 0 && want.Keys != got.Keys {
		why = append(why, fmt.Sprintf("keys %d, twin %d", want.Keys, got.Keys))
	}
	if want.Cards >= 0 && got.Cards >= 0 && want.Cards != got.Cards {
		why = append(why, fmt.Sprintf("cards %d, twin %d", want.Cards, got.Cards))
	}
	return strings.Join(why, ", ")
}

// secretShapes is how many values the log redactor would take out of the
// bytes: the same shapes the fleet's logs are held to (internal/log), counted,
// never shown.
func secretShapes(b []byte) int {
	s := string(b)
	return strings.Count(log.Redact(s), log.Redacted) - strings.Count(s, log.Redacted)
}
