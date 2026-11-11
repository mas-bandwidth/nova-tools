//go:build functional

package contract

import (
	"os"
	"regexp"
)

func init() {
	dir := func(e *Env) { e.Var("r", e.Dir+"/r") }
	receipts := regexp.MustCompile(`^nova-reserve: (HELD|CAPACITY SET)`)
	killed := func(sig string) []string {
		return []string{"run", "--dir", "{r}", "--slots", "2", "--", "sh", "-c", "kill -" + sig + " $$"}
	}
	register(
		Fault{Tool: "reserve", Name: "status: nothing ever reserved there", Setup: dir, Argv: []string{"status", "--dir", "{r}"},
			Want: Want{Reason: []string{"not a reservation directory"}}},
		Fault{Tool: "reserve", Name: "set: no slot count", Setup: dir, Argv: []string{"set", "--dir", "{r}"},
			Want: Want{Reason: []string{"--slots"}}},
		Fault{Tool: "reserve", Name: "run: asks for other slots than the machine has",
			Setup: func(e *Env) { dir(e); e.Exec("reserve", "", "run", "--dir", "{r}", "--slots", "8", "--", "true") },
			Argv:  []string{"run", "--dir", "{r}", "--slots", "12", "--", "true"},
			Want:  Want{Exit: []int{2}, Reason: []string{"capacity"}, Remedy: Fix, Info: receipts}},
		Fault{Tool: "reserve", Name: "run: capacity file corrupt",
			Setup: func(e *Env) { dir(e); os.MkdirAll(e.Dir+"/r", 0o755); e.Write("r/capacity", "zz\n") },
			Argv:  []string{"run", "--dir", "{r}", "--", "true"},
			Want:  Want{Exit: []int{2}, Reason: []string{"capacity"}, Remedy: Fix, Info: receipts}},
		Fault{Tool: "reserve", Name: "run: directory path runs through a file",
			Setup: func(e *Env) { e.Write("afile", "x"); e.Var("r", e.Dir+"/afile/r") },
			Argv:  []string{"run", "--dir", "{r}", "--slots", "2", "--", "true"},
			Want:  Want{Reason: []string{"not a directory"}}},
		Fault{Tool: "reserve", Name: "run: parent directory not writable",
			Setup: func(e *Env) { os.Mkdir(e.Dir+"/ro", 0o755); e.Chmod(e.Dir+"/ro", 0o500); e.Var("r", e.Dir+"/ro/r") },
			Argv:  []string{"run", "--dir", "{r}", "--slots", "2", "--", "true"},
			Want:  Want{Reason: []string{"not writable"}}},
		Fault{Tool: "reserve", Name: "run: command not found", Setup: dir, Argv: []string{"run", "--dir", "{r}", "--slots", "2", "--", "nosuchcmd"},
			Want: Want{Exit: []int{127}, Reason: []string{"not found"}, Remedy: None, Info: receipts}},
		Fault{Tool: "reserve", Name: "run: command killed by SIGKILL", Setup: dir, Argv: killed("9"),
			Want: Want{Exit: []int{137}, Reason: []string{"killed by signal 9"}, Remedy: None, Info: receipts}, Known: "signal-death-line", Gap: []string{`no reason token "killed by signal 9"`}},
		Fault{Tool: "reserve", Name: "run: command killed by SIGTERM", Setup: dir, Argv: killed("TERM"),
			Want: Want{Exit: []int{143}, Reason: []string{"killed by signal 15"}, Remedy: None, Info: receipts}, Known: "signal-death-line", Gap: []string{`no reason token "killed by signal 15"`}},
	)
}
