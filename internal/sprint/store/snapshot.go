package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc64"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
)

// A verified snapshot of the store (docs/SPEC-SPRINT.md, store-snapshot-verb):
// the store is asked for a snapshot (BGSAVE, waited for), the RDB is copied
// into a directory with its SHA-256 beside it, the copy is loaded into a twin
// and its counts compared with the store's at the save, and, when the source
// can say its sprint state and the twin can load the dump's (RestoreLevel), the
// two states compared part for part (SemanticRestore); the copies beyond the
// keep count are pruned. A snapshot that fails any check is removed and the
// older ones stay: the directory holds only verified snapshots, each with the
// level it was verified at. A restore drill checks a file's integrity and
// reports its counts; it never opens the live store, has no source to compare
// with, and is never a semantic restore.

// SnapshotCounts is what a snapshot holds that a twin can count. A count of -1
// is unknown (the loader or the source cannot say) and is not compared.
type SnapshotCounts struct {
	Keys  int `json:"keys"`
	Cards int `json:"cards"`
}

// SnapshotSource is the store asked for a snapshot.
type SnapshotSource interface {
	// Save has the store write a snapshot, waits until it is written, and
	// returns the RDB bytes with the counts the store held at the save.
	Save(ctx context.Context) ([]byte, SnapshotCounts, error)
}

// SnapshotTwin loads an RDB into a throwaway store and counts it.
type SnapshotTwin interface {
	Load(rdb []byte) (SnapshotCounts, error)
}

// Snapshotter takes, verifies and prunes the snapshots in Dir.
type Snapshotter struct {
	Dir    string
	Keep   int
	Source SnapshotSource
	Twin   SnapshotTwin
	Now    func() time.Time
}

// SnapshotTaken is one verified snapshot.
type SnapshotTaken struct {
	File   string         `json:"file"`
	SHA256 string         `json:"sha256"`
	Bytes  int            `json:"bytes"`
	Counts SnapshotCounts `json:"counts"`
	// Restore is the level the copy was verified at: RestoreSemantic, its
	// sprint state compared with the store's, or RestoreIntegrity, the file
	// alone (the RDB's header, version and checksum).
	Restore string   `json:"restore"`
	Pruned  []string `json:"pruned,omitempty"`
}

const (
	snapshotPrefix = "snapshot-"
	snapshotSuffix = ".rdb"
	sumSuffix      = ".sha256"
)

