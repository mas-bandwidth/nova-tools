package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// The sprint backup into a directory (docs/SPEC-SPRINT.md, sprint-backup-out):
// the hand procedure of 2026-10-04 as one verb, its steps in its order. The
// epoch's keys and the shared keys are written as a RESTORE text dump,
// compressed with xz -9 and split into parts under --part-bytes; the sums of
// the text and of the xz are recorded; the parts are put together again,
// checked against both sums and restored into a throwaway store holding this
// build's function library. A twin's restore is compared as sprint state
// (restore=semantic). A Redis's restore compares the same sprint state and the
// keys and column counts, under this build's function library. The
// dump and the restored values are scanned for every nova-secrets value by a
// child of nova-secrets exec, the one way a value reaches a process, which
// prints counts only; and the README says what the files are and how to load
// them. All of it is done in a private work directory beside --out, which
// becomes --out only when every step passed: a failed backup writes no --out.

// backupPartBytes is the largest part by default: under 100 MB, the most the
// forge takes in one file.
const backupPartBytes = 95_000_000

// backupSource is the store a backup is taken from: its epoch, the keys of
// that epoch and the shared ones, and the cards of its work table by column.
type backupSource interface {
	Take(ctx context.Context) (epoch uint64, keys []store.DumpKey, cols map[string]int, err error)
}

// stateSource is a backup source whose sprint state can be read, so the
// restore can be proved semantic rather than by counts.
type stateSource interface {
	State(ctx context.Context) (store.SprintState, error)
}

// stateTwin is a throwaway whose restored sprint state can be read.
type stateTwin interface {
	State(ctx context.Context) (store.SprintState, error)
}

// backupTwin is the throwaway store a dump is restored into.
type backupTwin interface {
	// Restore loads the keys into the empty twin and counts what it holds.
	Restore(ctx context.Context, keys []store.DumpKey) (store.BackupCounts, error)
	// Values writes every key the twin holds and every value of it, decoded,
	// for the secret scan; it is written to the scan's pipe and never to a file.
	Values(ctx context.Context, w io.Writer) error
	// Library names the function library the twin restored under.
	Library() string
	Close()
}

// backupOut is one run of backup --out, every outside thing it uses a field.
type backupOut struct {
	out       string
	partBytes int64
	src       backupSource
	twin      func(ctx context.Context, work string) (backupTwin, error)
	xz, split string
	scan      backupScanner
	// load is the README's load command's address words (-h <host> -p <port>).
	load string
}

// backupScanner runs the secret scan of a dump: the names of the sealed
// values, and the scan of the stream under nova-secrets exec.
type backupScanner struct {
	bin, store, as, key, sops string
	self                      string
}

// backupResult is what a backup printed: one line per file, then the counts.
type backupResult struct {
	files []string
	line  string
}

