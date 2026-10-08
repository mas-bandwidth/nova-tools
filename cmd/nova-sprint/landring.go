package main

// landring.go is the gate bench's hash ring (docs/SPEC-SPRINT.md section 7, the tree
// gate). The up benches, in the fleet's order, are a ring; a batch's gate starts at the
// slot its stream hashes to, FNV-1a 64-bit of the stream name modulo the ring's size, and
// steps to the next slot while a lane is held. Until 2026-10-07 every gate asked the
// benches in the fleet's order and so every gate ran on the first up member (the first in order, the
// weakest); the owner: "you should be distributing according to our regular trick, where we
// do the uint64 and it is modulo % n", "even when we have parallel land".

import "hash/fnv"

// ringHash is key's FNV-1a 64-bit hash.
func ringHash(key string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(key))
	return h.Sum64()
}

// ringSlot is the slot key hashes to on a ring of n: ringHash(key) % n, and 0 for a ring
// of none.
func ringSlot(key string, n int) int {
	if n <= 0 {
		return 0
	}
	return int(ringHash(key) % uint64(n))
}

// benchRing is hosts (the up benches in the fleet's order) rotated so that key's slot is
// first, then (slot+1) % n, ... (slot+n-1) % n: every host once, in the order the gate asks
// their lanes. No hosts is nil.
func benchRing(key string, hosts []string) []string {
	n := len(hosts)
	if n == 0 {
		return nil
	}
	s := ringSlot(key, n)
	ring := make([]string, 0, n)
	ring = append(ring, hosts[s:]...)
	return append(ring, hosts[:s]...)
}