// Take is one snapshot: saved, written with its SHA-256, loaded into a twin,
// counts compared, then the directory pruned to Keep (the model's keep law:
// the newest Keep verified snapshots stay, and a snapshot is pruned only after
// a newer one has verified).
func (s *Snapshotter) Take(ctx context.Context) (SnapshotTaken, error) {
	var out SnapshotTaken
	if s.Keep < 1 {
		return out, errors.New("--keep must be at least 1; run: nova-sprint snapshot --dir <dir> --keep 7")
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return out, fmt.Errorf("the snapshot directory %s cannot be made: %v; run: nova-sprint snapshot --dir <a writable directory>", s.Dir, err)
	}
	rdb, live, err := s.Source.Save(ctx)
	if err != nil {
		return out, fmt.Errorf("the store gave no snapshot: %w", err)
	}
	level := RestoreLevel(s.Source, s.Twin)
	var state SprintState
	if level == RestoreSemantic {
		if state, err = s.Source.(StateSource).State(ctx); err != nil {
			return out, fmt.Errorf("the store's sprint state at the save cannot be read: %w", err)
		}
	}
	name, err := s.name()
	if err != nil {
		return out, err
	}
	path := filepath.Join(s.Dir, name)
	sum := sha256.Sum256(rdb)
	hexsum := hex.EncodeToString(sum[:])
	if err := writeFile(path, rdb); err != nil {
		return out, err
	}
	if err := writeFile(path+sumSuffix, []byte(hexsum+"  "+name+"\n")); err != nil {
		_ = os.Remove(path) // ignored: removing the failed copy; the error that failed it is what is returned
		return out, err
	}
	fail := func(err error) (SnapshotTaken, error) {
		_ = os.Remove(path)             // ignored: removing the failed copy; the error that failed it is what is returned
		_ = os.Remove(path + sumSuffix) // ignored: removing the failed copy; the error that failed it is what is returned
		return out, err
	}
	copied, err := os.ReadFile(path)
	if err != nil {
		return fail(fmt.Errorf("the copy %s cannot be read back: %v", name, err))
	}
	if got := sha256.Sum256(copied); got != sum {
		return fail(fmt.Errorf("the copy %s does not match its checksum on reading it back; the snapshot was removed", name))
	}
	got, err := s.Twin.Load(copied)
	if err != nil {
		return fail(fmt.Errorf("the copy %s does not load into a twin: %v; the snapshot was removed", name, err))
	}
	if why := countsDiffer(live, got); why != "" {
		return fail(fmt.Errorf("the copy %s loaded with other counts than the store held at the save (%s); the snapshot was removed", name, why))
	}
	if level == RestoreSemantic {
		if err := SemanticRestore(ctx, state, s.Twin.(StateTwin), copied); err != nil {
			return fail(fmt.Errorf("the copy %s does not restore the sprint the store held at the save: %v; the snapshot was removed", name, err))
		}
	}
	out = SnapshotTaken{File: path, SHA256: hexsum, Bytes: len(rdb), Counts: got, Restore: level}
	if out.Pruned, err = s.prune(); err != nil {
		return out, err
	}
	return out, nil
}

// name is the next file name: the clock's second, and a counter when two
// snapshots fall in one second.
func (s *Snapshotter) name() (string, error) {
	base := snapshotPrefix + s.Now().UTC().Format("20060102T150405Z")
	for i := 0; i < 1000; i++ {
		n := base + snapshotSuffix
		if i > 0 {
			n = fmt.Sprintf("%s-%d%s", base, i, snapshotSuffix)
		}
		if _, err := os.Stat(filepath.Join(s.Dir, n)); os.IsNotExist(err) {
			return n, nil
		}
	}
	return "", errors.New("a thousand snapshots in one second; the clock is stuck")
}

func writeFile(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("%s cannot be written: %v", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp) // ignored: a leftover temp file; the write error is what is returned
		return fmt.Errorf("%s cannot be written: %v", path, err)
	}
	return nil
}

func countsDiffer(want, got SnapshotCounts) string {
	var why []string
	if want.Keys >= 0 && got.Keys >= 0 && want.Keys != got.Keys {
		why = append(why, fmt.Sprintf("keys %d, twin %d", want.Keys, got.Keys))
	}
	if want.Cards >= 0 && got.Cards >= 0 && want.Cards != got.Cards {
		why = append(why, fmt.Sprintf("cards %d, twin %d", want.Cards, got.Cards))
	}
	return strings.Join(why, ", ")
}

// SnapshotFiles is the snapshots in dir, oldest first (names sort by time).
func SnapshotFiles(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		n := e.Name()
		if !e.IsDir() && strings.HasPrefix(n, snapshotPrefix) && strings.HasSuffix(n, snapshotSuffix) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *Snapshotter) prune() ([]string, error) {
	files, err := SnapshotFiles(s.Dir)
	if err != nil {
		return nil, err
	}
	var gone []string
	for len(files) > s.Keep {
		p := filepath.Join(s.Dir, files[0])
		if err := os.Remove(p); err != nil {
			return gone, fmt.Errorf("%s cannot be pruned: %v", p, err)
		}
		_ = os.Remove(p + sumSuffix) // ignored: the sidecar of a snapshot already pruned or removed; its absence is the goal
		gone = append(gone, p)
		files = files[1:]
	}
	return gone, nil
}

