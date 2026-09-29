//go:build functional

package main

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

func TestBatchCLIHelp(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runTable("batch", "-h")
	if code != 0 {
		t.Fatalf("expected code 0, got %d (stderr: %q)", code, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "batch (<manifest-file> | - | '<json>')") {
		t.Fatalf("expected stdout to mention batch syntax, got:\n%s", stdout)
	}
}

func TestBatchCLIExecution(t *testing.T) {
	t.Parallel()
	addr := throwaway(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	cols, err := ntable.ParseColumns("ready,working,done")
	if err != nil {
		t.Fatal(err)
	}
	if err := ntable.Create(ctx, c, ntable.Table{Name: "demo", Columns: cols}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}

	manifestJSON := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"2","operation_id":"op-cli-1","actor":"cli-user","members":[{"id":"m1","expect":{"absent":true},"create":{"row":"build","col":"ready","score":10},"set":{"role":"builder"}}]}`

	// 1. Run with inline JSON
	code, stdout, stderr := runTable("batch", "--redis", addr, manifestJSON)
	if code != 0 {
		t.Fatalf("expected code 0, got %d (stderr: %q)", code, stderr)
	}
	if !strings.Contains(stdout, "TABLE BATCH table=demo operation=op-cli-1") {
		t.Errorf("expected TABLE BATCH header in stdout, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "TABLE RECEIPT event=") {
		t.Errorf("expected TABLE RECEIPT in stdout, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, `MEMBER m1 place=-->build:ready score=-->10 member_revision=0->1 fields={"role":[null,"builder"]}`) {
		t.Errorf("expected MEMBER before/after line in stdout, got:\n%s", stdout)
	}

	// 2. Run with manifest file
	dir := t.TempDir()
	manifestFile := filepath.Join(dir, "manifest.json")
	manifest2JSON := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"3","operation_id":"op-cli-2","actor":"cli-user","members":[{"id":"m1","expect":{"revision":"1","place":{"row":"build","col":"ready"}},"move":{"row":"build","col":"working"}}]}`
	if err := os.WriteFile(manifestFile, []byte(manifest2JSON), 0644); err != nil {
		t.Fatal(err)
	}

	code2, stdout2, stderr2 := runTable("batch", "--redis", addr, manifestFile)
	if code2 != 0 {
		t.Fatalf("expected code 0, got %d (stderr: %q)", code2, stderr2)
	}
	if !strings.Contains(stdout2, "TABLE BATCH table=demo operation=op-cli-2") {
		t.Errorf("expected TABLE BATCH header in stdout2, got:\n%s", stdout2)
	}
	if !strings.Contains(stdout2, "MEMBER m1 place=build:ready->build:working score=10->10 member_revision=1->2 fields={}") {
		t.Errorf("expected MEMBER move line in stdout2, got:\n%s", stdout2)
	}
}

func TestBatchCLIRefusesMalformedRawManifestsWithZeroMutations(t *testing.T) {
	t.Parallel()
	addr := throwaway(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	cols, err := ntable.ParseColumns("ready,working,done")
	if err != nil {
		t.Fatal(err)
	}
	if err := ntable.Create(ctx, c, ntable.Table{Name: "demo", Columns: cols}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}

	// Initial state assertions: table revision is 2.
	snap, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"none"})
	if err != nil {
		t.Fatalf("table snapshot read: %v", err)
	}
	if snap.Revision != 2 {
		t.Fatalf("expected initial revision 2, got %d", snap.Revision)
	}

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
			if code != 2 {
				t.Fatalf("expected exit code 2, got %d (stdout: %q, stderr: %q)", code, stdout, stderr)
			}
			if !strings.Contains(stderr, "nova-table batch:") || !strings.Contains(stderr, tc.errSubstr) {
				t.Errorf("expected stderr to contain refusal with %q, got: %s", tc.errSubstr, stderr)
			}
			if stdout != "" {
				t.Errorf("expected empty stdout on refusal, got: %s", stdout)
			}

			// Verify 0 store mutations:
			// 1. Table revision must still be 2.
			curSnap, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"none"})
			if err != nil {
				t.Fatal(err)
			}
			if curSnap.Revision != 2 {
				t.Fatalf("store mutation detected: table revision moved from 2 to %d", curSnap.Revision)
			}
			// 2. Members must not exist.
			keys, err := c.Keys(ctx, "table::member:*").Result()
			if err != nil {
				t.Fatal(err)
			}
			if len(keys) != 0 {
				t.Fatalf("store mutation detected: member keys exist: %v", keys)
			}
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
	if err != nil {
		t.Fatal(err)
	}
	if err := ntable.Create(ctx, c, ntable.Table{Name: "demo", Columns: cols}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}

	manifestJSON := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"2","operation_id":"op-stdin-1","actor":"cli-stdin","members":[{"id":"stdin-m1","expect":{"absent":true},"create":{"row":"build","col":"ready","score":50}}]}`

	var out, errs strings.Builder
	app := &application{in: strings.NewReader(manifestJSON)}
	code := app.run([]string{"batch", "--redis", addr, "-"}, &out, &errs)
	if code != 0 {
		t.Fatalf("expected exit code 0 from stdin manifest, got %d (stderr: %s)", code, errs.String())
	}
	if !strings.Contains(out.String(), "TABLE BATCH table=demo operation=op-stdin-1") {
		t.Errorf("expected TABLE BATCH header in stdout, got:\n%s", out.String())
	}

	// Verify member was physically created in store
	memKey := "table::member:stdin-m1"
	exists, err := c.Exists(ctx, memKey).Result()
	if err != nil || exists != 1 {
		t.Fatalf("expected member %s in store, exists=%d, err=%v", memKey, exists, err)
	}
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
		if code != 2 {
			t.Fatalf("expected exit code 2 for unsupported flag %s, got %d", flag, code)
		}
		if !strings.Contains(stderr, "unknown flag") && !strings.Contains(stderr, "flag provided but not defined") {
			t.Errorf("expected unknown flag in stderr, got: %s", stderr)
		}
	}
}

func TestBatchCLIReceiptFlagSuppression(t *testing.T) {
	t.Parallel()
	addr := throwaway(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	cols, err := ntable.ParseColumns("ready,working,done")
	if err != nil {
		t.Fatal(err)
	}
	if err := ntable.Create(ctx, c, ntable.Table{Name: "demo", Columns: cols}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}

	manifestJSON := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"2","operation_id":"op-rcpt-off","members":[{"id":"m-rcpt","expect":{"absent":true},"create":{"row":"build","col":"ready","score":10}}]}`

	// Run with --receipt=false
	code, stdout, stderr := runTable("batch", "--redis", addr, "--receipt=false", manifestJSON)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr: %s)", code, stderr)
	}
	if strings.Contains(stdout, "TABLE RECEIPT") {
		t.Errorf("expected TABLE RECEIPT to be suppressed when --receipt=false, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "TABLE BATCH table=demo operation=op-rcpt-off") {
		t.Errorf("expected TABLE BATCH in stdout, got:\n%s", stdout)
	}
}

