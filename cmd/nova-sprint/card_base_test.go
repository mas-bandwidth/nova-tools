package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// card base re-points a merging card only: a card before merging is refused before any git,
// naming the verb that changes its brief, and a merging card naming no REPO: line with no
// --repo-dir is refused naming the flag; nothing is written either way.
func TestCardBaseRefusesWhatItCannotRepoint(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 2")
	before := r.applies()
	code, _, errs := r.do("card base s1-1 main")
	assert.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "card base re-points a merging card")
	assert.Equal(t, before, r.applies())

	r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n")}, "s1-1")
	before = r.applies()
	code, _, errs = r.do("card base s1-1 main")
	assert.Equal(t, 2, code, errs)
	assert.Contains(t, errs, "s1-1 names no REPO: line and no --repo-dir was given")
	assert.Equal(t, before, r.applies())

	// a --count card's brief names no BASE: line to re-point
	code, _, errs = r.do("card base s1-1 main --repo-dir " + r.clone)
	assert.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "s1-1's brief names no BASE: line")
	assert.Equal(t, before, r.applies())
}
