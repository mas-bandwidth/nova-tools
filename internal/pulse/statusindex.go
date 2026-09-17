package pulse

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// statusIndexReads counts the job files the index has opened. It is a test hook for
// #1088: the fast suite asserts a warm tick reads the index and opens no job file.
var statusIndexReads int64

// statusIndexName is the per-root cache status reads instead of opening every job's
// usage.tsv on every tick (#1088). It lives beside the root it describes, so
// `<root>/status-index.tsv` travels with the root and a fresh process pays no warmup.
//
// The columns are `job  key  class  tokens` and then the usage rows the two status readers
// fold -- five fields per row, `started  ended  rc  usd  tokens`. `job` is the usage.tsv
// path relative to the root and `key` is the mtime+size of the three files a job writes as
// it finishes: usage.tsv, its harness store data/opencode/opencode.db and RESULT.md. A job
// is refreshed only when one of those tuples moves, never when any other file in its
// directory -- the
// harness log, the store's own wal -- does; run and harvest append a finished job's rows as
// they fold it, so a new job is in the index before the next tick reads it. A root with no
// index is walked once and the index written, and a root with an index opens no job file at
// all. `class` is RESULT.md's verdict, not the usage row's return code.
const statusIndexName = "status-index.tsv"

// indexEntry is one job's cached class, usage rows and the key that says they hold.
type indexEntry struct {
	key   string
	class string
	rows  []indexRow
}

// indexRow is one usage.tsv data row kept verbatim: the raw cells are parsed again on
// load, which is cheaper than opening the job's file and reading it.
type indexRow struct {
	started string // "started" cell, RFC3339
	ended   string // "ended" cell, RFC3339
	rc      string // "rc" cell
	usd     string // "usd" cell, "-" is an unmeasured cost and not a zero
	tokens  string // tokens_in + tokens_out, "-" when neither was written
}

// usage reduces one index row the way parseUsage did, reporting false when the row is not
// a measured row (a started or ended stamp the file did not carry).
func (r indexRow) usage() (usageRow, bool) {
	st, errS := time.Parse(time.RFC3339, r.started)
	en, errE := time.Parse(time.RFC3339, r.ended)
	if errS != nil || errE != nil {
		return usageRow{}, false
	}
	rc, _ := strconv.Atoi(r.rc)
	usd := 0.0
	if r.usd != "-" {
		usd, _ = strconv.ParseFloat(r.usd, 64)
	}
	return usageRow{started: st, ended: en, hasTime: true, rc: rc, usd: usd,
		wall: en.Sub(st).Seconds()}, true
}

// usageFile is one usage.tsv under a root as the per-root index remembers it.
type usageFile struct {
	path string
	rows []indexRow
}

// first is the file's first measured row, the row status's rate arithmetic reads.
func (f usageFile) first() (usageRow, bool) {
	for _, r := range f.rows {
		if u, ok := r.usage(); ok {
			return u, true
		}
	}
	return usageRow{}, false
}

// loadUsageFiles returns every root's usage rows through the status index, refreshing only
// the jobs whose directory mtime moved.
func loadUsageFiles(roots []string) []usageFile {
	var out []usageFile
	for _, root := range roots {
		out = append(out, refreshStatusIndex(root)...)
	}
	return out
}

// refreshStatusIndex answers one root's usage files from its index, re-reading only the
// jobs whose usage.tsv, harness store or RESULT.md moved. A root without an index is walked
// once and the index written; a job whose usage.tsv vanished is dropped. The index is
// rewritten only when something moved. New jobs are appended by run and harvest as they
// finish, so this never re-walks a root the first call already built.
func refreshStatusIndex(root string) []usageFile {
	entries, haveIndex := readStatusIndex(root)
	changed := false
	if !haveIndex {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || d.Name() != "usage.tsv" {
				return nil
			}
			if rel, ok := indexRelative(root, path); ok {
				entries[rel] = readIndexEntry(path)
				changed = true
			}
			return nil
		})
	} else {
		for rel := range entries {
			abs := filepath.Join(root, rel)
			if _, err := os.Stat(abs); err != nil {
				delete(entries, rel)
				changed = true
				continue
			}
			if indexKey(abs) == entries[rel].key {
				continue
			}
			entries[rel] = readIndexEntry(abs)
			changed = true
		}
	}
	if changed {
		writeStatusIndex(root, entries)
	}
	files := make([]usageFile, 0, len(entries))
	for rel, e := range entries {
		files = append(files, usageFile{path: filepath.Join(root, rel), rows: e.rows})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	return files
}