// The CLI prints a score as the exact decimal string the store holds.
func TestBatchCLIPrintsScoresWithoutLosingPrecision(t *testing.T) {
	t.Parallel()
	addr, rev := batchFixture(t)
	manifest := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"` + rev + `","operation_id":"exact","members":[` +
		`{"id":"x1","expect":{"absent":true},"create":{"row":"build","col":"done","score":0.30000000000000004}},` +
		`{"id":"x2","expect":{"absent":true},"create":{"row":"build","col":"done","score":0.3}}]}`
	code, stdout, stderr := runTable("batch", "--redis", addr, manifest)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	// Redis's RESP2 ZSCORE text is the oracle: versions may spell 0.3 as
	// 0.29999999999999999, while both inputs must remain distinguishable.
	resp2 := redis.NewClient(&redis.Options{Addr: addr, Protocol: 2})
	defer resp2.Close()
	seen := map[string]string{}
	for id, input := range map[string]string{"x1": "0.30000000000000004", "x2": "0.3"} {
		score, err := resp2.Do(context.Background(), "ZSCORE", ntable.CellKey("demo", "build", "done"), id).Text()
		if err != nil {
			t.Fatal(err)
		}
		got, err := strconv.ParseFloat(score, 64)
		if err != nil {
			t.Fatalf("%s store score %q: %v", id, score, err)
		}
		want, err := strconv.ParseFloat(input, 64)
		if err != nil {
			t.Fatal(err)
		}
		if math.Float64bits(got) != math.Float64bits(want) {
			t.Errorf("%s store score %q is not input %q", id, score, input)
		}
		if other, dup := seen[score]; dup {
			t.Fatalf("adjacent input scores for %s and %s collapsed to %q", id, other, score)
		}
		seen[score] = id
		w := "MEMBER " + id + " place=-->build:done score=-->" + score + " "
		if !strings.Contains(stdout, w) {
			t.Errorf("stdout lacks %q:\n%s", w, stdout)
		}
	}
}
