package safepath

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RemoveUnder and RemoveUnderRoots guard the home with a lookup that ignored its own
// failure: `if home, err := homeFn(); err == nil && strings.TrimSpace(home) != ""`.
// A home that could not be looked up, or that answered blank, skipped the comparison
// and let the removal proceed -- the one case where the home could not be checked was
// the case waved through, the opposite of the FAILS CLOSED rule stated above
// refuseUnsafeRoot. The same failed lookup is fed to both removal doors, and to
// refuseUnsafePath directly, because a root refused earlier masks the path guard in
// the removal doors and would leave it untested.
func TestRemovalRefusesUnknownHome(t *testing.T) {
	t.Parallel()

	// A known home is any real directory that is neither the victim nor its root, so
	// a policy with an answer still removes a permitted child.
	knownHome := t.TempDir()

	cases := []struct {
		name        string
		home        func() (string, error)
		wantRefusal bool
	}{
		{
			name:        "a home lookup that errors",
			home:        func() (string, error) { return "", errors.New("no home for this session") },
			wantRefusal: true,
		},
		{
			name:        "a home lookup that answers empty",
			home:        func() (string, error) { return "", nil },
			wantRefusal: true,
		},
		{
			name:        "a home lookup that answers whitespace only",
			home:        func() (string, error) { return " \t ", nil },
			wantRefusal: true,
		},
		{
			name:        "a home lookup that answers a real directory",
			home:        func() (string, error) { return knownHome, nil },
			wantRefusal: false,
		},
	}

	doors := []struct {
		name string
		call func(p Policy, root, victim string) error
	}{
		{
			name: "RemoveUnder",
			call: func(p Policy, root, victim string) error { return p.RemoveUnder(root, victim) },
		},
		{
			name: "RemoveUnderRoots",
			call: func(p Policy, root, victim string) error { return p.RemoveUnderRoots(victim, root) },
		},
	}

	for _, door := range doors {
		for _, c := range cases {
			t.Run(door.name+"/"+c.name, func(t *testing.T) {
				t.Parallel()

				parent := t.TempDir()
				root := filepath.Join(parent, "root")
				victim := filepath.Join(root, "victim")
				sentinel := filepath.Join(victim, "keep")
				mustWrite(t, sentinel, "sentinel")
				require.NoError(t, os.Chmod(victim, 0o750))
				require.NoError(t, os.Chmod(sentinel, 0o640))

				policy := Policy{UserHomeDir: c.home}
				err := door.call(policy, root, victim)

				if !c.wantRefusal {
					require.NoError(t, err, "%s with a known home = %v, want the permitted child removed", door.name, err)
					assert.False(t, exists(victim), "%s left the permitted child %s behind", door.name, victim)
					return
				}

				assert.ErrorIs(t, err, ErrUnsafe, "%s = %v, want a refusal that wraps ErrUnsafe", door.name, err)
				assert.ErrorContains(t, err, "home", "%s = %v, want the refusal to name the home it could not identify", door.name, err)
				assert.ErrorContains(t, err, "identified", "%s = %v, want an actionable home-identification diagnostic", door.name, err)
				assert.True(t, exists(sentinel), "%s removed %s while the home was unknown; a refusal must leave the victim", door.name, sentinel)

				body, rerr := os.ReadFile(sentinel)
				require.NoError(t, rerr, "could not read the sentinel after the refusal: %v", rerr)
				assert.Equal(t, "sentinel", string(body), "%s changed the sentinel content, so it acted before refusing", door.name)

				info, serr := os.Stat(sentinel)
				require.NoError(t, serr, "could not stat the sentinel after the refusal: %v", serr)
				assert.Equal(t, os.FileMode(0o640), info.Mode().Perm(), "%s changed the sentinel permissions, so it acted before refusing", door.name)

				vinfo, verr := os.Stat(victim)
				require.NoError(t, verr, "could not stat the victim after the refusal: %v", verr)
				assert.Equal(t, os.FileMode(0o750), vinfo.Mode().Perm(), "%s changed the victim permissions, so it acted before refusing", door.name)
			})
		}
	}

	// The second home guard, called directly on the same failed lookup. In the removal
	// doors a failed home lookup already refuses at the root, so refuseUnsafePath is
	// never reached and this is the only way to pin it.
	t.Run("refuseUnsafePath on a failed lookup", func(t *testing.T) {
		t.Parallel()

		policy := Policy{UserHomeDir: func() (string, error) { return "", errors.New("no home for this session") }}
		target := t.TempDir()

		err := policy.refuseUnsafePath(target)
		assert.ErrorIs(t, err, ErrUnsafe, "refuseUnsafePath = %v, want a refusal that wraps ErrUnsafe", err)
		assert.ErrorContains(t, err, "home", "refuseUnsafePath = %v, want the refusal to name the home it could not identify", err)
		assert.ErrorContains(t, err, "identified", "refuseUnsafePath = %v, want an actionable home-identification diagnostic", err)
	})
}
