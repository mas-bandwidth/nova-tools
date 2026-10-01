package yield

import "syscall"

// darwin's background state, from <sys/resource.h>: PRIO_DARWIN_PROCESS selects
// the process, PRIO_DARWIN_BG puts it in the background.
const (
	prioDarwinProcess = 4
	prioDarwinBG      = 0x1000
)

// behind puts this process in darwin's background state (setpriority
// PRIO_DARWIN_PROCESS, 0, PRIO_DARWIN_BG): background QoS, so on Apple silicon
// its threads run on the efficiency cores; throttled disk I/O; and sockets in the
// background traffic class. Every descendant inherits it (measured on darwin 27:
// a child and a grandchild through sh read it as set).
func behind(string) string {
	if err := syscall.Setpriority(prioDarwinProcess, 0, prioDarwinBG); err != nil {
		return "setpriority PRIO_DARWIN_BG: " + err.Error()
	}
	return ""
}

func behindNote() string { return "" }
