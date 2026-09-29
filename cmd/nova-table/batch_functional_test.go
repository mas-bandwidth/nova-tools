//go:build functional

package main

import (
	"context"
	"os"
	"path/filepath"
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
	if !strings.Contains(stdout, "batch <manifest>") {
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
	if !strings.Contains(stdout, "MEMBER m1 place=-->build:ready rev=0->1") && !strings.Contains(stdout, "MEMBER m1 place=--->build:ready rev=0->1") && !strings.Contains(stdout, "MEMBER m1 place=-->build:ready rev=0->1") {
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
	if !strings.Contains(stdout2, "MEMBER m1 place=build:ready->build:working rev=1->2") {
		t.Errorf("expected MEMBER move line in stdout2, got:\n%s", stdout2)
	}
}
