package pulse

// fairshare.go is the deal itself (#2008): how a tick's cards are split across the benches
// that have room.
//
// The rule, in order:
//
//  1. A bench with no free slot takes nothing.
//  2. THE FLOOR. Every bench with room takes at least one card, and the floor is handed out
//     SMALLEST BENCH FIRST, so a bench with two free slots is fed before a bench with two
//     hundred has drained the pool. That ordering is the fix for the first defect the
//     hand-built dealer met on the night of the 2026-09-20 load test: rounding in list order
//     starved whatever sat at the end of the list.
//  3. THE PROPORTION. What is left is dealt in proportion to free slots, floored, never past
//     a bench's own free count.
//  4. THE REMAINDER. Flooring loses cards; they go round again, smallest first, to whoever
//     still has room, so the pool is emptied rather than left in the queue.
//
// Nothing here reads a clock, a host or a latency. That is the point: the share a bench gets
// is a function of its free slots and of nothing else, so the MacBook Air 93 ms away is dealt
// exactly what hulk on the LAN would be dealt with the same room. Glenn, that night:
// "everybody should get busy, not just the low ping bastards."

import "sort"

// fairShares deals n cards over the benches whose free-slot counts are given, in the bench's
// own index order, and answers how many each one takes. The answer never gives a bench more
// than its free count and never deals more than n in total; when the pool is bigger than the
// fleet's free capacity, every bench simply fills.
func fairShares(free []int, n int) []int {
	out := make([]int, len(free))
	if n <= 0 {
		return out
	}
	total := 0
	order := make([]int, 0, len(free))
	for i, f := range free {
		if f > 0 {
			total += f
			order = append(order, i)
		}
	}
	if total == 0 {
		return out
	}
	// Smallest room first, ties in the caller's order: the order the floor and the remainder
	// are handed out in.
	sort.SliceStable(order, func(a, b int) bool { return free[order[a]] < free[order[b]] })

	left := n
	for _, i := range order { // 2. the floor
		if left == 0 {
			break
		}
		out[i] = 1
		left--
	}
	for _, i := range order { // 3. the proportion
		if left == 0 {
			break
		}
		want := n * free[i] / total
		if want > free[i] {
			want = free[i]
		}
		add := want - out[i]
		if add > left {
			add = left
		}
		if add > 0 {
			out[i] += add
			left -= add
		}
	}
	for left > 0 { // 4. the remainder
		gave := false
		for _, i := range order {
			if left == 0 {
				break
			}
			if out[i] < free[i] {
				out[i]++
				left--
				gave = true
			}
		}
		if !gave {
			break
		}
	}
	return out
}
