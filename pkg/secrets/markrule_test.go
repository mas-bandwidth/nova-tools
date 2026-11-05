package secrets

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A seat ruled before the mark existed has no unencrypted_regex for it, so sops sealed the
// mark and the gate refused every re-seal (the security read of 2026-10-06). The verb that
// writes the file gives the rule the regex in the same commit; nothing else moves.
func TestSeatMarkRuleAdmitsTheMarkInTheSeatsRuleOnly(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, in, want string
		changed        bool
	}{
		{"a rule with no regex gains one",
			"creation_rules:\n  - path_regex: ^ada\\.yaml$\n    age: a,r\n  - path_regex: ^bo\\.yaml$\n    age: b,r\n",
			"creation_rules:\n  - path_regex: ^ada\\.yaml$\n    age: a,r\n  - path_regex: ^bo\\.yaml$\n    unencrypted_regex: ^NOVA_SECRETS_WRITTEN_BY$\n    age: b,r\n", true},
		{"a regex of its own is extended",
			"creation_rules:\n  - path_regex: ^bo\\.yaml$\n    unencrypted_regex: ^(MACHINE|HOST)$\n    age: b,r\n",
			"creation_rules:\n  - path_regex: ^bo\\.yaml$\n    unencrypted_regex: (?:^(MACHINE|HOST)$)|^NOVA_SECRETS_WRITTEN_BY$\n    age: b,r\n", true},
		{"a quoted regex stays quoted",
			"creation_rules:\n  - path_regex: ^bo\\.yaml$\n    unencrypted_regex: '^HOST$'\n    age: b,r\n",
			"creation_rules:\n  - path_regex: ^bo\\.yaml$\n    unencrypted_regex: '(?:^HOST$)|^NOVA_SECRETS_WRITTEN_BY$'\n    age: b,r\n", true},
		// A flag group at the front of the rule's own regex stays inside it: `(?i)` must
		// not make the mark key's other spellings clear (the second read of 2026-10-06).
		{"a leading flag stays in the rule's own group",
			"creation_rules:\n  - path_regex: ^bo\\.yaml$\n    unencrypted_regex: (?i)^host$\n    age: b,r\n",
			"creation_rules:\n  - path_regex: ^bo\\.yaml$\n    unencrypted_regex: (?:(?i)^host$)|^NOVA_SECRETS_WRITTEN_BY$\n    age: b,r\n", true},
		{"a rule that admits the mark is left alone",
			"creation_rules:\n  - path_regex: ^bo\\.yaml$\n    unencrypted_regex: ^NOVA_SECRETS_WRITTEN_BY$\n    age: b,r\n",
			"creation_rules:\n  - path_regex: ^bo\\.yaml$\n    unencrypted_regex: ^NOVA_SECRETS_WRITTEN_BY$\n    age: b,r\n", false},
		{"a rule for another file is no rule for this one",
			"creation_rules:\n  - path_regex: ^ada\\.yaml$\n    age: a,r\n",
			"creation_rules:\n  - path_regex: ^ada\\.yaml$\n    age: a,r\n", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, changed, err := seatMarkRule([]byte(c.in), "bo.yaml")
			require.NoError(t, err)
			assert.Equal(t, c.changed, changed)
			assert.Equal(t, c.want, string(got))
			if changed {
				cfg, err := parseSopsConfig(strings.NewReader(string(got)))
				require.NoError(t, err)
				re := regexp.MustCompile(cfg.CreationRules[len(cfg.CreationRules)-1].UnencryptedRegex)
				assert.True(t, re.MatchString(SeatMarkKey), "the mark is not admitted")
				assert.False(t, re.MatchString(strings.ToLower(SeatMarkKey)), "another spelling of the mark key is admitted")
			}
		})
	}
}
