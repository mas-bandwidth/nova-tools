//go:build functional

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

func TestRenderAndWatchEscapeStoredControls(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	raw := "before\x1b[2Jafter\nsecond"
	for _, args := range [][]string{
		{"create", "t", "--columns", "a:count:sum:" + raw + ",note:text", "--footer", raw},
		{"row", "add", "t", "r", "--label", raw},
		{"row", "set", "t", "r", "note=" + raw},
		{"view", "set", "v", "--tables", "t", "--title", raw},
	} {
		if code, out, errout := runTable(at(addr, args...)...); code != 0 {
			t.Fatalf("setup %v: %d %s %s", args, code, out, errout)
		}
	}
	for _, args := range [][]string{{"render", "t"}, {"render", "--view", "v"}, {"watch", "--view", "v", "--once"}, {"watch", "t", "--title", raw, "--once"}} {
		code, out, errout := runTable(at(addr, args...)...)
		if code != 0 || errout != "" || strings.Contains(out, "\x1b") || strings.Contains(out, "after\nsecond") || !strings.Contains(out, `before\x1b[2Jafter\x0asecond`) {
			t.Fatalf("%v: %d %q %q", args, code, out, errout)
		}
	}
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	tab, err := ntable.Read(context.Background(), c, "t")
	if err != nil || tab.Rows[0].Texts["note"] != raw || tab.Rows[0].Label != raw || tab.FooterLabel != raw || tab.Columns[0].Label != raw {
		t.Fatalf("display changed stored data: %+v %v", tab, err)
	}
}
