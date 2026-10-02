package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// FileStore is the Store kept in one local JSON file: the rows and the
// history of a Mem, read when it is opened and written whole after every
// write. It is for trying the tool with no database (--file <path>): the
// same kinds, refusals, history and revisions as PostgreSQL, because it is
// the same strict Mem the tests hold to the store contract. It is never the
// fleet's store: apply and inventory read Redis, and the runtime tools read
// PostgreSQL. One process writes it at a time.
//
// Libraries considered: natefinch/atomic (not a module of this repository);
// the write is os.CreateTemp in the file's directory and os.Rename, so a
// reader never sees half a file.
type FileStore struct {
	*Mem
	path   string
	exists bool
}

// fileState is the file's one JSON object.
type fileState struct {
	Schema  int                       `json:"schema"`
	Rows    map[string]map[string]Row `json:"rows"`
	History []Change                  `json:"history"`
}

// MaxStoreFileBytes bounds a store file, read whole when it is opened.
const MaxStoreFileBytes = 16 << 20

// OpenFile opens the store file at path. A file that is not there is an
// empty store, as a database migrate has not run on: every read and write
// refuses until Migrate writes the file.
func OpenFile(path string) (*FileStore, error) {
	f := &FileStore{Mem: NewMem(), path: path}
	f.Now = func() time.Time { return time.Now().UTC().Truncate(time.Second) }
	fi, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return f, nil
	case err != nil:
		return nil, fmt.Errorf("--file %s: %w", path, err)
	case fi.Size() > MaxStoreFileBytes:
		return nil, fmt.Errorf("--file %s is %d bytes, over %d", path, fi.Size(), MaxStoreFileBytes)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--file %s: %w", path, err)
	}
	var st fileState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&st); err != nil {
		return nil, fmt.Errorf("--file %s is not a nova-config store file (%v); give a new path, and migrate --file <path> makes one", path, err)
	}
	for kind, rows := range st.Rows {
		k, ok := Lookup(kind)
		if !ok {
			return nil, fmt.Errorf("--file %s holds rows of kind %q, which this binary does not know", path, kind)
		}
		f.rows[kind] = map[string]Row{}
		for name, r := range rows {
			// a field a later migration added reads as its default, as the column would
			for _, fl := range k.Fields {
				if _, has := r.Fields[fl.Name]; !has {
					if r.Fields == nil {
						r.Fields = map[string]string{}
					}
					r.Fields[fl.Name] = fl.zero()
				}
			}
			f.rows[kind][name] = r
		}
	}
	f.history = st.History
	f.exists = true
	return f, nil
}

// Path is the file the store is kept in.
func (f *FileStore) Path() string { return f.path }

// absent is the refusal of every read and write before migrate made the file.
func (f *FileStore) absent() error {
	if f.exists {
		return nil
	}
	return fmt.Errorf("--file %s is not there yet; run: nova-config migrate --file %s", f.path, f.path)
}

// save writes the whole store to the file: a temporary file beside it, then a
// rename over it.
func (f *FileStore) save() error {
	all, err := Migrations()
	if err != nil {
		return err
	}
	f.Mem.mu.Lock()
	st := fileState{Schema: len(all), Rows: f.rows, History: f.history}
	raw, err := json.MarshalIndent(st, "", "  ")
	f.Mem.mu.Unlock()
	if err != nil {
		return fmt.Errorf("--file %s: encode: %w", f.path, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(f.path), "."+filepath.Base(f.path)+".*")
	if err != nil {
		return fmt.Errorf("--file %s: %w", f.path, err)
	}
	_, werr := tmp.Write(append(raw, '\n'))
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		// ignored: the temporary file is removed on a failed write; the write's error is the one returned
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("--file %s: write: %w", f.path, err)
	}
	if err := os.Rename(tmp.Name(), f.path); err != nil {
		// ignored: as above, the rename's error is the one returned
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("--file %s: %w", f.path, err)
	}
	f.exists = true
	return nil
}

func (f *FileStore) Get(ctx context.Context, kind, name string) (Row, bool, error) {
	if err := f.absent(); err != nil {
		return Row{}, false, err
	}
	return f.Mem.Get(ctx, kind, name)
}

func (f *FileStore) List(ctx context.Context, kind string) ([]Row, error) {
	if err := f.absent(); err != nil {
		return nil, err
	}
	return f.Mem.List(ctx, kind)
}

func (f *FileStore) Insert(ctx context.Context, kind string, row Row, actor string) (int64, error) {
	if err := f.absent(); err != nil {
		return 0, err
	}
	id, err := f.Mem.Insert(ctx, kind, row, actor)
	if err != nil {
		return 0, err
	}
	return id, f.save()
}

func (f *FileStore) Update(ctx context.Context, kind, name string, changes map[string]string, actor string) (Row, int64, error) {
	if err := f.absent(); err != nil {
		return Row{}, 0, err
	}
	row, id, err := f.Mem.Update(ctx, kind, name, changes, actor)
	if err != nil {
		return Row{}, 0, err
	}
	return row, id, f.save()
}

func (f *FileStore) Delete(ctx context.Context, kind, name, actor string) (int64, error) {
	if err := f.absent(); err != nil {
		return 0, err
	}
	id, err := f.Mem.Delete(ctx, kind, name, actor)
	if err != nil {
		return 0, err
	}
	return id, f.save()
}

func (f *FileStore) History(ctx context.Context, kind, name string) ([]Change, error) {
	if err := f.absent(); err != nil {
		return nil, err
	}
	return f.Mem.History(ctx, kind, name)
}

func (f *FileStore) Rev(ctx context.Context, kind string) (int64, error) {
	if err := f.absent(); err != nil {
		return 0, err
	}
	return f.Mem.Rev(ctx, kind)
}

func (f *FileStore) Counts(ctx context.Context) (map[string]int, error) {
	if err := f.absent(); err != nil {
		return nil, err
	}
	return f.Mem.Counts(ctx)
}

// Version is the schema the file is at: 0 before migrate made it, else
// this binary's (a file is read up to it when opened).
func (f *FileStore) Version(context.Context) (int, error) {
	if !f.exists {
		return 0, nil
	}
	all, err := Migrations()
	if err != nil {
		return 0, err
	}
	return len(all), nil
}

// Migrate makes the file when it is not there (from 0, every migration
// applied) and otherwise rewrites it at this binary's schema, applying none.
func (f *FileStore) Migrate(ctx context.Context) (from, to int, applied []int, err error) {
	all, err := Migrations()
	if err != nil {
		return 0, 0, nil, err
	}
	if from, err = f.Version(ctx); err != nil {
		return 0, 0, nil, err
	}
	if from == 0 {
		for _, m := range all {
			applied = append(applied, m.Version)
		}
	}
	if err := f.save(); err != nil {
		return from, from, nil, err
	}
	return from, len(all), applied, nil
}

// Close writes nothing: every write saved the file as it landed.
func (f *FileStore) Close() error { return nil }
