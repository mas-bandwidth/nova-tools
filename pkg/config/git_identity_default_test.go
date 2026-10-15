package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The sprint machine's default commit identity is general (the owner's standing rule, as
// internal/ci/generality_class_test.go and sprint_fields_test.go hold it): no host, machine,
// friend, person, account or organisation name in a shipped default. Migration 0037 carries
// the same text, so a fresh store and a fresh Postgres agree. A deployed sprint sets
// git_identity itself with nova-config; only a fresh store reads the default. Ported from
// nova-sprint's pkg/config copy with the copy itself (v1.3 dedupe); no clock, no socket.

// genericDefaultGitIdentity is the generic identity the rule must accept, spelled out so the
// rule's own proof does not lean on the constant it checks.
const genericDefaultGitIdentity = "nova-sprint <nova-sprint@example.invalid>"

// identityForbiddenTokens is the generality inventory (forbiddenTokens in
// internal/ci/generality_class_test.go) as it can appear in a name or an address: the
// accounts and organisations, the persons, the hosts. A token matches as a whole word, so
// "nova-sprint" and "invalid" do not trip "vision" or "mini".
var identityForbiddenTokens = []string{
	"alex", "antman", "batman", "captainamerica", "emma", "freddy", "glenn", "hetzner",
	"hulk", "johnny", "macbook", "mas-bandwidth", "mini", "rowan", "space", "spacegame",
	"stella", "studio", "superman", "vision",
}

// identityForbiddenReason names why a "Name <email>" identity is not a general default, ""
// when it is one: it must parse, carry no forbidden token as a whole word in the name or the
// address, and have an address on a reserved domain (RFC 2606 / 6761: .invalid, .test,
// .example, example.com/net/org), which no organisation or person owns.
func identityForbiddenReason(identity string) string {
	id, ok := ParseGitIdentity(identity)
	if !ok {
		return "it does not parse as 'Name <email>'"
	}
	lower := strings.ToLower(id.Name + " " + id.Email)
	for _, tok := range identityForbiddenTokens {
		re := regexp.MustCompile(`(^|[^a-z0-9-])` + regexp.QuoteMeta(tok) + `($|[^a-z0-9-])`)
		if re.MatchString(lower) {
			return "it names " + tok + ", an organisation, person or host of ours"
		}
	}
	at := strings.LastIndex(id.Email, "@")
	if at < 0 {
		return "its address has no domain"
	}
	domain := strings.ToLower(id.Email[at+1:])
	for _, reserved := range []string{".invalid", ".test", ".example", ".localhost"} {
		if strings.HasSuffix(domain, reserved) {
			return ""
		}
	}
	switch domain {
	case "example.com", "example.net", "example.org":
		return ""
	}
	return "its domain " + domain + " is not a reserved one, so it is a real organisation's or person's address"
}

func TestTheDefaultGitIdentityNamesNoOrganisationOrPerson(t *testing.T) {
	t.Parallel()
	assert.Empty(t, identityForbiddenReason(DefaultGitIdentity),
		"DefaultGitIdentity %q must be a generic machine identity on a reserved domain: nothing of ours in a shipped default", DefaultGitIdentity)
}

// TestParseGitIdentityReadsNameAndEmail: the lander commits as what ParseGitIdentity reads,
// so it splits at the last " <" and refuses what spells no name and email; Env sets both
// author and committer to it.
func TestParseGitIdentityReadsNameAndEmail(t *testing.T) {
	t.Parallel()
	id, ok := ParseGitIdentity("  The <Machine> <bot@example.invalid>  ")
	require.True(t, ok)
	assert.Equal(t, GitIdentity{Name: "The <Machine>", Email: "bot@example.invalid"}, id)
	assert.Equal(t, []string{
		"GIT_AUTHOR_NAME=The <Machine>", "GIT_AUTHOR_EMAIL=bot@example.invalid",
		"GIT_COMMITTER_NAME=The <Machine>", "GIT_COMMITTER_EMAIL=bot@example.invalid",
	}, id.Env())
	for _, bad := range []string{"", "  ", "bot", "<bot@example.invalid>", "bot <>", "bot >x<", "bot <x"} {
		_, ok := ParseGitIdentity(bad)
		assert.False(t, ok, "%q parsed as an identity", bad)
	}
}

func TestTheSprintRowsGitIdentityFieldDefaultsToTheGenericIdentity(t *testing.T) {
	t.Parallel()
	for _, k := range Kinds {
		if k.Name != KindSprint {
			continue
		}
		for _, f := range k.Fields {
			if f.Name == FieldGitIdentity {
				assert.Equal(t, DefaultGitIdentity, f.Default)
				assert.Empty(t, identityForbiddenReason(f.Default))
				return
			}
		}
	}
	t.Fatal("the sprint row has no git_identity field")
}

func TestMigration0037DefaultsToTheGenericIdentity(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(filepath.Join("migrations", "0037_sprint_git_identity.sql"))
	require.NoError(t, err)
	assert.Contains(t, string(b), "NOT NULL DEFAULT '"+DefaultGitIdentity+"'",
		"the migration's column default is the Go default, byte for byte (the migration itself is never rewritten once applied; the Go constant follows it)")
}

// The rule's own proof: it refuses the old default and every spelling of the class, and it
// accepts the generic one, so the test above is not vacuous.
func TestTheGeneralityRuleForAGitIdentityRefusesOursAndAcceptsTheGenericOne(t *testing.T) {
	t.Parallel()
	for _, c := range [][2]string{
		{"the old default", "nova-sprint <nova-sprint@mas-bandwidth.com>"},
		{"an organisation in the name", "mas-bandwidth <bot@example.invalid>"},
		{"a person in the address", "bot <glenn@example.invalid>"},
		{"a friend in the name", "Stella <bot@example.invalid>"},
		{"a real domain", "nova-sprint <nova-sprint@gmail.com>"},
		{"a real company domain", "nova-sprint <nova-sprint@networknext.com>"},
		{"a host in the address", "nova-sprint <nova-sprint@hetzner.example.invalid>"},
		{"empty", ""},
		{"no address", "nova-sprint"},
	} {
		assert.NotEmpty(t, identityForbiddenReason(c[1]), c[0])
	}
	for _, c := range [][2]string{
		{"the generic default", genericDefaultGitIdentity},
		{"a test identity", "ci <ci@example.invalid>"},
		{"example.com", "bot <bot@example.com>"},
	} {
		assert.Empty(t, identityForbiddenReason(c[1]), c[0])
	}
}

// TestGitIdentityEnvSetsAuthorAndCommitter pins the commit environment pkg/config's
// GitIdentity carries (nova-sprint's identity.go and swarm stage.go call it through
// pkg/config): the author and committer name and email, in that order, so a commit takes
// the configured identity whatever the host's git config held. GitIdentity is an alias of
// gitrun.GitIdentity, so Env is gitrun's method reached through this package.
func TestGitIdentityEnvSetsAuthorAndCommitter(t *testing.T) {
	t.Parallel()
	id, ok := ParseGitIdentity("Ada Lovelace <ada@example.invalid>")
	require.True(t, ok)
	assert.Equal(t, []string{
		"GIT_AUTHOR_NAME=Ada Lovelace",
		"GIT_AUTHOR_EMAIL=ada@example.invalid",
		"GIT_COMMITTER_NAME=Ada Lovelace",
		"GIT_COMMITTER_EMAIL=ada@example.invalid",
	}, id.Env())
}
