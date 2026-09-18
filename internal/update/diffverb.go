package update

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
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
		return nil, fmt.Errorf("its header is not %q", snapshotHeader)
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
		rows[f[0]] = snapRow{f[0], f[1], f[2], f[3]}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("cannot read")
	}
	return rows, nil
}

// diffVerb compares two snapshots and prints one line per changed binary. The
// closing DIFF OK counts the state -- every name on either side -- not the
// output.
func diffVerb(name string, args []string, out, errs io.Writer) int {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var from, to string
	fs.StringVar(&from, "from", "", "snapshot to compare from")
	fs.StringVar(&to, "to", "", "snapshot to compare to")
	if err := fs.Parse(interspersed(fs, args)); err != nil {
		return refusal(errs, "DIFF", fmt.Errorf("%s (run %s help)", err, name))
	}
	var missing []string
	if from == "" {
		missing = append(missing, "--from")
	}
	if to == "" {
		missing = append(missing, "--to")
	}
	if len(missing) > 0 {
		return refusal(errs, "DIFF", fmt.Errorf("missing %s; refusing to guess (supply each named flag; run: %s help)", strings.Join(missing, ", "), name))
	}
	if len(fs.Args()) != 0 {
		return refusal(errs, "DIFF", fmt.Errorf("diff takes no positional arguments (run %s help)", name))
	}
	before, err := readSnapshotFile(from)
	if err != nil {
		return refusal(errs, "DIFF", fmt.Errorf("cannot read %s as a snapshot (%s) (write one with nova-version snapshot --bin <dir> --out <file.tsv>)", from, err))
	}
	after, err := readSnapshotFile(to)
	if err != nil {
		return refusal(errs, "DIFF", fmt.Errorf("cannot read %s as a snapshot (%s) (write one with nova-version snapshot --bin <dir> --out <file.tsv>)", to, err))
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
	changed := 0
	for _, n := range ordered {
		a, inA := before[n]
		b, inB := after[n]
		switch {
		case inA && inB && a == b:
			continue
		case !inA:
			fmt.Fprintf(out, "DIFF CHANGED name=%s from=- to=%s\n", field(n), field(b.stamp))
		case !inB:
			fmt.Fprintf(out, "DIFF CHANGED name=%s from=%s to=-\n", field(n), field(a.stamp))
		default:
			fmt.Fprintf(out, "DIFF CHANGED name=%s from=%s to=%s\n", field(n), field(a.stamp), field(b.stamp))
		}
		changed++
	}
	fmt.Fprintf(out, "DIFF OK from=%s to=%s tools=%d changed=%d\n", field(from), field(to), len(ordered), changed)
	return 0
}
