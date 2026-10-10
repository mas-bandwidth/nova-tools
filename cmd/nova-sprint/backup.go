package main

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/log"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
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
	file := fs.String("file", "", "the file the backup is written to (it must not exist); or --out")
	dry := fs.Bool("dry-run", false, "verify the backup in memory without writing --file; writes nothing")
	outDir := fs.String("out", "", "the directory the RESTORE dump's parts, SHA256SUMS and README.md are written to (it must not exist or be empty); or --file")
	partBytes := fs.Int64("part-bytes", backupPartBytes, "the largest part of the xz with --out, in bytes (under 100 MB)")
	novaSecrets := fs.String("nova-secrets", "nova-secrets", "the nova-secrets program the --out scan runs under")
	secStore := fs.String("secrets-store", "", "the nova-secrets store the --out scan reads (default: the seat login's)")
	secAs := fs.String("secrets-as", "", "the nova-secrets seat the --out scan reads (default: the seat login's)")
	secKey := fs.String("secrets-key", "", "the seat's age key file (default: the seat login's)")
	sops := fs.String("sops", "", "the sops program nova-secrets exec runs (default: the seat login's)")
	xz := fs.String("xz", "xz", "the xz program --out compresses with")
	split := fs.String("split", "split", "the split program --out splits with")
	redisServer := fs.String("redis-server", "redis-server", "the redis-server --out restores a Redis store's dump into, a throwaway on a unix socket")
	scan := fs.String("scan", "", "the child nova-secrets exec runs: count the values of these variables found on stdin (backup --out runs it)")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "backup", argErr("takes no words ", err, pos...))
	}
	if *scan != "" {
		if err := backupScan(strings.Split(*scan, ","), a.getenv, os.Stdin, stdout); err != nil {
			return backupFailed(stderr, "the scan could not read its stream: "+err.Error())
		}
		return 0
	}
	if *outDir != "" {
		if *file != "" || *dry {
			return refuse(stderr, "backup", "--out takes no --file or --dry-run; run: nova-sprint backup --out <dir>")
		}
		if *partBytes < 1 || *partBytes >= 100_000_000 {
			return refuse(stderr, "backup", "--part-bytes is a size above zero and under 100 MB; run: nova-sprint backup --out <dir>")
		}
		sc := backupScanner{bin: *novaSecrets, store: *secStore, as: *secAs, key: *secKey, sops: *sops}
		if l, ok, err := a.recordedLogin(); err == nil && ok {
			sc.store, sc.as, sc.key, sc.sops = cmp.Or(sc.store, l.Store), cmp.Or(sc.as, l.As), cmp.Or(sc.key, l.Key), cmp.Or(sc.sops, l.Sops)
		}
		exe := os.Executable
		if a.executable != nil {
			exe = a.executable
		}
		if sc.self, err = exe(); err != nil {
			return backupFailed(stderr, "this binary has no path for the scan to run: "+err.Error())
		}
		return a.backupOut(c, &backupOut{out: *outDir, partBytes: *partBytes, xz: *xz, split: *split, scan: sc}, *redisServer, stdout, stderr)
	}
	if *file == "" {
		return refuse(stderr, "backup", "wants --out <dir> or --file <path>, one that does not exist yet; run: nova-sprint backup --out sprint-backup")
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
		src, twin = store.MemSource{M: b, Names: st.Names}, store.MemTwin{Names: st.Names}
	default:
		return backupFailed(stderr, "this store has no backup; run: nova-sprint backup --redis <a Redis address> --file "+*file)
	}
	var out strings.Builder
	if err := runBackup(context.Background(), src, twin, *file, *dry, &out); err != nil {
		return backupFailed(stderr, err.Error())
	}
	if c.json {
		m := map[string]any{"file": *file, "line": strings.TrimSpace(out.String())}
		if *dry {
			m["dry_run"] = true
		}
		b, _ := json.Marshal(m)
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprint(stdout, out.String())
	return 0
}

