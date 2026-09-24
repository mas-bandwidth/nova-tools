package pulse

// sysctlLoadavg is the darwin half of ReadLoad1. It is here, on its own, so the one place
// this package shells out for a load average is one line a reader can find.

import (
	"os/exec"
)

func sysctlLoadavg() (string, error) {
	out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
