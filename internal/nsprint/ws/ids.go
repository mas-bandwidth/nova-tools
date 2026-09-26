package ws

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// ReadIDs reads the task ids a verb's --ids argument names: "@<file>" reads
// the file ("@-" reads in), one or more ids per line split on whitespace or
// commas, '#' to the end of a line a comment; anything else is the ids
// themselves, comma- or space-separated. Duplicates are dropped, first
// occurrence kept, so the order is the file's.
func ReadIDs(arg string, in io.Reader) ([]string, error) {
	var r io.Reader
	switch {
	case arg == "@-":
		if in == nil {
			return nil, fmt.Errorf("--ids @- needs standard input")
		}
		r = in
	case strings.HasPrefix(arg, "@"):
		f, err := os.Open(arg[1:])
		if err != nil {
			return nil, fmt.Errorf("--ids %s: %w", arg, err)
		}
		defer f.Close()
		r = f
	default:
		r = strings.NewReader(arg)
	}
	seen := map[string]bool{}
	var ids []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		for _, id := range strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("--ids %s: %w", arg, err)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("--ids %s names no id", arg)
	}
	return ids, nil
}
