//go:build functional

package main

import (
	"github.com/redis/go-redis/v9"
	"strconv"

	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// show prints the table's properties, one TABLE PROP line each, in name order
// (L1 contract amendment, table properties).
func TestShowPrintsTheTablesProperties(t *testing.T) {
	t.Parallel()
	addr, _ := batchFixture(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	tb, err := ntable.Read(t.Context(), c, "demo")
	require.NoError(t, err, "%v", err)
	m := ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: strconv.FormatUint(tb.Revision, 10), OperationID: "props",
		Members: []ntable.BatchMemberEntry{}, Props: map[string]string{"z_index": "2", "deal_index": "m1"}}
	{
		_, err := ntable.ApplyBatch(t.Context(), c, m)
		require.NoError(t, err, "%v", err)
	}
	code, stdout, stderr := runTable("show", "--redis", addr, "demo")
	require.EqualValues(t, 0, code, "exit %d\n%s%s", code, stdout, stderr)
	want := "TABLE PROP table=demo deal_index=m1\nTABLE PROP table=demo z_index=2\n"
	assert.Contains(t, stdout, want, "show:\n%s\nwant the lines\n%s", stdout, want)
}
