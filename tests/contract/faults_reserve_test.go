//go:build functional

package contract

import (
	"os"
	"regexp"
)

func init() {
	dir := func(e *Env) { e.Var("r", e.Dir+"/r") }
	withCap := func(e *Env) {
		dir(e)
		e.Exec("reserve", "", "set", "--dir", "{r}", "--cpu", "4", "--memory", "8", "--cores", "4")
	}
	receipts := regexp.MustCompile(`^nova-reserve: (HELD|CAPACITY SET|MEASURE OK)`)
	killed := func(sig string) []string {
		return []string{"run", "--dir", "{r}", "--", "sh", "-c", "kill -" + sig + " $$"}
	}
	register(
		Fault{Tool: "reserve", Name: "status: nothing ever reserved there", Setup: dir, Argv: []string{"status", "--dir", "{r}"},
			Want: Want{Reason: []string{"not a reservation directory"}}},
		Fault{Tool: "reserve", Name: "set: no capacity field", Setup: dir, Argv: []string{"set", "--dir", "{r}"},
			Want: Want{Reason: []string{"--cpu", "--memory"}}},
		Fault{Tool: "reserve", Name: "run: custom directory has no capacity.json", Setup: dir,
			Argv: []string{"run", "--dir", "{r}", "--", "true"},
			Want: Want{Exit: []int{2}, Reason: []string{"capacity.json"}, Remedy: Fix, Info: receipts}},
		Fault{Tool: "reserve", Name: "run: needs exceed machine capacity",
			Setup: withCap,
			Argv:  []string{"run", "--dir", "{r}", "--needs", "cpu 12, memory 4", "--", "true"},
			Want:  Want{Exit: []int{1}, Reason: []string{"capacity"}, Remedy: Look, Info: receipts}},
		Fault{Tool: "reserve", Name: "run: capacity file corrupt",
			Setup: func(e *Env) { dir(e); os.MkdirAll(e.Dir+"/r", 0o755); e.Write("r/capacity.json", "zz\n") },
			Argv:  []string{"run", "--dir", "{r}", "--", "true"},
			Want:  Want{Exit: []int{2}, Reason: []string{"capacity"}, Remedy: Fix, Info: receipts}},
		Fault{Tool: "reserve", Name: "run: directory path runs through a file",
			Setup: func(e *Env) { e.Write("afile", "x"); e.Var("r", e.Dir+"/afile/r") },
			Argv:  []string{"run", "--dir", "{r}", "--", "true"},
			Want:  Want{Reason: []string{"not a directory"}}},
		Fault{Tool: "reserve", Name: "run: parent directory not writable",
			Setup: func(e *Env) { os.Mkdir(e.Dir+"/ro", 0o755); e.Chmod(e.Dir+"/ro", 0o500); e.Var("r", e.Dir+"/ro/r") },
			Argv:  []string{"run", "--dir", "{r}", "--", "true"},
			Want:  Want{Reason: []string{"not writable"}}},
		Fault{Tool: "reserve", Name: "run: command not found", Setup: dir, Argv: []string{"run", "--dir", "{r}", "--", "nosuchcmd"},
			Want: Want{Exit: []int{127}, Reason: []string{"not found"}, Remedy: None, Info: receipts}},
		Fault{Tool: "reserve", Name: "run: command killed by SIGKILL", Setup: withCap, Argv: killed("9"),
			Want: Want{Exit: []int{137}, Reason: []string{"killed by signal 9"}, Remedy: None, Info: receipts}, Known: "signal-death-line", Gap: []string{`no reason token "killed by signal 9"`}},
		Fault{Tool: "reserve", Name: "run: command killed by SIGTERM", Setup: withCap, Argv: killed("TERM"),
			Want: Want{Exit: []int{143}, Reason: []string{"killed by signal 15"}, Remedy: None, Info: receipts}, Known: "signal-death-line", Gap: []string{`no reason token "killed by signal 15"`}},
	)
}
