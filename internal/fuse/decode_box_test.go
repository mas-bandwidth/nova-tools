package fuse

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadBoxRejectsNoncanonicalOrRepeatedTopLevelKeys(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"lockdown alias after":        `{"lockdown":{"at":"x","reason":"stop"},"Lockdown":null}`,
		"lockdown alias before":       `{"Lockdown":null,"lockdown":{"at":"x","reason":"stop"}}`,
		"lockdown alias alone":        `{"LOCKDOWN":null}`,
		"mixed case alias":            `{"lockdown":{"at":"x","reason":"stop"},"lOcKdOwN":null}`,
		"escaped case alias":          `{"lockdown":{"at":"x","reason":"stop"},"\u004cockdown":null}`,
		"unicode case alias":          `{"lockdown":{"at":"x","reason":"stop"},"loc\u212Adown":null}`,
		"quarantine alias after":      `{"quarantine":{"s":{"at":"x","reason":"stop"}},"Quarantine":null}`,
		"quarantine alias before":     `{"Quarantine":null,"quarantine":{"s":{"at":"x","reason":"stop"}}}`,
		"quarantine alias alone":      `{"QUARANTINE":null}`,
		"unknown key alone":           `{"other":null}`,
		"unknown key after":           `{"lockdown":{"at":"x","reason":"stop"},"other":null}`,
		"unknown key before":          `{"other":null,"lockdown":{"at":"x","reason":"stop"}}`,
		"duplicate lockdown after":    `{"lockdown":{"at":"x","reason":"stop"},"lockdown":null}`,
		"duplicate lockdown before":   `{"lockdown":null,"lockdown":{"at":"x","reason":"stop"}}`,
		"escaped duplicate lockdown":  `{"lockdown":{"at":"x","reason":"stop"},"lock\u0064own":null}`,
		"duplicate quarantine after":  `{"quarantine":{"s":{"at":"x","reason":"stop"}},"quarantine":null}`,
		"duplicate quarantine before": `{"quarantine":null,"quarantine":{"s":{"at":"x","reason":"stop"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := boxIn(t)
			write(t, path, body)
			_, err := ReadBox(path)
			assert.Error(t, err, "an ambiguous or unknown top-level key cannot prove a box clear")
		})
	}
}

func TestReadBoxRetainsCanonicalAndNullFieldCompatibility(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body           string
		lockdown, quarantine bool
	}{
		{"empty object", `{}`, false, false},
		{"null lockdown", `{"lockdown":null}`, false, false},
		{"null quarantine", `{"quarantine":null}`, false, false},
		{"both null fields", `{"lockdown":null,"quarantine":null}`, false, false},
		{"lowercase lockdown", `{"lockdown":{"at":"x","reason":"stop"}}`, true, false},
		{"escaped lowercase lockdown", `{"lock\u0064own":{"at":"x","reason":"stop"}}`, true, false},
		{"lowercase quarantine", `{"quarantine":{"S":{"at":"x","reason":"stop"}}}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := boxIn(t)
			write(t, path, tc.body)
			box, err := ReadBox(path)
			require.NoError(t, err)
			require.NotNil(t, box.Quarantine)
			_, _, blocked := box.Quarantined("s")
			assert.Equal(t, tc.quarantine, blocked)
			if tc.lockdown {
				require.NotNil(t, box.Lockdown)
				assert.Equal(t, Fuse{At: "x", Reason: "stop"}, *box.Lockdown)
			} else {
				assert.Nil(t, box.Lockdown)
			}
			if tc.quarantine {
				assert.Equal(t, Fuse{At: "x", Reason: "stop"}, box.Quarantine["S"])
			} else {
				assert.Empty(t, box.Quarantine)
			}
		})
	}
}

// Note 2: nested aliases and duplicates can change the audit fields, but cannot
// turn a present lockdown or quarantine entry into a verified clear answer.
func TestNestedAliasesAndDuplicatesCannotClearABlownFuse(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body string
		lockdown   bool
	}{
		{"lockdown reason alias after", `{"lockdown":{"reason":"stop","Reason":""}}`, true},
		{"lockdown reason alias before", `{"lockdown":{"Reason":"","reason":"stop"}}`, true},
		{"lockdown duplicate reason", `{"lockdown":{"reason":"stop","reason":""}}`, true},
		{"lockdown timestamp alias", `{"lockdown":{"at":"x","At":null,"reason":"stop"}}`, true},
		{"quarantine reason alias", `{"quarantine":{"s":{"reason":"stop","Reason":""}}}`, false},
		{"quarantine duplicate reason", `{"quarantine":{"s":{"reason":"stop","reason":""}}}`, false},
		{"duplicate surface null after", `{"quarantine":{"s":{"reason":"stop"},"s":null}}`, false},
		{"duplicate surface null before", `{"quarantine":{"s":null,"s":{"reason":"stop"}}}`, false},
		{"escaped duplicate surface", `{"quarantine":{"s":{"reason":"stop"},"\u0073":null}}`, false},
		{"fold equivalent surfaces", `{"quarantine":{"s":{"reason":"stop"},"S":null}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := boxIn(t)
			write(t, path, tc.body)
			box, err := ReadBox(path)
			if err != nil {
				return // CANNOT TELL is also fail-closed if nested parsing becomes stricter.
			}
			if tc.lockdown {
				assert.NotNil(t, box.Lockdown)
			} else {
				_, _, blocked := box.Quarantined("s")
				assert.True(t, blocked)
			}
		})
	}
}
