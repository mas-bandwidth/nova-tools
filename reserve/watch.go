package main

import "time"

// guard is the watchdog's decision, with its world passed in: while the
// command's process group lives, kill it the moment nova-reserve is gone.
type guard struct {
	parentGone func() bool
	groupGone  func() bool
	killGroup  func()
	sleep      func(d time.Duration)
}

// watch returns true when it killed the group because the parent was gone.
func (g guard) watch() bool {
	for !g.groupGone() {
		if g.parentGone() {
			g.killGroup()
			return true
		}
		g.sleep(poll)
	}
	return false
}
