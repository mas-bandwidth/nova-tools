package pulse

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestStatusCountsAGatewayDeathApartFromCardFailures is #2634 part 2: a gateway
// death — no model turn, gateway 5xx or no tokens and no error on a failed
// attempt — increments gateway and does not increment card_fail. A card's own
// abstain, a 5xx after the model had a turn, and a wall death stay card failures.
func TestStatusCountsAGatewayDeathApartFromCardFailures(t *testing.T) {
	const gateway5xx = "Error: {\"name\":\"UnknownError\",\"data\":{\"message\":\"Unexpected server error. Check server logs for details.\",\"ref\":\"err_abc12345\"}}\n"
	cases := []struct {
		name    string
		card    int
		gateway int
		files   map[string]string
	}{
		{
			name:    "gateway 5xx",
			gateway: 1,
			files: map[string]string{
				filepath.Join("1", "jobs", "gw", "usage.tsv"):          usage13("gw", "1", "-", "-"),
				filepath.Join("1", "jobs", "gw", "harness-output.log"): gateway5xx,
			},
		},
		{
			name:    "no tokens and no error",
			gateway: 1,
			files: map[string]string{
				filepath.Join("1", "jobs", "quiet", "usage.tsv"): usage13("quiet", "1", "0", "0"),
			},
		},
		{
			name: "card abstain",
			card: 1,
			files: map[string]string{
				filepath.Join("1", "jobs", "card", "usage.tsv"): usage13("card", "0", "100", "20"),
				filepath.Join("1", "jobs", "card", "RESULT.md"): "RESULT card sha=abc\nABSTAIN the card stopped\n",
			},
		},
		{
			name: "5xx after a model turn",
			card: 1,
			files: map[string]string{
				filepath.Join("1", "jobs", "ran", "usage.tsv"):          usage13("ran", "1", "10", "4"),
				filepath.Join("1", "jobs", "ran", "harness-output.log"): gateway5xx,
			},
		},
		{
			name:    "usage end=provider",
			gateway: 1,
			files: map[string]string{
				filepath.Join("1", "jobs", "prov", "usage.tsv"): usage16("prov", "provider", "1", "0", "0"),
			},
		},
		{
			name: "wall death is not a gateway death",
			card: 1,
			files: map[string]string{
				filepath.Join("1", "jobs", "wall", "usage.tsv"): usage16("wall", "wall", "1", "0", "0"),
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "bench")
			for rel, body := range c.files {
				writeStatusFile(t, root, rel, body)
			}
			queue, roots, specs, now := setupStatus(t, root)
			fakeGh(t, specs, "[]")
			writeStatusFile(t, queue, "COORDINATOR", "glenn\n")

			out, errs, code := runStatus(t, queue, roots, now, 20)
			if code != 0 {
				t.Fatalf("exit %d\n%s\n%s", code, out, errs)
			}
			want := fmt.Sprintf("STATUS FAILURES card_fail=%d gateway=%d", c.card, c.gateway)
			if !strings.Contains(out, want+"\n") {
				t.Fatalf("status columns:\nwant %s\ngot\n%s", want, out)
			}

			var one, oneErr bytes.Buffer
			if code := StatusLine(StatusInput{
				Queue: queue, Roots: roots, Day: "2026-09-15",
				Stdout: &one, Stderr: &oneErr, Now: func() time.Time { return now },
			}); code != 0 {
				t.Fatalf("oneline exit %d: %s", code, oneErr.String())
			}
			if !strings.Contains(one.String(), fmt.Sprintf("card_fail=%d gateway=%d", c.card, c.gateway)) {
				t.Fatalf("oneline columns: %s", one.String())
			}

			if c.gateway == 1 && c.card == 0 {
				raw, err := os.ReadFile(filepath.Join(root, statusIndexName))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(raw), "\tgateway\t") || strings.Contains(string(raw), "\tfailed\t") {
					t.Fatalf("index class should be gateway, not a card failure:\n%s", raw)
				}
			}
		})
	}
}

func usage13(job, rc, tin, tout string) string {
	return "job\tattempt\tstarted\tended\trc\tprovider\tmodel\ttokens_in\ttokens_out\tcache_write\tcache_read\treasoning\tusd\n" +
		job + "\t1\t2026-09-15T11:40:00Z\t2026-09-15T11:40:02Z\t" + rc + "\tinception\tmercury\t" + tin + "\t" + tout + "\t-\t-\t-\t-\n"
}

func usage16(job, end, rc, tin, tout string) string {
	return "job\tattempt\tfrom\tstarted\tended\tend\trc\tprovider\tmodel\trepo\ttokens_in\ttokens_out\tcache_write\tcache_read\treasoning\tusd\n" +
		job + "\t1\t-\t2026-09-15T11:40:00Z\t2026-09-15T11:40:02Z\t" + end + "\t" + rc + "\tinception\tmercury\t-\t" + tin + "\t" + tout + "\t-\t-\t-\t-\n"
}
