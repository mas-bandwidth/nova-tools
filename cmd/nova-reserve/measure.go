package main

import (
	"cmp"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// standardCoreRate is the benchmark iterations/sec of one standard server core
// (measured on a 16-core / 32-thread server: 1 physical core = 7,800 iters/s ->
// 1 CPU unit; 32 SMT threads = 156,750 iters/s -> 24 CPU units).
const (
	standardCoreRate = 6500.0
	benchDuration    = 1500 * time.Millisecond
	busyLimitPct     = 25
)

// benchSink prevents dead-code elimination of benchmark work.
var benchSink atomic.Uint64

// defaultBench measures CPU units and usable GiB on the live host.
func defaultBench(cores int, force bool) (cpuUnits, memGB int, err error) {
	if !force {
		if busy, ok := cpuBusyPct(100 * time.Millisecond); ok && busy > busyLimitPct {
			return 0, 0, fmt.Errorf("machine is busy (%d%% CPU in use); stop other work or pass --force", busy)
		}
	}
	cores = max(1, cores)
	rate := runBenchmark(cores, benchDuration)
	cpuUnits = max(1, int(rate/standardCoreRate))
	memBytes := hostMemoryBytes()
	memGB = max(1, int(memBytes>>30))
	return cpuUnits, memGB, nil
}

// runBenchmark runs a deterministic compiler/test workload across cores workers:
// L2-resident symbol/AST hash-table probing (256 KiB/worker), integer slice
// sorting + binary search, and SHA-256 / FNV byte-stream scanning.
func runBenchmark(cores int, dur time.Duration) float64 {
	prev := runtime.GOMAXPROCS(cores)
	defer runtime.GOMAXPROCS(prev)

	start := time.Now()
	stopAt := start.Add(dur)
	var total atomic.Uint64
	var wg sync.WaitGroup
	wg.Add(cores)
	for id := range cores {
		go func(worker int) {
			defer wg.Done()
			arena := make([]uint64, 32768) // 256 KiB AST/symbol table in L2 cache
			nums := make([]int64, 1024)
			blob := make([]byte, 8192)
			seed := uint64(0x9e3779b97f4a7c15) ^ uint64(worker+1)*0xbf58476d1ce4e5b9
			var iters, acc uint64
			for {
				seed = benchStep(seed, arena, nums, blob)
				acc ^= seed
				iters++
				if time.Now().After(stopAt) {
					break
				}
			}
			benchSink.Add(acc)
			total.Add(iters)
		}(id)
	}
	wg.Wait()
	elapsed := max(time.Since(start).Seconds(), 1e-6)
	return float64(total.Load()) / elapsed
}

func benchStep(seed uint64, arena []uint64, nums []int64, blob []byte) uint64 {
	x := seed
	mask := uint64(len(arena) - 1)
	// 1. Lexer/symbol-table hash probing & pointer-chasing updates across 256 KiB.
	for i := range 8192 {
		x ^= x >> 12
		x ^= x << 25
		x ^= x >> 27
		h := x * 0x2545f4914f6cdd1d
		idx := (h + uint64(i)) & mask
		v := arena[idx]
		if v&3 == 0 {
			arena[idx] = h ^ (v + uint64(i))
		} else {
			next := (v ^ h) & mask
			arena[next] = v + h
			x ^= arena[idx]
		}
	}
	// 2. SSA/test-like slice sort and binary search lookups.
	for i := range nums {
		x = x*6364136223846793005 + 1442695040888963407
		nums[i] = int64(x >> 17)
	}
	slices.Sort(nums)
	hits := 0
	for i := 0; i < len(nums); i += 8 {
		if _, ok := slices.BinarySearch(nums, nums[i]); ok {
			hits++
		}
	}
	x ^= uint64(hits)
	// 3. Serialization & cryptographic digest pass.
	for i := 0; i < len(blob); i += 8 {
		x = x*0x5851f42d4c957f2d + 1
		binary.LittleEndian.PutUint64(blob[i:], x)
	}
	sum := sha256.Sum256(blob)
	return x ^ binary.LittleEndian.Uint64(sum[:8])
}

// linuxMemoryBytes returns usable memory bytes on Linux, bounded by cgroup
// memory.max (v2, walking ancestors) or memory.limit_in_bytes (v1) when set.
func linuxMemoryBytes(meminfoPath, selfCgroupPath, cgroupRoot string) uint64 {
	var phys uint64
	if b, err := os.ReadFile(meminfoPath); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if rest, ok := strings.CutPrefix(line, "MemTotal:"); ok {
				f := strings.Fields(rest)
				if len(f) >= 1 {
					if kb, err := strconv.ParseUint(f[0], 10, 64); err == nil {
						phys = kb * 1024
					}
				}
				break
			}
		}
	}
	limit := phys
	clamp := func(p string) {
		b, err := os.ReadFile(p)
		if err != nil {
			return
		}
		s := strings.TrimSpace(string(b))
		if s == "" || s == "max" {
			return
		}
		if v, err := strconv.ParseUint(s, 10, 64); err == nil && v > 0 && v < (1<<60) {
			if limit == 0 || v < limit {
				limit = v
			}
		}
	}
	clamp(filepath.Join(cgroupRoot, "memory.max"))
	clamp(filepath.Join(cgroupRoot, "memory", "memory.limit_in_bytes"))
	if b, err := os.ReadFile(selfCgroupPath); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			parts := strings.SplitN(strings.TrimSpace(line), ":", 3)
			if len(parts) != 3 || parts[2] == "" {
				continue
			}
			rel := strings.TrimPrefix(parts[2], "/")
			for cur := rel; cur != "." && cur != ""; cur = filepath.Dir(cur) {
				clamp(filepath.Join(cgroupRoot, cur, "memory.max"))
				if parts[1] == "memory" {
					clamp(filepath.Join(cgroupRoot, "memory", cur, "memory.limit_in_bytes"))
				}
			}
		}
	}
	return limit
}

