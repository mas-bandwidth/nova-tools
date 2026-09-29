//go:build functional

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMemberReadPrintsPlaceScoreRevisionFieldsAndTheMissing(t *testing.T) {
	t.Parallel()
	addr, _ := batchFixture(t) // m1 at build:ready, revision 1, role=builder
	if code, _, stderr := runTable("member", "create", "--redis", addr, "demo", "loose"); code != 0 {
		t.Fatal(stderr)
	}
	code, stdout, stderr := runTable("member", "read", "--redis", addr, "demo", "m1", "loose", "zz")
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, w := range []string{
		"TABLE READ table=demo epoch=0 table_revision=4 members=2 missing=1 trips=1\n",
		`MEMBER m1 place=build:ready score=1 member_revision=1 fields={"role":"builder"}` + "\n",
		"MEMBER loose place=- score=- member_revision=1 fields={}\n",
		"MISSING zz\n",
	} {
		if !strings.Contains(stdout, w) {
			t.Errorf("stdout lacks %q:\n%s", w, stdout)
		}
	}
	// a whole cell, and a cell that is empty
	code, stdout, stderr = runTable("member", "read", "--redis", addr, "demo", "--cell", "build:ready", "--cell", "build:done")
	if code != 0 || !strings.Contains(stdout, "members=1 missing=0") || !strings.Contains(stdout, "MEMBER m1 ") {
		t.Errorf("--cell: exit %d\n%s%s", code, stdout, stderr)
	}
	// JSON for a program
	code, stdout, stderr = runTable("member", "read", "--redis", addr, "--json", "demo", "m1", "zz")
	if code != 0 {
		t.Fatal(stderr)
	}
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
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if got.Table != "demo" || got.Epoch != "0" || got.TableRevision != "4" || got.Trips != 1 || len(got.Missing) != 1 || got.Missing[0] != "zz" ||
		len(got.Members) != 1 || got.Members[0].ID != "m1" || *got.Members[0].Place != "build:ready" || *got.Members[0].Score != "1" ||
		got.Members[0].MemberRevision != "1" || got.Members[0].Fields["role"] != "builder" {
		t.Errorf("JSON: %+v", got)
	}
	// usage
	for _, args := range [][]string{{"member", "read", "--redis", addr, "demo"}, {"member", "read", "--redis", addr, "demo", "m1", "--cell", "build:ready"}, {"member", "read", "--redis", addr, "demo", "--cell", "nocolon"}} {
		if code, _, stderr := runTable(args...); code != 2 || !strings.Contains(stderr, "member read") {
			t.Errorf("%v: exit %d %s", args, code, stderr)
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
		if code != 0 {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		var r receipt
		if err := json.Unmarshal([]byte(stdout), &r); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, stdout)
		}
		return r
	}
	first, again := run(), run()
	if first.Replay || !again.Replay {
		t.Errorf("replay: first %v, second %v; want false then true", first.Replay, again.Replay)
	}
	if first.Event != again.Event || first.TableRevision != again.TableRevision || first.Table != "demo" || first.OperationID != "json-1" ||
		first.Selected != 2 || first.Changed != 2 || first.Guards != 0 || first.Trips != 1 || again.Trips != 1 || first.Outcome != "changed" {
		t.Errorf("receipts: %+v %+v", first, again)
	}
	m1 := first.Members[0]
	if m1.ID != "m1" || *m1.Place.Before != "build:ready" || *m1.Place.After != "build:working" || *m1.Score.Before != "1" || *m1.Score.After != "2.5" ||
		m1.MemberRevision.After != "2" || *m1.Fields["role"][0] != "builder" || *m1.Fields["role"][1] != "lead" {
		t.Errorf("m1: %+v", m1)
	}
	if m2 := first.Members[1]; m2.Place.Before != nil || *m2.Place.After != "build:done" || m2.Score.Before != nil || *m2.Score.After != "7" {
		t.Errorf("m2: %+v", m2)
	}
	// the text form says so too
	code, stdout, _ := runTable("batch", "--redis", addr, manifest)
	if code != 0 || !strings.Contains(stdout, "replay=yes") {
		t.Errorf("text replay: %d %s", code, stdout)
	}
}
