package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mas-bandwidth/nova-tools/internal/config"
)

// The twin: `--pg file:<path>` (or NOVA_PG_DSN=file:<path>) runs every
// verb against the in-memory store the tests use (config.Mem), loaded from
// the file before the verb and saved to it after, so a configuration lifecycle
// can be tried without a Postgres or Redis server. It is for learning and
// tests, never a fleet's store: one command at a time, one machine.

// twinPrefix is what marks an address as a twin file.
const twinPrefix = config.TwinPrefix

// isTwin says a DSN names a twin: file, or file:<path>.
func isTwin(dsn string) bool {
	return config.IsTwin(dsn)
}

// twinStore is one open twin file: the store, schema version, and the bytes
// last loaded or saved, so a verb that changed nothing writes nothing.
type twinStore struct {
	*config.Mem
	path    string
	version int
	last    []byte
	mu      sync.Mutex
}

func (t *twinStore) Migrate(ctx context.Context) (from, to int, applied []int, err error) {
	all, err := config.Migrations()
	if err != nil {
		return 0, 0, nil, err
	}
	from = t.version
	for _, mg := range all {
		if mg.Version > from {
			applied = append(applied, mg.Version)
			t.version = mg.Version
		}
	}
	to = t.version
	return from, to, applied, nil
}

func (t *twinStore) Version(ctx context.Context) (int, error) {
	return t.version, nil
}

func (t *twinStore) Close() error {
	return t.save()
}

func (t *twinStore) save() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	doc, err := t.Mem.Snapshot(t.version)
	if err != nil {
		return err
	}
	if string(doc) == string(t.last) {
		return nil
	}
	if err := writeAtomic(t.path, doc); err != nil {
		return fmt.Errorf("the twin file %s was not saved: %w", t.path, err)
	}
	t.last = doc
	return nil
}

// openTwin opens the twin file once per process, loading it when it is
// there: a file that is not a twin snapshot is refused, never overwritten.
func openTwin(dsn string) (pgStore, error) {
	path := strings.TrimPrefix(dsn, twinPrefix)
	if dsn == "file" || strings.TrimSpace(path) == "" {
		return nil, errors.New("a twin is a file: --pg file:<path> (a store in memory alone would be gone when this command ends); the twin is for learning and tests, not for a fleet")
	}
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		return nil, fmt.Errorf("the twin file %s is a directory; want --pg file:<path>", path)
	}
	t := &twinStore{
		Mem:     config.NewMem(),
		path:    path,
		version: config.LatestMigrationVersion(),
	}
	doc, err := os.ReadFile(path)
	switch {
	case err == nil:
		v, err := t.Mem.Restore(doc)
		if err != nil {
			return nil, fmt.Errorf("the twin file %s: %w; it is left as it is", path, err)
		}
		t.version = v
		t.last = doc
	case errors.Is(err, os.ErrNotExist):
		initDoc, err := t.Mem.Snapshot(t.version)
		if err == nil {
			t.last = initDoc
		}
	default:
		return nil, fmt.Errorf("the twin file %s: %w", path, err)
	}
	return t, nil
}

// writeAtomic writes the file whole or not at all: a temporary file in the
// same directory, synced, then renamed over the target.
func writeAtomic(path string, doc []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_, err = f.Write(doc)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(name, 0o600)
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		// ignored: best-effort removal of temporary file when rename fails
		_ = os.Remove(name)
	}
	return err
}

// twinRedis is a cold in-memory fake redis for apply --check when running
// against a file twin with no redis flag given.
type twinRedis struct{}

func newTwinRedis() *twinRedis {
	return &twinRedis{}
}

func (tr *twinRedis) Read(ctx context.Context, kind string) (map[string]config.View, int64, error) {
	out := map[string]config.View{}
	if k, _ := config.Lookup(kind); k != nil && k.Singleton {
		out[kind] = config.View{}
		for _, fl := range k.Fields {
			out[kind][fl.Name] = ""
		}
	}
	return out, 0, nil
}

func (tr *twinRedis) Prepare(ctx context.Context) error {
	return nil
}

func (tr *twinRedis) Write(ctx context.Context, kind string, row config.Row, prev config.View, actor, idem string) error {
	return nil
}

func (tr *twinRedis) Remove(ctx context.Context, kind, name, actor, idem string) error {
	return nil
}

func (tr *twinRedis) Stamp(ctx context.Context, kind string, prev, rev int64) error {
	return nil
}

func (tr *twinRedis) Beats(ctx context.Context, names []string) (map[string]*config.Beat, error) {
	return nil, nil
}

func (tr *twinRedis) FriendHosts(ctx context.Context, names []string) (map[string]string, error) {
	return nil, nil
}

func (tr *twinRedis) Close() error {
	return nil
}
