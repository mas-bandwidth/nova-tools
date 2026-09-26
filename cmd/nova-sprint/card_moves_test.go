package main

import (
	"testing"
)

// TestCardMoveDispatch: the table moves own deal, work, land, cancel,
// expire, table, consumers and render; end and beat are theirs with --id,
// --ids or --as and the bench attempt form's with a label and --token; fsck
// is theirs with no --sprint.
func TestCardMoveDispatch(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"deal", "--to", "bench:b", "--n", "3"}, true},
		{[]string{"work", "--as", "friend:f", "--fill"}, true},
		{[]string{"end", "--ids", "p~1", "--ok"}, true},
		{[]string{"end", "lbl", "--sprint", "s", "--token", "t"}, false},
		{[]string{"beat", "--as", "bench:b", "--ids", "p~1"}, true},
		{[]string{"beat", "lbl", "--sprint", "s", "--token", "t"}, false},
		{[]string{"fsck"}, true},
		{[]string{"fsck", "--sprint", "s"}, false},
		{[]string{"push", "--sprint", "s"}, false},
		{[]string{"land", "--stream", "s", "--sha", "x"}, true},
	} {
		if got := isCardMove(tc.args[0], tc.args[1:]); got != tc.want {
			t.Errorf("isCardMove(%v) = %v, want %v", tc.args, got, tc.want)
		}
	}
}