// RestoreDrill loads a snapshot file into a twin and returns its counts and
// SHA-256. The file's checksum beside it, when there is one, must match; the
// live store is never named. It proves integrity (RestoreIntegrity) and no
// more: with no source there is no sprint state to compare the file's with.
func RestoreDrill(file string, twin SnapshotTwin) (SnapshotCounts, string, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return SnapshotCounts{}, "", fmt.Errorf("%v; run: nova-sprint snapshot --restore-drill <a file snapshot wrote>", err)
	}
	sum := sha256.Sum256(b)
	hexsum := hex.EncodeToString(sum[:])
	if side, err := os.ReadFile(file + sumSuffix); err == nil {
		if want, _, _ := strings.Cut(strings.TrimSpace(string(side)), " "); want != hexsum {
			return SnapshotCounts{}, hexsum, fmt.Errorf("%s does not match its checksum %s (it is %s); the file is damaged and is not restored", file, want, hexsum)
		}
	}
	c, err := twin.Load(b)
	if err != nil {
		return SnapshotCounts{}, hexsum, fmt.Errorf("%s does not load into a twin: %v", file, err)
	}
	return c, hexsum, nil
}

// SnapshotEvery takes a snapshot, then another each time wait returns, until
// ctx ends. A failed take does not stop the loop (the older snapshots stay):
// it is handed to report, and the next try is a wait away. The wait is
// injected so a test never sleeps.
func SnapshotEvery(ctx context.Context, take func(context.Context) error, wait func(context.Context) error, report func(error)) {
	for ctx.Err() == nil {
		if err := take(ctx); err != nil && ctx.Err() == nil {
			report(err)
		}
		if wait(ctx) != nil {
			return
		}
	}
}

// RDBTwin is the integrity check of a real RDB (RestoreIntegrity): what can be
// checked without a server, the REDIS magic and version and the CRC-64 the file
// ends with. It counts no keys and no cards (-1, not compared) and reads nothing
// of the sprint, so a dump that lost records passes it with a valid checksum.
// The semantic restore of an RDB loads it into an isolated Redis with this
// build's function library and compares ReadState with the source's: the bench
// test TestARedisDumpRestoresTheSprintOnAnIsolatedRedis
// (restore_functional_test.go), with a server of its own.
type RDBTwin struct{}

var redisCRC = crc64.MakeTable(0x95ac9329ac4bc9b5)

// redisCRC64 is Redis's CRC-64 (Jones, initial 0, no final xor).
func redisCRC64(b []byte) uint64 { return ^crc64.Update(^uint64(0), redisCRC, b) }

// Load checks the RDB's header and its trailing checksum.
func (RDBTwin) Load(rdb []byte) (SnapshotCounts, error) {
	none := SnapshotCounts{Keys: -1, Cards: -1}
	if len(rdb) < 9+1+8 || !bytes.HasPrefix(rdb, []byte("REDIS")) {
		return none, errors.New("not an RDB (no REDIS header)")
	}
	var v int
	if _, err := fmt.Sscanf(string(rdb[5:9]), "%d", &v); err != nil || v < 1 {
		return none, fmt.Errorf("not an RDB (version %q)", rdb[5:9])
	}
	if v >= 5 {
		body, tail := rdb[:len(rdb)-8], binary.LittleEndian.Uint64(rdb[len(rdb)-8:])
		if tail != 0 && tail != redisCRC64(body) {
			return none, errors.New("the RDB's CRC-64 does not match its contents")
		}
	}
	return none, nil
}

// MemSource is the in-memory store as a snapshot source: its document
// (Mem.Snapshot) stands for the RDB, and its counts and its sprint state (under
// Names) are the store's own.
type MemSource struct {
	M     *Mem
	Names sprint.Names
}

// Save is the store's document and the counts it holds.
func (s MemSource) Save(context.Context) ([]byte, SnapshotCounts, error) {
	doc, err := s.M.Snapshot()
	if err != nil {
		return nil, SnapshotCounts{Keys: -1, Cards: -1}, err
	}
	c, err := MemTwin{Names: s.Names}.Load(doc)
	return doc, c, err
}

