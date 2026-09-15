package swarm

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The private-repository admission tests stand an in-process round trip in for github. The
// probe's base URL is moved off the network with the NOVA_SWARM_PROBE_BASE test seam and
// probeClient is pointed at a transport that answers in memory, so no test reaches a real
// repository and none needs a listening socket (which this bench's sandbox refuses to bind).

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// useProbeTransport swaps probeClient for one that routes every request through the given
// handler and restores the production client when the test ends.
func useProbeTransport(t *testing.T, handler func(http.ResponseWriter, *http.Request)) {
	t.Helper()
	rec := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		w := &probeResponseWriter{header: make(http.Header)}
		handler(w, r)
		return &http.Response{
			StatusCode: w.status,
			Header:     w.header,
			Body:       http.NoBody,
			Request:    r,
		}, nil
	})
	old := probeClient
	probeClient = &http.Client{Timeout: probeTimeout, Transport: rec}
	t.Cleanup(func() { probeClient = old })
}

// probeResponseWriter is the sliver of http.ResponseWriter a status-only probe needs.
type probeResponseWriter struct {
	header http.Header
	status int
}

func (w *probeResponseWriter) Header() http.Header { return w.header }
func (w *probeResponseWriter) Write([]byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return 0, nil
}
func (w *probeResponseWriter) WriteHeader(code int) { w.status = code }

// admitCard admits one card and returns its label and its refusal line -- ADMIT REFUSED
// <label> <why> -- which is empty when the card was admitted. A refusal is the card's own
// (issue #529): readCards refuses the card, never the batch.
func admitCard(t *testing.T, body string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	card := writeCard(t, dir, "a.card", body)
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\t1\tmodel\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cards, err := readCards(tsv)
	if err != nil {
		t.Fatalf("the TSV is readable, got %v", err)
	}
	if len(cards) != 1 {
		t.Fatalf("one card admitted, got %d", len(cards))
	}
	if cards[0].admitWhy == "" {
		return cards[0].label, ""
	}
	return cards[0].label, admitRefusalLine(cards[0].label, cards[0].admitWhy)
}

func TestAdmitAcceptsPublicRepo(t *testing.T) {
	useProbeTransport(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("probe wants a HEAD request, got %s", r.Method)
		}
		if r.URL.Path != "/owner/public" {
			t.Errorf("probe wants /owner/public, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	})

	if _, why := admitCard(t, "RESULT: a\nREPOS: owner/public\nall green"); why != "" {
		t.Fatalf("a public repository is admitted, got: %s", why)
	}
}

func TestAdmitRefusesPrivateRepo(t *testing.T) {
	useProbeTransport(t, func(w http.ResponseWriter, r *http.Request) {
		// github answers an unauthenticated request for a private repository with 404 so
		// the repository's existence is not leaked.
		w.WriteHeader(http.StatusNotFound)
	})

	_, why := admitCard(t, "RESULT: a\nREPOS: owner/secret\nall green")
	if why == "" {
		t.Fatal("a private repository is refused at admission, got no refusal")
	}
	want := "ADMIT REFUSED a private-repo: owner/secret unreachable without auth"
	if !strings.Contains(why, want) {
		t.Fatalf("the refusal names the card, the kind and the repository:\nwant %q\ngot  %q", want, why)
	}
}

func TestAdmitRefusesWhenProbeFails(t *testing.T) {
	// A transport error stands in for a network refusal: the probe gets no response at
	// all, which is a probe failure, never a pass.
	rec := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("dial tcp: connection refused")
	})
	old := probeClient
	probeClient = &http.Client{Timeout: probeTimeout, Transport: rec}
	t.Cleanup(func() { probeClient = old })

	_, why := admitCard(t, "RESULT: a\nhttps://github.com/owner/public\nall green")
	if why == "" {
		t.Fatal("a failed probe refuses the card, got no refusal")
	}
	if !strings.Contains(why, "ADMIT REFUSED a probe:") {
		t.Fatalf("a probe failure is refused as probe:, never folded: %q", why)
	}
}