// backupOut opens the store and runs backup --out on it: a twin store
// restores into a throwaway twin, a Redis store into a throwaway redis-server.
func (a *app) backupOut(c *common, b *backupOut, redisServer string, stdout, stderr io.Writer) int {
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "backup", err.Error())
	}
	switch be := st.B.(type) {
	case *store.Mem:
		b.src = memBackup{st: st}
		b.twin = func(context.Context, string) (backupTwin, error) { return &memTwin{names: st.Names}, nil }
		b.load = "-h <host> -p <port>"
	case *store.Redis:
		b.src = redisBackup{b: be, names: st.Names}
		b.twin = func(ctx context.Context, work string) (backupTwin, error) {
			return startServerTwin(ctx, redisServer, work, st.Names)
		}
		b.load = "-h <host> -p <port>"
	default:
		return backupFailed(stderr, "this store has no backup; run: nova-sprint backup --redis <a Redis address> --out "+b.out)
	}
	res, err := b.run(context.Background())
	if err != nil {
		return backupFailed(stderr, err.Error())
	}
	for _, f := range res.files {
		fmt.Fprintln(stdout, f)
	}
	fmt.Fprintln(stdout, res.line)
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
// compare counts and, on a twin store, the restored store's sprint state with
// the store's part for part (store.SemanticRestore) and its own document, scan
// for secrets. A file that fails any step is removed, so the path holds only a
// verified, secret-free backup. The one line it prints says what was proved:
// restore=semantic, or restore=integrity (the RDB's header, version and
// checksum alone), which is never reported as a semantic restore.
func runBackup(ctx context.Context, src store.SnapshotSource, twin store.SnapshotTwin, path string, dry bool, out io.Writer) error {
	rdb, live, err := src.Save(ctx)
	if err != nil {
		return fmt.Errorf("the store gave no backup: %w; run: nova-sprint backup --file "+path, err)
	}
	level := store.RestoreLevel(src, twin)
	var state store.SprintState
	if level == store.RestoreSemantic {
		if state, err = src.(store.StateSource).State(ctx); err != nil {
			return fmt.Errorf("the store's sprint state at the backup cannot be read: %v; nothing was written; run: nova-sprint backup --file %s", err, path)
		}
	}
	sum := sha256.Sum256(rdb)
	if dry {
		got, compared, err := verifyBackup(ctx, twin, level, live, state, rdb)
		if err != nil {
			return fmt.Errorf("the backup %s %v", path, err)
		}
		if lines := secretLines(rdb); len(lines) > 0 {
			return fmt.Errorf("the backup %s holds secret-shaped text on line %s (the value is not shown); find the card or key that holds it and remove it; run: nova-sprint backup --file %s", path, joinInts(lines), path)
		}
		fmt.Fprintf(out, "BACKUP DRY-RUN file=%s sha256=%s bytes=%d %s restore=%s compared=%s secrets=none%s; nothing was written\n", path, hex.EncodeToString(sum[:]), len(rdb), countsText(got), level, compared, integrityOnly(level))
		return nil
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
	if sha256.Sum256(copied) != sum {
		return fail(fmt.Errorf("the backup %s does not match its checksum on reading it back; it was removed", path))
	}
	got, compared, err := verifyBackup(ctx, twin, level, live, state, copied)
	if err != nil {
		return fail(fmt.Errorf("the backup %s %v; it was removed", path, err))
	}
	if lines := secretLines(copied); len(lines) > 0 {
		return fail(fmt.Errorf("the backup %s holds secret-shaped text on line %s (the value is not shown); it was removed; find the card or key that holds it and remove it; run: nova-sprint backup --file %s", path, joinInts(lines), path))
	}
	fmt.Fprintf(out, "BACKUP OK file=%s sha256=%s bytes=%d %s restore=%s compared=%s secrets=none%s\n", path, hex.EncodeToString(sum[:]), len(rdb), countsText(got), level, compared, integrityOnly(level))
	return nil
}

// verifyBackup restores the bytes into the twin and compares them with the
// store's: the counts, and at the semantic level the sprint state and the
// document. It returns the counts and what was compared, or the step that
// failed, worded to follow "the backup <path>".
func verifyBackup(ctx context.Context, twin store.SnapshotTwin, level string, live store.SnapshotCounts, state store.SprintState, b []byte) (store.SnapshotCounts, string, error) {
	got, err := twin.Load(b)
	if err != nil {
		return got, "", fmt.Errorf("does not restore into a twin: %v", err)
	}
	if why := backupCountsDiffer(live, got); why != "" {
		return got, "", fmt.Errorf("restored with other counts than the store held (%s)", why)
	}
	if level != store.RestoreSemantic {
		return got, "header+checksum", nil
	}
	if err := store.SemanticRestore(ctx, state, twin.(store.StateTwin), b); err != nil {
		return got, "", fmt.Errorf("does not restore the sprint the store held: %v", err)
	}
	if _, ok := twin.(store.MemTwin); ok {
		// a document is restored into a store and written again; that document
		// restored and written once more is the same one, so the restore
		// loses nothing the document holds (a first write may differ from a
		// store's own, which writes an absent field as null)
		once, err := backupRoundTrip(b)
		if err == nil {
			var twice []byte
			twice, err = backupRoundTrip(once)
			if err == nil && string(twice) != string(once) {
				err = errors.New("the restored store writes a different document when restored again")
			}
		}
		if err != nil {
			return got, "", fmt.Errorf("does not restore into a twin that holds what it holds: %v", err)
		}
		return got, "state+document+counts", nil
	}
	return got, "state+counts", nil
}

// integrityOnly is what a success line says after it when the restore was
// proved at the integrity level: that it is not a semantic restore.
func integrityOnly(level string) string {
	if level == store.RestoreSemantic {
		return ""
	}
	return "; integrity only, not a semantic restore: the sprint's state was not loaded and compared"
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
