// Package fillcfg is where the fill verb's capacity comes from: configuration, not argv and
// not a file on the bench (nx-e06, "capacity is config"; Glenn 2026-09-20: "capacity belongs
// in a per-bench registry field read every tick").
//
// The field is `share=<n>` in the notes column of the machines registry -- the same file the
// fill already reads to decide which hosts are benches, beside `certified=<date>`. It is
// read on EVERY call, so a share edited between two ticks moves the cap on the next tick,
// with no restart and no flag. Neither ramp.tsv nor a bench's shares.tsv is read here.
package fillcfg

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
)

// SharePrefix is the notes field that carries a bench's fill share.
const SharePrefix = "share="

// ShareOf reads `share=<n>` out of one machine's notes. It answers ok=false when the notes
// carry no share at all -- a bench with no configured share, which is not a share of zero --
// and an error for a share that is there and is not one whole number of 0 or more.
func ShareOf(m fleet.Machine) (n int, ok bool, err error) {
	var found []string
	for _, word := range strings.Fields(m.Notes) {
		if strings.HasPrefix(word, SharePrefix) {
			found = append(found, strings.TrimPrefix(word, SharePrefix))
		}
	}
	switch len(found) {
	case 0:
		return 0, false, nil
	case 1:
	default:
		return 0, false, fmt.Errorf("%s carries %d share= fields in its notes; one bench has one share", m.Name, len(found))
	}
	n, err = strconv.Atoi(found[0])
	if err != nil || n < 0 {
		return 0, false, fmt.Errorf("%s wants share=<whole number of 0 or more> in its notes, got share=%q", m.Name, found[0])
	}
	return n, true, nil
}

// Source is the machines registry a fill reads its shares from.
type Source struct {
	Machines string // the machines registry path
}

// Share answers one bench's configured share, re-reading the registry on every call so an
// edit takes effect on the next tick. A bench the registry does not name is an error.
func (s Source) Share(bench string) (int, bool, error) {
	if strings.TrimSpace(s.Machines) == "" {
		return 0, false, fmt.Errorf("no machines registry named; the fill share is read from its share= field")
	}
	reg, err := fleet.ReadRegistry(s.Machines)
	if err != nil {
		return 0, false, err
	}
	m, found := reg.Lookup(bench)
	if !found {
		return 0, false, fmt.Errorf("%s does not name %s, so it carries no share for it", s.Machines, bench)
	}
	return ShareOf(m)
}
