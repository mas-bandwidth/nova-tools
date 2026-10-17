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
	// SizedLargeCPUs is the logical CPU count from which a machine is large: it gets
	// SizedLargeProcs processes and, with SizedLargeRAM of memory or more, SizedLargeMem.
	SizedLargeCPUs        = 32
	SizedLargeProcs       = 1024
	SizedLargeMem   int64 = 16 << 30
	SizedLargeRAM   int64 = 64 << 30
	// SizedProcs is every other machine's process cap.
	SizedProcs = 512
)

// SizedCaps is the process and memory cap for a machine of cpus logical CPUs and ram
// bytes of physical memory (0 when it could not be read). A large machine (SizedLargeCPUs
// or more) gets SizedLargeProcs processes, any other SizedProcs. The memory cap is
// SizedLargeMem on a large machine with SizedLargeRAM or more, DefaultMaxMem otherwise,
// and never more than half the machine's memory when that is known: one tree may not take
// the memory the machine's other work runs in.
func SizedCaps(cpus int, ram int64) (int, int64) {
	procs, mem := SizedProcs, DefaultMaxMem
	if cpus >= SizedLargeCPUs {
		procs = SizedLargeProcs
		if ram >= SizedLargeRAM {
			mem = SizedLargeMem
		}
	}
	if ram > 0 && mem > ram/2 {
		mem = ram / 2
	}
	return procs, mem
}

// MachineCaps is SizedCaps for this machine: runtime.NumCPU and its physical memory.
func MachineCaps() (int, int64) {
	return SizedCaps(runtime.NumCPU(), machineRAM())
}
