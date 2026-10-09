//go:build darwin

package hostload

import (
	"strconv"
	"strings"
)

// ParseLsof is the holders in lsof's field output (`lsof -n -P -F pcLf`): for each
// process a p line (its pid), c (its command) and L (its user), then an f line per open
// file; only numbered descriptors count (cwd, txt, mem and the like are none). A process
// with no numbered descriptor is no holder.
func ParseLsof(s string) []Holder {
	var out []Holder
	var cur *Holder
	flush := func() {
		if cur != nil && cur.Open > 0 {
			out = append(out, *cur)
		}
		cur = nil
	}
	for _, line := range strings.Split(s, "\n") {
		if line == "" {
			continue
		}
		v := line[1:]
		switch line[0] {
		case 'p':
			flush()
			if pid, err := strconv.Atoi(v); err == nil {
				cur = &Holder{PID: pid}
			}
		case 'c':
			if cur != nil {
				cur.Command = v
			}
		case 'L':
			if cur != nil {
				cur.User = v
			}
		case 'f':
			if _, err := strconv.Atoi(v); err == nil && cur != nil {
				cur.Open++
			}
		}
	}
	flush()
	return out
}