// MemTwin loads a Mem document into a fresh Mem, as the twin the document is
// proved on, and counts its tables and the cards of the work table (under
// Names); LoadState reads its sprint state.
type MemTwin struct{ Names sprint.Names }

// Load restores the document into a new Mem (a document of another version is
// refused) and counts it.
func (t MemTwin) Load(doc []byte) (SnapshotCounts, error) {
	if err := NewMem().Restore(doc); err != nil {
		return SnapshotCounts{Keys: -1, Cards: -1}, err
	}
	var d memSnapshot
	if err := json.Unmarshal(doc, &d); err != nil {
		return SnapshotCounts{Keys: -1, Cards: -1}, err
	}
	c := SnapshotCounts{Keys: len(d.Tables)}
	if w := d.Tables[t.Names.Table(sprint.Work)]; w != nil {
		c.Cards = len(w.Members)
	}
	return c, nil
}

// The sprint backup's key-level dump (docs/SPEC-SPRINT.md, sprint-backup-out):
// the keys of the sprint's epoch and the keys every epoch shares, each as one
// RESTORE line a redis-cli loads, so the dump restores into any empty Redis
// that holds this build's function library.

// DumpKey is one key of a RESTORE dump: its name, its time to live in
// milliseconds (0: none) and its DUMP payload.
type DumpKey struct {
	Key     string
	TTL     int64
	Payload []byte
}

// BackupCounts is what a backup's restore is compared on: the keys it holds
// and the cards of the work table at the epoch, by column.
type BackupCounts struct {
	Keys    int            `json:"keys"`
	Columns map[string]int `json:"columns"`
}

// Text is the counts on one line: keys=<n> cards=<n> (<col>=<n> ...), the
// columns in the work table's order.
func (c BackupCounts) Text() string {
	var cols []string
	total := 0
	for _, col := range workColumns() {
		if n := c.Columns[col]; n > 0 {
			cols = append(cols, fmt.Sprintf("%s=%d", col, n))
			total += n
		}
	}
	return fmt.Sprintf("keys=%d cards=%d (%s)", c.Keys, total, strings.Join(cols, " "))
}

// Differ says where two counts differ, "" when they are the same.
func (c BackupCounts) Differ(got BackupCounts) string {
	var why []string
	if c.Keys != got.Keys {
		why = append(why, fmt.Sprintf("keys %d, restored %d", c.Keys, got.Keys))
	}
	for _, col := range workColumns() {
		if c.Columns[col] != got.Columns[col] {
			why = append(why, fmt.Sprintf("%s %d, restored %d", col, c.Columns[col], got.Columns[col]))
		}
	}
	return strings.Join(why, ", ")
}

// workColumns are the work table's placing columns, in its order.
func workColumns() []string {
	for _, t := range (sprint.Names{}).Definitions() {
		if t.Name == sprint.Work {
			var out []string
			for _, c := range t.Columns {
				if c.Projection != "text" {
					out = append(out, c.Name)
				}
			}
			return out
		}
	}
	return nil
}

// KeyEpoch is the epoch a key belongs to, and false for a key every epoch
// shares: a table generation's key (table:<t>:<n>:...), a sprint key of an
// epoch (<name>@<n>, Names.KeyAt) and a record or operation of a stored id
// (<...>~<n>, StoredID). A key of epoch 0 carries no epoch and is shared.
func KeyEpoch(key string) (uint64, bool) {
	digits := func(s string) (uint64, bool) {
		if s == "" || strings.Trim(s, "0123456789") != "" {
			return 0, false
		}
		n, err := strconv.ParseUint(s, 10, 64)
		return n, err == nil
	}
	if rest, ok := strings.CutPrefix(key, "table:"); ok {
		if p := strings.SplitN(rest, ":", 3); len(p) >= 2 && p[0] != "" {
			if n, ok := digits(p[1]); ok {
				return n, true
			}
		}
	}
	if i := strings.LastIndexByte(key, '@'); i >= 0 {
		if n, ok := digits(key[i+1:]); ok {
			return n, true
		}
	}
	if i := strings.LastIndexByte(key, '~'); i >= 0 {
		if n, ok := digits(key[i+1:]); ok {
			return n, true
		}
	}
	return 0, false
}

