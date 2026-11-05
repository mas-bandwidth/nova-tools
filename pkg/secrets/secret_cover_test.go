package secrets

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSecretCoverStringRedacts pins Secret.String: every value, loaded or not,
// yields Redacted and never the credential.
func TestSecretCoverStringRedacts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		s    Secret
	}{
		{"loaded", NewSecret("hunter2")},
		{"empty-but-loaded", NewSecret("")},
		{"zero-value", Secret{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, Redacted, tc.s.String())
			assert.NotContains(t, tc.s.String(), "hunter2")
		})
	}
}

// TestSecretCoverGoStringRedacts pins Secret.GoString, the route %#v takes.
func TestSecretCoverGoStringRedacts(t *testing.T) {
	t.Parallel()

	s := NewSecret("hunter2")
	assert.Equal(t, "secrets.Secret("+Redacted+")", s.GoString())
	assert.NotContains(t, s.GoString(), "hunter2")
}

// TestSecretCoverFormatRedactsEveryVerb pins Secret.Format: %v, %s, %q, %x, %d
// and %#v all close on Redacted, so no fmt verb bypasses Stringer.
func TestSecretCoverFormatRedactsEveryVerb(t *testing.T) {
	t.Parallel()

	s := NewSecret("hunter2")
	for _, verb := range []string{"%v", "%s", "%q", "%x", "%X", "%d", "%#v", "%+v"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			got := fmt.Sprintf(verb, s)
			assert.Contains(t, got, Redacted, "verb %s must yield Redacted, got %q", verb, got)
			assert.NotContains(t, got, "hunter2", "verb %s leaked the value: %q", verb, got)
		})
	}
}

// TestSecretCoverMarshalTextRedacts pins Secret.MarshalText: the encoded text is
// exactly Redacted.
func TestSecretCoverMarshalTextRedacts(t *testing.T) {
	t.Parallel()

	got, err := NewSecret("hunter2").MarshalText()
	require.NoError(t, err)
	assert.Equal(t, []byte(Redacted), got)
	assert.NotContains(t, string(got), "hunter2")
}

// TestSecretCoverMarshalJSONRedacts pins Secret.MarshalJSON and the encoding/json
// path it serves: the value encodes as the redacted word, not the credential.
func TestSecretCoverMarshalJSONRedacts(t *testing.T) {
	t.Parallel()

	s := NewSecret("hunter2")
	got, err := s.MarshalJSON()
	require.NoError(t, err)
	assert.Equal(t, `"`+Redacted+`"`, string(got))

	encoded, err := json.Marshal(s)
	require.NoError(t, err)
	assert.Equal(t, `"`+Redacted+`"`, string(encoded))
	assert.NotContains(t, string(encoded), "hunter2")
}

// TestSecretCoverUseRefusesNilFunction pins Use's one refusal: handing a secret
// to nothing is refused, not silently exposed.
func TestSecretCoverUseRefusesNilFunction(t *testing.T) {
	t.Parallel()

	err := NewSecret("hunter2").Use(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to expose a secret to nothing")
}
