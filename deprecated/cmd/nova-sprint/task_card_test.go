package main

import (
	"testing"
)

func TestTaskCardDispatch(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"push", "--actor", "rowan", "--id", "x"}, true},
		{[]string{"push", "--id", "x", "--to", "a"}, false},
		{[]string{"take", "--actor", "rowan"}, true},
		{[]string{"take", "--as", "a"}, false},
		{[]string{"done", "--actor", "rowan", "--id", "x", "--evidence", "e"}, true},
		{[]string{"done", "--actor", "rowan", "--id", "x", "--token", "t"}, false},
		{[]string{"cancel", "--actor", "rowan", "--ids", "a,b"}, false},
		{[]string{"cancel", "--actor", "rowan", "--id", "x", "--why", "w"}, true},
		{[]string{"move", "--id", "x", "--to", "b", "--actor", "a"}, false},
		{[]string{"move", "--id", "x", "--to-friend", "b", "--actor", "a"}, true},
		{[]string{"front", "--id", "x", "--as", "a", "--actor", "a"}, false},
		{[]string{"land", "--id", "x"}, true},
		{[]string{"fsck"}, true},
		{[]string{"ls"}, true},
		{[]string{"list", "--actor", "a"}, false},
	} {
		if got := isTaskCard(tc.args[0], tc.args[1:]); got != tc.want {
			t.Errorf("isTaskCard(%v) = %v, want %v", tc.args, got, tc.want)
		}
	}
}
