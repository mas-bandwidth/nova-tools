//go:build darwin

package main

import "syscall"

// PRIO_DARWIN_PROCESS and PRIO_DARWIN_BG are Darwin's process-scope priority
// selectors; syscall does not spell them. PRIO_DARWIN_BG is the background QoS,
// the class the scheduler keeps behind the foreground.
const (
	prioDarwinProcess = 4
	prioDarwinBG      = 0x1000
)

// setBackgroundQoS puts the process led by pid, and everything it forks after, at
// the background QoS from outside the card's wall (docs/SPEC-SWARM.md, the server
// host reserve). macOS schedules by QoS class, where a POSIX nice value moves
// little, so this is the lever that holds on the server host.
func setBackgroundQoS(pid int) error {
	return syscall.Setpriority(prioDarwinProcess, pid, prioDarwinBG)
}
