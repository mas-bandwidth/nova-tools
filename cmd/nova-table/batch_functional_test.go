//go:build functional

package main

import (
	"context"
	"github.com/redis/go-redis/v9"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBatchCLIHelp(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runTable("batch", "-h")
	require.EqualValues(t, 0, code, "expected code 0, got %d (stderr: %q)", code, stderr)
	require.Empty(t, stderr, "expected empty stderr, got %q", stderr)
	require.Contains(t, stdout, "batch (<manifest-file> | - | '<json>')", "expected stdout to mention batch syntax, got:\n%s", stdout)
}

func TestBatchCLIExecution(t *testing.T) {
	t.Parallel()
	addr := throwaway(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	cols, err := ntable.ParseColumns("ready,working,done")
	require.NoError(t, err, "%v", err)
	require.NoError(t, ntable.Create(ctx, c, ntable.Table{Name: "demo", Columns: cols}, time.Now()))
	{
		_, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{})
		require.NoError(t, err, "%v", err)
	}

	manifestJSON := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"2","operation_id":"op-cli-1","actor":"cli-user","members":[{"id":"m1","expect":{"absent":true},"create":{"row":"build","col":"ready","score":10},"set":{"role":"builder"}}]}`

	// 1. Run with inline JSON
	code, stdout, stderr := runTable("batch", "--redis", addr, manifestJSON)
	require.EqualValues(t, 0, code, "expected code 0, got %d (stderr: %q)", code, stderr)
	assert.Contains(t, stdout, "TABLE BATCH table=demo operation=op-cli-1", "expected TABLE BATCH header in stdout, got:\n%s", stdout)
	assert.Contains(t, stdout, "TABLE RECEIPT event=", "expected TABLE RECEIPT in stdout, got:\n%s", stdout)
	assert.Contains(t, stdout, `MEMBER m1 place=-->build:ready score=-->10 member_revision=0->1 fields={"role":[null,"builder"]}`, "expected MEMBER before/after line in stdout, got:\n%s", stdout)

	// 2. Run with manifest file
	dir := t.TempDir()
	manifestFile := filepath.Join(dir, "manifest.json")
	manifest2JSON := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"3","operation_id":"op-cli-2","actor":"cli-user","members":[{"id":"m1","expect":{"revision":"1","place":{"row":"build","col":"ready"}},"move":{"row":"build","col":"working"}}]}`
	require.NoError(t, os.WriteFile(manifestFile, []byte(manifest2JSON), 0644))

	code2, stdout2, stderr2 := runTable("batch", "--redis", addr, manifestFile)
	require.EqualValues(t, 0, code2, "expected code 0, got %d (stderr: %q)", code2, stderr2)
	assert.Contains(t, stdout2, "TABLE BATCH table=demo operation=op-cli-2", "expected TABLE BATCH header in stdout2, got:\n%s", stdout2)
	assert.Contains(t, stdout2, "MEMBER m1 place=build:ready->build:working score=10->10 member_revision=1->2 fields={}", "expected MEMBER move line in stdout2, got:\n%s", stdout2)
}

