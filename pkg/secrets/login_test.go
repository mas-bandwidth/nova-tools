package secrets

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A login reads its one name from the seat in process, and every way it can fail
// is a refusal that names the next step, never an empty password.
func TestReadLoginReadsTheNameAndRefusesWhatIsMissing(t *testing.T) {
	t.Parallel()
	const pw = "Zq7xWp4Lk9mN2vB8tY"
	l := Login{Store: "/s", As: "studio", Key: "/k/studio.key", Sops: "/bin/sops", Name: "NOVA_REDIS_COORDINATOR_PASSWORD"}
	open := func(store, as, key, sops string) (SeatFile, error) {
		assert.Equal(t, []string{"/s", "studio", "/k/studio.key", "/bin/sops"}, []string{store, as, key, sops})
		return SeatFile{Path: "/s/studio.yaml", Secrets: map[string]Secret{
			"NOVA_REDIS_COORDINATOR_PASSWORD": NewSecret(pw),
			"EMPTY":                           NewSecret(""),
		}}, nil
	}

	s, err := readLogin(l, open)
	require.NoError(t, err)
	got := ""
	require.NoError(t, s.Use(func(v string) error { got = v; return nil }))
	assert.Equal(t, pw, got)
	assert.False(t, Leaks(fmt.Sprintf("%v %s %+v %#v", s, s, l, l), s), "neither the secret nor the login prints the value")

	missing := l
	missing.Name = "NOVA_REDIS_OTHER"
	_, err = readLogin(missing, open)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "holds no NOVA_REDIS_OTHER")
	assert.Contains(t, err.Error(), "run: nova-secrets names --store /s --as studio")

	empty := l
	empty.Name = "EMPTY"
	_, err = readLogin(empty, open)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "an empty value is no password")

	_, err = readLogin(l, func(string, string, string, string) (SeatFile, error) {
		return SeatFile{}, errors.New("key file /k/studio.key is absent")
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "seat studio of store /s did not open: key file /k/studio.key is absent")

	_, err = readLogin(Login{Store: "/s"}, open)
	require.Error(t, err)
	assert.Equal(t, "the login names no --as <seat>, --key <file>, --sops <path>, --secret <NAME>", err.Error())
}
