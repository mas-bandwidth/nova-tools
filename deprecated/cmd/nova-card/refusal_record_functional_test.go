//go:build functional

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestRefusalLeavesARecord is the DONE-WHEN control of #3420: a detached
// wrapper's stdout is /dev/null, so a refusal before `launched` (exit 1
// configuration, 2 could not run, 4 not dealt, 6 Redis) leaves exactly one
// line naming the card and the reason, never the token: an entry on the
// sprint log (s:<S>:log, kind "wrapper refused") when Redis is reachable,
// else an append-only <results>/refused/<S>/<label>/<attempt>.line.
func TestRefusalLeavesARecord(t *testing.T) {
	t.Parallel()

	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	down := closedAddr(t)
	ctx := context.Background()

	cases := []struct {
		name  string
		label string
		code  int
		stdin bool // the launch line on stdin; false leaves it empty
		env   map[string]string
		redis bool   // the record is on the sprint log; false: the file
		why   string // the reason the record must carry
	}{
		{"exit 1 config, redis up", "card-cfg", card.WrapperExitUsage, true,
			map[string]string{"NOVA_CARD_REDIS": addr, "NOVA_CARD_CLOCK": ""}, true, "missing or bad"},
		{"exit 1 config, no redis", "card-cfg-file", card.WrapperExitUsage, true,
			map[string]string{"NOVA_CARD_CLOCK": ""}, false, "missing or bad"},
		{"exit 2 no launch line", "card-nostdin", 2, false,
			map[string]string{"NOVA_CARD_REDIS": addr}, true, "no launch line"},
		{"exit 2 launch deadline", "card-late", card.WrapperExitCouldNot, true,
			map[string]string{"NOVA_CARD_REDIS": addr, "NOVA_CARD_LAUNCH_DEADLINE_MS": "1"}, true, "launch deadline exceeded"},
		{"exit 4 not dealt", "card-undealt", card.WrapperExitNotDealt, true,
			map[string]string{"NOVA_CARD_REDIS": addr}, true, "no such card"},
		{"exit 6 redis down", "card-down", card.WrapperExitRedis, true,
			map[string]string{"NOVA_CARD_REDIS": down}, false, "redis"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			results := t.TempDir()
			env := map[string]string{
				"NOVA_CARD_BENCH": "bench-one", "NOVA_CARD_HARNESS": "/bin/true",
				"NOVA_CARD_JOBS": t.TempDir(), "NOVA_CARD_RESULTS": results,
				"NOVA_CARD_CLOCK": "45m",
			}
			for k, v := range tc.env {
				env[k] = v // "" unsets it: getenv reads "" either way
			}
			const sprint = "s3420"
			cardName := sprint + "/" + tc.label + "/1"
			stdin := ""
			if tc.stdin {
				stdin = sprint + " " + tc.label + " 1 " + testToken + "\n"
			}
			var out, errb strings.Builder
			code := run([]string{cardName}, strings.NewReader(stdin), &out, &errb, func(k string) string { return env[k] })
			if code != tc.code {
				t.Fatalf("exit %d, want %d; stdout %q stderr %q", code, tc.code, out.String(), errb.String())
			}

			var lines []string
			entries, err := client.XRange(ctx, "s:"+sprint+":log", "-", "+").Result()
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if e.Values["id"] == tc.label {
					if e.Values["kind"] != "wrapper refused" {
						t.Fatalf("log entry kind %v, want wrapper refused: %v", e.Values["kind"], e.Values)
					}
					lines = append(lines, e.Values["line"].(string))
				}
			}
			file := filepath.Join(results, "refused", sprint, tc.label, "1.line")
			raw, ferr := os.ReadFile(file)
			if tc.redis {
				if ferr == nil {
					t.Fatalf("Redis was reachable but the file %s was written too: %q", file, raw)
				}
			} else {
				if len(lines) != 0 {
					t.Fatalf("Redis was not the record but the log carries %q", lines)
				}
				if ferr != nil {
					t.Fatalf("no refusal record: %v", ferr)
				}
				lines = strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
			}
			if len(lines) != 1 {
				t.Fatalf("want one refusal line, got %d: %q", len(lines), lines)
			}
			l := lines[0]
			if !strings.HasPrefix(l, "REFUSED nova-card "+cardName+" ") || !strings.Contains(l, tc.why) || !strings.Contains(l, "bench=bench-one") {
				t.Fatalf("refusal line %q does not name card %s, bench and why %q", l, cardName, tc.why)
			}
			if strings.Contains(l, "0123456789abcdef0123456789abcdef") {
				t.Fatalf("refusal line carries the token: %q", l)
			}
		})
	}
}
