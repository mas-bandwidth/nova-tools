package update

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// snapshotTimeout bounds one tool's `version` read. The snapshot verb asks each
// nova-* executable in --bin for its version, so a hung tool cannot stall the
// whole write: each read gets this deadline and nothing more.
const snapshotTimeout = 5 * time.Second

// snapshotVerb writes the rule-2 manifest that CARD-306 left missing: one line
// per tool, six tab-separated fields, so the adoption report has a hand-editable
// draft instead of nothing. kind is binary (never one of the five curated kinds
// — a person promotes it), latest and apply are - (unknown, never guessed), and
// owner is --owner or -. A tool whose version cannot be read is counted, never
// written: it is the person's line to finish by hand.
func snapshotVerb(name string, args []string, out, errs io.Writer) int {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var bin, outp, owner string
	fs.StringVar(&bin, "bin", "", "directory of nova-* executables")
	fs.StringVar(&outp, "out", "", "manifest path to write")
	fs.StringVar(&owner, "owner", "", "owner label for every line")
	if err := fs.Parse(args); err != nil {
		return refusal(errs, "SNAPSHOT", fmt.Errorf("%s (run %s help)", err, name))
	}
	var missing []string
	if bin == "" {
		missing = append(missing, "--bin")
	}
	if outp == "" {
		missing = append(missing, "--out")
	}
	if len(missing) > 0 {
		return refusal(errs, "SNAPSHOT", fmt.Errorf("missing %s; refusing to guess (run: %s help)", strings.Join(missing, ", "), name))
	}
	if len(fs.Args()) != 0 {
		return refusal(errs, "SNAPSHOT", fmt.Errorf("snapshot takes no positional arguments (run %s help)", name))
	}
	if owner == "" {
		owner = "-"
	}
	names, err := snapshotBinNames(bin)
	if err != nil {
		return refusal(errs, "SNAPSHOT", fmt.Errorf("cannot read %s (supply a readable --bin)", bin))
	}
	var lines []string
	tools, unreadable := 0, 0
	for _, n := range names {
		full := filepath.Join(bin, n)
		ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout)
		p := process(ctx, []string{full, "version"}, nil, ChildCap)
		cancel()
		raw := p.Stdout
		if raw == "" {
			raw = p.Stderr
		}
		if p.Reason != "" {
			unreadable++
			continue
		}
		v, verr := versionKey(firstLine(raw))
		if verr != nil {
			unreadable++
			continue
		}
		lines = append(lines, strings.Join([]string{n, "binary", v, "-", "-", owner}, "\t"))
		tools++
	}
	content := Header + "\n" + strings.Join(lines, "\n")
	if len(lines) > 0 {
		content += "\n"
	}
	if err := os.WriteFile(outp, []byte(content), 0644); err != nil {
		return refusal(errs, "SNAPSHOT", fmt.Errorf("cannot write %s: %v", outp, err))
	}
	fmt.Fprintf(out, "SNAPSHOT OK tools=%d unreadable=%d out=%s\n", tools, unreadable, field(outp))
	return 0
}

func snapshotBinNames(bin string) ([]string, error) {
	entries, err := os.ReadDir(bin)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, "nova-") || e.IsDir() {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	return names, nil
}
