//go:build functional

package main

import (
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// show is the record of the table: a cell that cannot be read prints as ?, and
// show says which key holds what, on stderr, and exits 1. render and watch keep
// drawing the ? and exit 0, as documented.
func TestShowNamesAnUnreadableCellAndExitsNonZero(t *testing.T) {
	t.Parallel()
	addr, _ := batchFixture(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	if err := c.Set(t.Context(), "table:demo:cell:build:working", "s", 0).Err(); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runTable("show", "--redis", addr, "demo")
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "TABLE ROW table=demo row=build ready=1 working=? done=0") {
		t.Errorf("show does not draw the unread cell as ?:\n%s", stdout)
	}
	for _, w := range []string{`table "demo" row "build" column "working" cannot be read: key table:demo:cell:build:working is string, expected zset`, "1 cell(s) printed as ?", "; run: nova-table check 'demo'"} {
		if !strings.Contains(stderr, w) {
			t.Errorf("stderr lacks %q:\n%s", w, stderr)
		}
	}
	if code, _, _ := runTable("render", "--redis", addr, "demo"); code != 0 {
		t.Errorf("render exits %d; it draws the ? and exits 0", code)
	}
	// a sound table is untouched
	if err := c.Del(t.Context(), "table:demo:cell:build:working").Err(); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runTable("show", "--redis", addr, "demo"); code != 0 || stderr != "" {
		t.Errorf("show of a sound table: exit %d %s", code, stderr)
	}
}
