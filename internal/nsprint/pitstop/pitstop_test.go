package pitstop_test

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pitstop"
)

// TestReadAndLineOnMiniredis: status is one HGETALL, so it runs on miniredis.
func TestReadAndLineOnMiniredis(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	m := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: m.Addr()})
	t.Cleanup(func() { _ = c.Close() })

	stop, err := pitstop.Read(ctx, c, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if stop.Set || stop.Line() != "PITSTOP sprint=s1 none" {
		t.Fatalf("absent key: %+v %q", stop, stop.Line())
	}
	if got := pitstop.Key("s1"); got != "s:s1:pitstop" {
		t.Fatalf("Key = %q", got)
	}
	c.HSet(ctx, pitstop.Key("s1"), "by", "glenn", "why", "rest tonight\nnow", "at", "1790000000000")
	stop, err = pitstop.Read(ctx, c, "s1")
	if err != nil {
		t.Fatal(err)
	}
	want := `PITSTOP sprint=s1 set by=glenn at=1790000000000 (2026-09-21T14:13:20Z) why="rest tonight\nnow"`
	if !stop.Set || stop.By != "glenn" || stop.At != 1790000000000 || stop.Line() != want {
		t.Fatalf("set key: %+v\n got %q\nwant %q", stop, stop.Line(), want)
	}
	if strings.Contains(stop.Line(), "\n") {
		t.Fatal("status line is more than one line")
	}
}

// TestInScopeFromHash: the Go reader answers the question the Lua clear
// asks (NS.pitstop.in_scope): a hash with no scope field (written before
// scope) or scope=all holds every stream it has not lifted; scope=streams
// holds only the stream:<name> fields; no stop holds nothing.
func TestInScopeFromHash(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		h    map[string]string
		in   []string
		out  []string
		line string
	}{
		{"none", nil, nil, []string{"a"}, "PITSTOP sprint=s1 none"},
		{"legacy", map[string]string{"by": "g", "why": "w", "at": "1"}, []string{"a", "b c"}, nil, ""},
		{"all lifted", map[string]string{"by": "g", "at": "1", "scope": "all", "lifted:a": "2"}, []string{"b c"}, []string{"a"}, ` lifted="a"`},
		{"streams", map[string]string{"by": "g", "at": "1", "scope": "streams", "stream:b c": "1", "stream:a": "1", "lifted:x": "1"},
			[]string{"a", "b c"}, []string{"x", "d"}, ` streams="a","b c"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := pitstop.FromHash("s1", tc.h)
			for _, x := range tc.in {
				if !s.InScope(x) {
					t.Errorf("%q not held", x)
				}
			}
			for _, x := range tc.out {
				if s.InScope(x) {
					t.Errorf("%q held", x)
				}
			}
			if !strings.HasSuffix(s.Line(), tc.line) {
				t.Errorf("line %q, want suffix %q", s.Line(), tc.line)
			}
		})
	}
}
