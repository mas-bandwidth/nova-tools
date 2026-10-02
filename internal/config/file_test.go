package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The file store (file.go): the strict Mem, kept in one JSON file, so a
// reader tries every verb with no database.

// reopening is a Store that opens the file afresh for every call, as one
// command line after another does: the contract held through it is the
// contract held across processes, every write and its history on disk.
type reopening struct {
	t    *testing.T
	path string
}

func (r reopening) open() *FileStore {
	r.t.Helper()
	f, err := OpenFile(r.path)
	require.NoError(r.t, err)
	return f
}

func (r reopening) Get(ctx context.Context, kind, name string) (Row, bool, error) {
	return r.open().Get(ctx, kind, name)
}
func (r reopening) List(ctx context.Context, kind string) ([]Row, error) {
	return r.open().List(ctx, kind)
}
func (r reopening) Insert(ctx context.Context, kind string, row Row, actor string) (int64, error) {
	return r.open().Insert(ctx, kind, row, actor)
}
func (r reopening) Update(ctx context.Context, kind, name string, changes map[string]string, actor string) (Row, int64, error) {
	return r.open().Update(ctx, kind, name, changes, actor)
}
func (r reopening) Delete(ctx context.Context, kind, name, actor string) (int64, error) {
	return r.open().Delete(ctx, kind, name, actor)
}
func (r reopening) History(ctx context.Context, kind, name string) ([]Change, error) {
	return r.open().History(ctx, kind, name)
}
func (r reopening) Rev(ctx context.Context, kind string) (int64, error) {
	return r.open().Rev(ctx, kind)
}
func (r reopening) Counts(ctx context.Context) (map[string]int, error) {
	return r.open().Counts(ctx)
}

// migratedFile is a file store made by Migrate in the test's own directory.
func migratedFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "try.json")
	f, err := OpenFile(path)
	require.NoError(t, err)
	_, _, _, err = f.Migrate(context.Background())
	require.NoError(t, err)
	return path
}

func TestFileStoreKeepsTheContractAcrossOpens(t *testing.T) {
	t.Parallel()
	storeTests(t, func(t *testing.T) Store { return reopening{t: t, path: migratedFile(t)} })
}

// A file migrate has not made is a store with no schema: every read and
// write refuses with the migrate to run; migrate makes it once.
func TestAFileStoreRefusesUntilMigrateMakesIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "try.json")
	f, err := OpenFile(path)
	require.NoError(t, err)
	v, err := f.Version(ctx)
	require.NoError(t, err)
	assert.Zero(t, v)
	_, err = f.List(ctx, KindMachine)
	assert.ErrorContains(t, err, "is not there yet; run: nova-config migrate --file "+path)
	_, err = f.Insert(ctx, KindMachine, Row{Name: "m1", Fields: map[string]string{"user": "u", "seat": "s", "slots": "1"}}, "a1")
	assert.ErrorContains(t, err, "is not there yet")
	assert.NoFileExists(t, path, "a refused write makes no file")
	all, err := Migrations()
	require.NoError(t, err)
	from, to, applied, err := f.Migrate(ctx)
	require.NoError(t, err)
	assert.Equal(t, []int{0, len(all), len(all)}, []int{from, to, len(applied)})
	again, err := OpenFile(path)
	require.NoError(t, err)
	from, to, applied, err = again.Migrate(ctx)
	require.NoError(t, err)
	assert.Equal(t, []int{len(all), len(all), 0}, []int{from, to, len(applied)}, "migrate twice applies nothing")
	rows, err := again.List(ctx, KindTier)
	require.NoError(t, err)
	assert.Len(t, rows, len(RouteTiers), "the rows a migration makes are there, as in PostgreSQL")
}

// A file that is not a store file, or names a kind this binary does not know,
// is refused by name; a field a later migration added reads as its default.
func TestAFileStoreReadsOnlyItsOwnShape(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, body, want string
	}{
		{"not JSON", "machines:\n  m1: {}\n", "is not a nova-config store file"},
		{"an unknown key", `{"schema":13,"rows":{},"history":[],"extra":1}`, "is not a nova-config store file"},
		{"an unknown kind", `{"schema":13,"rows":{"lane":{}},"history":[]}`, `holds rows of kind "lane"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "x.json")
			require.NoError(t, os.WriteFile(path, []byte(tc.body), 0o600))
			_, err := OpenFile(path)
			assert.ErrorContains(t, err, tc.want)
		})
	}
	t.Run("a field added later reads as its default", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "old.json")
		require.NoError(t, os.WriteFile(path, []byte(`{"schema":11,"rows":{"machine":{"m1":{"Name":"m1","Fields":{"user":"u","seat":"s","slots":"8","runners":"0"}}}},"history":[]}`), 0o600))
		f, err := OpenFile(path)
		require.NoError(t, err)
		row, found, err := f.Get(context.Background(), KindMachine, "m1")
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, "0", row.Fields["width"])
	})
}
