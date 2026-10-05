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
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A verified snapshot of the store (docs/SPEC-SPRINT.md, store-snapshot-verb):
// the store is asked for a snapshot (BGSAVE, waited for), the RDB is copied
// into a directory with its SHA-256 beside it, the copy is loaded into a twin
// and its counts compared with the store's at the save, and the copies beyond
// the keep count are pruned. A snapshot that fails either check is removed and
// the older ones stay: the directory holds only verified snapshots. A restore
// drill loads a file into a twin and reports its counts; it never opens the
// live store.

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
	Pruned []string       `json:"pruned,omitempty"`
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
	out = SnapshotTaken{File: path, SHA256: hexsum, Bytes: len(rdb), Counts: got}
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
// live store is never named.
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

// RDBTwin is the loader of a real RDB: it checks what a twin can check without
// a server: the REDIS magic and version, and the CRC-64 the file ends with. It
// counts no keys and no cards (-1): the card-level load is owed.
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
// (Mem.Snapshot) stands for the RDB, and its counts are the store's own.
type MemSource struct{ M *Mem }

// Save is the store's document and the counts it holds.
func (s MemSource) Save(context.Context) ([]byte, SnapshotCounts, error) {
	doc, err := s.M.Snapshot()
	if err != nil {
		return nil, SnapshotCounts{Keys: -1, Cards: -1}, err
	}
	c, err := MemTwin{}.Load(doc)
	return doc, c, err
}

// MemTwin loads a Mem document into a fresh Mem, as the twin the document is
// proved on, and counts its tables and the cards of the work table.
type MemTwin struct{}

// Load restores the document into a new Mem (a document of another version is
// refused) and counts it.
func (MemTwin) Load(doc []byte) (SnapshotCounts, error) {
	if err := NewMem().Restore(doc); err != nil {
		return SnapshotCounts{Keys: -1, Cards: -1}, err
	}
	var d memSnapshot
	if err := json.Unmarshal(doc, &d); err != nil {
		return SnapshotCounts{Keys: -1, Cards: -1}, err
	}
	c := SnapshotCounts{Keys: len(d.Tables)}
	if t := d.Tables[sprint.Work]; t != nil {
		c.Cards = len(t.Members)
	}
	return c, nil
}
