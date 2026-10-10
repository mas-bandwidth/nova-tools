package friend

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// launchd's override database can hold a label disabled after an earlier
// bootout (or a hand launchctl disable); a bootstrap of a disabled label
// answers exit 5, Input/output error, the same words launchd answers while it
// tears an old agent down. Install reads `launchctl print-disabled` before any
// bootstrap and enables a disabled label before the first bootstrap, and a
// bootstrap that still fails names the label, the domain, launchd's exit and
// the enable line that is the remedy (docs/SPEC-FRIEND.md, install).
func TestInstallEnablesADisabledLabelBeforeTheBootstrap(t *testing.T) {
	t.Parallel()
	a := agent()
	a.LaunchdLog = t.TempDir() + "/launchd.log"
	const target = "gui/501/com.nova.friend-bob"

	// fake is launchctl over one override database and a queue of bootstrap
	// answers: the first failBootstraps answers are exit 5, EIO, then launchd
	// loads the label. Every call is recorded raw.
	fake := func(disabled bool, failBootstraps int) (Launchctl, *[]string) {
		ran := &[]string{}
		left := failBootstraps
		return func(_ context.Context, args ...string) (string, error) {
			*ran = append(*ran, strings.Join(args, " "))
			switch args[0] {
			case "print-disabled":
				if disabled {
					return "disabled services = {\n\t\"" + a.Label() + "\" => true\n}\n", nil
				}
				return "disabled services = {\n}\n", nil
			case "bootstrap":
				if left > 0 {
					left--
					return "Bootstrap failed: 5: Input/output error", errors.New("exit 5")
				}
				return "", nil
			}
			return "", nil
		}, ran
	}

	t.Run("a disabled label is enabled then bootstrapped", func(t *testing.T) {
		t.Parallel()
		ctl, raw := fake(true, 1)
		_, commands, err := Install(context.Background(), a, 501, ctl, recordWrite(map[string]string{}), func() {})
		require.NoError(t, err)
		assert.Equal(t, []string{"launchctl bootout " + target,
			"launchctl enable " + target,
			"launchctl bootstrap gui/501 " + a.PlistPath(),
			"launchctl bootstrap gui/501 " + a.PlistPath()}, commands)
		assert.Equal(t, []string{"bootout " + target,
			"print " + target,
			"print-disabled gui/501",
			"enable " + target,
			"bootstrap gui/501 " + a.PlistPath(),
			"bootstrap gui/501 " + a.PlistPath()}, *raw,
			"the disabled label is read and enabled before any bootstrap")
		assert.Less(t, slices.Index(*raw, "print-disabled gui/501"), slices.Index(*raw, "bootstrap gui/501 "+a.PlistPath()),
			"the override database is read before the first bootstrap")
	})

	t.Run("an enabled label is bootstrapped without an enable", func(t *testing.T) {
		t.Parallel()
		ctl, raw := fake(false, 0)
		_, commands, err := Install(context.Background(), a, 501, ctl, recordWrite(map[string]string{}), func() {})
		require.NoError(t, err)
		assert.Equal(t, []string{"launchctl bootout " + target,
			"launchctl bootstrap gui/501 " + a.PlistPath()}, commands)
		for _, c := range *raw {
			assert.NotContains(t, c, "enable", "no enable step for a label that is not disabled")
		}
	})

	t.Run("a bootstrap failing with status 5 names the label and the remedy", func(t *testing.T) {
		t.Parallel()
		ctl, _ := fake(false, BootstrapTries)
		_, _, err := Install(context.Background(), a, 501, ctl, recordWrite(map[string]string{}), func() {})
		require.Error(t, err)
		assert.ErrorContains(t, err, "com.nova.friend-bob", "the label")
		assert.ErrorContains(t, err, "gui/501", "the domain")
		assert.ErrorContains(t, err, "exit 5", "launchd's exit")
		assert.Contains(t, err.Error(), "not disabled", "whether the label was disabled")
		assert.ErrorContains(t, err, "run: launchctl enable "+target, "the enable line that is the remedy")
	})
}
