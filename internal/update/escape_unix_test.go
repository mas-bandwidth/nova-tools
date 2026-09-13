//go:build !windows

package update

import (
	"os"
	"os/exec"
)

// spawnEscapedHolder starts a "hold" grandchild in its own process group, so the
// version command's group kill (kill(-pgid)) cannot reach it. It inherits the
// caller's stdout, keeping that pipe's write end open past the caller's death.
func spawnEscapedHolder(d string) error {
	c := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--", "hold", d)
	c.Env = append(os.Environ(), "NOVA_UPDATE_HELPER=1")
	c.Stdout = os.Stdout
	setGroup(c)
	return c.Start()
}
