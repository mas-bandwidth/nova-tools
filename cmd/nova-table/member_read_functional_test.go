//go:build functional

package main

import (
	"encoding/json"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMemberReadPrintsPlaceScoreRevisionFieldsAndTheMissing(t *testing.T) {
	t.Parallel()
	addr, _ := batchFixture(t) // m1 at build:ready, revision 1, role=builder
	{
		code, _, stderr := runTable("member", "create", "--redis", addr, "demo", "loose")
		require.EqualValues(t, 0, code, "%v", stderr)
	}
	code, stdout, stderr := runTable("member", "read", "--redis", addr, "demo", "m1", "loose", "zz")
	require.EqualValues(t, 0, code, "exit %d: %s", code, stderr)
	require.Empty(t, stderr, "exit %d: %s", code, stderr)
	for _, w := range []string{
		"TABLE READ table=demo epoch=0 table_revision=4 members=2 missing=1 trips=1\n",
		`MEMBER m1 place=build:ready score=1 member_revision=1 fields={"role":"builder"}` + "\n",
		"MEMBER loose place=- score=- member_revision=1 fields={}\n",
		"MISSING zz\n",
	} {
		assert.Contains(t, stdout, w, "stdout lacks %q:\n%s", w, stdout)
	}
	// a whole cell, and a cell that is empty
	code, stdout, stderr = runTable("member", "read", "--redis", addr, "demo", "--cell", "build:ready", "--cell", "build:done")
	assert.EqualValues(t, 0, code, "--cell: exit %d\n%s%s", code, stdout, stderr)
	assert.Contains(t, stdout, "members=1 missing=0", "--cell: exit %d\n%s%s", code, stdout, stderr)
	assert.Contains(t, stdout, "MEMBER m1 ", "--cell: exit %d\n%s%s", code, stdout, stderr)
	// JSON for a program
	code, stdout, stderr = runTable("member", "read", "--redis", addr, "--json", "demo", "m1", "zz")
	require.EqualValues(t, 0, code, "%v", stderr)
	var got struct {
		Table         string   `json:"table"`
		Epoch         string   `json:"epoch"`
		TableRevision string   `json:"table_revision"`
		Missing       []string `json:"missing"`
		Trips         int      `json:"trips"`
		Members       []struct {
			ID             string            `json:"id"`
			Place          *string           `json:"place"`
			Score          *string           `json:"score"`
			MemberRevision string            `json:"member_revision"`
			Fields         map[string]string `json:"fields"`
		} `json:"members"`
	}
	{
		err := json.Unmarshal([]byte(stdout), &got)
		require.NoError(t, err, "not JSON: %v\n%s", err, stdout)
	}
	assert.Equal(t, "demo", got.Table, "JSON: %+v", got)
	assert.Equal(t, "0", got.Epoch, "JSON: %+v", got)
	assert.Equal(t, "4", got.TableRevision, "JSON: %+v", got)
	assert.Equal(t, 1, got.Trips, "JSON: %+v", got)
	assert.Equal(t, 1, len(got.Missing), "JSON: %+v", got)
	assert.Equal(t, "zz", got.Missing[0], "JSON: %+v", got)
	assert.Equal(t, 1, len(got.Members), "JSON: %+v", got)
	assert.Equal(t, "m1", got.Members[0].ID, "JSON: %+v", got)
	assert.Equal(t, "build:ready", *got.Members[0].Place, "JSON: %+v", got)
	assert.Equal(t, "1", *got.Members[0].Score, "JSON: %+v", got)
	assert.Equal(t, "1", got.Members[0].MemberRevision, "JSON: %+v", got)
	assert.Equal(t, "builder", got.Members[0].Fields["role"], "JSON: %+v", got)
	// usage
	for _, args := range [][]string{{"member", "read", "--redis", addr, "demo"}, {"member", "read", "--redis", addr, "demo", "m1", "--cell", "build:ready"}, {"member", "read", "--redis", addr, "demo", "--cell", "nocolon"}} {
		{
			code, _, stderr := runTable(args...)
			assert.EqualValues(t, 2, code, "%v: exit %d %s", args, code, stderr)
			assert.Contains(t, stderr, "member read", "%v: exit %d %s", args, code, stderr)
		}
	}
}

func TestBatchJSONReceiptAndReplay(t *testing.T) {
	t.Parallel()
	addr, rev := batchFixture(t)
	manifest := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"` + rev + `","operation_id":"json-1","members":[` +
		`{"id":"m1","expect":{"revision":"1"},"move":{"row":"build","col":"working","score":2.5},"set":{"role":"lead"}},` +
		`{"id":"m2","expect":{"absent":true},"create":{"row":"build","col":"done","score":7}}]}`
	type receipt struct {
		Table         string                         `json:"table"`
		OperationID   string                         `json:"operation_id"`
		Epoch         string                         `json:"epoch"`
		TableRevision struct{ Before, After string } `json:"table_revision"`
		Outcome       string                         `json:"outcome"`
		Selected      int                            `json:"selected"`
		Guards        int                            `json:"guards"`
		Changed       int                            `json:"changed"`
		Event         string                         `json:"event"`
		Replay        bool                           `json:"replay"`
		Trips         int                            `json:"trips"`
		Members       []struct {
			ID             string `json:"id"`
			Place          struct{ Before, After *string }
			Score          struct{ Before, After *string }
			MemberRevision struct{ Before, After string } `json:"member_revision"`
			Fields         map[string][2]*string          `json:"fields"`
		} `json:"members"`
	}
	run := func() receipt {
		code, stdout, stderr := runTable("batch", "--redis", addr, "--json", manifest)
		require.EqualValues(t, 0, code, "exit %d: %s", code, stderr)
		var r receipt
		{
			err := json.Unmarshal([]byte(stdout), &r)
			require.NoError(t, err, "not JSON: %v\n%s", err, stdout)
		}
		return r
	}
	first, again := run(), run()
	assert.False(t, first.Replay, "replay: first %v, second %v; want false then true", first.Replay, again.Replay)
	assert.True(t, again.Replay, "replay: first %v, second %v; want false then true", first.Replay, again.Replay)
	assert.Equal(t, again.Event, first.Event, "receipts: %+v %+v", first, again)
	assert.Equal(t, again.TableRevision, first.TableRevision, "receipts: %+v %+v", first, again)
	assert.Equal(t, "demo", first.Table, "receipts: %+v %+v", first, again)
	assert.Equal(t, "json-1", first.OperationID, "receipts: %+v %+v", first, again)
	assert.EqualValues(t, 2, first.Selected, "receipts: %+v %+v", first, again)
	assert.EqualValues(t, 2, first.Changed, "receipts: %+v %+v", first, again)
	assert.EqualValues(t, 0, first.Guards, "receipts: %+v %+v", first, again)
	assert.EqualValues(t, 1, first.Trips, "receipts: %+v %+v", first, again)
	assert.EqualValues(t, 1, again.Trips, "receipts: %+v %+v", first, again)
	assert.Equal(t, "changed", first.Outcome, "receipts: %+v %+v", first, again)
	m1 := first.Members[0]
	assert.Equal(t, "m1", m1.ID, "m1: %+v", m1)
	assert.Equal(t, "build:ready", *m1.Place.Before, "m1: %+v", m1)
	assert.Equal(t, "build:working", *m1.Place.After, "m1: %+v", m1)
	assert.Equal(t, "1", *m1.Score.Before, "m1: %+v", m1)
	assert.Equal(t, "2.5", *m1.Score.After, "m1: %+v", m1)
	assert.Equal(t, "2", m1.MemberRevision.After, "m1: %+v", m1)
	assert.Equal(t, "builder", *m1.Fields["role"][0], "m1: %+v", m1)
	assert.Equal(t, "lead", *m1.Fields["role"][1], "m1: %+v", m1)
	{
		m2 := first.Members[1]
		assert.Nil(t, m2.Place.Before, "m2: %+v", m2)
		assert.Equal(t, "build:done", *m2.Place.After, "m2: %+v", m2)
		assert.Nil(t, m2.Score.Before, "m2: %+v", m2)
		assert.Equal(t, "7", *m2.Score.After, "m2: %+v", m2)
	}
	// the text form says so too
	code, stdout, _ := runTable("batch", "--redis", addr, manifest)
	assert.EqualValues(t, 0, code, "text replay: %d %s", code, stdout)
	assert.Contains(t, stdout, "replay=yes", "text replay: %d %s", code, stdout)
}
