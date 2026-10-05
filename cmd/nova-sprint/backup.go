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
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/log"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func init() {
	// backup is the schedule of the store's host, like snapshot: it needs no
	// actor and reads and writes no card (the class test, coordinator.go).
	verbClasses["backup"] = classMachine
	// a Redis store's RDB is read from the disk its own host writes it to
	notServed = append(notServed, "backup")
}

// cmdBackup is the sprint backup as one verb: the store is written to --file,
// the file is read back and restored into a twin, the twin is compared with the
// store, and the file is scanned for secrets (SPEC-SPRINT, sprint-backup-verb).
// It uses no clock and opens no socket of its own: on a twin store it runs
// whole in memory and on the file system.
func (a *app) cmdBackup(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("backup")
	file := fs.String("file", "", "the file the backup is written to (required; it must not exist)")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "backup", argErr("takes no words ", err, pos...))
	}
	if *file == "" {
		return refuse(stderr, "backup", "wants --file <path>, a file that does not exist yet; run: nova-sprint backup --file sprint-backup.rdb")
	}
	if _, err := os.Lstat(*file); err == nil {
		return backupFailed(stderr, *file+" exists and is never overwritten; run: nova-sprint backup --file <a path that does not exist>")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "backup", err.Error()) // a store that did not answer: exit 2, as every verb's
	}
	var src store.SnapshotSource
	var twin store.SnapshotTwin = store.RDBTwin{}
	switch b := st.B.(type) {
	case *store.Redis:
		src = &redisSource{b: b}
	case *store.Mem:
		src, twin = store.MemSource{M: b}, store.MemTwin{}
	default:
		return backupFailed(stderr, "this store has no backup; run: nova-sprint backup --redis <a Redis address> --file "+*file)
	}
	var out strings.Builder
	if err := runBackup(context.Background(), src, twin, *file, &out); err != nil {
		return backupFailed(stderr, err.Error())
	}
	if c.json {
		b, _ := json.Marshal(map[string]any{"file": *file, "line": strings.TrimSpace(out.String())})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprint(stdout, out.String())
	return 0
}

// backupFailed is a backup that did not happen: exit 1, the verb's own words
// (usage and a store that did not answer are exit 2).
func backupFailed(stderr io.Writer, what string) int {
	fmt.Fprintf(stderr, "%s backup FAILED: %s\n", prog, oneline.Escape(what))
	return 1
}

// runBackup is the verb's law (the store-snapshot model's checks, over one
// file): write, read back against the checksum, restore into the twin and
// compare counts and (a document) the restored store's own document, scan for
// secrets. A file that fails any step is removed, so the path holds only a
// verified, secret-free backup. The one line it prints says what was proved.
func runBackup(ctx context.Context, src store.SnapshotSource, twin store.SnapshotTwin, path string, out io.Writer) error {
	rdb, live, err := src.Save(ctx)
	if err != nil {
		return fmt.Errorf("the store gave no backup: %w; run: nova-sprint backup --file "+path, err)
	}
	if err := writeAtomic(path, rdb); err != nil {
		return fmt.Errorf("%s cannot be written: %v; run: nova-sprint backup --file <a writable path>", path, err)
	}
	fail := func(err error) error {
		_ = os.Remove(path) // ignored: removing the failed backup; the error that failed it is what is returned
		return err
	}
	copied, err := os.ReadFile(path)
	if err != nil {
		return fail(fmt.Errorf("the backup %s cannot be read back: %v; it was removed", path, err))
	}
	sum := sha256.Sum256(rdb)
	if sha256.Sum256(copied) != sum {
		return fail(fmt.Errorf("the backup %s does not match its checksum on reading it back; it was removed", path))
	}
	got, err := twin.Load(copied)
	if err != nil {
		return fail(fmt.Errorf("the backup %s does not restore into a twin: %v; it was removed", path, err))
	}
	if why := backupCountsDiffer(live, got); why != "" {
		return fail(fmt.Errorf("the backup %s restored with other counts than the store held (%s); it was removed", path, why))
	}
	compared := "counts"
	if _, ok := twin.(store.MemTwin); ok {
		// a document is restored into a store and written again; that document
		// restored and written once more is the same one, so the restore
		// loses nothing the document holds (a first write may differ from a
		// store's own, which writes an absent field as null)
		once, err := backupRoundTrip(copied)
		if err == nil {
			var twice []byte
			twice, err = backupRoundTrip(once)
			if err == nil && string(twice) != string(once) {
				err = errors.New("the restored store writes a different document when restored again")
			}
		}
		if err != nil {
			return fail(fmt.Errorf("the backup %s does not restore into a twin that holds what it holds: %v; it was removed", path, err))
		}
		compared = "document+counts"
	}
	if lines := secretLines(copied); len(lines) > 0 {
		return fail(fmt.Errorf("the backup %s holds secret-shaped text on line %s (the value is not shown); it was removed; find the card or key that holds it and remove it; run: nova-sprint backup --file %s", path, joinInts(lines), path))
	}
	fmt.Fprintf(out, "BACKUP OK file=%s sha256=%s bytes=%d %s restored=twin compared=%s secrets=none\n", path, hex.EncodeToString(sum[:]), len(rdb), countsText(got), compared)
	return nil
}

// backupRoundTrip restores a document into a fresh store and writes it again.
func backupRoundTrip(doc []byte) ([]byte, error) {
	m := store.NewMem()
	if err := m.Restore(doc); err != nil {
		return nil, err
	}
	return m.Snapshot()
}

// backupCountsDiffer says where the restored counts differ from the store's;
// a count of -1 is unknown and not compared.
func backupCountsDiffer(live, got store.SnapshotCounts) string {
	switch {
	case live.Keys >= 0 && got.Keys >= 0 && live.Keys != got.Keys:
		return fmt.Sprintf("keys %d, restored %d", live.Keys, got.Keys)
	case live.Cards >= 0 && got.Cards >= 0 && live.Cards != got.Cards:
		return fmt.Sprintf("cards %d, restored %d", live.Cards, got.Cards)
	}
	return ""
}

// secretLines are the 1-based lines of the file the log package's redaction
// would change: the same rules that keep a secret out of a log keep it out of
// a backup. At most twenty are named.
func secretLines(b []byte) []int {
	var hits []int
	for i, l := range strings.Split(string(b), "\n") {
		if log.Redact(l) != l {
			if hits = append(hits, i+1); len(hits) == 20 {
				break
			}
		}
	}
	return hits
}

func joinInts(n []int) string {
	s := make([]string, len(n))
	for i, v := range n {
		s[i] = fmt.Sprint(v)
	}
	return strings.Join(s, ",")
}
