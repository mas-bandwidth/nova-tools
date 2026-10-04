package decide

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestJevCoverNameNamesTheBackendWithItsModel reaches Jev.Name (jev.go:40),
// the name the record gives the backend: "jev:" and the model. A row of each
// model shape is the whole of it: Name is a pure concatenation and refuses
// nothing, so every row is the main path (SPEC-NOVA-DECIDE section 3).
func TestJevCoverNameNamesTheBackendWithItsModel(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, model, want string }{
		{"the sprint model", JevModel, "jev:jev-latest"},
		{"another model", "jev-next", "jev:jev-next"},
		{"no model set", "", "jev:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Jev{Model: tc.model}.Name())
		})
	}
}

// TestJevCoverJevHTTPWiresTheRealTransport reaches JevHTTP (jev.go:102): the
// backend carries the model constant, the record name over it, and a Send to
// the real transport, whatever the key and timeout. The Send it builds posts
// over a socket, so calling it is not a unit test: its refusals (HTTP status,
// a body that echoes the key, a transport that does not answer) are held by
// TestHTTPSendPostsWithTheKeyAndNamesAFailure over the roundTrip seam.
func TestJevCoverJevHTTPWiresTheRealTransport(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		key     string
		timeout time.Duration
	}{
		{"with a key", "k-test", JevTimeout},
		{"no key yet", "", 30 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j := JevHTTP(tc.key, tc.timeout)
			require.NotNil(t, j.Send)
			assert.Equal(t, JevModel, j.Model)
			assert.Equal(t, "jev:"+JevModel, j.Name())
		})
	}
}
