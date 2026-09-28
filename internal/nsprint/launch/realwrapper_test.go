//go:build unix

package launch

import (
	"os"
	"path/filepath"
)

// realHarnessEnv turns the test binary into the harness the real nova-card
// runs. The wrapper strips NOVA_CARD_* and anything naming a token from the
// harness's environment, so the switch has its own name.
const realHarnessEnv = "NOVA_LAUNCH_TEST_HARNESS"

// realHarness writes a RESULT line into the card's out dir and exits DONE.
func realHarness() int {
	out := os.Getenv("NOVA_CARD_OUT")
	if out == "" {
		return 9
	}
	if err := os.WriteFile(filepath.Join(out, "RESULT.md"), []byte("RESULT: "+os.Getenv("NOVA_CARD")+" sha=000000000000\n"), 0o644); err != nil {
		return 9
	}
	return 0
}