// InBackup says the key goes into the backup of epoch: it carries that epoch
// or none.
func InBackup(key string, epoch uint64) bool {
	n, ok := KeyEpoch(key)
	return !ok || n == epoch
}

// SprintKey says a key of a Redis store is the sprint's: its sprint keys and
// records (sprint:...), its four tables (table:<t>, table:<t>:...), its view
// (view:sprint), and the table layer's registries its tables and view are
// listed in (tables, views). Any other tool's key on the same Redis is not.
func SprintKey(n sprint.Names, key string) bool {
	if key == "tables" || key == "views" || strings.HasPrefix(key, n.Key("")) || key == "view:"+n.View() || strings.HasPrefix(key, "view:"+n.View()+":") {
		return true
	}
	for _, t := range []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet} {
		d := "table:" + n.Table(t)
		if key == d || strings.HasPrefix(key, d+":") {
			return true
		}
	}
	return false
}

// RestoreLine is one key as a RESTORE command line: the key and the payload
// quoted as redis-cli quotes (and its argument splitter reads) them.
func RestoreLine(k DumpKey) string {
	return "RESTORE " + quoteArg([]byte(k.Key)) + " " + strconv.FormatInt(k.TTL, 10) + " " + quoteArg(k.Payload) + "\n"
}

func quoteArg(b []byte) string {
	var s strings.Builder
	s.WriteByte('"')
	for _, c := range b {
		switch {
		case c == '\\' || c == '"':
			s.WriteByte('\\')
			s.WriteByte(c)
		case c == '\n':
			s.WriteString(`\n`)
		case c == '\r':
			s.WriteString(`\r`)
		case c == '\t':
			s.WriteString(`\t`)
		case c == '\a':
			s.WriteString(`\a`)
		case c == '\b':
			s.WriteString(`\b`)
		case c >= 0x20 && c < 0x7f:
			s.WriteByte(c)
		default:
			fmt.Fprintf(&s, `\x%02x`, c)
		}
	}
	s.WriteByte('"')
	return s.String()
}

// ParseRestoreDump reads a dump RestoreLine wrote, one key a line, and
// refuses any line that is not one.
func ParseRestoreDump(text []byte) ([]DumpKey, error) {
	var out []DumpKey
	for i, line := range strings.Split(string(text), "\n") {
		if line == "" {
			continue
		}
		k, err := parseRestoreLine(line)
		if err != nil {
			return nil, fmt.Errorf("line %d of the dump: %v", i+1, err)
		}
		out = append(out, k)
	}
	return out, nil
}

func parseRestoreLine(line string) (DumpKey, error) {
	rest, ok := strings.CutPrefix(line, "RESTORE ")
	if !ok {
		return DumpKey{}, errors.New("not a RESTORE line")
	}
	key, rest, err := unquoteArg(rest)
	if err != nil {
		return DumpKey{}, err
	}
	ttlText, rest, ok := strings.Cut(rest, " ")
	if !ok {
		return DumpKey{}, errors.New("no payload")
	}
	ttl, err := strconv.ParseInt(ttlText, 10, 64)
	if err != nil || ttl < 0 {
		return DumpKey{}, fmt.Errorf("a ttl %q", ttlText)
	}
	payload, rest, err := unquoteArg(rest)
	if err != nil {
		return DumpKey{}, err
	}
	if rest != "" {
		return DumpKey{}, errors.New("text after the payload")
	}
	return DumpKey{Key: string(key), TTL: ttl, Payload: payload}, nil
}

