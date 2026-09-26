package pr_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// landing is one unit landed through the real lander functions.
type landing struct {
	batch, trainHead, mergeSHA string
}

// repoServer answers the card lint's public-repository probe on loopback.
func repoServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/acme/public" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// until reads monitor lines until one contains marker. The bound is generous;
// the test asserts the marker line arrives, not how fast.
func until(t *testing.T, ch <-chan string, marker string) []string {
	t.Helper()
	var lines []string
	deadline := time.After(2 * time.Minute)
	for {
		select {
		case line, ok := <-ch:
			if !ok {
				t.Fatalf("monitor closed before %q", marker)
			}
			if strings.Contains(line, marker) {
				return lines
			}
			lines = append(lines, line)
		case <-deadline:
			t.Fatalf("monitor never showed %q", marker)
		}
	}
}

// commandName is the first quoted token of a MONITOR line:
// +<ts> [0 127.0.0.1:port] "HGETALL" "s:..." -> HGETALL.
func commandName(line string) string {
	i := strings.Index(line, "] \"")
	if i < 0 {
		return ""
	}
	rest := line[i+3:]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}
	return strings.ToUpper(rest[:j])
}
