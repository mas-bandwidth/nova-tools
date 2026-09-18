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

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
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

// parseVersionLine takes apart the Conventions line a binary's `version` prints
// -- <tool> <stamp> <goos>/<goarch> <go version>, and then any named extras --
// through internal/buildinfo, the one place in this tree that both WRITES that
// line and reads it.
//
// It used to demand exactly four tokens, and on 2026-09-18 that cost a whole
// install: nova-merge prints a fifth `build=<hex>`, the sha256 of its own file,
// and one snapshot of ~/.local/bin refused every binary in it (#1297). An extra
// is a tool saying one more true thing about itself; every column this verb
// writes is read out of the four tokens the whole set shares, so an extra
// changes nothing here except that it is no longer a refusal.
func parseVersionLine(s string) (stamp, revision, platform string, ok bool) {
	f, ok := buildinfo.Parse(s)
	if !ok {
		return "", "", "", false
	}
	return f.Version, revisionOf(f.Version), f.Platform, true
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
		// NEITHER PATH IS GUESSED, and the refusal now says so with a line
		// somebody can paste. `--bin <dir>` reads as a complete command and is
		// not one; the fourth release dogfood met that as `missing --out` with
		// no example of what --out should be. SPEC-UPDATE rule 1 -- no search
		// of the cwd, no $HOME -- is why there is no default to fall back on.
		return refusal(errs, "SNAPSHOT", fmt.Errorf("missing %s; refusing to guess (both paths are the caller's to name, for example: %s snapshot --bin ./bin --out ./before.tsv; run: %s help)",
			strings.Join(missing, ", "), name, name))
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
			return refusal(errs, "SNAPSHOT", fmt.Errorf("cannot read %s version (it printed no version line: want `<tool> <stamp> <goos>/<goarch> <go version>` and then any key=value extras) (repair the build there: go build ./cmd/%s)", e.Name(), e.Name()))
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