// run is the backup, its steps in the hand procedure's order.
func (b *backupOut) run(ctx context.Context) (backupResult, error) {
	var res backupResult
	if ents, err := os.ReadDir(b.out); err == nil && len(ents) > 0 {
		return res, fmt.Errorf("--out %s is not empty, and a backup is never written over another; run: nova-sprint backup --out <a new directory>", b.out)
	} else if err != nil && !os.IsNotExist(err) {
		return res, fmt.Errorf("--out %s cannot be read: %v; run: nova-sprint backup --out <a new directory>", b.out, err)
	}
	parent := filepath.Dir(filepath.Clean(b.out))
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return res, fmt.Errorf("the directory %s cannot be made: %v", parent, err)
	}
	work, err := os.MkdirTemp(parent, ".nova-sprint-backup-")
	if err != nil {
		return res, fmt.Errorf("no work directory beside --out: %v", err)
	}
	defer func() {
		_ = safepath.RemoveUnder(parent, work) // ignored: the work directory goes whatever happened; a leftover is beside --out, never in it
	}()
	final := filepath.Join(work, "out")
	if err := os.Mkdir(final, 0o755); err != nil {
		return res, err
	}

	// 1. the RESTORE text dump of the epoch's keys and the shared keys
	epoch, keys, cols, err := b.src.Take(ctx)
	if err != nil {
		return res, fmt.Errorf("the store gave no dump: %w", err)
	}
	var want store.SprintState
	semantic := false
	if src, ok := b.src.(stateSource); ok {
		if want, err = src.State(ctx); err != nil {
			return res, fmt.Errorf("the store's sprint state at the backup cannot be read: %v", err)
		}
		semantic = true
	}
	live := store.BackupCounts{Keys: len(keys), Columns: cols}
	base := fmt.Sprintf("sprint-epoch%d.restore.txt", epoch)
	textPath, xzPath := filepath.Join(work, base), filepath.Join(work, base+".xz")
	tf, err := os.OpenFile(textPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return res, err
	}
	tw := bufio.NewWriter(tf)
	for _, k := range keys {
		if _, err := tw.WriteString(store.RestoreLine(k)); err != nil {
			_ = tf.Close() // ignored: the write error is what is returned
			return res, err
		}
	}
	if err := tw.Flush(); err != nil {
		_ = tf.Close() // ignored: the flush error is what is returned
		return res, err
	}
	if err := tf.Close(); err != nil {
		return res, err
	}

	// 2. xz -9
	if err := runTo(ctx, xzPath, nil, b.xz, "-9", "-c", textPath); err != nil {
		return res, fmt.Errorf("xz -9 failed: %v; run: nova-sprint backup --xz <the path of xz>", err)
	}

	// 3. split into parts under --part-bytes
	prefix := filepath.Join(final, base+".xz.part-")
	if out, err := subproc.Context(ctx, b.split, "-b", strconv.FormatInt(b.partBytes, 10), "-d", "-a", "3", xzPath, prefix).CombinedOutput(); err != nil {
		return res, fmt.Errorf("split failed: %v: %s; run: nova-sprint backup --split <the path of split>", err, strings.TrimSpace(string(out)))
	}
	parts, err := filepath.Glob(prefix + "*")
	if err != nil || len(parts) == 0 {
		return res, fmt.Errorf("split wrote no part of %s", xzPath)
	}
	sort.Strings(parts)

	// 4. the sums of the text and of the xz (and of each part)
	textSum, err := fileSum(textPath)
	if err != nil {
		return res, err
	}
	xzSum, err := fileSum(xzPath)
	if err != nil {
		return res, err
	}
	sums := textSum + "  " + base + "\n" + xzSum + "  " + base + ".xz\n"
	partSums := make([]string, len(parts))
	for i, p := range parts {
		if partSums[i], err = fileSum(p); err != nil {
			return res, err
		}
		sums += partSums[i] + "  " + filepath.Base(p) + "\n"
	}
	if err := os.WriteFile(filepath.Join(final, "SHA256SUMS"), []byte(sums), 0o644); err != nil {
		return res, err
	}

	// 5. the parts put together, checked against both sums, restored into a
	// throwaway store and counted
	var joined []byte
	for _, p := range parts {
		pb, err := os.ReadFile(p)
		if err != nil {
			return res, err
		}
		joined = append(joined, pb...)
	}
	if sumOf(joined) != xzSum {
		return res, errors.New("the parts put together are not the xz they were split from")
	}
	var text bytes.Buffer
	dec := subproc.Context(ctx, b.xz, "-dc")
	dec.Stdin, dec.Stdout = bytes.NewReader(joined), &text
	if err := dec.Run(); err != nil {
		return res, fmt.Errorf("the parts do not decompress: %v", err)
	}
	if sumOf(text.Bytes()) != textSum {
		return res, errors.New("the parts decompress to another text than the dump")
	}
	restored, err := store.ParseRestoreDump(text.Bytes())
	if err != nil {
		return res, err
	}
	twin, err := b.twin(ctx, work)
	if err != nil {
		return res, fmt.Errorf("no throwaway store to restore into: %v", err)
	}
	defer twin.Close()
	got, err := twin.Restore(ctx, restored)
	if err != nil {
		return res, fmt.Errorf("the dump does not restore: %v", err)
	}
	if why := live.Differ(got); why != "" {
		return res, fmt.Errorf("the dump restored with other counts than the store held (%s); run the backup again when the sprint is quiet", why)
	}
	level, compared := store.RestoreIntegrity, "counts"
	if st, ok := twin.(stateTwin); semantic && ok {
		gotState, err := st.State(ctx)
		if err != nil {
			return res, fmt.Errorf("the dump does not restore into a store whose sprint can be read: %v", err)
		}
		if why := sprintStateDiff(want, gotState); why != "" {
			return res, fmt.Errorf("the dump does not restore the sprint the store held (%s)", why)
		}
		level, compared = store.RestoreSemantic, "state+counts"
	}

	// 6. the scan for every nova-secrets value, counts only
	names, matched, err := b.scan.sealedNames(ctx, func(w io.Writer) error {
		if _, err := w.Write(text.Bytes()); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
		return twin.Values(ctx, w)
	})
	if err != nil {
		return res, fmt.Errorf("the secret scan did not run: %v", err)
	}
	if matched > 0 {
		return res, &secretsFound{names: names, matched: matched}
	}

	// 7. the README section
	readme := backupReadme(epoch, base, parts, textSum, xzSum, partSums, live, twin.Library(), b.load, level)
	if err := os.WriteFile(filepath.Join(final, "README.md"), []byte(readme), 0o644); err != nil {
		return res, err
	}

	// every step passed: the work's output becomes --out
	_ = os.Remove(b.out) // ignored: an empty --out goes so the rename takes its place; one that is not empty was refused above
	if err := os.Rename(final, b.out); err != nil {
		return res, fmt.Errorf("--out %s cannot be written: %v", b.out, err)
	}
	ents, err := os.ReadDir(b.out)
	if err != nil {
		return res, err
	}
	for _, e := range ents {
		p := filepath.Join(b.out, e.Name())
		s, err := fileSum(p)
		if err != nil {
			return res, err
		}
		fi, err := os.Stat(p)
		if err != nil {
			return res, err
		}
		res.files = append(res.files, fmt.Sprintf("BACKUP FILE %s bytes=%d sha256=%s", e.Name(), fi.Size(), s))
	}
	res.line = fmt.Sprintf("BACKUP OK out=%s epoch=%d %s restored=%s %s parts=%d text_sha256=%s xz_sha256=%s secrets=%d matched=0 restore=%s compared=%s%s",
		b.out, epoch, live.Text(), twin.Library(), got.Text(), len(parts), textSum, xzSum, names, level, compared, integrityOnly(level))
	return res, nil
}

