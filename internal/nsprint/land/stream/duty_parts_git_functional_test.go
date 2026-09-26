//go:build functional

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
	t.Parallel()

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
