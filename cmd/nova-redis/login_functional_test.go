//go:build functional

package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

// TestSpillAndRecallLogInAsTheUserItIsGiven: spill and recall open a store
// through redisconn and log in as each of loginCases' logins. The store is a
// miniredis that lets in only the login the case wants, so an exit 0 (spill)
// or a RECALL MISSING (recall) is that login made; a refusal opens no
// connection. The resolved options, and fn's, are the unit
// TestEveryVerbLogsInAsTheUserItIsGiven.
func TestSpillAndRecallLogInAsTheUserItIsGiven(t *testing.T) {
	t.Parallel()
	for _, c := range loginCases() {
		user, password, _ := strings.Cut(c.want, "/")
		for _, verb := range loginVerbs {
			if verb[0] == "fn" {
				continue
			}
			mr := miniredis.RunT(t)
			switch {
			case c.refusal != "":
			case user != "":
				mr.RequireUserAuth(user, password)
			default:
				mr.RequireAuth(password)
			}
			args := append(slices.Clone(verb), c.flag...)
			args[2] = mr.Addr()
			d := deps{
				now:    func() time.Time { return time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC) },
				getenv: func(k string) string { return c.env[k] },
			}
			var out, errb bytes.Buffer
			code := run(args, &out, &errb, d)
			switch {
			case c.refusal != "":
				if want := "nova-redis " + verb[0] + ": " + c.refusal; code != 2 || errb.String() != want || mr.TotalConnectionCount() != 0 {
					t.Errorf("%s, %q: exit %d stderr %q connections %d; want exit 2, %q and none", c.name, args, code, errb.String(), mr.TotalConnectionCount(), want)
				}
			case verb[0] == "spill":
				if code != 0 || !strings.HasPrefix(out.String(), "SPILL OK ") {
					t.Errorf("%s, %q: exit %d stdout %q stderr %q; want SPILL OK as %s", c.name, args, code, out.String(), errb.String(), c.want)
				}
			default:
				if code != 1 || !strings.HasPrefix(out.String(), "RECALL MISSING ") {
					t.Errorf("%s, %q: exit %d stdout %q stderr %q; want RECALL MISSING as %s", c.name, args, code, out.String(), errb.String(), c.want)
				}
			}
		}
	}
}