// appendStatusIndex refreshes a set of finished jobs in one root's index in a single pass,
// so run and harvest pay one index write for a tick's harvest, not one per card.
func appendStatusIndex(root string, jobDirs []string) {
	if len(jobDirs) == 0 {
		return
	}
	// A root with no index is built whole before the append: seeding the index with only
	// the finished job would hide every other job from status's stat-only refresh.
	if _, have := readStatusIndex(root); !have {
		refreshStatusIndex(root)
	}
	entries, _ := readStatusIndex(root)
	for _, dir := range jobDirs {
		usage := filepath.Join(dir, "usage.tsv")
		rel, ok := indexRelative(root, usage)
		if !ok {
			continue
		}
		if _, err := os.Stat(usage); err != nil {
			continue
		}
		entries[rel] = readIndexEntry(usage)
	}
	writeStatusIndex(root, entries)
}

// indexRelative is a path relative to its root, false when it is outside the root.
func indexRelative(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return rel, true
}

// statusHarnessDB is the harness store a job keeps under its own data home, beside
// usage.tsv and RESULT.md. SPEC-SWARM's readers name the same store (rule 13).
const statusHarnessDB = "data/opencode/opencode.db"

// indexKey is the job's cache key: the mtime and size of the three files the job writes as
// it finishes -- usage.tsv, its harness store data/opencode/opencode.db and RESULT.md. A
// file that is not there is 0,0. The pair, not the mtime alone, makes the key content-stable
// on APFS: a file rewritten in the same clock tick still moves the key through its size, and
// a tuple that did not move at all is unchanged, so a finished job is never re-opened for
// the harness log or the store's wal (#1088).
func indexKey(usagePath string) string {
	dir := filepath.Dir(usagePath)
	m1, s1 := fileStamp(usagePath)
	m2, s2 := fileStamp(filepath.Join(dir, filepath.FromSlash(statusHarnessDB)))
	m3, s3 := fileStamp(filepath.Join(dir, "RESULT.md"))
	return fmt.Sprintf("%d,%d,%d,%d,%d,%d", m1, s1, m2, s2, m3, s3)
}

// fileStamp is one file's mtime in UnixNano and its size, 0,0 when it is not there.
func fileStamp(path string) (mtime, size int64) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, 0
	}
	return fi.ModTime().UnixNano(), fi.Size()
}

// resultClass is the class RESULT.md states -- abstain, blocked or done -- falling back to
// the usage row's own return code when the job wrote no RESULT.md. The class is cached with
// the rows so status never opens RESULT.md or the usage file twice for one finished job.
func resultClass(jobDir string, rows []indexRow) string {
	raw, err := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
	if err == nil {
		lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
		if len(lines) > 1 {
			switch line2 := strings.TrimSpace(lines[1]); {
			case strings.HasPrefix(line2, "ABSTAIN"):
				return "abstain"
			case strings.HasPrefix(line2, "BLOCKED"):
				return "blocked"
			case strings.HasPrefix(line2, "DONE"):
				return "done"
			}
		}
	}
	class, _ := entrySummary(rows)
	return class
}

// readIndexEntry parses one job's usage.tsv into its rows, carrying the cache key and
// RESULT.md's class. A file with no measured rows still gets an entry so its key is
// remembered and the job is not re-opened every tick; the readers skip the rows they cannot
// use.
func readIndexEntry(path string) indexEntry {
	atomic.AddInt64(&statusIndexReads, 1)
	e := indexEntry{key: indexKey(path)}
	raw, err := os.ReadFile(path)
	if err == nil {
		for _, l := range strings.Split(string(raw), "\n") {
			if l == "" || strings.HasPrefix(l, "job") {
				continue
			}
			f := strings.Split(l, "\t")
			e.rows = append(e.rows, indexRow{
				started: cell(f, 2),
				ended:   cell(f, 3),
				rc:      cell(f, 4),
				usd:     cell(f, 12),
				tokens:  tokensCell(f),
			})
		}
	}
	e.class = resultClass(filepath.Dir(path), e.rows)
	return e
}

