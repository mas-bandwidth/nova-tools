//go:build functional

package ntable_test

// The operation is looked up before the request is judged: a recorded request
// replays, and a different request under a recorded id conflicts, whatever the
// current rules say about either.

import (
	"encoding/json"
	"reflect"
	"testing"
)

// asReplay returns a raw replay reply without its marker, and says whether it had one.
func asReplay(ans []any) ([]any, bool) {
	if len(ans) == 3 && ans[2] == "REPLAY" {
		return ans[:2], true
	}
	return ans, false
}

func TestBatchReplayPrecedesTheStaticChecks(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	// A record an earlier server wrote for bytes it accepted and this server's rules refuse
	// (a score given as a string).
	recorded := manifestWith(probeRev(ctx, c), "old", `{"id":"h","expect":{"absent":true},"create":{"row":"build","col":"ready","score":"0x10"}}`)
	result := `["OK",["RECEIPT","1-0","0","3","4","changed","{}"]]`
	record, err := json.Marshal(map[string]string{"operation_id": "old", "digest": "x", "request": recorded, "stream_id": "1-0",
		"epoch": "0", "rev_before": "3", "rev_after": "4", "outcome": "changed", "result": result})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, "table:demo:ops", "0:old", string(record)).Err(); err != nil {
		t.Fatal(err)
	}

	// the recorded bytes replay
	want := []any{"OK", []any{"RECEIPT", "1-0", "0", "3", "4", "changed", "{}"}}
	before := storeImage(t, c)
	ans, err := rawApply(ctx, c, recorded)
	ans, marked := asReplay(ans)
	if err != nil || !marked || !reflect.DeepEqual(ans, want) {
		t.Errorf("replay of a recorded request that a newer rule refuses: %v %v; want the original result", trunc(ans), err)
	}
	if !reflect.DeepEqual(before, storeImage(t, c)) {
		t.Errorf("a replay wrote")
	}

	// other bytes under the recorded id conflict, even when they are also invalid
	other := manifestWith(probeRev(ctx, c), "old", `{"id":"h","expect":{"absent":true},"create":{"row":"build","col":"ready","score":"nan"}}`)
	ans, err = rawApply(ctx, c, other)
	if err != nil || len(ans) < 2 || ans[0] != "REFUSED" || ans[1] != "OPCONFLICT" {
		t.Errorf("different bytes under a recorded id: %v %v; want REFUSED OPCONFLICT", trunc(ans), err)
	}

	// an unrecorded invalid request is still refused by its rule
	fresh := manifestWith(probeRev(ctx, c), "new", `{"id":"h","expect":{"absent":true},"create":{"row":"build","col":"ready","score":"0x10"}}`)
	ans, err = rawApply(ctx, c, fresh)
	if err != nil || len(ans) < 2 || ans[0] != "REFUSED" || ans[1] != "SCORE" {
		t.Errorf("an unrecorded request with a string score: %v %v; want REFUSED SCORE", trunc(ans), err)
	}

	// a valid recorded request replays after the revisions moved on
	valid := manifestWith(probeRev(ctx, c), "ok", `{"id":"h2","expect":{"absent":true},"create":{"row":"build","col":"ready","score":3}}`)
	first, err := rawApply(ctx, c, valid)
	if err != nil || first[0] != "OK" {
		t.Fatalf("%v %v", trunc(first), err)
	}
	if _, err := rawApply(ctx, c, manifestWith(probeRev(ctx, c), "move-on", `{"id":"h3","expect":{"absent":true},"create":{"row":"build","col":"ready","score":4}}`)); err != nil {
		t.Fatal(err)
	}
	again, err := rawApply(ctx, c, valid)
	again, marked = asReplay(again)
	if err != nil || !marked || !reflect.DeepEqual(first, again) {
		t.Errorf("replay after the table moved on: %v %v", trunc(again), err)
	}
}
