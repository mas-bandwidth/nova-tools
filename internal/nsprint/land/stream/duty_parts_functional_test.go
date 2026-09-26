//go:build functional

package stream

import (
	"context"
	"testing"
)

// TestMembersCountReadPostLines (nova-tools #3898): a SCORE line only in
// pr:<name>:<n>:lines (where read post stores it) makes the member landable;
// the same line in both places counts once.
func TestMembersCountReadPostLines(t *testing.T) {
	t.Parallel()

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