func TestBatchCLIRefusesMalformedRawManifestsWithZeroMutations(t *testing.T) {
	t.Parallel()
	addr := throwaway(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	cols, err := ntable.ParseColumns("ready,working,done")
	require.NoError(t, err, "%v", err)
	require.NoError(t, ntable.Create(ctx, c, ntable.Table{Name: "demo", Columns: cols}, time.Now()))
	{
		_, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{})
		require.NoError(t, err, "%v", err)
	}

	// Initial state assertions: table revision is 2.
	snap, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"none"})
	require.NoError(t, err, "table snapshot read: %v", err)
	require.EqualValues(t, 2, snap.Revision, "expected initial revision 2, got %d", snap.Revision)

	cases := []struct {
		name      string
		rawJSON   string
		errSubstr string
	}{
		{
			name: "duplicate key in raw JSON",
			rawJSON: `{
				"schema": 1,
				"table": "demo",
				"actor": "user1",
				"actor": "user2",
				"epoch": "0",
				"expected_table_revision": "2",
				"operation_id": "op-dup",
				"members": [{"id":"bad1","expect":{"absent":true},"create":{"row":"build","col":"ready","score":1}}]
			}`,
			errSubstr: `duplicate key "actor" in manifest`,
		},
		{
			name: "unknown field in raw JSON",
			rawJSON: `{
				"schema": 1,
				"table": "demo",
				"unknown_field": "disallowed",
				"epoch": "0",
				"expected_table_revision": "2",
				"operation_id": "op-unk",
				"members": [{"id":"bad2","expect":{"absent":true},"create":{"row":"build","col":"ready","score":1}}]
			}`,
			errSubstr: `unknown field "unknown_field"`,
		},
		{
			name: "remove false in raw JSON",
			rawJSON: `{
				"schema": 1,
				"table": "demo",
				"epoch": "0",
				"expected_table_revision": "2",
				"operation_id": "op-rem",
				"members": [{"id":"bad3","expect":{"absent":true},"create":{"row":"build","col":"ready","score":1},"remove":false}]
			}`,
			errSubstr: `remove must be true`,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runTable("batch", "--redis", addr, tc.rawJSON)
			require.EqualValues(t, 2, code, "expected exit code 2, got %d (stdout: %q, stderr: %q)", code, stdout, stderr)
			assert.Contains(t, stderr, "nova-table batch:", "expected stderr to contain refusal with %q, got: %s", tc.errSubstr, stderr)
			assert.Contains(t, stderr, tc.errSubstr, "expected stderr to contain refusal with %q, got: %s", tc.errSubstr, stderr)
			assert.Empty(t, stdout, "expected empty stdout on refusal, got: %s", stdout)

			// Verify 0 store mutations:
			// 1. Table revision must still be 2.
			curSnap, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"none"})
			require.NoError(t, err, "%v", err)
			require.EqualValues(t, 2, curSnap.Revision, "store mutation detected: table revision moved from 2 to %d", curSnap.Revision)
			// 2. Members must not exist.
			keys, err := c.Keys(ctx, "table::member:*").Result()
			require.NoError(t, err, "%v", err)
			require.Len(t, keys, 0, "store mutation detected: member keys exist: %v", keys)
		})
	}
}

func TestBatchCLIStdinReading(t *testing.T) {
	t.Parallel()
	addr := throwaway(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	cols, err := ntable.ParseColumns("ready,working,done")
	require.NoError(t, err, "%v", err)
	require.NoError(t, ntable.Create(ctx, c, ntable.Table{Name: "demo", Columns: cols}, time.Now()))
	{
		_, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{})
		require.NoError(t, err, "%v", err)
	}

	manifestJSON := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"2","operation_id":"op-stdin-1","actor":"cli-stdin","members":[{"id":"stdin-m1","expect":{"absent":true},"create":{"row":"build","col":"ready","score":50}}]}`

	var out, errs strings.Builder
	app := &application{in: strings.NewReader(manifestJSON)}
	code := app.run([]string{"batch", "--redis", addr, "-"}, &out, &errs)
	require.EqualValues(t, 0, code, "expected exit code 0 from stdin manifest, got %d (stderr: %s)", code, errs.String())
	assert.Contains(t, out.String(), "TABLE BATCH table=demo operation=op-stdin-1", "expected TABLE BATCH header in stdout, got:\n%s", out.String())

	// Verify member was physically created in store
	memKey := "table::member:stdin-m1"
	exists, err := c.Exists(ctx, memKey).Result()
	require.NoError(t, err, "expected member %s in store, exists=%d, err=%v", memKey, exists, err)
	require.EqualValues(t, 1, exists, "expected member %s in store, exists=%d, err=%v", memKey, exists, err)
}

func TestBatchCLIRejectsUnsupportedFlags(t *testing.T) {
	t.Parallel()
	addr := throwaway(t)
	validJSON := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"2","operation_id":"op-flags","members":[]}`

	for _, flag := range []string{"--fence=f123", "--fence", "--idem=i123", "--idem"} {
		args := []string{"batch", "--redis", addr, flag, validJSON}
		if flag == "--fence" || flag == "--idem" {
			args = []string{"batch", "--redis", addr, flag, "token", validJSON}
		}
		code, _, stderr := runTable(args...)
		require.EqualValues(t, 2, code, "expected exit code 2 for unsupported flag %s, got %d", flag, code)
		assert.False(t, !strings.Contains(stderr, "unknown flag") && !strings.Contains(stderr, "flag provided but not defined"), "expected unknown flag in stderr, got: %s", stderr)
	}
}

