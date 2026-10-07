package up

import (
	"fmt"
)

// The ssh step: ssh (openssh-client) is available on every bench for the
// coordinator to reach them. nova-up does not install packages: it only
// reports what is missing. A missing ssh is listed with the install command
// for the bench's OS (docs/SPEC-UP.md "Steps", 3).
func init() {
	Register(Step{Name: "ssh", Order: 25, Plan: planSSH, Apply: func(*Env) error { return nil }})
}

func planSSH(e *Env) Finding {
	_, err := e.Exec.LookPath("ssh")
	if err != nil {
		install := "sudo apt-get install -y openssh-client"
		if e.GOOS == "darwin" {
			install = "brew install openssh"
		}
		return Finding{Missing, fmt.Sprintf("ssh not on PATH; install: %s", install)}
	}
	return Finding{OK, "ssh available"}
}
