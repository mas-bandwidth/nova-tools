package swarm

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Work-list item 9: the 429 backoff. The wait doubles from --backoff's 30-second default
// and never passes five minutes; the provider's own interval wins when it names one; and
// the 429 itself is read from the harness's text, because POSIX truncates the status to a
// number below 256 and a token count is not a rate limit.
func TestBackoffDoublesToACap(t *testing.T) {
	if got := Backoff(1, 30*time.Second); got != 30*time.Second {
		t.Errorf("the first retry waits the base, got %s", got)
	}
	if got := Backoff(2, 30*time.Second); got != time.Minute {
		t.Errorf("the second retry doubles, got %s", got)
	}
	if got := Backoff(20, 30*time.Second); got != MaxBackoff {
		t.Errorf("the doubling caps at five minutes, got %s", got)
	}
	if got := Backoff(1, 0); got != DefaultBackoff {
		t.Errorf("no base takes the 30-second default, got %s", got)
	}
}

func TestProviderRetryAfterNamesTheInterval(t *testing.T) {
	for _, c := range []struct {
		log  string
		want time.Duration
		ok   bool
	}{
		{"provider busy\nretry-after: 12\n", 12 * time.Second, true},
		{"retry-after=3", 3 * time.Second, true},
		{"no interval here", 0, false},
	} {
		got, ok := ProviderRetryAfter([]byte(c.log))
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("ProviderRetryAfter(%q) = %s, %v; want %s, %v", c.log, got, ok, c.want, c.ok)
		}
	}
}

func TestRateLimitedReadsTheStatusFromTheText(t *testing.T) {
	if !RateLimited([]byte("error: the provider answered HTTP 429 Too Many Requests")) {
		t.Error("a 429 beside a status word is a rate limit")
	}
	if !RateLimited([]byte("rate limited, try later")) {
		t.Error("the phrase is a rate limit")
	}
	if RateLimited([]byte("tokens_in=429 tokens_out=8")) {
		t.Error("a bare token count of 429 is not a rate limit")
	}
}

// Issue #263: an absent USD value is unknown, while a reported zero remains a
// measured zero. Mixed rows retain the known subtotal but identify missing
// pricing instead of presenting it as a complete total.
func TestCostPreservesUnknownUSD(t *testing.T) {
	tests := []struct {
		name        string
		usd         []string
		wantUSD     string
		wantKnown   string
		wantMissing int
	}{
		{name: "unknown-only", usd: []string{Dash, Dash}, wantUSD: "-", wantKnown: "0.0000", wantMissing: 2},
		{name: "mixed-known", usd: []string{"1.2500", Dash}, wantUSD: "1.2500", wantKnown: "1.2500", wantMissing: 1},
		{name: "measured-zero", usd: []string{"0"}, wantUSD: "0.0000", wantKnown: "0.0000", wantMissing: 0},
		{name: "all-known", usd: []string{"1.2500", "0.7500"}, wantUSD: "2.0000", wantKnown: "2.0000", wantMissing: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newCostPool(t, tt.usd)
			var stdout, stderr bytes.Buffer
			if exit := Cost(p, "", 0, "", false, &stdout, &stderr); exit != 0 {
				t.Fatalf("Cost exited %d: %s", exit, stderr.String())
			}
			output := stdout.String()
			var summary string
			for _, line := range strings.Split(output, "\n") {
				if strings.HasPrefix(line, "COST OK ") {
					if summary != "" {
						t.Fatalf("multiple summary lines:\n%s", output)
					}
					summary = line
				}
			}
			if summary == "" {
				t.Fatalf("missing COST OK summary:\n%s", output)
			}
			fields := map[string]string{}
			for _, field := range strings.Fields(summary)[2:] {
				key, value, ok := strings.Cut(field, "=")
				if ok {
					fields[key] = value
				}
			}
			for key, want := range map[string]string{
				"usd":         tt.wantUSD,
				"known_usd":   tt.wantKnown,
				"usd_missing": fmt.Sprint(tt.wantMissing),
			} {
				if got := fields[key]; got != want {
					t.Fatalf("summary %s=%q, want %q:\n%s", key, got, want, output)
				}
			}
		})
	}
}

func newCostPool(t *testing.T, usd []string) *Pool {
	t.Helper()
	p, err := OpenPool(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i, value := range usd {
		id := fmt.Sprintf("20260913T00000%dZ-cost-%d", i, i)
		row := UsageRow{
			"job": id, "attempt": "1", "started": "2026-09-13T00:00:00Z",
			"ended": "2026-09-13T00:00:01Z", "end": EndDone,
			"tokens_in": "10", "tokens_out": "5", "usd": value,
		}
		if _, _, err := p.WriteUsage(id, row); err != nil {
			t.Fatal(err)
		}
	}
	return p
}
