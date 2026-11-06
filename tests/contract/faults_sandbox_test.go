//go:build functional

package contract

import "os"

// walled skips a row on a machine where the wall cannot be raised.
func walled(e *Env) {
	if r := e.Exec("sandbox", "", "check"); r.code != 0 {
		e.skip("no sandbox backend here: %s", r.out)
	}
	os.Mkdir(e.Dir+"/ok", 0o755)
}

func init() {
	register(
		Fault{Tool: "sandbox", Name: "grant is not an absolute path", Argv: []string{"--read", "relative", "--", "true"},
			Want: Want{Exit: []int{2}, Reason: []string{"absolute"}}},
		Fault{Tool: "sandbox", Name: "grant is not an existing directory", Argv: []string{"--read", "{dir}/nodir", "--", "true"},
			Want: Want{Exit: []int{2}, Reason: []string{"not an existing directory"}}},
		Fault{Tool: "sandbox", Name: "--gpu on Linux", Argv: []string{"--gpu", "--read", "{dir}", "--", "true"},
			Want: Want{Exit: []int{2}, Reason: []string{"--gpu"}}},
		Fault{Tool: "sandbox", Name: "--net-allow without --net-deny", Argv: []string{"--net-allow", "127.0.0.1:1", "--read", "{dir}", "--", "true"},
			Want: Want{Exit: []int{2}, Reason: []string{"--net-allow"}}},
		Fault{Tool: "sandbox", Name: "no command after the flags", Argv: []string{"--read", "{dir}"},
			Want: Want{Exit: []int{2}, Reason: []string{"command|--"}}},
		Fault{Tool: "sandbox", Name: "command not found", Setup: walled, Argv: []string{"--read", "{dir}", "--", "nosuchcmd"},
			Want: Want{Exit: []int{127}, Reason: []string{"not_found"}, Remedy: None}},
		Fault{Tool: "sandbox", Name: "working directory outside the grants", Setup: walled, Argv: []string{"--read", "{dir}/ok", "--", "ls", "."},
			Want: Want{Reason: []string{"cwd|working directory"}}, Known: "4"},
		Fault{Tool: "sandbox", Name: "command killed by SIGKILL", Setup: walled, Argv: []string{"--read", "{dir}", "--", "sh", "-c", "kill -9 $$"},
			Want: Want{Signal: 9}},
	)
}