// cell is field i of a usage row, "" when the row is short.
func cell(f []string, i int) string {
	if i >= len(f) {
		return ""
	}
	return f[i]
}

// tokensCell is tokens_in + tokens_out for one row, "-" when neither is a number.
func tokensCell(f []string) string {
	in, errIn := strconv.Atoi(strings.TrimSpace(cell(f, 7)))
	out, errOut := strconv.Atoi(strings.TrimSpace(cell(f, 8)))
	if errIn != nil && errOut != nil {
		return "-"
	}
	return strconv.Itoa(in + out)
}

// readStatusIndex loads a root's index; haveIndex is false when the file is not there yet,
// which is status's cue to build it once by walking.
func readStatusIndex(root string) (map[string]indexEntry, bool) {
	entries := map[string]indexEntry{}
	raw, err := os.ReadFile(filepath.Join(root, statusIndexName))
	if err != nil {
		return entries, false
	}
	for _, l := range strings.Split(string(raw), "\n") {
		if l == "" || strings.HasPrefix(l, "job\t") {
			continue
		}
		f := strings.Split(l, "\t")
		if len(f) < 4 {
			continue
		}
		e := indexEntry{key: f[1], class: f[2]}
		for i := 4; i+4 < len(f); i += 5 {
			e.rows = append(e.rows, indexRow{started: f[i], ended: f[i+1], rc: f[i+2], usd: f[i+3], tokens: f[i+4]})
		}
		if e.class == "" {
			// A reader that could not touch the store leaves the row's numbers unknown,
			// never the class: an index line always names a class, so a missing sqlite3
			// yields the usage row's own verdict rather than an empty field.
			e.class = entrySummaryClass(e.rows)
		}
		entries[f[0]] = e
	}
	return entries, true
}

// writeStatusIndex writes the index atomically, one line per job. class and tokens are the
// readable summary of the job's rows; the readers fold the rows themselves.
func writeStatusIndex(root string, entries map[string]indexEntry) {
	names := make([]string, 0, len(entries))
	for rel := range entries {
		names = append(names, rel)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("job\tkey\tclass\ttokens\tstarted\tended\trc\tusd\ttokens\n")
	for _, rel := range names {
		e := entries[rel]
		_, tokens := entrySummary(e.rows)
		class := e.class
		if class == "" {
			class = entrySummaryClass(e.rows)
		}
		b.WriteString(rel)
		b.WriteByte('\t')
		b.WriteString(e.key)
		b.WriteByte('\t')
		b.WriteString(class)
		b.WriteByte('\t')
		b.WriteString(tokens)
		for _, r := range e.rows {
			b.WriteByte('\t')
			b.WriteString(r.started)
			b.WriteByte('\t')
			b.WriteString(r.ended)
			b.WriteByte('\t')
			b.WriteString(r.rc)
			b.WriteByte('\t')
			b.WriteString(r.usd)
			b.WriteByte('\t')
			b.WriteString(r.tokens)
		}
		b.WriteByte('\n')
	}
	path := filepath.Join(root, statusIndexName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// entrySummaryClass is the usage-row class alone: done when the first row's rc is 0, else
// failed. It is the fallback for a job that wrote no RESULT.md.
func entrySummaryClass(rows []indexRow) string {
	class, _ := entrySummary(rows)
	return class
}

// entrySummary is the class and token count the index carries per job: done when the first
// row's rc is 0, else failed, and the sum of the numeric token cells the rows carried. A
// count nobody wrote is "-", never a zero.
func entrySummary(rows []indexRow) (class, tokens string) {
	class = "done"
	total, known := 0, false
	for i, r := range rows {
		if i == 0 {
			if rc, err := strconv.Atoi(r.rc); err == nil && rc != 0 {
				class = "failed"
			}
		}
		if r.tokens == "-" {
			continue
		}
		if n, err := strconv.Atoi(r.tokens); err == nil {
			total += n
			known = true
		}
	}
	if !known {
		return class, "-"
	}
	return class, strconv.Itoa(total)
}
