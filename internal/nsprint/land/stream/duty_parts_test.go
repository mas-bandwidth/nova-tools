package stream

import (
	"context"
	"testing"
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

// TestMembersCountReadPostLines (nova-tools #3898): a SCORE line only in
// pr:<name>:<n>:lines (where read post stores it) makes the member landable;
// the same line in both places counts once.
func TestMembersCountReadPostLines(t *testing.T) {
	c := newRedis(t)
	ctx := context.Background()
	head1, head2 := "1111111111111111111111111111111111111111", "2222222222222222222222222222222222222222"
	seed(t, c, 1, head1, 100)
	seed(t, c, 2, head2, 200, score("emma", head2, 9))
	c.RPush(ctx, LinesKey(repo, 1), score("stella", head1, 9))
	c.RPush(ctx, LinesKey(repo, 2), score("emma", head2, 9))
	got, skips, err := Members(ctx, c, repo, []string{strm}, 8)
	if err != nil || len(skips) != 0 || len(got) != 2 || got[0].N != 1 || got[0].Who != "stella" {
		t.Fatalf("members %+v skips %+v err %v", got, skips, err)
	}
	recs, _ := LoadPRs(ctx, c, repo, []int{2})
	if len(recs[0].Reads) != 1 {
		t.Fatalf("the line in both places counted %d times", len(recs[0].Reads))
	}
}
