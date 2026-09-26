package main

import (
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

// TestFriendShowMissingFriendIsData (#4399 round 3): the store answered, so
// a friend the registry does not hold is one data line on stdout and exit 1,
// with the line that lists every friend; never exit 2 with a usage tail.
func TestFriendShowMissingFriendIsData(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	code, out, errOut := runSprint("friend", "show", "--as", "rowan", "--redis", mr.Addr())
	want := `FRIEND SHOW REFUSED as=rowan why="rowan is not a registered friend" remedy="nova-sprint friend show"` + "\n"
	if code != 1 || out != want || errOut != "" || strings.Contains(out+errOut, "usage:") {
		t.Fatalf("exit %d stdout %q stderr %q; want exit 1 and %q", code, out, errOut, want)
	}
}
