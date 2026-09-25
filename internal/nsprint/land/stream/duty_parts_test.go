package stream

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/line"
)

// TestBuildParkConflictsLandsTheRest (nova-tools #3898): with ParkConflicts
// a member whose merge conflicts is parked conflict:<files> and the batch
// goes on without it; with the red member too, the bisect never re-merges
// the parked one.
func TestBuildParkConflictsLandsTheRest(t *testing.T) {
	f := newFixture(t)
	b := build(f, t.TempDir(), testCmd)
	b.ParkConflicts = true
	res, err := b.Run(context.Background(), members(f, 1, 3, 4, 2, 5))
	if err != nil {
		t.Fatal(err)
	}
	if res.Conflict != nil || res.BaseRed {
		t.Fatalf("result %+v", res)
	}
	if len(res.Kept) != 3 || res.Kept[0].N != 1 || res.Kept[1].N != 3 || res.Kept[2].N != 5 {
		t.Fatalf("kept %+v, want #1 #3 #5", res.Kept)
	}
	if len(res.Parked) != 2 || res.Parked[0].N != 4 || res.Parked[0].Why != "conflict:a.txt" || res.Parked[0].Task != "t4" ||
		res.Parked[1].N != 2 || res.Parked[1].Why != "red:batch-test" {
		t.Fatalf("parked %+v, want #4 conflict:a.txt then #2 red", res.Parked)
	}
}

// TestMembersCountReadPostLines (nova-tools #3898, #3874): a SCORE line
// read post stores (one ns_line_post call, the line record at the head) makes
// the member landable, and a reader's line stored twice counts once: the
// line records are the one read record.
func TestMembersCountReadPostLines(t *testing.T) {
	c := newRedis(t)
	ctx := context.Background()
	head1, head2 := "1111111111111111111111111111111111111111", "2222222222222222222222222222222222222222"
	seed(t, c, 1, head1, 100)
	seed(t, c, 2, head2, 200, score("emma", head2, 9))
	for _, p := range []struct {
		n    string
		text string
	}{{"1", score("stella", head1, 9)}, {"2", score("emma", head2, 9)}} {
		if got, err := line.Post(ctx, c, repo, p.n, p.text, line.Scope{}); err != nil || got.Status != "POSTED" {
			t.Fatalf("post #%s: %+v %v", p.n, got, err)
		}
	}
	got, skips, err := Members(ctx, c, repo, []string{strm}, 8)
	if err != nil || len(skips) != 0 || len(got) != 2 || got[0].N != 1 || got[0].Who != "stella" {
		t.Fatalf("members %+v skips %+v err %v", got, skips, err)
	}
	recs, _ := LoadPRs(ctx, c, repo, []int{2})
	if len(recs[0].Reads) != 1 {
		t.Fatalf("the line stored twice counted %d times", len(recs[0].Reads))
	}
}
