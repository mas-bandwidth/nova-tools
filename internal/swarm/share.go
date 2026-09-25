package swarm

// A bench's share from memory, not a guess (nova-tools#2019).
//
// `slots init` and the provisioning scripts sized a bench by hand: an operator
// guessed how many cards the box would hold, and the fleet ran far below what
// its memory allowed (vision 75 GB free at 72 cards, hulk 102 GB at 37). The
// opencode harness process averaged 540-640 MB RSS per card, so a share can be
// derived from two measured numbers instead: the bench's total usable memory
// and the per-card figure the harness actually takes, with headroom.

// ShareMemoryHeadroom is how many times the measured per-card RSS a card is
// planned at before the divide. A card measured at ~600 MB is budgeted ~1.2 GB,
// so a spike in one card does not spill into its neighbour and the OS keeps a
// cushion of its own rather than the whole box being priced at the measured
// minimum.
const ShareMemoryHeadroom = 2

// ShareFromMemory derives how many cards a bench may run at once from its total
// usable memory and a measured per-card figure, both in bytes: the total is
// divided by the per-card figure scaled by ShareMemoryHeadroom. It is the
// memory half of a bench's width -- the number that used to be a guess. A
// non-positive total or per-card figure derives no share: nothing was measured,
// or there is nothing to divide into, and 0 is "unmeasured", never a number
// somebody invented.
func ShareFromMemory(totalMemBytes, perCardBytes int64) int {
	if totalMemBytes <= 0 || perCardBytes <= 0 {
		return 0
	}
	return int(totalMemBytes / (perCardBytes * ShareMemoryHeadroom))
}
