//go:build windows

package update

import (
	"os"
	"os/exec"
)

// Windows has no process group to escape here; the owed Windows termination
// validation is named in the pull request and the escaped-pipe witness is
// skipped there, so this holder only needs to exist to compile.
func spawnEscapedHolder(d string) error {
	c := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--", "hold", d)
	c.Env = append(os.Environ(), "NOVA_UPDATE_HELPER=1")
	c.Stdout = os.Stdout
	return c.Start()
}
