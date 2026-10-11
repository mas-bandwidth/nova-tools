package main

// actor_flag_test.go pins the family's one name for the name a write is
// recorded under: --actor is the flag, its old spellings --as (the writer
// verbs) and --friend (the recorded login) still work for one release, and a
// run that spells an old name says so on one NOTE (docs/STANDARD.md section
// 2, "One shape across the set").

import (
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/secrets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// actorHarness is a harness with a --file store to open.
func actorHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness()
	h.dir = t.TempDir()
	code, _, errs := h.run(t, "migrate", "--file", "try.json")
	require.Equal(t, 0, code, errs)
	return h
}

// TestActorIsTheFamilyNameAndAsIsItsOldSpelling: the writer verbs take
// --actor, which prints no NOTE; --as sets the same name and prints the one
// NOTE that says it is old. It holds for the lines and for --json, on add,
// set and remove.
func TestActorIsTheFamilyNameAndAsIsItsOldSpelling(t *testing.T) {
	t.Parallel()
	h := actorHarness(t)

	code, out, errs := h.run(t, "machine", "add", "m1", "--user", "u", "--seat", "s", "--slots", "8", "--width", "4", "--actor", "a1", "--file", "try.json")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "CONFIG ADD kind=machine name=m1 rev=1")
	assert.NotContains(t, out, "NOTE --as is --actor", out)

	code, out, errs = h.run(t, "machine", "add", "m2", "--user", "u", "--seat", "s", "--slots", "8", "--width", "4", "--as", "a2", "--file", "try.json")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "CONFIG ADD kind=machine name=m2 rev=2")
	assert.Contains(t, out, "NOTE --as is --actor", out)

	code, out, errs = h.run(t, "machine", "set", "m2", "--width", "6", "--actor", "a2", "--file", "try.json")
	require.Equal(t, 0, code, errs)
	assert.NotContains(t, out, "NOTE --as is --actor", out)

	code, out, errs = h.run(t, "machine", "remove", "m2", "--as", "a2", "--file", "try.json")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "CONFIG REMOVE kind=machine name=m2 rev=")
	assert.Contains(t, out, "NOTE --as is --actor", out)
}

// TestAsIsANoteOfTheJSONResultToo: the alias rides the one result value, so
// --json carries it among the notes, as the lines do.
func TestAsIsANoteOfTheJSONResultToo(t *testing.T) {
	t.Parallel()
	h := actorHarness(t)
	code, out, errs := h.run(t, "machine", "add", "m1", "--user", "u", "--seat", "s", "--slots", "8", "--width", "4", "--as", "a1", "--file", "try.json", "--json")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, `"--as is --actor"`, out)
}

// TestTheMissingActorRefusalNamesTheFamilyFlag: an invocation with no actor
// names --actor, the flag it wants, not the old spelling.
func TestTheMissingActorRefusalNamesTheFamilyFlag(t *testing.T) {
	t.Parallel()
	h := actorHarness(t)
	code, out, errs := h.run(t, "machine", "add", "m1", "--user", "u", "--seat", "s", "--slots", "8", "--width", "4", "--file", "try.json")
	require.Equal(t, 2, code, "%q %q", out, errs)
	assert.Empty(t, out)
	assert.Contains(t, errs, "--actor is required")
}

// TestTheActorHelpNamesBothSpellings: the writer verb's help names --actor
// and says --as is its old spelling.
func TestTheActorHelpNamesBothSpellings(t *testing.T) {
	t.Parallel()
	h := newHarness()
	code, out, errs := h.run(t, "machine", "add", "-h")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "--actor <name>", out)
	assert.Contains(t, out, "--as <name>", out)
	assert.Contains(t, out, "old spelling of --actor", out)
}

// loginAliasHarness is a harness whose login records without a real secrets
// store: the secret is read from the reader registered for its file.
func loginAliasHarness(t *testing.T) (*harness, func(args ...string) (int, string, string)) {
	t.Helper()
	h := newHarness()
	h.dir = t.TempDir()
	h.env["XDG_CONFIG_HOME"] = filepath.Join(h.dir, "config")
	read := func(secrets.Login) (secrets.Secret, error) { return secrets.NewSecret("pw-not-printed"), nil }
	getenv := withLogin(func(k string) string { return h.env[k] }, read)
	t.Cleanup(func() {
		if p := getenv(loginFileKey); p != "" {
			loginReaders.Delete(p)
		}
	})
	d := h.deps()
	d.getenv = getenv
	return h, func(args ...string) (int, string, string) { return runCapture(args, d) }
}

// loginAliasArgs is one login with the actor flag named by flag.
func loginAliasArgs(h *harness, flag, name string) []string {
	return []string{"login", "--store", filepath.Join(h.dir, "secrets"), "--as", "studio",
		"--key", filepath.Join(h.dir, "studio.key"), "--sops", "/usr/local/bin/sops",
		"--secret", "NOVA_PG_CONFIG_PASSWORD", "--dsn", dsn, flag, name}
}

// TestTheLoginActorHelpNamesBothSpellings: the login help names --actor and
// says --friend is its old spelling.
func TestTheLoginActorHelpNamesBothSpellings(t *testing.T) {
	t.Parallel()
	h := newHarness()
	code, out, errs := h.run(t, "login", "-h")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "--actor <name>", out)
	assert.Contains(t, out, "--friend <name>", out)
	assert.Contains(t, out, "old spelling of --actor", out)
}

// TestTheLoginActorIsTheFamilyNameAndFriendIsItsOldSpelling: the recorded
// login takes --actor with no NOTE; --friend records the same name and prints
// the one NOTE that says it is old.
func TestTheLoginActorIsTheFamilyNameAndFriendIsItsOldSpelling(t *testing.T) {
	t.Parallel()
	h, do := loginAliasHarness(t)

	code, out, errs := do(loginAliasArgs(h, "--actor", "rowan")...)
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "LOGIN RECORDED")
	assert.Contains(t, out, "friend=rowan")
	assert.NotContains(t, out, "NOTE --friend is --actor", out)

	code, out, errs = do(loginAliasArgs(h, "--friend", "stella")...)
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "friend=stella")
	assert.Contains(t, out, "NOTE --friend is --actor", out)
}
