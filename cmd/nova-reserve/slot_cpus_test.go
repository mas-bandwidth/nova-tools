package main

import (
	"errors"
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// span is lo..hi, ascending.
func span(lo, hi int) []int {
	var s []int
	for c := lo; c <= hi; c++ {
		s = append(s, c)
	}
	return s
}

func TestSlotShareIsItsPartOfTheUsableCPUs(t *testing.T) {
	t.Parallel()
	holes := append(span(0, 3), span(8, 11)...) // an allowed set of 0-3,8-11
	for _, c := range []struct {
		name   string
		usable []int
		n, k   int
		want   string
	}{
		{"16 cpus 8 slots, first", span(0, 15), 8, 1, "0-1"},
		{"16 cpus 8 slots, third", span(0, 15), 8, 3, "4-5"},
		{"16 cpus 8 slots, last", span(0, 15), 8, 8, "14-15"},
		{"32 cpus 16 slots, third", span(0, 31), 16, 3, "4-5"},
		{"32 cpus 16 slots, last", span(0, 31), 16, 16, "30-31"},
		{"64 cpus less 1-15, 8 slots, first", append([]int{0}, span(16, 63)...), 8, 1, "0,16-20"},
		{"64 cpus less 1-15, 8 slots, second", append([]int{0}, span(16, 63)...), 8, 2, "21-26"},
		{"64 cpus less 1-15, 8 slots, last", append([]int{0}, span(16, 63)...), 8, 8, "57-63"},
		{"4 cpus 16 slots, first", span(0, 3), 16, 1, "0"},
		{"4 cpus 16 slots, fourth", span(0, 3), 16, 4, "3"},
		{"4 cpus 16 slots, fifth wraps", span(0, 3), 16, 5, "0"},
		{"4 cpus 16 slots, last", span(0, 3), 16, 16, "3"},
		{"holes, 2 slots, first", holes, 2, 1, "0-3"},
		{"holes, 2 slots, second", holes, 2, 2, "8-11"},
		{"holes, 3 slots, second", holes, 3, 2, "2-3,8"},
		{"holes, 3 slots, third", holes, 3, 3, "9-11"},
		{"one slot", span(0, 7), 1, 1, "0-7"},
	} {
		assert.Equal(t, c.want, cpuList(share(c.usable, c.n, c.k)), c.name)
	}
}

// TestSlotSharesCoverTheUsableCPUsOnce: with no more slots than CPUs, the
// shares are disjoint, in order, cover every usable CPU, and differ in size by
// at most one; with more, every slot still has one CPU.
func TestSlotSharesCoverTheUsableCPUsOnce(t *testing.T) {
	t.Parallel()
	for m := 1; m <= 40; m++ {
		usable := span(100, 100+m-1)
		for n := 1; n <= 50; n++ {
			var all []int
			sizes := map[int]bool{}
			for k := 1; k <= n; k++ {
				s := share(usable, n, k)
				require.NotEmpty(t, s, "m=%d n=%d k=%d", m, n, k)
				all = append(all, s...)
				sizes[len(s)] = true
			}
			if n > m {
				assert.Len(t, all, n, "m=%d n=%d: one CPU each", m, n)
				continue
			}
			assert.Equal(t, usable, all, "m=%d n=%d", m, n)
			size := slices.Sorted(maps.Keys(sizes))
			assert.LessOrEqual(t, size[len(size)-1]-size[0], 1, "m=%d n=%d", m, n)
		}
	}
	assert.Empty(t, share(nil, 8, 1), "no usable CPUs: nothing to pin")
}

func TestCPUListIsTheKernelsRangeForm(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", cpuList(nil))
	assert.Equal(t, "5", cpuList([]int{5}))
	assert.Equal(t, "0-3", cpuList(span(0, 3)))
	assert.Equal(t, "0,2,4-5,9", cpuList([]int{0, 2, 4, 5, 9}))
}

// pinning gives a harness 16 usable CPUs and records what run pins.
func pinning(h *harness) *[]int {
	var pinned []int
	h.w.usable = func() ([]int, error) { return span(0, 15), nil }
	h.w.pin = func(cpus []int) error { pinned = cpus; return nil }
	return &pinned
}

func TestRunPinsTheCommandToItsSlotsShare(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 8)
	pinned := pinning(h)
	h.take(t, h.opts(0, false, 0)) // slot1 is someone else's
	require.Equal(t, 0, run([]string{"--", "make"}, h.w))
	assert.Equal(t, "nova-reserve: HELD mode=shared slot=2/8 pid=42 cpus=2-3\n", h.err.String())
	assert.Equal(t, []int{2, 3}, *pinned)
	assert.Equal(t, []string{"make"}, h.ran)
}

func TestExclusiveRunPinsTheCommandToEveryUsableCPU(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setCap(t, 8)
	pinned := pinning(h)
	require.Equal(t, 0, run([]string{"--exclusive", "--", "make"}, h.w))
	assert.Equal(t, "nova-reserve: HELD mode=exclusive slot=all pid=42 cpus=0-15\n", h.err.String())
	assert.Equal(t, span(0, 15), *pinned)
}

func TestRunThatCannotPinRunsUnpinned(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name       string
		usableErr  error
		pinErr     error
		wantReason string
	}{
		{"setting fails", nil, errors.New("sched_setaffinity: operation not permitted"), "sched_setaffinity: operation not permitted"},
		{"reading fails", errors.New("sched_getaffinity: invalid argument"), nil, "sched_getaffinity: invalid argument"},
	} {
		h := newHarness(t)
		h.setCap(t, 8)
		pinned := pinning(h)
		if c.usableErr != nil {
			h.w.usable = func() ([]int, error) { return nil, c.usableErr }
		}
		if c.pinErr != nil {
			h.w.pin = func([]int) error { return c.pinErr }
		}
		h.code = 3
		assert.Equal(t, 3, run([]string{"--", "make"}, h.w), c.name)
		assert.Equal(t, "nova-reserve: running unpinned: "+c.wantReason+"\nnova-reserve: HELD mode=shared slot=1/8 pid=42\n", h.err.String(), c.name)
		assert.Equal(t, []string{"make"}, h.ran, c.name)
		assert.Empty(t, *pinned, c.name)
	}
}
