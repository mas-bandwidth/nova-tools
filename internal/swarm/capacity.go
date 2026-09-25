package swarm

// Backpressure: the capacity line and the pull that never crosses it (docs/SPEC-JOBS.md
// section 7).
//
// Game engines bound a queue and refuse producers when it is full rather than let a hunch
// set the admission. A bench's capacity line is its measured CPU headroom, its disk above a
// 25G floor and its free memory, whichever is smallest:
//
//	min(cores*1.5 - load1, (free_gb-25)/2, memfree_gb/2)
//
// nova-swarm pull starts no more workers on a bench than the line allows. A full bench
// admits nothing and leaves every card in queue/.

// AdmissionLine is section 7's capacity line, in whole workers and whole numbers. It is
// section 2's CapacityLine (queue.go) read on integer probe numbers rather than measured
// floats, and it is named apart so neither section borrows the other's rounding: the number of workers nova-swarm pull may start
// on a bench, min(cores*1.5 - load1, (free_gb-25)/2, memfree_gb/2), floored at zero so a
// loaded, full or memory-starved bench admits nothing rather than a negative count.
func AdmissionLine(cores, load1, freeGB, memFreeGB int) int {
	if cores < 0 {
		cores = 0
	}
	line := cores*3/2 - load1
	if disk := (freeGB - 25) / 2; disk < line {
		line = disk
	}
	if mem := memFreeGB / 2; mem < line {
		line = mem
	}
	if line < 0 {
		return 0
	}
	return line
}

// Admission is how many workers a bench with this capacity line and this many already on it
// may still start, never more than the cards waiting and never negative. A full bench admits
// nothing.
func Admission(line, running, queued int) int {
	free := line - running
	if free > queued {
		free = queued
	}
	if free < 0 {
		return 0
	}
	return free
}
