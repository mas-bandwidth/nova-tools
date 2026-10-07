package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
)

// Cold review of 1122805e (the answer is gzip for a client that takes it): the
// shell reads Accept-Encoding by substring, so a client that names gzip only
// to refuse it (q=0, RFC 9110 section 12.5.3: "not acceptable") is answered in
// gzip all the same, and a client that cannot read it gets bytes it did not
// ask for.
func TestTheShellAnswersPlainToAClientThatRefusesGzip(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, twoLanes()...)
	for _, ae := range []string{"gzip;q=0", "identity, gzip;q=0", "gzip; q=0.000"} {
		req := httptest.NewRequest(http.MethodPost, sprintwire.Path, strings.NewReader(`{"verbs":[["queue","--as","m1","--json"]]}`))
		req.Header.Set("Accept-Encoding", ae)
		rec := httptest.NewRecorder()
		r.a.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, ae)
		assert.Empty(t, rec.Header().Get("Content-Encoding"), "Accept-Encoding: %s refuses gzip", ae)
		assert.True(t, strings.HasPrefix(rec.Body.String(), "{"), "Accept-Encoding: %s: the answer is plain JSON", ae)
	}
}
