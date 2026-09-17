package pulse

import (
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
// The columns are `job  mtime  class  tokens` and then the usage rows the two status
// readers fold -- five fields per row, `started  ended  rc  usd  tokens`. `job` is the
// usage.tsv path relative to the root and `mtime` the job directory's mtime in UnixNano.
// status refreshes one job only when that directory's mtime moved; run and harvest append
// a finished job's rows as they fold it, so a new job is in the index before the next tick
// reads it. A root with no index is walked once and the index written, and a root with an
// index opens no job file at all.
const statusIndexName = "status-index.tsv"

// indexEntry is one job's cached usage rows and the directory mtime that says they hold.
type indexEntry struct {
	mtime int64
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

// refreshStatusIndex answers one root's usage files from its index, stat-ing each indexed
// job directory and re-reading only the jobs whose mtime moved. A root without an index is
// walked once and the index written; a job directory that vanished is dropped. The index is
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
			fi, err := os.Stat(filepath.Dir(abs))
			if err != nil {
				delete(entries, rel)
				changed = true
				continue
			}
			if fi.ModTime().UnixNano() == entries[rel].mtime {
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

// readIndexEntry parses one job's usage.tsv into its rows, carrying the job directory's
// mtime. A file with no measured rows still gets an entry so the mtime is remembered and
// the job is not re-opened every tick; the readers skip the rows they cannot use.
func readIndexEntry(path string) indexEntry {
	atomic.AddInt64(&statusIndexReads, 1)
	e := indexEntry{}
	fi, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return e
	}
	e.mtime = fi.ModTime().UnixNano()
	raw, err := os.ReadFile(path)
	if err != nil {
		return e
	}
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
		mtime, _ := strconv.ParseInt(f[1], 10, 64)
		e := indexEntry{mtime: mtime}
		for i := 4; i+4 < len(f); i += 5 {
			e.rows = append(e.rows, indexRow{started: f[i], ended: f[i+1], rc: f[i+2], usd: f[i+3], tokens: f[i+4]})
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
	b.WriteString("job\tmtime\tclass\ttokens\tstarted\tended\trc\tusd\ttokens\n")
	for _, rel := range names {
		e := entries[rel]
		class, tokens := entrySummary(e.rows)
		b.WriteString(rel)
		b.WriteByte('\t')
		b.WriteString(strconv.FormatInt(e.mtime, 10))
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