// unquoteArg reads one quoted argument and the space after it, returning the
// rest of the line.
func unquoteArg(s string) ([]byte, string, error) {
	if !strings.HasPrefix(s, `"`) {
		return nil, "", errors.New("an argument that is not quoted")
	}
	var b []byte
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			return b, strings.TrimPrefix(s[i+1:], " "), nil
		case c != '\\':
			b = append(b, c)
		case i+1 >= len(s):
			return nil, "", errors.New("a line ending in an escape")
		default:
			i++
			switch e := s[i]; e {
			case 'n':
				b = append(b, '\n')
			case 'r':
				b = append(b, '\r')
			case 't':
				b = append(b, '\t')
			case 'a':
				b = append(b, '\a')
			case 'b':
				b = append(b, '\b')
			case 'x':
				if i+2 >= len(s) {
					return nil, "", errors.New("a short \\x escape")
				}
				v, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
				if err != nil {
					return nil, "", errors.New("a bad \\x escape")
				}
				b = append(b, byte(v))
				i += 2
			default:
				b = append(b, e)
			}
		}
	}
	return nil, "", errors.New("an argument with no closing quote")
}

// CardsByColumn is the work table's cards at the store's epoch, counted by
// column, read as every verb reads them (Store.Load): on a Redis through the
// function library the store holds, so a restore into a Redis with another
// build's library fails here, not later.
func CardsByColumn(ctx context.Context, b Backend, names sprint.Names) (map[string]int, error) {
	st := &Store{B: b, Names: names}
	s, err := st.Load(ctx, []string{sprint.Work}, nil)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, c := range s.T(sprint.Work).Cards() {
		if c.Placed() {
			out[c.Col]++
		}
	}
	return out, nil
}

