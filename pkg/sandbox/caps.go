package sandbox

import (
	"runtime"
)

// SIZED CAPS: the bare form's caps follow the machine (docs/SPEC-SANDBOX.md,
// "wall-caps-processes.w1"). The fixed 256 processes and 8 GiB killed ordinary builds on
// the fleet's large members: on 2026-10-17 the sprint store logged about 90 worker
// children ended by the wall in a day (`SANDBOX RUNAWAY runaway: 287..697 processes (cap
// 256)`, and `8611000320 bytes of memory (cap 8589934592)`), most of them a parallel C++
// build or an unbounded `go test ./...`. The caps still bound a runaway; they are sized so
// a build the machine can carry is not one.
const (
	// SizedProcs is the least process cap of any machine.
	SizedProcs = 512
	// SizedProcsPerCPU is the process cap per logical CPU, above SizedProcs.
	SizedProcsPerCPU = 64
	// SizedMemShare is the share of the machine's memory one tree may hold: ram over it.
	SizedMemShare = 8
	// SizedSliceTrees is how many capped trees must fit together under the login slice's
	// limits (MemoryMax, TasksMax): one tree may hold the slice's limit over it.
	SizedSliceTrees = 4
)

// Machine is what the caps are sized from. Zero is unknown for every field: RAM when it
// could not be read, SliceMem and SliceTasks when no cgroup limit is visible above this
// process (the login slice's MemoryMax and TasksMax, the tightest of its ancestors).
type Machine struct {
	CPUs       int
	RAM        int64
	SliceMem   int64
	SliceTasks int
}

// SizedCaps is the process and memory cap for a machine. Processes follow the CPUs:
// SizedProcsPerCPU per CPU and at least SizedProcs, but never more than the slice's
// TasksMax over SizedSliceTrees (and at least 1). Memory follows the memory, never the CPU count: an
// eighth of the machine (SizedMemShare), never more than the slice's MemoryMax over
// SizedSliceTrees, with a DefaultMaxMem floor, then never more than half the machine.
// The half-machine ceiling can put the result below DefaultMaxMem on machines under
// 16 GiB. With machine memory unread, the memory cap is DefaultMaxMem. A slice share
// below the floor can be exceeded on a small slice.
//
// The share is small on purpose. The wall kills only the runaway tree; the slice's OOM
// killer kills whatever it picks, often a sibling worker. So SizedSliceTrees trees at
// their caps fit under the slice together where the share is above the floor (hetzner2: 125 GiB, slice 106 GiB, caps
// 15.6 GiB). The first sizing (v1.5.1) keyed both caps on 32 CPUs, so a 16-thread member
// with 124 GiB got 512 processes and 8 GiB, and on 2026-10-17 the wall still ended about
// half the fleet's worker runs (`runaway: 714 processes (cap 512)`, `8714186752 bytes of
// memory (cap 8589934592)`).
func SizedCaps(m Machine) (int, int64) {
	procs := max(SizedProcs, m.CPUs*SizedProcsPerCPU)
	if m.SliceTasks > 0 {
		procs = max(1, min(procs, m.SliceTasks/SizedSliceTrees))
	}
	mem := DefaultMaxMem
	if m.RAM > 0 {
		share := m.RAM / SizedMemShare
		if m.SliceMem > 0 {
			share = min(share, m.SliceMem/SizedSliceTrees)
		}
		mem = min(max(DefaultMaxMem, share), m.RAM/2)
	}
	return procs, mem
}

// MachineCaps is SizedCaps for this machine: runtime.NumCPU, its physical memory and the
// cgroup limits above this process.
func MachineCaps() (int, int64) {
	sliceMem, sliceTasks := machineSlice()
	return SizedCaps(Machine{CPUs: runtime.NumCPU(), RAM: machineRAM(), SliceMem: sliceMem, SliceTasks: sliceTasks})
}
