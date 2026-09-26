package main

// The verb face of the runner receipt (card gh-ci-receipts) without a
// store: every refusal is one line on stderr naming the field and the
// remedy, exit 2, and nothing is dialed.

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCIGitHubFromRunnerRefusesWithOneLine(t *testing.T) {
	t.Parallel()

	sha := strings.Repeat("a", 40)
	full := []string{"github", "--from-runner", "--redis", "127.0.0.1:1", "--repo", "mas-bandwidth/nova-tools", "--sha", sha,
		"--run-id", "1", "--event", "pull_request", "--workflow", "ci", "--conclusion", "success", "--job", "lint=success"}
	without := func(flag string) []string {
		var out []string
		for i := 0; i < len(full); i++ {
			if full[i] == flag {
				i++
				continue
			}
			out = append(out, full[i])
		}
		return out
	}
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no sha", without("--sha"), "--sha wants the 40-hex head"},
		{"no repo", without("--repo"), "--repo wants owner/name"},
		{"no run id", without("--run-id"), "--run-id wants the decimal github.run_id"},
		{"no event", without("--event"), "--event wants pull_request, merge_group, push or workflow_dispatch"},
		{"no workflow", without("--workflow"), "--workflow wants the workflow's name"},
		{"no conclusion", without("--conclusion"), "--conclusion wants success, failure or cancelled"},
		{"bad job", append(without("--job"), "--job", "lint"), "--job wants <name>=<result>"},
		{"positional", append(append([]string{}, full...), "extra"), "takes flags only, nothing positional"},
	}
	if redisDefault() == "" { // the one resolver finds no address in this environment
		cases = append(cases, struct {
			name string
			args []string
			want string
		}{"no redis", without("--redis"), "needs --redis <addr> or NOVA_SPRINT_REDIS"})
	}
	for _, c := range cases {
		var out, errOut bytes.Buffer
		code := runCI(context.Background(), c.args, &out, &errOut)
		msg := errOut.String()
		if code != 2 || out.Len() != 0 || !strings.Contains(msg, c.want) || !strings.Contains(msg, "ci github --from-runner") ||
			strings.Count(msg, "\n") != 1 {
			t.Errorf("%s: exit %d stdout=%q stderr=%q, want exit 2, one line naming %q", c.name, code, out.String(), msg, c.want)
		}
	}
}
