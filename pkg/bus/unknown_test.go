package bus

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/stretchr/testify/require"
)

// remedyFlags is the flags the refusal's `nova-config friend add <n> ...`
// remedy passes, up to --as, as the field map NewRow takes.
func remedyFlags(t *testing.T, msg, name string) map[string]string {
	t.Helper()
	_, rest, ok := strings.Cut(msg, "nova-config friend add "+name+" ")
	require.True(t, ok, "the refusal names no friend add remedy: %q", msg)
	rest, _, ok = strings.Cut(rest, " --as ")
	require.True(t, ok, "the remedy has no --as: %q", msg)
	words := strings.Fields(rest)
	require.Equal(t, 0, len(words)%2, "the remedy's flags are not --flag value pairs: %q", rest)
	fields := map[string]string{}
	for i := 0; i < len(words); i += 2 {
		require.True(t, strings.HasPrefix(words[i], "--"), "%q is no flag", words[i])
		fields[strings.TrimPrefix(words[i], "--")] = words[i+1]
	}
	return fields
}

// TestTheUnknownNameRemedyIsAFriendRowTheKindTakes: the command the refusal
// of an unknown name prints must be one nova-config accepts. A friend row has
// only width since 2026-10-13 (no slots), so a remedy saying --slots would be
// refused; the reversed witness is that old remedy, which the friend kind
// refuses.
func TestTheUnknownNameRemedyIsAFriendRowTheKindTakes(t *testing.T) {
	t.Parallel()

	friend, ok := config.Lookup(config.KindFriend)
	require.True(t, ok)
	_, err := friend.NewRow("ada", remedyFlags(t, unknown("ada"), "ada"))
	require.NoError(t, err, "the unknown-name remedy is a friend row nova-config refuses: %s", unknown("ada"))

	old := strings.Replace(unknown("ada"), "--width 1", "--slots 1", 1)
	_, err = friend.NewRow("ada", remedyFlags(t, old, "ada"))
	require.ErrorContains(t, err, "--slots is not a friend field", "the reversed witness (the old --slots remedy) was accepted")
}