// MemDump is a twin store as the keys a Redis store of the same state would
// be split into, named as the Redis store names them (Names, ntable's key
// helpers), each payload the JSON of its part of the twin's document: the
// store's meta and views and machine records, each table's definition, each
// table generation, each record and each epoch's sprint keys. Only the keys
// InBackup of epoch are returned.
func MemDump(m *Mem, epoch uint64) ([]DumpKey, error) {
	doc, err := m.Snapshot()
	if err != nil {
		return nil, err
	}
	var s memSnapshot
	if err := json.Unmarshal(doc, &s); err != nil {
		return nil, err
	}
	var out []DumpKey
	add := func(key string, v any) error {
		if !InBackup(key, epoch) {
			return nil
		}
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		out = append(out, DumpKey{Key: key, Payload: b})
		return nil
	}
	n := sprint.Names{}
	meta := memSnapshot{Version: s.Version, EpochSet: s.EpochSet, EpochN: s.EpochN, Cleared: s.Cleared, Owed: s.Owed, Seq: s.Seq}
	if err := add(n.Key("mem:meta"), meta); err != nil {
		return nil, err
	}
	for name, v := range s.Views {
		if err := add("view:"+name, v); err != nil {
			return nil, err
		}
	}
	for k, v := range s.KV {
		if err := add(n.Key("mem:kv:"+k), v); err != nil {
			return nil, err
		}
	}
	for name, r := range s.Dropped {
		if err := add(n.Key("mem:dropped:"+name), r); err != nil {
			return nil, err
		}
	}
	for e, l := range s.Logs {
		if err := add(n.KeyAt("mem:log", e), l); err != nil {
			return nil, err
		}
	}
	for name, t := range s.Tables {
		for e, ep := range t.Epochs {
			if err := add(ntable.EpochPrefix(name, e)+":epoch", ep); err != nil {
				return nil, err
			}
		}
		for id, mem := range t.Members {
			if err := add(t.Def.MemberPrefix+id, memDumpMember{Table: name, Member: mem}); err != nil {
				return nil, err
			}
		}
		def := *t
		def.Epochs, def.Members = nil, nil
		if err := add(ntable.DefKey(name), def); err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// memDumpMember is a record's key in a twin's dump: the record and its table.
type memDumpMember struct {
	Table  string          `json:"table"`
	Member *memberSnapshot `json:"member"`
}

// MemRestore is MemDump's inverse: the keys put back together into a twin
// store's document and restored into a new twin. A key MemDump does not
// write is refused.
func MemRestore(keys []DumpKey) (*Mem, error) {
	n := sprint.Names{}
	s := memSnapshot{Tables: map[string]*tableSnapshot{}, Dropped: map[string]*residueSnap{}, Views: map[string]ntable.View{}, Logs: map[uint64]*logSnapshot{}, KV: map[string]string{}}
	var members []memDumpMember
	var memberIDs []string
	epochs := map[string]map[uint64]*epochSnapshot{}
	sawMeta := false
	for _, k := range keys {
		dec := func(v any) error {
			d := json.NewDecoder(bytes.NewReader(k.Payload))
			d.DisallowUnknownFields()
			if err := d.Decode(v); err != nil {
				return fmt.Errorf("the key %s does not restore: %v", k.Key, err)
			}
			return nil
		}
		var err error
		switch key := k.Key; {
		case key == n.Key("mem:meta"):
			var meta memSnapshot
			err = dec(&meta)
			s.Version, s.EpochSet, s.EpochN, s.Cleared, s.Owed, s.Seq = meta.Version, meta.EpochSet, meta.EpochN, meta.Cleared, meta.Owed, meta.Seq
			sawMeta = true
		case strings.HasPrefix(key, "view:"):
			var v ntable.View
			err = dec(&v)
			s.Views[strings.TrimPrefix(key, "view:")] = v
		case strings.HasPrefix(key, n.Key("mem:kv:")):
			var v string
			err = dec(&v)
			s.KV[strings.TrimPrefix(key, n.Key("mem:kv:"))] = v
		case strings.HasPrefix(key, n.Key("mem:dropped:")):
			var r residueSnap
			err = dec(&r)
			s.Dropped[strings.TrimPrefix(key, n.Key("mem:dropped:"))] = &r
		case strings.HasPrefix(key, n.Key("mem:log")):
			e, _ := KeyEpoch(key)
			var l logSnapshot
			err = dec(&l)
			s.Logs[e] = &l
		case strings.HasPrefix(key, "table:") && strings.HasSuffix(key, ":epoch"):
			name, rest, _ := strings.Cut(strings.TrimPrefix(key, "table:"), ":")
			var e uint64
			if rest != "epoch" {
				e, _ = KeyEpoch(key)
			}
			var ep epochSnapshot
			err = dec(&ep)
			if epochs[name] == nil {
				epochs[name] = map[uint64]*epochSnapshot{}
			}
			epochs[name][e] = &ep
		case strings.HasPrefix(key, "table:"):
			var t tableSnapshot
			err = dec(&t)
			s.Tables[strings.TrimPrefix(key, "table:")] = &t
		default:
			var m memDumpMember
			if err = dec(&m); err == nil && m.Member == nil {
				err = fmt.Errorf("the key %s is no key of a twin's dump", key)
			}
			members = append(members, m)
			memberIDs = append(memberIDs, key)
		}
		if err != nil {
			return nil, err
		}
	}
	if !sawMeta {
		return nil, errors.New("the dump holds no " + n.Key("mem:meta") + " key: it is not a twin's dump")
	}
	for name, eps := range epochs {
		t := s.Tables[name]
		if t == nil {
			return nil, fmt.Errorf("the dump holds a generation of table %s and not the table", name)
		}
		t.Epochs = eps
	}
	for i, m := range members {
		t := s.Tables[m.Table]
		if t == nil {
			return nil, fmt.Errorf("the dump holds a record of table %s and not the table", m.Table)
		}
		if t.Members == nil {
			t.Members = map[string]*memberSnapshot{}
		}
		t.Members[strings.TrimPrefix(memberIDs[i], t.Def.MemberPrefix)] = m.Member
	}
	doc, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	m := NewMem()
	if err := m.Restore(doc); err != nil {
		return nil, err
	}
	return m, nil
}
