//go:build functional

package healthreport

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func trustedClient(t *testing.T, s *httptest.Server) (*Client, Enrollment, *Capability) {
	t.Helper()
	e := enrolled(s.URL + Path)
	capability, err := NewCapability(e, secret())
	require.NoError(t, err)
	roots := x509.NewCertPool()
	roots.AddCert(s.Certificate())
	client, err := NewClient(capability, roots)
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return client, e, capability
}

func tlsReceiver(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	s := httptest.NewUnstartedServer(h)
	s.Config.ErrorLog = log.New(io.Discard, "", 0)
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}

func TestClientReportsOnlyToItsVerifiedAudienceAndAcceptsExactReceipt(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	var enrolledCapability atomic.Pointer[Capability]
	s := tlsReceiver(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != Path || r.TLS == nil {
			http.Error(w, "refused", 400)
			return
		}
		principal, err := enrolledCapability.Load().Authenticate(r.Header)
		if err != nil {
			http.Error(w, "refused", 401)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, MaxEvent+1))
		if err != nil {
			http.Error(w, "cut", 400)
			return
		}
		v, err := principal.ParseEvent(raw)
		if err != nil {
			http.Error(w, "refused", 403)
			return
		}
		calls.Add(1)
		// This witness tests transport/receipt binding only. No durable acceptance
		// or delivered-alarm claim is made by this stateless TLS fixture.
		_ = json.NewEncoder(w).Encode(ReceiptFor(v, raw))
	}))
	client, e, capability := trustedClient(t, s)
	enrolledCapability.Store(capability)
	event := failed(e)
	raw := encoded(t, event)
	got, err := client.Report(context.Background(), raw)
	require.NoError(t, err)
	assert.Equal(t, ReceiptFor(event, raw), got)
	assert.EqualValues(t, 1, calls.Load())
	other := event
	other.Friend = "alice"
	_, err = client.Report(context.Background(), encoded(t, other))
	require.Error(t, err)
	assert.EqualValues(t, 1, calls.Load(), "wrong identity refused before network")
	untrusted, err := NewClient(capability, nil)
	require.NoError(t, err)
	defer untrusted.Close()
	_, err = untrusted.Report(context.Background(), raw)
	require.Error(t, err)
	assert.EqualValues(t, 1, calls.Load(), "untrusted TLS must never reach handler")
	assert.NotContains(t, err.Error(), secret())
}

func TestClientDoesNotFollowRedirectsOrExposeReceiverErrorBodies(t *testing.T) {
	t.Parallel()
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	s := tlsReceiver(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
		_, _ = io.WriteString(w, secret())
	}))
	client, e, _ := trustedClient(t, s)
	_, err := client.Report(context.Background(), encoded(t, failed(e)))
	require.Error(t, err)
	assert.Zero(t, targetCalls.Load())
	assert.NotContains(t, err.Error(), secret())
}

func TestClientRejectsWrongMalformedAndOversizedAcceptanceReceipts(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`{"sequence":7,"episode":"older","digest":"x"}`,
		`{"sequence":7,"sequence":8,"episode":"episode-3","digest":"x"}`,
		`{"secret":"` + secret() + `"}`,
		string(make([]byte, MaxReceipt+1)),
	} {
		t.Run(body[:min(len(body), 12)], func(t *testing.T) {
			t.Parallel()
			s := tlsReceiver(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
			client, e, _ := trustedClient(t, s)
			_, err := client.Report(context.Background(), encoded(t, failed(e)))
			require.Error(t, err)
			assert.NotContains(t, err.Error(), secret())
		})
	}
}
