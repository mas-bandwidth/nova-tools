package keyshape

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Every fixture is BUILT here, at test time, and is a valid credential for nothing: the
// shapes are public forms, so a literal in the tree would be a key-shaped string in the
// repository for no reason.
func fixture(prefix string, n int) string {
	return prefix + strings.Repeat("A", n)
}

func TestTheShapesLoad(t *testing.T) {
	t.Parallel()

	list, err := Shapes()
	require.NoError(t, err, "keyshapes.txt")
	require.GreaterOrEqual(t, len(list), 10, "only %d shapes loaded", len(list))
	seen := map[string]bool{}
	for _, s := range list {
		require.False(t, seen[s.Name], "two rows named %s", s.Name)
		seen[s.Name] = true
	}
	for _, want := range []string{"forge-token", "age-secret-key", "openai-api-key", "xai-api-key", "google-api-key", "pem-private-key"} {
		require.True(t, seen[want], "the shape %s is not on the list", want)
	}
}

func TestEachShapeCatchesItsOwnForm(t *testing.T) {
	t.Parallel()

	cases := []struct{ shape, text string }{
		{"forge-token", fixture("ghp_", 30)},
		{"forge-fine-grained-token", fixture("github_pat_", 30)},
		{"age-secret-key", fixture("AGE-SECRET-KEY-1", 20)},
		{"openai-api-key", fixture("sk-", 32)},
		{"anthropic-api-key", fixture("sk-ant-", 30)},
		{"xai-api-key", fixture("xai-", 30)},
		{"google-api-key", fixture("AIza", 35)},
		{"pem-private-key", "-----BEGIN OPENSSH PRIVATE KEY-----"},
	}
	for _, c := range cases {
		found, err := ScanText("RESULT.md", "out: "+c.text+"\n", nil)
		require.NoError(t, err)
		hit := false
		for _, f := range found {
			if f.Shape == c.shape {
				hit = true
			}
		}
		require.True(t, hit, "%s was not caught in %d findings", c.shape, len(found))
	}
}

// TestTheScanNeverPrintsWhatItMatched is the rule the whole package exists under: a
// finding's text is a shape name, a path and a line, and the value is in none of them.
func TestTheScanNeverPrintsWhatItMatched(t *testing.T) {
	t.Parallel()

	secret := fixture("ghp_", 30)
	found, err := ScanText("RESULT.md", "out: "+secret+"\n", nil)
	require.NoError(t, err)
	require.NotEmpty(t, found, "nothing was caught, so this test proves nothing")
	for _, f := range found {
		require.NotContains(t, f.String(), secret, "the finding quotes the key: %s", f.String())
		require.NotContains(t, f.String(), secret[:12], "the finding quotes a prefix of the key: %s", f.String())
	}
}

// TestASecretNamedVariablesValueIsCaughtByLengthAndName is the shape-less half: the key
// this fleet holds may not match any published prefix, and the check still finds it --
// reporting the VARIABLE's name and the value's LENGTH, never the value.
func TestASecretNamedVariablesValueIsCaughtByLengthAndName(t *testing.T) {
	t.Parallel()

	value := "zzq" + strings.Repeat("7", 29) // no published prefix; 32 characters
	env := []string{"SEAT_PROVIDER_KEY=" + value, "PATH=/usr/bin", "HOME=/home/x"}
	found, err := ScanText("RESULT.md", "line one\nout: "+value+"\nline three\n", env)
	require.NoError(t, err)
	require.Len(t, found, 1, "want one finding, got %d: %v", len(found), found)
	f := found[0]
	if f.Shape != "env-value" || f.Name != "SEAT_PROVIDER_KEY" || f.Line != 2 || f.Len != len(value) {
		t.Fatalf("finding = %+v", f)
	}
	require.NotContains(t, f.String(), value, "the finding quotes the value: %s", f.String())
}

// TestPlainProseIsNoFinding: a shape that matched prose would be switched off within a
// week, so the ordinary text a card writes must pass.
func TestPlainProseIsNoFinding(t *testing.T) {
	t.Parallel()

	prose := `RESULT: fix the thing ok
DONE
BRANCH rowan/fix-1814
REPO mas-bandwidth/nova-tools
red: TestThing -- the key is never printed, only its length
green: TestThing
files: internal/pulse/harvest.go
usd=0.0013 tokens_in=6176 sha=d5666ab9 eyJ
`
	env := []string{"SHORT_KEY=abc", "EMPTY_TOKEN="}
	found, err := ScanText("RESULT.md", prose, env)
	require.NoError(t, err)
	require.Empty(t, found, "prose was called a secret: %v", found)
}

func TestScanFileIsQuietAboutAMissingFileAndLoudAboutAnUnreadableOne(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	found, err := ScanFile(filepath.Join(dir, "nothing.md"), nil)
	require.NoError(t, err, "a missing file: %v %v", found, err)
	require.Empty(t, found, "a missing file: %v %v", found, err)
	require.NoError(t, os.Mkdir(filepath.Join(dir, "adir"), 0o755))
	_, err = ScanFile(filepath.Join(dir, "adir"), nil)
	require.Error(t, err, "a directory read as a clean file; a check that could not run must not read as a check that passed")
}

func TestSecretNameIsTheArgvLogsPredicate(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"DEEPSEEK_API_KEY", "GH_TOKEN", "SOPS_AGE_SECRET", "lower_case_key", "MiXeD_ToKeN"} {
		require.True(t, SecretName(name), "%s is not recognised as a secret name", name)
	}
	// The predicate is deliberately blunt -- it is a substring test, so MONKEY carries
	// KEY -- and blunt in the safe direction: it over-scrubs and never under-scrubs.
	for _, name := range []string{"PATH", "HOME", "LANG", "TERM", "NOVA_SWARM_JOB", "XDG_DATA_HOME"} {
		require.False(t, SecretName(name), "%s was called a secret name", name)
	}
	require.True(t, SecretName("MONKEY"), "the predicate stopped being a substring test; cmd/nova-swarm's argv log and shell shim assume it is one")
}
