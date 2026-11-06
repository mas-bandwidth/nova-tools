//go:build functional

package contract

import (
	"os"
	"path/filepath"
	"regexp"
)

// fleetBin is a PATH holding nova-config and, when ansible is not "", a fake
// ansible-playbook with that body (the real wording of its failures).
func fleetBin(ansible string, config bool) func(e *Env) {
	return func(e *Env) {
		d := e.Tools("sh")
		if config {
			os.Symlink(filepath.Join(binDir, "nova-config"), filepath.Join(d, "nova-config"))
		}
		if ansible != "" {
			e.Script(d, "ansible-playbook", ansible)
		}
		e.Path(d)
		e.Write("inv", "[all]\n")
	}
}

func init() {
	play := []string{"health", "--inventory", "{dir}/inv"}
	ran := regexp.MustCompile(`^nova-fleet: ansible-playbook=`)
	register(
		Fault{Tool: "fleet", Name: "play: ansible-playbook not on PATH", Setup: fleetBin("", true), Argv: play,
			Want: Want{Exit: []int{2}, Reason: []string{"ansible-playbook"}}, Known: "3"},
		Fault{Tool: "fleet", Name: "play: nova-config not on PATH", Setup: fleetBin("exit 0", false), Argv: []string{"health"},
			Want: Want{Exit: []int{2}, Reason: []string{"nova-config"}, Remedy: Runs, Stdout: "CHECK MISSING nova-config"}},
		Fault{Tool: "fleet", Name: "play: inventory file absent", Setup: fleetBin("exit 0", true), Argv: []string{"health", "--inventory", "{dir}/none"},
			Want: Want{Exit: []int{2}, Reason: []string{"no such file"}}},
		Fault{Tool: "fleet", Name: "play: host unreachable (ansible exit 4)",
			Setup: fleetBin(`echo 'fatal: [m1]: UNREACHABLE! => {"changed": false, "msg": "Failed to connect to the host via ssh", "unreachable": true}'; exit 4`, true),
			Argv:  play, Want: Want{Exit: []int{1}, Reason: []string{"exit=4"}, Stdout: "UNREACHABLE", Info: ran}, Known: "3"},
		Fault{Tool: "fleet", Name: "play: ansible-playbook killed by SIGKILL", Setup: fleetBin("kill -9 $$", true), Argv: play,
			Want: Want{Exit: []int{1}, Reason: []string{"killed by signal 9"}, Remedy: None, Info: ran}, Known: "10"},
		Fault{Tool: "fleet", Name: "check: a bare machine", Setup: func(e *Env) { e.Path(e.Tools("sh")) }, Argv: []string{"check"},
			Want: Want{Exit: []int{1}, Reason: []string{"CHECK FAILED"}, Stdout: "CHECK MISSING ansible-playbook", Remedy: None}},
		Fault{Tool: "fleet", Name: "up: nothing named", Argv: []string{"up"},
			Want: Want{Exit: []int{2}, Reason: []string{"<name>=<address>", "--tools"}}},
	)
}
