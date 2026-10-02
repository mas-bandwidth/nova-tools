package main

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
)

// A failed log poll cannot satisfy inbox --wait: a subsequent ordinary inbox
// read may succeed, but says nothing about whether the requested tick ended.
func TestAnInboxWaitSurfacesItsFailedLogPoll(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"transport", "refused", "malformed"} {
		for _, initial := range []bool{true, false} {
			name := failure + "/after-valid-poll"
			if initial {
				name = failure + "/initial"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				a := newApp(func(k string) string {
					if k == ServerEnv {
						return "127.0.0.1:6390"
					}
					return ""
				})
				t.Cleanup(a.close)
				now := time.Unix(100, 0)
				a.now = func() time.Time { return now }
				a.sleep = func(d time.Duration) { now = now.Add(d) }
				polls, inboxes := 0, 0
				a.forward = func(_ context.Context, _ string, verbs ...[]string) ([]sprintwire.Result, error) {
					require.Len(t, verbs, 1)
					if verbs[0][0] == "inbox" {
						inboxes++
						return []sprintwire.Result{{Stdout: "{\"groups\":[]}\n"}}, nil
					}
					require.Equal(t, "log", verbs[0][0])
					polls++
					if !initial && polls == 1 {
						return []sprintwire.Result{{Stdout: "{\"lines\":[]}\n"}}, nil
					}
					switch failure {
					case "transport":
						return nil, errors.New("log poll unavailable")
					case "refused":
						return []sprintwire.Result{{Code: 2, Stderr: "log poll refused\n"}}, nil
					default:
						return []sprintwire.Result{{Stdout: "invalid log JSON"}}, nil
					}
				}
				var out, errs bytes.Buffer
				code := a.run([]string{"inbox", "--wait", "--timeout", "1m", "--json"}, &out, &errs)
				assert.NotZero(t, code, "a failed poll did not complete the wait")
				assert.NotEmpty(t, errs.String(), "the failed poll is visible")
				assert.Empty(t, out.String(), "no successful inbox or woke result masks the failure")
				assert.Zero(t, inboxes, "do not fall back to an ordinary inbox after losing the wait")
			})
		}
	}
}
