package sprintfn

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// testLibrary is a library whose digest is its source's first bytes.
func testLibrary(src string) *Library {
	return &Library{Name: "nova_sprint", Source: func() (string, error) { return src, nil },
		Sum: func(s string) string { return "sum(" + s + ")" }}
}

// libraryConn is a fakeConn whose FUNCTION LIST answers the library it holds.
type libraryConn struct {
	*fakeConn
	code  string
	found bool
	lists int
}

func (c *libraryConn) FunctionList(ctx context.Context, q redis.FunctionListQuery) *redis.FunctionListCmd {
	c.lists++
	cmd := redis.NewFunctionListCmd(ctx)
	if c.found {
		cmd.SetVal([]redis.Library{{Name: q.LibraryNamePattern, Code: c.code}})
	} else {
		cmd.SetVal([]redis.Library{})
	}
	return cmd
}

// TestLibraryMatches: the new path's library check (the grammar decisions, 30;
// the present command's libraryMatches) passes a store that holds this build's
// nova_sprint library, and refuses one that holds none, one of another build
// (naming both digests and the command that loads it), and every store when
// this build's own library does not assemble (the sprint profile before G0).
func TestLibraryMatches(t *testing.T) {
	t.Parallel()
	const addr = "store:6379"
	lib := testLibrary("lib-a")
	if err := lib.Matches(addr, "lib-a", true, "lib-a", nil); err != nil {
		t.Fatalf("this build's library: %v", err)
	}
	for _, c := range []struct {
		code  string
		found bool
		want  []string
	}{
		{"", false, []string{"holds no nova_sprint function library", "nova-redis fn load --addr " + addr}},
		{"lib-b", true, []string{"sum(lib-b)", "sum(lib-a)", "nova-redis fn load --addr " + addr}},
	} {
		err := lib.Matches(addr, c.code, c.found, "lib-a", nil)
		for _, w := range c.want {
			if err == nil || !strings.Contains(err.Error(), w) {
				t.Fatalf("%q found=%v: %v, want %q", c.code, c.found, err, w)
			}
		}
	}
	if err := lib.Matches(addr, "lib-a", true, "", errors.New("the sprint profile is refused before G0")); err == nil || !strings.Contains(err.Error(), "does not assemble") {
		t.Fatalf("an unassembled build: %v", err)
	}
}

// TestRedisChecksTheLibraryOnceBeforeItsFirstCall: the client reads the
// store's library once, before its first pipeline, and sends nothing to a
// store whose library is not this build's; with this build's it sends, and
// does not read the library again.
func TestRedisChecksTheLibraryOnceBeforeItsFirstCall(t *testing.T) {
	t.Parallel()
	for _, found := range []bool{false, true} {
		conn := &libraryConn{fakeConn: &fakeConn{replies: []fakeReply{{val: countReply}, {val: countReply}}}, code: "other", found: found}
		r := newRedisWithClient(conn, testNames)
		r.addr, r.library = "store:6379", testLibrary("this build")
		if _, err := r.Pipeline(context.Background(), []Item{{Read: countRead()}}); err == nil || conn.pipelines != 0 || conn.lists != 1 {
			t.Fatalf("found=%v: %v, %d pipelines, %d lists; want refused, none sent, one list", found, err, conn.pipelines, conn.lists)
		}
	}
	conn := &libraryConn{fakeConn: &fakeConn{replies: []fakeReply{{val: countReply}, {val: countReply}}}, code: "this build", found: true}
	r := newRedisWithClient(conn, testNames)
	r.addr, r.library = "store:6379", testLibrary("this build")
	for i := 0; i < 2; i++ {
		if _, err := r.Pipeline(context.Background(), []Item{{Read: countRead()}}); err != nil {
			t.Fatal(err)
		}
	}
	if conn.lists != 1 || conn.pipelines != 2 {
		t.Fatalf("%d lists and %d pipelines; want 1 and 2", conn.lists, conn.pipelines)
	}
}

// countRead is a read of one cell's count.
func countRead() *ReadRequest {
	return &ReadRequest{Epoch: "0", Tset: []tset.ReadQuery{{Kind: "count", Table: "work", Cells: []string{"s1:waiting"}}}}
}
