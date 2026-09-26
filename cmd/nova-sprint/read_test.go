package main

import (
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// TestReadUsageRefusalsOpenNoStore: every usage error is exit 2 on stderr
// before Redis is touched.
func TestReadUsageRefusalsOpenNoStore(t *testing.T) {
	t.Setenv(store.UserEnv, "")
	t.Setenv("NOVA_SPRINT_REDIS", "")
	t.Setenv("NOVA_REDIS_ADDR", "")
	mr := miniredis.RunT(t)
	for _, args := range [][]string{
		{"read"},
		{"read", "brief"},
		{"read", "brief", "--repo", "mas-bandwidth/nova-tools/x", "--n", "3", "--out", "d", "--redis", mr.Addr()},
		{"read", "brief", "--repo", "nova-tools", "--n", "x", "--out", "d", "--redis", mr.Addr()},
		{"read", "brief", "--repo", "nova-tools", "--n", "3", "--out", "d"},
		{"read", "post", "--repo", "nova-tools", "--n", "3", "--redis", mr.Addr()},
		{"read", "post", "--repo", "nova-tools", "--n", "3", "--line", "SCORE who=r head=abcdef0 score=9/10", "--out", "d", "--redis", mr.Addr()},
		{"read", "lines", "--repo", "nova-tools", "--n", "3", "--redis", mr.Addr()},
		{"read", "brief", "--repo", "nova-tools", "--n", "3", "--out", "d", "--redis", mr.Addr(), "extra"},
		{"read", "brief", "--ids", "read-3-x", "--repo", "nova-tools", "--out", "d", "--redis", mr.Addr()},
		{"read", "post", "--ids", "read-3-x", "--line", "SCORE who=r head=abcdef0 score=9/10", "--redis", mr.Addr()},
		{"read", "brief", "--ids", "read-3-x", "--out", "d"},
		// #4335: brief --pr and post --file
		{"read", "brief", "--pr", "7"},
		{"read", "brief", "--pr", "7", "--n", "3", "--redis", mr.Addr()},
		{"read", "brief", "--pr", "7", "--out", "d", "--redis", mr.Addr()},
		{"read", "brief", "--pr", "x", "--redis", mr.Addr()},
		{"read", "brief", "--pr", "7", "--repo", "a/b/c", "--redis", mr.Addr()},
		{"read", "brief", "--issue", "4335", "--redis", mr.Addr()},
		{"read", "post", "--pr", "7", "--redis", mr.Addr()},
		{"read", "brief", "--file", "s.tsv", "--redis", mr.Addr()},
		{"read", "post", "--file", "s.tsv"},
		{"read", "post", "--file", "s.tsv", "--line", "SCORE who=r head=abcdef0 score=9/10", "--redis", mr.Addr()},
		{"read", "post", "--file", "s.tsv", "--repo", "nova-tools", "--redis", mr.Addr()},
	} {
		code, stdout, stderr := runSprint(args...)
		if code != 2 || stdout != "" || !strings.HasPrefix(stderr, "nova-sprint read") {
			t.Fatalf("%v: exit %d stdout %q stderr %q; want 2 and a usage line", args, code, stdout, stderr)
		}
	}
	if n := mr.TotalConnectionCount(); n != 0 {
		t.Fatalf("%d store connection(s) on usage errors", n)
	}
}
