//go:build functional

package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/stretchr/testify/assert"
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
				{
					want := "nova-redis " + verb[0] + ": " + c.refusal
					if assert.Equal(t, 2, code, "%s, %q: exit %d stderr %q connections %d; want exit 2, %q and none", c.name, args, code, errb.String(), mr.TotalConnectionCount(), want) {
						if assert.Equal(t, want, errb.String(), "%s, %q: exit %d stderr %q connections %d; want exit 2, %q and none", c.name, args, code, errb.String(), mr.TotalConnectionCount(), want) {
							assert.Zero(t, mr.TotalConnectionCount(), "%s, %q: exit %d stderr %q connections %d; want exit 2, %q and none", c.name, args, code, errb.String(), mr.TotalConnectionCount(), want)
						}
					}
				}
			case verb[0] == "spill":
				if assert.Zero(t, code, "%s, %q: exit %d stdout %q stderr %q; want SPILL OK as %s", c.name, args, code, out.String(), errb.String(), c.want) {
					assert.True(t, strings.HasPrefix(out.String(), "SPILL OK "), "%s, %q: exit %d stdout %q stderr %q; want SPILL OK as %s", c.name, args, code, out.String(), errb.String(), c.want)
				}
			default:
				if assert.Equal(t, 1, code, "%s, %q: exit %d stdout %q stderr %q; want RECALL MISSING as %s", c.name, args, code, out.String(), errb.String(), c.want) {
					assert.True(t, strings.HasPrefix(out.String(), "RECALL MISSING "), "%s, %q: exit %d stdout %q stderr %q; want RECALL MISSING as %s", c.name, args, code, out.String(), errb.String(), c.want)
				}
			}
		}
	}
}
