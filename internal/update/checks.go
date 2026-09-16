package update

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// checksHeader is the one line a checks file must carry first, byte for byte,
// mirroring rule 2's header for the versions manifest: the adoption checks file
// is kept in git and names one check per line, so a person's edit to what the
// rebuild must adopt is a diff somebody read.
const checksHeader = "name\targv"

// check is one line of a checks file: the check's name and the argv that runs
// it. The argv is rule 3's: split on single spaces, executed directly, no shell.
type check struct {
	Name string
	Argv []string
}

func loadChecks(r io.Reader) ([]check, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4096), 1024*1024)
	if !sc.Scan() || sc.Text() != checksHeader {
		return nil, fmt.Errorf("line 1: invalid header (put the header back exactly: %s)", checksHeader)
	}
	var out []check
	line := 1
	for sc.Scan() {
		line++
		s := sc.Text()
		if strings.HasPrefix(s, "#") {
			continue
		}
		f := strings.Split(s, "\t")
		if len(f) != 2 {
			return nil, fmt.Errorf("line %d: %d fields, want 2 (use the two-column TSV header)", line, len(f))
		}
		for _, v := range f {
			if v == "" {
				return nil, fmt.Errorf("line %d: empty field (supply both fields)", line)
			}
		}
		a, err := argv(f[1])
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		out = append(out, check{Name: f[0], Argv: a})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("line %d: unreadable checks file (use lines below 1 MiB)", line)
	}
	return out, nil
}
