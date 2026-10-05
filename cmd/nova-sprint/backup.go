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
	"regexp"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func init() {
	// backup is the schedule of the store's host, as snapshot is: it needs no
	// actor and reads and writes no card (coordinator.go).
	verbClasses["backup"] = classMachine
	notServed = append(notServed, "backup")
}

// secretPatterns are the shapes of a credential a store file must not hold
// (docs/SPEC-SPRINT.md, backup-verb); a hit names the pattern and the line,
// never the value.
var secretPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"private key block", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"AWS access key id", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"GitHub token", regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{30,})\b`)},
	{"API key (sk-)", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`)},
	{"Slack token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`)},
	{"bearer token", regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{20,}`)},
	{"password assignment", regexp.MustCompile(`(?i)\b(?:password|passwd|secret|api[_-]?key|token)\b["']?\s*[:=]\s*["']?[^\s"',;]{8,}`)},
}

// secretFinding is one credential-shaped place in a backup file.
type secretFinding struct {
	Pattern string `json:"pattern"`
	Line    int    `json:"line"`
}

// scanSecrets is every place doc matches a secret pattern, by pattern and
// line, in line order; it never returns the matched text.
func scanSecrets(doc []byte) []secretFinding {
	var out []secretFinding
	for _, p := range secretPatterns {
		for _, loc := range p.re.FindAllIndex(doc, -1) {
			out = append(out, secretFinding{p.name, 1 + bytes.Count(doc[:loc[0]], []byte("\n"))})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out
}

// backupResult is one verified backup.
type backupResult struct {
	File    string               `json:"file"`
	SHA256  string               `json:"sha256"`
	Bytes   int                  `json:"bytes"`
	Counts  store.SnapshotCounts `json:"counts"`
	Verify  string               `json:"verified"`
	Secrets string               `json:"secrets"`
}

// cmdBackup is the sprint's backup as one verb, the procedure a child ran by
// hand (docs/SPEC-SPRINT.md, backup-verb): the store is written to --file, the
// file is read back and checked against its checksum, restored into a twin and
// compared with the store, and scanned for secrets. A file that fails a check
// is removed, so a file at --file is a verified one.
func (a *app) cmdBackup(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("backup")
	file := fs.String("file", "", "the file the store is written to (required)")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "backup", argErr("takes no words ", err, pos...))
	}
	if *file == "" {
		return refuse(stderr, "backup", "wants --file <path>; run: nova-sprint backup --file sprint.backup")
	}
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
		return refuse(stderr, "backup", "this store has no backup; run: nova-sprint backup --redis <a Redis address> --file "+*file)
	}
	got, err := backupTake(context.Background(), *file, src, twin)
	if err != nil {
		fmt.Fprintf(stderr, "%s backup FAILED: %s\n", prog, err)
		return 1
	}
	if c.json {
		b, _ := json.Marshal(got)
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprintf(stdout, "BACKUP OK file=%s sha256=%s bytes=%d %s verified=%s secrets=%s\n", got.File, got.SHA256, got.Bytes, countsText(got.Counts), got.Verify, got.Secrets)
	return 0
}

// backupTake writes src's document to file, then verifies it: read back equal
// to the checksum, loaded into the twin with the counts the store held, and
// free of secrets. Any failure removes the file and says what failed.
func backupTake(ctx context.Context, file string, src store.SnapshotSource, twin store.SnapshotTwin) (backupResult, error) {
	var out backupResult
	doc, live, err := src.Save(ctx)
	if err != nil {
		return out, fmt.Errorf("the store gave no backup: %w", err)
	}
	sum := sha256.Sum256(doc)
	if err := writeAtomic(file, doc); err != nil {
		return out, fmt.Errorf("%s cannot be written: %v; run: nova-sprint backup --file <a writable path>", file, err)
	}
	fail := func(format string, a ...any) (backupResult, error) {
		_ = os.Remove(file) // ignored: removing the failed backup; the failure is what is returned
		return out, fmt.Errorf(format, a...)
	}
	copied, err := os.ReadFile(file)
	if err != nil {
		return fail("the backup %s cannot be read back: %v", file, err)
	}
	if sha256.Sum256(copied) != sum {
		return fail("the backup %s does not match its checksum on reading it back; the file was removed", file)
	}
	got, err := twin.Load(copied)
	if err != nil {
		return fail("the backup %s does not restore into a twin: %v; the file was removed", file, err)
	}
	if why := countsMismatch(live, got); why != "" {
		return fail("the backup %s restored with other counts than the store held (%s); the file was removed", file, why)
	}
	if _, ok := twin.(store.MemTwin); ok {
		if err := memRoundTrip(copied); err != nil {
			return fail("the backup %s restored into a twin that differs from it: %v; the file was removed", file, err)
		}
	}
	if found := scanSecrets(copied); len(found) > 0 {
		var where []string
		for _, f := range found {
			where = append(where, fmt.Sprintf("%s at line %d", f.Pattern, f.Line))
		}
		return fail("the backup %s holds what looks like a secret (%s); the file was removed; find the row that holds it, remove it from the store, and run the backup again", file, strings.Join(where, "; "))
	}
	verify := "checksum+twin"
	if _, ok := twin.(store.MemTwin); ok {
		verify = "checksum+twin+compare"
	}
	return backupResult{File: file, SHA256: hex.EncodeToString(sum[:]), Bytes: len(doc), Counts: got, Verify: verify, Secrets: "none"}, nil
}

// memCanon is doc restored into a fresh twin and the twin's own document: the
// form a twin gives (an empty field is written the one way).
func memCanon(doc []byte) ([]byte, error) {
	m := store.NewMem()
	if err := m.Restore(doc); err != nil {
		return nil, err
	}
	return m.Snapshot()
}

// memRoundTrip restores doc into a twin and restores the twin's document into
// another: the two documents must be the same bytes, so what the file holds
// comes back whole and a second restore adds or loses nothing.
func memRoundTrip(doc []byte) error {
	once, err := memCanon(doc)
	if err != nil {
		return err
	}
	twice, err := memCanon(once)
	if err != nil {
		return err
	}
	if !bytes.Equal(once, twice) {
		return fmt.Errorf("a second restore is %d bytes and the first %d", len(twice), len(once))
	}
	return nil
}

// countsMismatch is how the twin's counts differ from the store's; a count of
// -1 is unknown and is not compared.
func countsMismatch(want, got store.SnapshotCounts) string {
	var why []string
	if want.Keys >= 0 && got.Keys >= 0 && want.Keys != got.Keys {
		why = append(why, fmt.Sprintf("keys %d, twin %d", want.Keys, got.Keys))
	}
	if want.Cards >= 0 && got.Cards >= 0 && want.Cards != got.Cards {
		why = append(why, fmt.Sprintf("cards %d, twin %d", want.Cards, got.Cards))
	}
	return strings.Join(why, ", ")
}
