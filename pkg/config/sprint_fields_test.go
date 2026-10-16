package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The sprint row's git_identity and max_attempts are nova-config's to set: nova-sprint reads
// both from the applied row (sprint:git_identity, which its lander commits as, the default
// when empty; sprint:max_attempts, its attempt bound), and nova-config registers only the
// fields its kind declares, migrations 0037 (git_identity) and 0039 (max_attempts).

func sprintKindOf(t *testing.T) *Kind {
	t.Helper()
	k, ok := Lookup(KindSprint)
	require.True(t, ok, "the sprint kind exists")
	return k
}

func sprintField(t *testing.T, name string) Field {
	t.Helper()
	for _, f := range sprintKindOf(t).Fields {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("the sprint row has no %s field", name)
	return Field{}
}

func TestTheSprintRowCarriesTheMachinesGitIdentity(t *testing.T) {
	t.Parallel()
	f := sprintField(t, FieldGitIdentity)
	assert.Equal(t, TypeText, f.Type)
	assert.Equal(t, DefaultGitIdentity, f.Default, "the machine's own identity by default, never the host's")
	assert.Equal(t, "nova-sprint <nova-sprint@example.invalid>", DefaultGitIdentity, "a generic machine identity, never an organisation's domain in this repository")
}

func TestTheSprintRowsMaxAttemptsDefaultsToFour(t *testing.T) {
	t.Parallel()
	f := sprintField(t, FieldMaxAttempts)
	assert.Equal(t, "4", f.Default, "the default is 4")
	assert.Equal(t, TypeInt, f.Type)
	assert.Contains(t, f.Help, "stream set <s> --attempts <n>", "the help names the stream's own override")
}

func TestCheckSprintHoldsMaxAttemptsToAWholeNumberFromOneToTheCeiling(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"0", "-1", "101", "x", "2.5"} {
		err := checkSprint(Row{Fields: map[string]string{FieldMaxAttempts: bad}})
		require.Error(t, err, "max_attempts %q is refused", bad)
		assert.Contains(t, err.Error(), "sprint: "+FieldMaxAttempts, "the field is named")
		assert.Contains(t, err.Error(), "--"+FieldMaxAttempts+" <n>", "and the remedy")
	}
	assert.Contains(t, checkSprint(Row{Fields: map[string]string{FieldMaxAttempts: "101"}}).Error(), "the sprint refuses a cap above 100")
	for _, ok := range []string{"1", "4", "100", ""} {
		require.NoError(t, checkSprint(Row{Fields: map[string]string{FieldMaxAttempts: ok}}), "max_attempts %q is accepted", ok)
	}
	require.NoError(t, checkSprint(Row{Fields: map[string]string{}}), "an absent max_attempts is the default")
}

// Each sprint field nova-sprint reads has the column its migration adds, so a store migrated
// by either repo's copy holds it: 0037 git_identity and 0039 max_attempts.
func TestTheSprintFieldsHaveTheirMigrations(t *testing.T) {
	t.Parallel()
	for file, col := range map[string]string{
		"0037_sprint_git_identity.sql": "git_identity",
		"0039_sprint_max_attempts.sql": "max_attempts",
	} {
		b, err := os.ReadFile(filepath.Join("migrations", file))
		require.NoError(t, err, "migration %s exists", file)
		assert.Contains(t, string(b), "ALTER TABLE config.sprint", file)
		assert.Contains(t, string(b), "ADD COLUMN IF NOT EXISTS "+col, file)
	}
}
