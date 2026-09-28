// Package metricstest scrapes a metrics.Set through the handler it registers,
// so each verb's test reads /metrics the way Prometheus does.
package metricstest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/metrics"
)

// Scrape registers s on a fresh mux behind an httptest server, GETs
// metrics.Path, and returns the exposition body; any failure fails t.
func Scrape(t testing.TB, s *metrics.Set) string {
	t.Helper()
	mux := http.NewServeMux()
	s.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := http.Get(srv.URL + metrics.Path)
	if err != nil {
		t.Fatalf("scrape %s: %v", metrics.Path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("scrape %s: status %d", metrics.Path, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read scrape: %v", err)
	}
	return string(b)
}

// ScrapeAddr GETs metrics.Path from a verb's live server at addr (host:port)
// and returns the exposition body; any failure fails t. It is how an entry
// point's test reads the /metrics that verb served.
func ScrapeAddr(t testing.TB, addr string) string {
	t.Helper()
	resp, err := http.Get("http://" + addr + metrics.Path)
	if err != nil {
		t.Fatalf("scrape %s%s: %v", addr, metrics.Path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("scrape %s%s: status %d", addr, metrics.Path, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read scrape: %v", err)
	}
	return string(b)
}

// AtClose arms metrics.BeforeClose to scrape the verb's live server once, as
// it finishes, and returns a func that yields that body (empty when the verb
// never served). The hook is disarmed at the end of t.
func AtClose(t testing.TB) func() string {
	t.Helper()
	var body string
	prev := metrics.BeforeClose
	metrics.BeforeClose = func(addr string) { body = ScrapeAddr(t, addr) }
	t.Cleanup(func() { metrics.BeforeClose = prev })
	return func() string { return body }
}

// Has reports whether body carries the exact line want.
func Has(body, want string) bool { return containsLine(body, want) }

// Want fails t for every line of wants the body does not contain.
func Want(t testing.TB, body string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !containsLine(body, w) {
			t.Errorf("scrape lacks %q:\n%s", w, body)
		}
	}
}

func containsLine(body, want string) bool {
	for start := 0; start <= len(body); {
		end := start
		for end < len(body) && body[end] != '\n' {
			end++
		}
		if body[start:end] == want {
			return true
		}
		start = end + 1
	}
	return false
}
