package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func span(lo, hi int) []int {
	var s []int
	for c := lo; c <= hi; c++ {
		s = append(s, c)
	}
	return s
}

func TestCPUListIsTheKernelsRangeForm(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", cpuList(nil))
	assert.Equal(t, "5", cpuList([]int{5}))
	assert.Equal(t, "0-3", cpuList(span(0, 3)))
	assert.Equal(t, "0,2,4-5,9", cpuList([]int{0, 2, 4, 5, 9}))
}

func TestCoresForHoldFollowsFormula(t *testing.T) {
	t.Parallel()
	// 16 ordinary cores = 16 CPU units: cpu 4 -> 4 cores.
	ordinary := &MachineSpec{UsableCores: 16, Resources: map[string]int{"cpu": 16, "memory": 64}}
	assert.Equal(t, 4, coresForHold(4, false, ordinary))
	assert.Equal(t, 1, coresForHold(1, false, ordinary))
	assert.Equal(t, 16, coresForHold(1, true, ordinary))

	// 16 stronger cores = 48 CPU units: cpu 4 -> 1 core, cpu 12 -> 4 cores, cpu 1 -> min 1 core.
	strong := &MachineSpec{UsableCores: 16, Resources: map[string]int{"cpu": 48, "memory": 64}}
	assert.Equal(t, 1, coresForHold(4, false, strong))
	assert.Equal(t, 4, coresForHold(12, false, strong))
	assert.Equal(t, 1, coresForHold(1, false, strong))
}

func TestSMTGroupsAndDisjointPhysicalCoreSelection(t *testing.T) {
	t.Parallel()
	sysfs := t.TempDir()
	// 8 logical CPUs = 4 physical cores: (0,4), (1,5), (2,6), (3,7).
	for c := range 4 {
		for _, sib := range []int{c, c + 4} {
			dir := filepath.Join(sysfs, "cpu"+strconv.Itoa(sib), "topology")
			require.NoError(t, os.MkdirAll(dir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "thread_siblings_list"),
				[]byte(strconv.Itoa(c)+","+strconv.Itoa(c+4)+"\n"), 0o644))
		}
	}
	usable := span(0, 7)
	groups := readSMTGroups(usable, sysfs)
	assert.Equal(t, [][]int{{0, 4}, {1, 5}, {2, 6}, {3, 7}}, groups)

	// Two 2-core holds get disjoint SMT sibling pairs: (0,4) then (1,5).
	inUse := map[int]int{}
	a := chooseCPUs(usable, groups, 2, inUse)
	assert.Equal(t, []int{0, 4}, a)
	for _, c := range a {
		inUse[c]++
	}
	b := chooseCPUs(usable, groups, 2, inUse)
	assert.Equal(t, []int{1, 5}, b)
	for _, c := range b {
		inUse[c]++
	}

	// Two 1-core holds get separate free physical cores: 2 then 3 (not 2 and 6!).
	c1 := chooseCPUs(usable, groups, 1, inUse)
	assert.Equal(t, []int{2}, c1)
	inUse[2]++
	c2 := chooseCPUs(usable, groups, 1, inUse)
	assert.Equal(t, []int{3}, c2)
}

func TestLinuxCgroupMemoryBoundsPhysicalMemory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	meminfo := filepath.Join(root, "meminfo")
	require.NoError(t, os.WriteFile(meminfo, []byte("MemTotal:       67108864 kB\n"), 0o644)) // 64 GiB

	cgRoot := filepath.Join(root, "cgroup")
	sliceDir := filepath.Join(cgRoot, "user.slice", "job.scope")
	require.NoError(t, os.MkdirAll(sliceDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cgRoot, "user.slice", "memory.max"), []byte("17179869184\n"), 0o644)) // 16 GiB
	require.NoError(t, os.WriteFile(filepath.Join(sliceDir, "memory.max"), []byte("max\n"), 0o644))

	selfCg := filepath.Join(root, "self.cgroup")
	require.NoError(t, os.WriteFile(selfCg, []byte("0::/user.slice/job.scope\n"), 0o644))

	got := linuxMemoryBytes(meminfo, selfCg, cgRoot)
	assert.Equal(t, uint64(16)<<30, got)
}

func TestRunPinsDisjointCPUsAndHandlesPinFailure(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	usable := span(0, 7)
	groups := [][]int{{0, 4}, {1, 5}, {2, 6}, {3, 7}}
	var pinned []int
	h.w.usable = func() ([]int, error) { return usable, nil }
	h.w.groups = func([]int) [][]int { return groups }
	h.w.pin = func(cpus []int) error { pinned = cpus; return nil }
	h.setCap(t, 8, 16, 8)
	h.startInMemoryLocalServer(t, usable, groups)

	// Hold 1 takes cpu 2 -> cpus 0,4.
	other := h.sibling(10)
	stopHold := other.startHold(t, "--needs", "cpu 2, memory 2")
	defer stopHold()

	// Run takes cpu 2 -> gets disjoint physical core cpus 1,5!
	require.Equal(t, 0, run([]string{"--dir", h.dir, "--needs", "cpu 2, memory 2", "--", "make"}, h.w))
	assert.Equal(t, []int{1, 5}, pinned)
	assert.Contains(t, h.err.String(), "host=local cpu=2 memory=2 cores=2 pid=42 cpus=1,5\n")

	// When pinning fails, command still runs with one warning line on stderr.
	h.err.Reset()
	pinned = nil
	h.w.pin = func([]int) error { return errors.New("sched_setaffinity: operation not permitted") }
	h.code = 3
	assert.Equal(t, 3, run([]string{"--dir", h.dir, "--needs", "cpu 2, memory 2", "--", "make"}, h.w))
	assert.Contains(t, h.err.String(), "nova-reserve: running unpinned: sched_setaffinity: operation not permitted\n")
	assert.Contains(t, h.err.String(), "host=local cpu=2 memory=2 cores=2 pid=42\n")
}
