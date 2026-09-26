//go:build functional

package main

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// TestFriendVerbsRefuseAnUnregisteredNameAlike (#4399 round 4): friend down
// and friend up hold the same rule as friend show, in the store (one
// ns_friend_down call): a name the friends set does not hold is refused
// with the same data line, exit 1, and no friend:<f>:down is written; a
// registered friend still goes down and up.
func TestFriendVerbsRefuseAnUnregisteredNameAlike(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	c.SAdd(ctx, "friends", "ada")
	const tail = ` as=nobody why="nobody is not a registered friend" remedy="nova-sprint friend show"` + "\n"
	for _, tc := range []struct {
		args []string
		code int
		out  string
	}{
		{[]string{"friend", "down", "--as", "friend:nobody", "--why", "gone"}, 1, "FRIEND DOWN REFUSED" + tail},
		{[]string{"friend", "up", "--as", "nobody"}, 1, "FRIEND UP REFUSED" + tail},
		{[]string{"friend", "show", "--as", "nobody"}, 1, "FRIEND SHOW REFUSED" + tail},
		{[]string{"friend", "down", "--as", "friend:ada", "--why", "lunch"}, 0, "FRIEND DOWN ada\n"},
		{[]string{"friend", "up", "--as", "ada"}, 0, "FRIEND UP ada\n"},
	} {
		code, out, errOut := runSprint(append(tc.args, "--redis", addr)...)
		if code != tc.code || out != tc.out || errOut != "" {
			t.Errorf("%v: exit %d stdout %q stderr %q; want exit %d and %q", tc.args, code, out, errOut, tc.code, tc.out)
		}
	}
	if n := c.Exists(ctx, "friend:nobody:down").Val(); n != 0 {
		t.Fatal("friend down wrote friend:nobody:down for a name the registry does not hold")
	}
}
