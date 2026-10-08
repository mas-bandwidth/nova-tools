package secrets

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The mark's value is pinned to `<verb> <version>` (the security read of 2026-10-06):
// the gate checked only the verb's prefix, so any cleartext after "seal " passed, and the
// gate, check and seat inject took the mark key in the clear whatever it held. A value
// under the mark key that is not a verb's mark is a plain value, refused like any other,
// whatever the rule's unencrypted_regex says.
var markValues = []struct {
	val string
	ok  bool
}{
	{"seal dev", true},
	{"seat add v1.2.3-rc1", true},
	{"seat add 1.2.3", true},
	{"seat add 1.2.3+build.7", false},
	{"seal v1.2.3-9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08", false},
	{"seal v1.2.3-rc1234", false},
	// The version is pinned too: a shape check alone let 256 bits of hex ride in the clear.
	{"seal 9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08", false},
	{"seal v1.2.3-rc1+build.7", false},
	{"seal v1.2", false},
	{"seat inject v1.2.0", true},
	{`"seal v1.2.0"`, true},
	{"seal sk-live-0123456789 and more words", false},
	{"seal ", false},
	{"sealdev", false},
	{"seat delete v1.2.0", false},
	{"hello", false},
	{"seal v1.2.0 ", true}, // trailing space is the line's, not the value's
}

func TestTheGateReadsOnlyAPinnedMark(t *testing.T) {
	t.Parallel()
	for _, c := range markValues {
		data := []byte("GH_TOKEN: ENC[AES256_GCM,data:x,iv:a,tag:b,type:str]\n" + SeatMarkKey + ": " + c.val + "\nsops:\n    age: []\n")
		assert.Equal(t, c.ok, gateHasMark(data), "gateHasMark of %q", c.val)
		keys, err := plainValues(data, seatMarkRegex)
		require.NoError(t, err)
		if c.ok {
			assert.Empty(t, keys, "a pinned mark %q is not a plain value", c.val)
		} else {
			assert.Equal(t, []string{SeatMarkKey}, keys, "cleartext %q under the mark key is a plain value, though the rule admits the key", c.val)
		}
	}
}

func TestCheckRefusesCleartextUnderTheMarkKey(t *testing.T) {
	t.Parallel()
	rule := seatRule("rowan", pubRowan, pubRecovery)
	rule.UnencryptedRegex = seatMarkRegex
	cfg := &SopsConfig{CreationRules: []CreationRule{rule}}
	for _, c := range markValues {
		body := "GH_TOKEN: ENC[AES256_GCM,data:x,iv:a,tag:b,type:str]\n" + SeatMarkKey + ": " + c.val + "\nsops:\n    age:\n        - recipient: " + pubRowan + "\n        - recipient: " + pubRecovery + "\n"
		fails, _, clear := CheckInvariant3(storeOf(t, map[string]string{"rowan.yaml": body}), cfg, []string{"rowan.yaml"})
		if c.ok {
			assert.Empty(t, fails, "check refused the pinned mark %q", c.val)
			assert.Equal(t, 1, clear, "the mark is the one clear key")
		} else {
			assert.Equal(t, []string{"unencrypted key NOVA_SECRETS_WRITTEN_BY holds a value that is not a verb's mark"}, reasons(fails), "check of %q", c.val)
		}
	}
}

func TestSeatInjectRefusesCleartextUnderTheMarkKey(t *testing.T) {
	t.Parallel()
	sops := gateSops("  - path_regex: ^rowan\\.yaml$\n    unencrypted_regex: ^NOVA_SECRETS_WRITTEN_BY$\n    age: " + pubRowan + "," + pubRecovery + "\n")
	for _, c := range markValues {
		body := injectTargetFile([]string{pubRowan, pubRecovery}, injectSealedBody+SeatMarkKey+": "+c.val+"\n")
		store := storeOf(t, map[string]string{".sops.yaml": sops, "rowan.yaml": body})
		_, err := seatInjectTarget(store, "rowan.yaml", pubRecovery)
		if c.ok {
			assert.NoError(t, err, "seat inject refused the pinned mark %q", c.val)
		} else if assert.Error(t, err, "seat inject took %q under the mark key", c.val) {
			assert.True(t, strings.Contains(err.Error(), "not a verb's mark"), "%v", err)
		}
	}
}
