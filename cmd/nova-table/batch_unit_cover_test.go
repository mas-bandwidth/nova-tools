package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNovaTableBatchCoverUnknownFlag(t *testing.T) {
	t.Parallel()
	app := &application{getenv: func(string) string { return "" }}
	var out, errout strings.Builder
	code := app.dispatch([]string{"batch", "--unknown-flag", batchManifest, "--redis", "/tmp/fake.sock"}, &out, &errout)
	require.EqualValues(t, 2, code)
	assert.Empty(t, out.String())
	assert.Contains(t, errout.String(), "BATCH REFUSED")
	assert.Contains(t, errout.String(), "unknown flag")
}

func TestNovaTableBatchCoverWrongPositionalCount(t *testing.T) {
	t.Parallel()
	app := &application{getenv: func(string) string { return "" }}
	var out, errout strings.Builder
	code := app.dispatch([]string{"batch", batchManifest, batchManifest, "--redis", "/tmp/fake.sock"}, &out, &errout)
	require.EqualValues(t, 2, code)
	assert.Empty(t, out.String())
	assert.Contains(t, errout.String(), "BATCH REFUSED")
	assert.Contains(t, errout.String(), "wants one manifest")
}

func TestNovaTableBatchCoverMissingManifestFile(t *testing.T) {
	t.Parallel()
	app := &application{getenv: func(string) string { return "" }}
	var out, errout strings.Builder
	code := app.dispatch([]string{"batch", "/tmp/nonexistent_manifest.json", "--redis", "/tmp/fake.sock"}, &out, &errout)
	require.EqualValues(t, 2, code)
	assert.Empty(t, out.String())
	assert.Contains(t, errout.String(), "cannot read the manifest file")
	assert.Contains(t, errout.String(), "changed=no")
}

func TestNovaTableBatchCoverDryRunRegularFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := dir + "/manifest.json"
	require.NoError(t, os.WriteFile(path, []byte(batchManifest), 0644))
	app := &application{getenv: func(string) string { return "" }}
	var out, errout strings.Builder
	code := app.dispatch([]string{"batch", path, "--dry-run", "--redis", ""}, &out, &errout)
	require.EqualValues(t, 0, code)
	assert.Empty(t, errout.String())
	assert.Contains(t, out.String(), "TABLE DRY-RUN verb=batch")
}

func TestNovaTableBatchCoverDryRunJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := dir + "/manifest.json"
	require.NoError(t, os.WriteFile(path, []byte(batchManifest), 0644))
	app := &application{getenv: func(string) string { return "" }}
	var out, errout strings.Builder
	code := app.dispatch([]string{"batch", path, "--dry-run", "--redis", "", "--json"}, &out, &errout)
	require.EqualValues(t, 0, code)
	assert.Empty(t, errout.String())
	assert.Contains(t, out.String(), `"dry_run":true`)
}

func TestNovaTableBatchCoverStdinForm(t *testing.T) {
	t.Parallel()
	in := strings.NewReader(batchManifest)
	app := &application{in: in, getenv: func(string) string { return "" }}
	var out, errout strings.Builder
	code := app.dispatch([]string{"batch", "-", "--dry-run", "--redis", ""}, &out, &errout)
	require.EqualValues(t, 0, code)
	assert.Empty(t, errout.String())
	assert.Contains(t, out.String(), "TABLE DRY-RUN")
}

func TestNovaTableBatchCoverTruncatedJSON(t *testing.T) {
	t.Parallel()
	app := &application{getenv: func(string) string { return "" }}
	var out, errout strings.Builder
	code := app.dispatch([]string{"batch", "{", "--redis", "/tmp/fake.sock"}, &out, &errout)
	require.EqualValues(t, 2, code)
	assert.Empty(t, out.String())
	assert.Contains(t, errout.String(), "invalid batch manifest")
}

func TestNovaTableBatchCoverSchemaMismatch(t *testing.T) {
	t.Parallel()
	manifest := `{"schema":2,"table":"demo","epoch":"0","expected_table_revision":"2","operation_id":"create-b1","members":[{"id":"b1","expect":{"absent":true},"create":{"row":"build","col":"ready","score":0}}]}`
	app := &application{getenv: func(string) string { return "" }}
	var out, errout strings.Builder
	code := app.dispatch([]string{"batch", manifest, "--redis", "/tmp/fake.sock"}, &out, &errout)
	require.EqualValues(t, 1, code)
	assert.Empty(t, out.String())
	assert.Contains(t, errout.String(), "code=SCHEMA")
	assert.Contains(t, errout.String(), "changed=no")
}

func TestNovaTableBatchCoverEpochMismatch(t *testing.T) {
	t.Parallel()
	app := &application{getenv: func(string) string { return "" }}
	var out, errout strings.Builder
	code := app.dispatch([]string{"batch", batchManifest, "--epoch", "5", "--redis", "/tmp/fake.sock"}, &out, &errout)
	require.EqualValues(t, 2, code)
	assert.Empty(t, out.String())
	assert.Contains(t, errout.String(), "--epoch 5 differs from the manifest's epoch \"0\"")
}

func TestNovaTableBatchCoverActorMismatch(t *testing.T) {
	t.Parallel()
	manifestWithActor := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"2","operation_id":"create-b1","actor":"a","members":[{"id":"b1","expect":{"absent":true},"create":{"row":"build","col":"ready","score":0}}]}`
	app := &application{getenv: func(string) string { return "" }}
	var out, errout strings.Builder
	code := app.dispatch([]string{"batch", manifestWithActor, "--actor", "b", "--redis", "/tmp/fake.sock", "--dry-run"}, &out, &errout)
	require.EqualValues(t, 2, code)
	assert.Empty(t, out.String())
	assert.Contains(t, errout.String(), "--actor \"b\" differs from the manifest's actor")
}

func TestNovaTableBatchCoverActorWithDryRun(t *testing.T) {
	t.Parallel()
	app := &application{getenv: func(string) string { return "" }}
	var out, errout strings.Builder
	code := app.dispatch([]string{"batch", batchManifest, "--actor", "a", "--dry-run", "--redis", ""}, &out, &errout)
	require.EqualValues(t, 0, code)
	assert.Empty(t, errout.String())
	assert.Contains(t, out.String(), "TABLE DRY-RUN")
}

func TestNovaTableBatchCoverActorFillsMissing(t *testing.T) {
	t.Parallel()
	app := &application{getenv: func(string) string { return "" }}
	var out, errout strings.Builder
	code := app.dispatch([]string{"batch", batchManifest, "--actor", "x", "--dry-run", "--redis", ""}, &out, &errout)
	require.EqualValues(t, 0, code)
	assert.Empty(t, errout.String())
	assert.Contains(t, out.String(), "TABLE DRY-RUN")
}

func TestNovaTableBatchCoverNoRedisRefusal(t *testing.T) {
	t.Parallel()
	app := &application{getenv: func(string) string { return "" }}
	var out, errout strings.Builder
	code := app.dispatch([]string{"batch", batchManifest}, &out, &errout)
	require.EqualValues(t, 2, code)
	assert.Empty(t, out.String())
	assert.Contains(t, errout.String(), "BATCH REFUSED")
	assert.Contains(t, errout.String(), "--redis <addr> is required")
}