// parseCPUList parses kernel range lists such as "0,2,4-7".
func parseCPUList(raw string) (map[int]bool, error) {
	out := map[int]bool{}
	for _, r := range strings.FieldsFunc(strings.TrimSpace(raw), func(c rune) bool { return c == ',' }) {
		lo, hi, isRange := strings.Cut(r, "-")
		a, err := strconv.Atoi(lo)
		b := a
		if isRange && err == nil {
			b, err = strconv.Atoi(hi)
		}
		if err != nil || a < 0 || b < a {
			return nil, fmt.Errorf("invalid CPU list %q", raw)
		}
		for c := a; c <= b; c++ {
			out[c] = true
		}
	}
	return out, nil
}

// cpuList formats ascending CPUs in kernel range form, such as "0,2,4-5".
func cpuList(cpus []int) string {
	var parts []string
	for i := 0; i < len(cpus); {
		j := i
		for j+1 < len(cpus) && cpus[j+1] == cpus[j]+1 {
			j++
		}
		s := strconv.Itoa(cpus[i])
		if j > i {
			s += "-" + strconv.Itoa(cpus[j])
		}
		parts = append(parts, s)
		i = j + 1
	}
	return strings.Join(parts, ",")
}

// readSMTGroups groups usable CPUs into physical-core sibling sets using
// <sysfsDir>/cpu<N>/topology/thread_siblings_list.
func readSMTGroups(usable []int, sysfsDir string) [][]int {
	if len(usable) == 0 {
		return nil
	}
	allow := map[int]bool{}
	for _, c := range usable {
		allow[c] = true
	}
	seen := map[int]bool{}
	var groups [][]int
	for _, c := range usable {
		if seen[c] {
			continue
		}
		p := filepath.Join(sysfsDir, "cpu"+strconv.Itoa(c), "topology", "thread_siblings_list")
		b, err := os.ReadFile(p)
		var g []int
		if err == nil {
			if m, perr := parseCPUList(string(b)); perr == nil {
				for cpu := range m {
					if allow[cpu] && !seen[cpu] {
						g = append(g, cpu)
					}
				}
			}
		}
		if len(g) == 0 {
			g = []int{c}
		}
		slices.Sort(g)
		for _, cpu := range g {
			seen[cpu] = true
		}
		groups = append(groups, g)
	}
	return groups
}

// chooseCPUs selects count CPUs from usable, preferring disjoint physical
// cores: whole free SMT sibling groups when count >= group size, and otherwise
// physical cores with the fewest occupied siblings first.
func chooseCPUs(usable []int, groups [][]int, count int, inUse map[int]int) []int {
	if len(usable) == 0 {
		return nil
	}
	count = max(1, min(len(usable), count))
	if len(groups) == 0 {
		for _, c := range usable {
			groups = append(groups, []int{c})
		}
	}
	grpOf := map[int]int{}
	grpLoad := make([]int, len(groups))
	for gi, g := range groups {
		for _, c := range g {
			grpOf[c] = gi
			grpLoad[gi] += inUse[c]
		}
	}
	taken := map[int]bool{}
	var picked []int
	freeIdx := make([]int, 0, len(groups))
	for gi := range groups {
		if grpLoad[gi] == 0 {
			freeIdx = append(freeIdx, gi)
		}
	}
	slices.SortFunc(freeIdx, func(a, b int) int {
		if d := cmp.Compare(len(groups[b]), len(groups[a])); d != 0 {
			return d
		}
		return cmp.Compare(groups[a][0], groups[b][0])
	})
	rem := count
	for _, gi := range freeIdx {
		g := groups[gi]
		if len(g) <= rem {
			for _, c := range g {
				taken[c] = true
				picked = append(picked, c)
				grpLoad[gi]++
			}
			rem -= len(g)
		}
	}
	for rem > 0 {
		best := -1
		for _, c := range usable {
			if taken[c] {
				continue
			}
			if best < 0 ||
				inUse[c] < inUse[best] ||
				(inUse[c] == inUse[best] && grpLoad[grpOf[c]] < grpLoad[grpOf[best]]) ||
				(inUse[c] == inUse[best] && grpLoad[grpOf[c]] == grpLoad[grpOf[best]] && c < best) {
				best = c
			}
		}
		if best < 0 {
			break
		}
		taken[best] = true
		picked = append(picked, best)
		grpLoad[grpOf[best]]++
		rem--
	}
	slices.Sort(picked)
	return picked
}
