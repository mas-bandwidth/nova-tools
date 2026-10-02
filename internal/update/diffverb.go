package update

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// readSnapshotFile reads the TSV `snapshot` writes. A missing or wrong header,
// or a row of the wrong arity, names the file; the two files are only read.
func readSnapshotFile(path string) (map[string]snapRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open")
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 4096), 1024*1024)
	if !sc.Scan() || sc.Text() != snapshotHeader {
		return nil, fmt.Errorf("its header is not %s", tabbed(snapshotHeader))
	}
	rows := map[string]snapRow{}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 4 {
			return nil, fmt.Errorf("a row has %d fields, not 4", len(f))
		}
		rows[f[0]] = snapRow{name: f[0], stamp: f[1], revision: f[2], platform: f[3]}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("cannot read")
	}
	return rows, nil
}

// diffVerb compares two snapshots: one item per changed binary, and a DIFF OK
// line that counts the state -- every name on either side -- not the output.
func diffVerb(c *tool.Call) *tool.Out {
	from, to := c.Str("from"), c.Str("to")
	// Both files are read before either is refused, so one run names both.
	before, errFrom := readSnapshotFile(from)
	after, errTo := readSnapshotFile(to)
	if errFrom != nil || errTo != nil {
		var why []string
		for _, f := range []struct {
			path string
			err  error
		}{{from, errFrom}, {to, errTo}} {
			if f.err != nil {
				why = append(why, fmt.Sprintf("cannot read %s as a snapshot (%s)", f.path, f.err))
			}
		}
		o := tool.Refuse(strings.Join(why, "; ") + " (write one with nova-version snapshot --bin <dir> --out <file.tsv>)")
		o.Remedy = "nova-version snapshot -h"
		return o
	}
	names := map[string]bool{}
	for n := range before {
		names[n] = true
	}
	for n := range after {
		names[n] = true
	}
	ordered := make([]string, 0, len(names))
	for n := range names {
		ordered = append(ordered, n)
	}
	sort.Strings(ordered)
	o := tool.Done().Fact("from", from).Fact("to", to).Fact("tools", len(ordered))
	changed := 0
	for _, n := range ordered {
		a, inA := before[n]
		b, inB := after[n]
		if inA && inB && a == b {
			continue
		}
		o.Item("changed", "name", n, "from", stampOrDash(a.stamp, inA), "to", stampOrDash(b.stamp, inB))
		changed++
	}
	return o.Fact("changed", changed)
}

// stampOrDash is a snapshot row's stamp, or "-" for a name absent on that side:
// the same value in the lines and in the JSON.
func stampOrDash(stamp string, present bool) string {
	if !present {
		return "-"
	}
	return stamp
}
