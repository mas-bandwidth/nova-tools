package tset

import (
	"reflect"
	"testing"
)

// shrinkFailingSequence removes actions while preserving the *same* failure,
// as defined by fails. For large traces the answer is deletion-minimal (no
// single remaining action can be removed); that does not imply the globally
// shortest failing subsequence. For traces of at most 16 actions, exhaustive
// subset search establishes the globally shortest subsequence instead.
func shrinkFailingSequence[T any](trace []T, fails func([]T) bool) (shortest []T, globallyShortest bool) {
	if !fails(trace) {
		return nil, false
	}
	if len(trace) <= 16 {
		for size := 0; size <= len(trace); size++ {
			if candidate, ok := failingCombination(trace, size, fails); ok {
				return candidate, true
			}
		}
	}

	shortest = append([]T(nil), trace...)
	for chunks := 2; len(shortest) > 0; {
		if chunks > len(shortest) {
			chunks = len(shortest)
		}
		removed := false
		for start := 0; start < len(shortest); start += (len(shortest) + chunks - 1) / chunks {
			end := start + (len(shortest)+chunks-1)/chunks
			if end > len(shortest) {
				end = len(shortest)
			}
			candidate := withoutRange(shortest, start, end)
			if fails(candidate) {
				shortest = candidate
				if chunks > 2 {
					chunks--
				}
				removed = true
				break
			}
		}
		if removed {
			continue
		}
		if chunks == len(shortest) {
			break
		}
		chunks *= 2
	}
	// ddmin's chunk partition may miss a single action at a partition edge.
	// This final pass establishes the promised one-deletion property.
	for i := 0; i < len(shortest); {
		candidate := withoutRange(shortest, i, i+1)
		if fails(candidate) {
			shortest = candidate
			i = 0 // deleting a later action can make an earlier one removable
			continue
		}
		i++
	}
	if len(shortest) == 0 {
		return shortest, true // no failing sequence can be shorter than empty
	}
	return shortest, false
}

func withoutRange[T any](in []T, start, end int) []T {
	out := make([]T, 0, len(in)-(end-start))
	out = append(out, in[:start]...)
	out = append(out, in[end:]...)
	return out
}

// failingCombination visits combinations in source order, so the first
// failure is deterministic. The caller only invokes it for tiny traces.
func failingCombination[T any](trace []T, size int, fails func([]T) bool) ([]T, bool) {
	candidate := make([]T, 0, size)
	var visit func(start int) ([]T, bool)
	visit = func(start int) ([]T, bool) {
		if len(candidate) == size {
			if fails(candidate) {
				return append([]T(nil), candidate...), true
			}
			return nil, false
		}
		for i := start; i <= len(trace)-(size-len(candidate)); i++ {
			candidate = append(candidate, trace[i])
			if found, ok := visit(i + 1); ok {
				return found, true
			}
			candidate = candidate[:len(candidate)-1]
		}
		return nil, false
	}
	return visit(0)
}

func TestSequenceShrinkerFindsShortestSmallFixture(t *testing.T) {
	t.Parallel()
	trace := []string{"noise", "open", "noise2", "close", "noise3", "trigger"}
	fails := func(xs []string) bool {
		opened, closed := false, false
		for _, x := range xs {
			switch x {
			case "open":
				opened = true
			case "close":
				closed = opened
			case "trigger":
				if closed {
					return true
				}
			}
		}
		return false
	}
	got, global := shrinkFailingSequence(trace, fails)
	want := []string{"open", "close", "trigger"}
	if !global || !reflect.DeepEqual(got, want) {
		t.Fatalf("shortest = %v, global = %v; want %v, true", got, global, want)
	}
}

func TestSequenceShrinkerLargeTraceIsOneMinimal(t *testing.T) {
	t.Parallel()
	trace := make([]int, 100)
	for i := range trace {
		trace[i] = i
	}
	fails := func(xs []int) bool {
		seen37 := false
		for _, x := range xs {
			if x == 37 {
				seen37 = true
			}
			if x == 91 && seen37 {
				return true
			}
		}
		return false
	}
	got, global := shrinkFailingSequence(trace, fails)
	if global || !reflect.DeepEqual(got, []int{37, 91}) {
		t.Fatalf("large shrink = %v, global = %v; want [37 91], false", got, global)
	}
	for i := range got {
		if fails(withoutRange(got, i, i+1)) {
			t.Fatalf("result is not one-minimal: removing position %d still fails", i)
		}
	}
}
