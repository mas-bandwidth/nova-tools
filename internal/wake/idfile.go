package wake

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// The two id files: --not-mine on watch and --rest on probe. Both are the
// caller's own list of facts this tool cannot compute, and both are read the
// same way -- blank lines and lines beginning # skipped, a malformed entry exit
// 2 naming the file and the line number, and a file that cannot be read exit 2
// rather than a silent empty set.
//
// THE UNREADABLE FILE IS THE POINT. A --not-mine that read as an empty set
// would wake the window on its own words and call that the default; a --rest
// that read as an empty roll would ping a line that had said it was stopping.
// So neither is ever inferred from a file this tool could not open.

// ReadIDs reads a file of one forge node id per line. This tool posts nothing
// and composes nothing, so it can only be TOLD which items are this actor's:
// an id is a fact the poster has and a login is not.
func ReadIDs(path string) (map[string]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]bool{}
	scan := bufio.NewScanner(f)
	n := 0
	for scan.Scan() {
		n++
		line := strings.TrimSpace(scan.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !nodeID(line) {
			return nil, fmt.Errorf("%s line %d: %q is not a forge node id; one id per line, and a head sha is one of them", path, n, line)
		}
		out[line] = true
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Rest is one declared rest: the name that rests, the id of the note IN WHICH
// THE LINE SAID SO, and when -- so the declaration is on the record and a
// person can open the note this tool does not read.
type Rest struct {
	Name  string
	Note  string
	Stamp string
}

// ReadRest reads --rest's roll. A named line found there is RESTING before
// anything else is decided -- no draft is sent and no ping is recorded -- and
// no clock ends it: a rest ends on the line's own return and the window's hand.
func ReadRest(path string) (map[string]Rest, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]Rest{}
	scan := bufio.NewScanner(f)
	n := 0
	for scan.Scan() {
		n++
		line := strings.TrimSpace(scan.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 3 {
			return nil, fmt.Errorf("%s line %d: a rest is <line name> <note id> <stamp>, three fields, and this line has %d", path, n, len(parts))
		}
		out[parts[0]] = Rest{Name: parts[0], Note: parts[1], Stamp: parts[2]}
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// nodeID is the shape check, and it is deliberately a SHAPE and not a
// vocabulary: a forge's node id is the forge's to spell, a head sha is one of
// the things this file may hold, and what this tool can say is that an id is
// one token with no whitespace in it.
func nodeID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_', r == '-', r == '=', r == '.', r == '/', r == ':':
		default:
			return false
		}
	}
	return true
}
