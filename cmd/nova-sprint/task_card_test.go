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
		{[]string{"push", "--as", "rowan", "--ids", "x"}, true},
		{[]string{"push", "--ids", "x", "--to", "a"}, false},
		{[]string{"take"}, true},
		{[]string{"take", "--as", "a"}, true},
		{[]string{"done", "--ids", "x", "--evidence", "e"}, true},
		{[]string{"done", "--ids", "x", "--token", "t"}, false},
		{[]string{"cancel", "--ids", "x", "--why", "w"}, true},
		{[]string{"cancel", "--ids", "x", "--token", "t", "--why", "w"}, false},
		{[]string{"beat", "--ids", "x"}, true},
		{[]string{"beat", "--ids", "x", "--token", "t"}, false},
		{[]string{"move", "--ids", "x", "--to", "b"}, true},
		{[]string{"front", "--ids", "x"}, true},
		{[]string{"land", "--ids", "x"}, true},
		{[]string{"fsck"}, true},
		{[]string{"ls"}, true},
		{[]string{"list"}, false},
		{[]string{"width", "--as", "a"}, false},
	} {
		if got := isTaskCard(tc.args[0], tc.args[1:]); got != tc.want {
			t.Errorf("isTaskCard(%v) = %v, want %v", tc.args, got, tc.want)
		}
	}
}