func TestBatchCLIReceiptFlagSuppression(t *testing.T) {
	t.Parallel()
	addr := throwaway(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	cols, err := ntable.ParseColumns("ready,working,done")
	require.NoError(t, err, "%v", err)
	require.NoError(t, ntable.Create(ctx, c, ntable.Table{Name: "demo", Columns: cols}, time.Now()))
	{
		_, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{})
		require.NoError(t, err, "%v", err)
	}

	manifestJSON := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"2","operation_id":"op-rcpt-off","members":[{"id":"m-rcpt","expect":{"absent":true},"create":{"row":"build","col":"ready","score":10}}]}`

	// Run with --receipt=false
	code, stdout, stderr := runTable("batch", "--redis", addr, "--receipt=false", manifestJSON)
	require.EqualValues(t, 0, code, "expected exit code 0, got %d (stderr: %s)", code, stderr)
	assert.NotContains(t, stdout, "TABLE RECEIPT", "expected TABLE RECEIPT to be suppressed when --receipt=false, got:\n%s", stdout)
	assert.Contains(t, stdout, "TABLE BATCH table=demo operation=op-rcpt-off", "expected TABLE BATCH in stdout, got:\n%s", stdout)
}

// The CLI prints a score as the exact decimal string the store holds.
func TestBatchCLIPrintsScoresWithoutLosingPrecision(t *testing.T) {
	t.Parallel()
	addr, rev := batchFixture(t)
	manifest := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"` + rev + `","operation_id":"exact","members":[` +
		`{"id":"x1","expect":{"absent":true},"create":{"row":"build","col":"done","score":0.30000000000000004}},` +
		`{"id":"x2","expect":{"absent":true},"create":{"row":"build","col":"done","score":0.3}}]}`
	code, stdout, stderr := runTable("batch", "--redis", addr, manifest)
	require.EqualValues(t, 0, code, "exit %d: %s", code, stderr)
	// Redis's RESP2 ZSCORE text is the oracle: versions may spell 0.3 as
	// 0.29999999999999999, while both inputs must remain distinguishable.
	resp2 := redis.NewClient(&redis.Options{Addr: addr, Protocol: 2})
	defer resp2.Close()
	seen := map[string]string{}
	for id, input := range map[string]string{"x1": "0.30000000000000004", "x2": "0.3"} {
		score, err := resp2.Do(context.Background(), "ZSCORE", ntable.CellKey("demo", "build", "done"), id).Text()
		require.NoError(t, err, "%v", err)
		got, err := strconv.ParseFloat(score, 64)
		require.NoError(t, err, "%s store score %q: %v", id, score, err)
		want, err := strconv.ParseFloat(input, 64)
		require.NoError(t, err, "%v", err)
		assert.Equal(t, math.Float64bits(want), math.Float64bits(got), "%s store score %q is not input %q", id, score, input)
		{
			other, dup := seen[score]
			require.False(t, dup, "adjacent input scores for %s and %s collapsed to %q", id, other, score)
		}
		seen[score] = id
		w := "MEMBER " + id + " place=-->build:done score=-->" + score + " "
		assert.Contains(t, stdout, w, "stdout lacks %q:\n%s", w, stdout)
	}
}