// sprintStateDiff names the parts where the restored sprint is not the source,
// or "" when they are the same. A count match is not this.
func sprintStateDiff(want, got store.SprintState) string {
	d := want.Diff(got)
	if len(d) == 0 {
		return ""
	}
	names := d
	more := ""
	if len(names) > 8 {
		names, more = names[:8], fmt.Sprintf(" and %d more", len(d)-8)
	}
	return fmt.Sprintf("%d part(s): %s%s", len(d), strings.Join(names, ", "), more)
}

// secretsFound is a backup whose dump holds nova-secrets values: the count of
// the values found, never a value or where it was.
type secretsFound struct{ names, matched int }

func (e *secretsFound) Error() string {
	return fmt.Sprintf("the dump holds nova-secrets values: secrets=%d matched=%d (no value and no place is shown); nothing was written to --out; find the key that holds it and remove it, then run the backup again", e.names, e.matched)
}

func runTo(ctx context.Context, path string, stdin io.Reader, name string, args ...string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	var errb bytes.Buffer
	c := subproc.Context(ctx, name, args...)
	c.Stdin, c.Stdout, c.Stderr = stdin, f, &errb
	runErr := c.Run()
	closeErr := f.Close()
	if runErr != nil {
		return fmt.Errorf("%v: %s", runErr, strings.TrimSpace(errb.String()))
	}
	return closeErr
}

func sumOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func fileSum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close() // ignored: a read-only file; the read's error is what is returned
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// backupReadme is the README section: what the files are, their order, the
// sums, and the load command.
func backupReadme(epoch uint64, base string, parts []string, textSum, xzSum string, partSums []string, c store.BackupCounts, library, load, level string) string {
	var b strings.Builder
	names := make([]string, len(parts))
	for i, p := range parts {
		names[i] = filepath.Base(p)
	}
	fmt.Fprintf(&b, "## Sprint backup, epoch %d\n\n", epoch)
	proved := "with the same counts; the counts are not a semantic restore, the sprint's state was not compared"
	if level == store.RestoreSemantic {
		proved = "with the same counts and the same sprint state"
	}
	fmt.Fprintf(&b, "Written by `nova-sprint backup --out`: the sprint store's keys of epoch %d, the keys every epoch shares and the records an older epoch left, one `RESTORE <key> <ttl ms> <payload>` line a key (`%s`), compressed with `xz -9` (`%s.xz`) and split into %d parts under 100 MB. It holds %s; it was restored into a throwaway store under %s %s, and scanned for every nova-secrets value with no match.\n\n", epoch, base, base, len(parts), c.Text(), library, proved)
	b.WriteString("| order | file | sha256 |\n|---|---|---|\n")
	for i, n := range names {
		fmt.Fprintf(&b, "| %d | `%s` | `%s` |\n", i+1, n, partSums[i])
	}
	fmt.Fprintf(&b, "\n- the xz, `%s.xz`: `%s`\n- the text, `%s`: `%s`\n\n`SHA256SUMS` holds the same sums.\n\n", base, xzSum, base, textSum)
	b.WriteString("To load it, into an empty Redis that holds this build's function library (the keys are restored as they were and are read only through it):\n\n```sh\n")
	fmt.Fprintf(&b, "cat %s > %s.xz\n", strings.Join(names, " "), base)
	fmt.Fprintf(&b, "xz -dk %s.xz\n", base)
	b.WriteString("sha256sum -c --ignore-missing SHA256SUMS\n")
	fmt.Fprintf(&b, "nova-redis fn load --addr <host>:<port>\n")
	fmt.Fprintf(&b, "redis-cli %s < %s\n```\n", load, base)
	return b.String()
}

