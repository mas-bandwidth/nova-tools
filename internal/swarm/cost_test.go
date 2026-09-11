package swarm

import (
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
