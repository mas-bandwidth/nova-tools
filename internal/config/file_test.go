package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
func (r reopening) Ownership(ctx context.Context) (Ownership, error) {
	return r.open().Ownership(ctx)
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
	all, err := Migrations()
	require.NoError(t, err)
	cases := []struct {
		name, body, want string
	}{
		{"not JSON", "machines:\n  m1: {}\n", "is not a nova-config store file"},
		{"empty object", `{}`, "is not a nova-config store file"},
		{"future schema", fmt.Sprintf(`{"schema":%d,"rows":{},"history":[]}`, len(all)+1), "is not a nova-config store file"},
		{"missing rows", fmt.Sprintf(`{"schema":%d,"history":[]}`, len(all)), "is not a nova-config store file"},
		{"second object", `{"schema":1,"rows":{}} {}`, "want one JSON object"},
		{"an unknown key", `{"schema":13,"rows":{},"history":[],"extra":1}`, "is not a nova-config store file"},
		{"an unknown kind", fmt.Sprintf(`{"schema":%d,"rows":{"lane":{}},"history":[]}`, len(all)), `holds rows of kind "lane"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "x.json")
			require.NoError(t, os.WriteFile(path, []byte(tc.body), 0o600))
			_, err := OpenFile(path)
			assert.ErrorContains(t, err, tc.want)
			got, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, tc.body, string(got), "refusing a file preserves its bytes")
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

// The final newline counts toward the read bound. A write crossing it
// refuses before replacing the previous readable store.
func TestAFileStoreWriteCannotOutgrowItsReadBound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := migratedFile(t)
	f, err := OpenFile(path)
	require.NoError(t, err)
	_, err = f.Insert(ctx, KindMachine, Row{Name: "m1", Fields: map[string]string{"user": "u", "seat": "s", "slots": "8", "width": "1"}}, "a1")
	require.NoError(t, err)
	all, err := Migrations()
	require.NoError(t, err)
	// Bring an existing history field exactly to the limit without a loop
	// generating thousands of otherwise identical writes.
	raw, err := json.MarshalIndent(fileState{Schema: len(all), Rows: f.rows, History: f.history}, "", "  ")
	require.NoError(t, err)
	f.history[0].Actor += strings.Repeat("x", MaxStoreFileBytes-len(raw)-1)
	require.NoError(t, f.save())
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Len(t, before, MaxStoreFileBytes)
	again, err := OpenFile(path)
	require.NoError(t, err)
	history, err := again.History(ctx, KindMachine, "m1")
	require.NoError(t, err)
	_, _, err = again.Update(ctx, KindMachine, "m1", map[string]string{"width": "2"}, "a1")
	require.ErrorContains(t, err, "nothing was written")
	row, found, err := again.Get(ctx, KindMachine, "m1")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "1", row.Fields["width"], "a failed save leaves the open store unchanged")
	afterHistory, err := again.History(ctx, KindMachine, "m1")
	require.NoError(t, err)
	assert.Equal(t, history, afterHistory)
	_, err = again.Insert(ctx, KindMachine, Row{Name: "m2", Fields: map[string]string{"user": "u", "seat": "s", "slots": "1"}}, "a1")
	require.ErrorContains(t, err, "nothing was written")
	_, found, err = again.Get(ctx, KindMachine, "m2")
	require.NoError(t, err)
	assert.False(t, found, "a refused insert leaves no row in memory")
	_, err = again.Delete(ctx, KindMachine, "m1", "a1")
	require.ErrorContains(t, err, "nothing was written")
	_, found, err = again.Get(ctx, KindMachine, "m1")
	require.NoError(t, err)
	assert.True(t, found, "a refused delete keeps its row in memory")
	afterHistory, err = again.History(ctx, KindMachine, "m1")
	require.NoError(t, err)
	assert.Equal(t, history, afterHistory)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	kept, err := OpenFile(path)
	require.NoError(t, err)
	row, found, err = kept.Get(ctx, KindMachine, "m1")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "1", row.Fields["width"])
}
