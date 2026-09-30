//go:build functional

package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// show prints the table's properties, one TABLE PROP line each, in name order
// (L1 contract amendment, table properties).
func TestShowPrintsTheTablesProperties(t *testing.T) {
	t.Parallel()
	addr, _ := batchFixture(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	tb, err := ntable.Read(t.Context(), c, "demo")
	if err != nil {
		t.Fatal(err)
	}
	m := ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: strconv.FormatUint(tb.Revision, 10), OperationID: "props",
		Members: []ntable.BatchMemberEntry{}, Props: map[string]string{"z_index": "2", "deal_index": "m1"}}
	if _, err := ntable.ApplyBatch(t.Context(), c, m); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runTable("show", "--redis", addr, "demo")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	want := "TABLE PROP table=demo deal_index=m1\nTABLE PROP table=demo z_index=2\n"
	if !strings.Contains(stdout, want) {
		t.Errorf("show:\n%s\nwant the lines\n%s", stdout, want)
	}
}
