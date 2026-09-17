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

// snapshotChildTimeout is the deadline one binary's `version` gets. It is a
// package seam so a test can be bounded by a short clock rather than the
// machine's default.
var snapshotChildTimeout = 5 * time.Second

// snapshotHeader is the TSV shape `snapshot` writes and `diff` reads. It is one
// string so the writer and the reader cannot drift.
const snapshotHeader = "name\tstamp\trevision\tplatform"

// row is one binary as its OWN `version` reported it. Name is the executable's
// file name; stamp, revision and platform are read off the four-token line, so
// a renamed stub cannot forge a row.
type snapRow struct{ name, stamp, revision, platform string }

// revisionOf is the twelve-hex commit the identity carries, or "-". The
// toolchain's vcs stamp is <utc time>-<12 hex>[-dirty]; a release tag carries
// no commit. Only an exactly-twelve hex run counts, which keeps the fourteen
// digit timestamp from being mistaken for the revision.
func revisionOf(stamp string) string {
	for _, part := range strings.FieldsFunc(stamp, func(r rune) bool {
		return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F')
	}) {
		if len(part) == 12 {
			return strings.ToLower(part)
		}
	}
	return "-"
}

// parseVersionLine parses the four-token Conventions line a binary's `version`
// prints: <tool> <stamp> <goos>/<goarch> <go version>.
func parseVersionLine(s string) (stamp, revision, platform string, ok bool) {
	f := strings.Fields(firstLine(s))
	if len(f) != 4 || !strings.Contains(f[2], "/") {
		return "", "", "", false
	}
	return f[1], revisionOf(f[1]), f[2], true
}

// snapshotVerb inventories a directory of binaries by running each one's own
// `version`. Every path comes from a flag; neither the file's name nor PATH is
// trusted for the reading.
func snapshotVerb(name string, args []string, out, errs io.Writer, env Environment) int {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var bin, outPath string
	fs.StringVar(&bin, "bin", "", "directory holding the binaries")
	fs.StringVar(&outPath, "out", "", "TSV snapshot to write")
	if err := fs.Parse(interspersed(fs, args)); err != nil {
		return refusal(errs, "SNAPSHOT", fmt.Errorf("%s (run %s help)", err, name))
	}
	var missing []string
	if bin == "" {
		missing = append(missing, "--bin")
	}
	if outPath == "" {
		missing = append(missing, "--out")
	}
	if len(missing) > 0 {
		return refusal(errs, "SNAPSHOT", fmt.Errorf("missing %s; refusing to guess (supply each named flag; run: %s help)", strings.Join(missing, ", "), name))
	}
	if len(fs.Args()) != 0 {
		return refusal(errs, "SNAPSHOT", fmt.Errorf("snapshot takes no positional arguments (run %s help)", name))
	}
	entries, err := os.ReadDir(bin)
	if err != nil {
		return refusal(errs, "SNAPSHOT", fmt.Errorf("cannot read --bin %s (supply a readable --bin: a directory of nova-* executables)", bin))
	}
	var rows []snapRow
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "nova-") {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		path := filepath.Join(bin, e.Name())
		ctx, cancel := context.WithTimeout(context.Background(), snapshotChildTimeout)
		p := process(ctx, []string{path, "version"}, nil, ChildCap)
		cancel()
		if p.Reason != "" {
			reason := p.Reason
			if reason == "timeout" {
				reason = "timeout after " + snapshotChildTimeout.String()
			}
			return refusal(errs, "SNAPSHOT", fmt.Errorf("cannot read %s version (%s) (repair the build there: go build ./cmd/%s)", e.Name(), reason, e.Name()))
		}
		stamp, revision, platform, ok := parseVersionLine(p.Stdout)
		if !ok {
			return refusal(errs, "SNAPSHOT", fmt.Errorf("cannot read %s version (it printed no four-token version line) (repair the build there: go build ./cmd/%s)", e.Name(), e.Name()))
		}
		rows = append(rows, snapRow{e.Name(), stamp, revision, platform})
	}
	if len(rows) == 0 {
		return refusal(errs, "SNAPSHOT", fmt.Errorf("--bin %s holds no nova-* regular file (supply a readable --bin: a directory of nova-* executables)", bin))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	for i := 1; i < len(rows); i++ {
		if rows[i].stamp != rows[0].stamp {
			return refusal(errs, "SNAPSHOT", fmt.Errorf("mixed stamps: %s=%s %s=%s (rebuild the set under one stamp with nova-update apply --sha, or use a --bin per set)", rows[0].name, rows[0].stamp, rows[i].name, rows[i].stamp))
		}
	}
	var b strings.Builder
	b.WriteString(snapshotHeader + "\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", r.name, r.stamp, r.revision, r.platform)
	}
	if err := os.WriteFile(outPath, []byte(b.String()), 0o644); err != nil {
		return refusal(errs, "SNAPSHOT", fmt.Errorf("cannot write --out %s (supply a writable --out path)", outPath))
	}
	fmt.Fprintf(out, "SNAPSHOT OK bin=%s out=%s tools=%d stamp=%s\n", field(bin), field(outPath), len(rows), field(rows[0].stamp))
	return 0
}
