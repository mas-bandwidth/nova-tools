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
	SizedMemShare = 4
)

// SizedCaps is the process and memory cap for a machine of cpus logical CPUs and ram
// bytes of physical memory (0 when it could not be read). Processes follow the CPUs:
// SizedProcsPerCPU per CPU and at least SizedProcs. Memory follows the memory, never the
// CPU count: a quarter of the machine (SizedMemShare) and at least DefaultMaxMem, and
// never more than half the machine: one tree may not take the memory the machine's other
// work runs in. With the memory unread the memory cap is DefaultMaxMem. The first sizing
// (v1.5.1) keyed both caps on 32 CPUs, so a 16-thread member with 124 GiB got 512
// processes and 8 GiB, and on 2026-10-17 the wall still ended about half the fleet's
// worker runs (`runaway: 714 processes (cap 512)`, `8714186752 bytes of memory (cap
// 8589934592)`).
func SizedCaps(cpus int, ram int64) (int, int64) {
	procs := max(SizedProcs, cpus*SizedProcsPerCPU)
	mem := DefaultMaxMem
	if ram > 0 {
		mem = min(max(DefaultMaxMem, ram/SizedMemShare), ram/2)
	}
	return procs, mem
}

// MachineCaps is SizedCaps for this machine: runtime.NumCPU and its physical memory.
func MachineCaps() (int, int64) {
	return SizedCaps(runtime.NumCPU(), machineRAM())
}