// run lists the seat's sealed names (nova-secrets names: names only, nothing
// decrypted) and runs the scan as a child of nova-secrets exec with those
// names: the values reach the scan only in its environment, and it prints
// the counts alone. The stream is written to its standard input.
func (s backupScanner) sealedNames(ctx context.Context, stream func(io.Writer) error) (names, matched int, err error) {
	if s.bin == "" || s.store == "" || s.as == "" || s.key == "" || s.sops == "" {
		return 0, 0, errors.New("the scan wants the nova-secrets seat: --secrets-store, --secrets-as, --secrets-key and --sops, or a login recorded by nova-sprint seat login")
	}
	seat := []string{"--store", s.store, "--as", s.as}
	out, err := subproc.Context(ctx, s.bin, append([]string{"names"}, append(seat, "--max", "0")...)...).Output()
	if err != nil {
		return 0, 0, fmt.Errorf("nova-secrets names failed: %v", err)
	}
	var sealed []string
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) == 4 && f[0] == "SECRETS" && f[1] == "NAME" && f[3] == "clear=false" {
			sealed = append(sealed, strings.TrimPrefix(f[2], "key="))
		}
	}
	if len(sealed) == 0 {
		return 0, 0, fmt.Errorf("the seat %s holds no sealed value, so a scan would prove nothing; run: nova-secrets names --store %s --as %s", s.as, s.store, s.as)
	}
	only := strings.Join(sealed, ",")
	args := append([]string{"exec"}, append(seat, "--key", s.key, "--sops", s.sops, "--only", only, "--", s.self, "backup", "--scan", only)...)
	c := subproc.Context(ctx, s.bin, args...)
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	in, err := c.StdinPipe()
	if err != nil {
		return 0, 0, err
	}
	if err := c.Start(); err != nil {
		return 0, 0, err
	}
	werr := stream(in)
	if cerr := in.Close(); werr == nil {
		werr = cerr
	}
	runErr := c.Wait()
	if runErr != nil {
		return 0, 0, fmt.Errorf("nova-secrets exec -- nova-sprint backup --scan failed: %v: %s", runErr, finalLine(stderr.String()))
	}
	if werr != nil {
		return 0, 0, fmt.Errorf("the dump could not be handed to the scan: %v", werr)
	}
	var n, present, m int
	if _, err := fmt.Sscanf(finalLine(stdout.String()), "BACKUP SCAN names=%d present=%d matched=%d", &n, &present, &m); err != nil {
		return 0, 0, fmt.Errorf("the scan answered no counts line: %v", err)
	}
	if n != len(sealed) || present != n {
		return 0, 0, fmt.Errorf("the scan was handed %d of the %d sealed values; run: nova-secrets check --store %s --as %s --key %s --sops %s", present, len(sealed), s.store, s.as, s.key, s.sops)
	}
	return n, m, nil
}

func finalLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// backupScan is backup --scan, the child nova-secrets exec becomes: it reads
// the stream on stdin and counts the values of the named variables it holds,
// each value once however often it is found. It prints the counts alone,
// never a value and never where one was found.
func backupScan(names []string, getenv func(string) string, stdin io.Reader, stdout io.Writer) error {
	var values [][]byte
	for _, n := range names {
		if v := getenv(n); v != "" {
			values = append(values, []byte(v))
		}
	}
	found := make([]bool, len(values))
	longest := 0
	for _, v := range values {
		longest = max(longest, len(v))
	}
	// a match may cross the edge of a read: the last longest-1 bytes are kept
	// and read again with the next
	var tail []byte
	buf := make([]byte, 1<<20)
	for {
		n, err := stdin.Read(buf)
		if n > 0 {
			window := append(tail, buf[:n]...)
			for i, v := range values {
				if !found[i] && bytes.Contains(window, v) {
					found[i] = true
				}
			}
			keep := min(len(window), max(longest-1, 0))
			tail = append([]byte(nil), window[len(window)-keep:]...)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	matched := 0
	for _, f := range found {
		if f {
			matched++
		}
	}
	_, err := fmt.Fprintf(stdout, "BACKUP SCAN names=%d present=%d matched=%d\n", len(names), len(values), matched)
	return err
}

// memBackup is a twin store as a backup's source.
type memBackup struct{ st *store.Store }

func (m memBackup) Take(ctx context.Context) (uint64, []store.DumpKey, map[string]int, error) {
	es, err := m.st.EpochNow(ctx)
	if err != nil {
		return 0, nil, nil, err
	}
	mem, ok := m.st.B.(*store.Mem)
	if !ok {
		return 0, nil, nil, errors.New("not a twin store")
	}
	keys, err := store.MemDump(mem, es.N)
	if err != nil {
		return 0, nil, nil, err
	}
	cols, err := store.CardsByColumn(ctx, m.st.B, m.st.Names)
	return es.N, keys, cols, err
}

// memTwin is a throwaway twin store a twin's dump is restored into.
type memTwin struct {
	m     *store.Mem
	epoch uint64
	names sprint.Names
}

func (t *memTwin) Restore(ctx context.Context, keys []store.DumpKey) (store.BackupCounts, error) {
	m, err := store.MemRestore(keys)
	if err != nil {
		return store.BackupCounts{}, err
	}
	es, err := m.Epoch(ctx)
	if err != nil {
		return store.BackupCounts{}, err
	}
	t.m, t.epoch = m, es.N
	again, err := store.MemDump(m, es.N)
	if err != nil {
		return store.BackupCounts{}, err
	}
	cols, err := store.CardsByColumn(ctx, m, t.names)
	return store.BackupCounts{Keys: len(again), Columns: cols}, err
}

func (m memBackup) State(ctx context.Context) (store.SprintState, error) {
	return store.ReadState(ctx, m.st.B, m.st.Names)
}

func (t *memTwin) State(ctx context.Context) (store.SprintState, error) {
	if t.m == nil {
		return store.SprintState{}, errors.New("the twin holds no restored sprint")
	}
	return store.ReadState(ctx, t.m, t.names)
}

func (t *memTwin) Values(_ context.Context, w io.Writer) error {
	keys, err := store.MemDump(t.m, t.epoch)
	if err != nil {
		return err
	}
	for _, k := range keys {
		if _, err := io.WriteString(w, k.Key+"\n"); err != nil {
			return err
		}
		var v any
		if err := json.Unmarshal(k.Payload, &v); err != nil {
			return err
		}
		if err := writeStrings(w, v); err != nil {
			return err
		}
	}
	return nil
}

// writeStrings writes every string of a JSON value, the names of its fields
// too, one a line, as they are and not as JSON escapes them.
func writeStrings(w io.Writer, v any) error {
	switch x := v.(type) {
	case string:
		_, err := io.WriteString(w, x+"\n")
		return err
	case []any:
		for _, e := range x {
			if err := writeStrings(w, e); err != nil {
				return err
			}
		}
	case map[string]any:
		for k, e := range x {
			if _, err := io.WriteString(w, k+"\n"); err != nil {
				return err
			}
			if err := writeStrings(w, e); err != nil {
				return err
			}
		}
	}
	return nil
}

func (t *memTwin) Library() string { return "twin" }
func (t *memTwin) Close()          {}

// redisBackup is a Redis store as a backup's source: SCAN for the sprint's
// keys (store.SprintKey) of its epoch, the shared ones and the records of
// every epoch (store.BackupKey), then DUMP and PTTL of each, a pipeline a
// batch.
type redisBackup struct {
	b     *store.Redis
	names sprint.Names
}

func (r redisBackup) Take(ctx context.Context) (uint64, []store.DumpKey, map[string]int, error) {
	es, err := r.b.Epoch(ctx)
	if err != nil {
		return 0, nil, nil, err
	}
	var names []string
	it := r.b.C.Scan(ctx, 0, "*", 1000).Iterator()
	for it.Next(ctx) {
		if k := it.Val(); store.BackupKey(r.names, k, es.N) {
			names = append(names, k)
		}
	}
	if err := it.Err(); err != nil {
		return 0, nil, nil, err
	}
	sort.Strings(names)
	names = compactStrings(names)
	keys, err := dumpKeys(ctx, r.b.C, names)
	if err != nil {
		return 0, nil, nil, err
	}
	cols, err := store.CardsByColumn(ctx, r.b, r.names)
	return es.N, keys, cols, err
}

func compactStrings(s []string) []string {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// State is the Redis backup source's logical sprint at the save (store.ReadState).
func (r redisBackup) State(ctx context.Context) (store.SprintState, error) {
	return store.ReadState(ctx, r.b, r.names)
}

// dumpKeys is DUMP and PTTL of each key, a pipeline of 500; a key gone
// between the SCAN and its DUMP is left out (the counts then differ, and the
// backup fails as a sprint that moved while it was read).
func dumpKeys(ctx context.Context, c redis.UniversalClient, names []string) ([]store.DumpKey, error) {
	var out []store.DumpKey
	for i := 0; i < len(names); i += 500 {
		batch := names[i:min(i+500, len(names))]
		p := c.Pipeline()
		dumps := make([]*redis.StringCmd, len(batch))
		ttls := make([]*redis.DurationCmd, len(batch))
		for j, k := range batch {
			dumps[j], ttls[j] = p.Dump(ctx, k), p.PTTL(ctx, k)
		}
		if _, err := p.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
		for j, k := range batch {
			payload, err := dumps[j].Bytes()
			if errors.Is(err, redis.Nil) {
				continue
			}
			if err != nil {
				return nil, err
			}
			ttl := int64(0)
			if d := ttls[j].Val(); d > 0 {
				ttl = d.Milliseconds()
			}
			out = append(out, store.DumpKey{Key: k, TTL: ttl, Payload: payload})
		}
	}
	return out, nil
}

// serverTwin is a throwaway redis-server on a unix socket in the work
// directory, saving nothing, holding this build's function library.
type serverTwin struct {
	cmd     *exec.Cmd
	c       *redis.Client
	names   sprint.Names
	lib     string
	sockDir string // a short private directory made for the socket, removed on Close; "" when the socket is in work
}

// twinSocketMax is below the shortest sockaddr_un path of the platforms this runs
// on (104 bytes on macOS, 108 on Linux, one for the terminator), as
// internal/tablemodel's maxSocketPath.
const twinSocketMax = 100

// twinSocket is where the throwaway's socket goes: in work when the path fits a
// Unix socket, else in a fresh private directory (0700) under the temp
// directory, else under /tmp; dir is that directory, for Close to remove, or ""
// when the socket is in work. A work directory deep under a long TMPDIR (a CI
// runner's) cannot hold a socket: redis-server refuses to bind it, and the
// twin never answers.
func twinSocket(work string) (sock, dir string, err error) {
	if sock = filepath.Join(work, "twin.sock"); len(sock) <= twinSocketMax {
		return sock, "", nil
	}
	for _, root := range []string{os.TempDir(), "/tmp"} {
		if len(filepath.Join(root, "nsbXXXXXXXXXX", "twin.sock")) > twinSocketMax {
			continue
		}
		if dir, err = os.MkdirTemp(root, "nsb"); err != nil {
			continue
		}
		return filepath.Join(dir, "twin.sock"), dir, nil
	}
	return "", "", fmt.Errorf("the throwaway redis-server's socket %s is %d bytes and a Unix socket path holds at most %d, and no shorter private directory could be made (%v); set TMPDIR to a shorter directory", sock, len(sock), twinSocketMax, err)
}

func startServerTwin(ctx context.Context, bin, work string, names sprint.Names) (*serverTwin, error) {
	sock, sockDir, err := twinSocket(work)
	if err != nil {
		return nil, err
	}
	cmd := subproc.Context(ctx, bin, "--port", "0", "--unixsocket", sock, "--unixsocketperm", "700", "--save", "", "--appendonly", "no", "--dir", work)
	if err := cmd.Start(); err != nil {
		if sockDir != "" {
			_ = os.Remove(sockDir) // ignored: the empty directory this call made; Remove takes nothing else
		}
		return nil, fmt.Errorf("%s does not start: %v; run: nova-sprint backup --redis-server <the path of redis-server>", bin, err)
	}
	t := &serverTwin{cmd: cmd, names: names, sockDir: sockDir, c: redis.NewClient(&redis.Options{Network: "unix", Addr: sock})}
	for i := 0; ; i++ {
		if err := t.c.Ping(ctx).Err(); err == nil {
			break
		} else if i == 100 {
			t.Close()
			return nil, fmt.Errorf("the throwaway redis-server did not answer in 10s: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := fn.Load(ctx, t.c); err != nil {
		t.Close()
		return nil, err
	}
	src, err := fn.Source()
	if err != nil {
		t.Close()
		return nil, err
	}
	t.lib = "redis(" + fn.Library + " " + fn.Sum(src) + ")"
	return t, nil
}

func (t *serverTwin) Restore(ctx context.Context, keys []store.DumpKey) (store.BackupCounts, error) {
	for i := 0; i < len(keys); i += 500 {
		p := t.c.Pipeline()
		for _, k := range keys[i:min(i+500, len(keys))] {
			p.Restore(ctx, k.Key, time.Duration(k.TTL)*time.Millisecond, string(k.Payload))
		}
		if _, err := p.Exec(ctx); err != nil {
			return store.BackupCounts{}, err
		}
	}
	n, err := t.c.DBSize(ctx).Result()
	if err != nil {
		return store.BackupCounts{}, err
	}
	if err := libraryMatches(ctx, t.c, "the throwaway store"); err != nil {
		return store.BackupCounts{}, err
	}
	cols, err := store.CardsByColumn(ctx, &store.Redis{C: t.c, Names: t.names}, t.names)
	return store.BackupCounts{Keys: int(n), Columns: cols}, err
}

// State reads the restored Redis sprint through this build's function library.
func (t *serverTwin) State(ctx context.Context) (store.SprintState, error) {
	return store.ReadState(ctx, &store.Redis{C: t.c, Names: t.names, Now: time.Now}, t.names)
}

func (t *serverTwin) Values(ctx context.Context, w io.Writer) error {
	it := t.c.Scan(ctx, 0, "*", 1000).Iterator()
	for it.Next(ctx) {
		k := it.Val()
		var vals []string
		switch typ, err := t.c.Type(ctx, k).Result(); {
		case err != nil:
			return err
		case typ == "string":
			v, err := t.c.Get(ctx, k).Result()
			if err != nil {
				return err
			}
			vals = []string{v}
		case typ == "hash":
			m, err := t.c.HGetAll(ctx, k).Result()
			if err != nil {
				return err
			}
			for f, v := range m {
				vals = append(vals, f, v)
			}
		case typ == "set":
			if vals, err = t.c.SMembers(ctx, k).Result(); err != nil {
				return err
			}
		case typ == "zset":
			if vals, err = t.c.ZRange(ctx, k, 0, -1).Result(); err != nil {
				return err
			}
		case typ == "list":
			if vals, err = t.c.LRange(ctx, k, 0, -1).Result(); err != nil {
				return err
			}
		case typ == "stream":
			msgs, err := t.c.XRange(ctx, k, "-", "+").Result()
			if err != nil {
				return err
			}
			for _, m := range msgs {
				for f, v := range m.Values {
					vals = append(vals, f, fmt.Sprint(v))
				}
			}
		default:
			return fmt.Errorf("a key of type %s the scan cannot read", typ)
		}
		if _, err := io.WriteString(w, k+"\n"+strings.Join(vals, "\n")+"\n"); err != nil {
			return err
		}
	}
	return it.Err()
}

func (t *serverTwin) Library() string { return t.lib }

func (t *serverTwin) Close() {
	_ = t.c.Close()          // ignored: the throwaway's client; the server goes next
	_ = t.cmd.Process.Kill() // ignored: the throwaway saves nothing, and an exited one is already gone
	_ = t.cmd.Wait()         // ignored: reaping the throwaway; its exit is the kill's
	if t.sockDir != "" {
		_ = os.Remove(filepath.Join(t.sockDir, "twin.sock")) // ignored: the socket, gone if the server removed it
		_ = os.Remove(t.sockDir)                             // ignored: the directory made for it, empty; Remove takes nothing else
	}
}
