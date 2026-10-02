package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// snapshotChildTimeout is the default deadline one binary's `version` gets, and
// `--timeout` is how a caller changes it. It is also a package seam so a test
// can be bounded by a short clock rather than the machine's default.
//
// THIRTY SECONDS, NOT FIVE, BECAUSE THIS VERB'S FIRST EXEC IS ALWAYS A COLD ONE.
// Every other verb in this package probes tools a person has been running for
// days; `snapshot` reads a directory that was written a command ago -- the
// documented sequence is `go install ./cmd/...` and then `nova-version
// snapshot` -- so every binary in it is one this machine has never executed,
// and the platform's one-time assessment of a never-seen executable is charged
// to that first exec. Measured on the darwin/arm64 Studio over fresh
// executables: 164-571 ms cold against 5 ms warm at load 121-151 on 32 cores,
// and 140 ms median cold with a 7.03 s maximum while the tree compiled beside
// it -- which is precisely the state `go install ./cmd/...` leaves the machine
// in. A five-second bound therefore refused healthy binaries and sent the
// person to repair a build that was fine (#890, and #1554 for the class).
//
// A warm-up exec outside the bound was the other candidate and was measured and
// rejected: an exec killed at 40 ms leaves the assessment unpaid (the next exec
// of that same file still cost 101 ms against a 140 ms cold and a 7 ms warm),
// so a warm-up bounded by the same `--timeout` buys nothing, and one bounded by
// `--budget` would turn a genuinely broken binary's prompt refusal into a
// whole-budget wait. One honest bound, reachable by flag, is the smaller thing.
var snapshotChildTimeout = 30 * time.Second

// snapshotBudget is the default deadline for the WHOLE run, and `--budget` is
// how a caller changes it. A per-binary bound alone is no bound on a directory:
// sixteen tools at thirty seconds each is eight minutes, which is not an
// inventory anybody waits for. It is the same pair -- per-child `--timeout`,
// per-run `--budget` -- that `check`, `report` and `watch` already take.
var snapshotBudget = 60 * time.Second

// snapshotAdoptedTimeout bounds one ADOPTED tool's identity read, the --file
// shape of this verb. It is report's own per-tool read, so an entry whose
// installed column records a version is known without starting a process; only
// an installed argv is probed, and it gets this deadline and no more. The bound
// is report's five-second default rather than the directory shape's thirty,
// because a recorded version never pays a first-exec toll and the count is a
// manifest of adopted tools, not a scan of freshly installed binaries (#890).
var snapshotAdoptedTimeout = 5 * time.Second

// snapshotHeader is the TSV shape `snapshot` writes and `diff` reads. It is one
// string so the writer and the reader cannot drift.
const snapshotHeader = "name\tstamp\trevision\tplatform"

// row is one binary as its OWN `version` reported it. Name is the executable's
// file name; stamp, revision and platform are read off the four-token line, so
// a renamed stub cannot forge a row. Source is the structured source metadata
// the line carries (repository, revision, dirty flag, build host), and has
// tells the mixed-source gate whether the line named source at all: a binary
// that did not name source contributes no opinion to that gate, and a binary
// that did is checked against every other binary that did (#2291,
// SPEC-VERSION item 6).
type snapRow struct {
	name, stamp, revision, platform string
	src                             buildinfo.Source
	has                             bool
}

// sourceString is the one line a Source reads as on a refusal: every field
// named, in the order internal/buildinfo writes them, so the reader of the
// refusal can match it against a build's manifest without holding the
// goroutine open.
func sourceString(s buildinfo.Source) string {
	return fmt.Sprintf("repo=%s revision=%s dirty=%t build_host=%s", s.Repository, s.Revision, s.Dirty, s.BuildHost)
}

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
//
// The structured source view -- repository, revision, dirty flag, build host
// -- is read from the same line and returned separately. A line that does not
// carry the four source keys (an old binary, a foreign tool, a `go install`
// from a tag) returns src with has=false: the row is still recorded, and
// "no source" is the honest answer rather than a refusal at this verb's
// normal case. The mixed-source gate downstream compares only what the rows
// carry (#2291, SPEC-VERSION item 6).
func parseVersionLine(s string) (stamp, revision, platform string, src buildinfo.Source, has bool, ok bool) {
	f, ok := buildinfo.Parse(s)
	if !ok {
		return "", "", "", buildinfo.Source{}, false, false
	}
	src, has = f.FindSource()
	return f.Version, revisionOf(f.Version), f.Platform, src, has, true
}

// snapshotVerb has two shapes. With --file <manifest> it scopes to the ADOPTED
// rule-2 manifest (#622): it reads the manifest's entries the way report does
// and reports how many answer -- the adopted sixteen -- never how many nova-*
// executables sit in a bin directory or on PATH. With --bin/--out it inventories
// a directory of binaries by running each one's own `version`. Every path comes
// from a flag; neither the file's name nor PATH is trusted for the reading.
func snapshotVerb(c *tool.Call) *tool.Out {
	// Read once at entry, so a refusal on the way keeps its own reason: the
	// skeleton fails a --dry-run call whose verb never read it.
	dryRun := c.DryRun()
	if c.Given("file") {
		// The manifest shape reads as report does, five seconds a tool, unless
		// the caller names --timeout; --budget bounds the run in both shapes.
		timeout := snapshotAdoptedTimeout
		if c.Given("timeout") {
			timeout = c.Dur("timeout")
		}
		return snapshotAdopted(c.Str("file"), timeout, c.Dur("budget"))
	}
	bin, outPath := c.Str("bin"), c.Str("out")
	timeout, budget := c.Dur("timeout"), c.Dur("budget")
	entries, err := os.ReadDir(bin)
	if err != nil {
		return tool.Refuse(fmt.Sprintf("cannot read --bin %s (supply a readable --bin: a directory of nova-* executables)", bin))
	}
	// The run's own deadline. Every child hangs off it, so a directory of
	// binaries cannot cost more than `--budget` however many of them there are
	// and however long each one is allowed.
	run, cancelRun := context.WithTimeout(context.Background(), budget)
	defer cancelRun()
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
		ctx, cancel := context.WithTimeout(run, timeout)
		p := process(ctx, []string{path, "version"}, nil, ChildCap)
		// A deadline spent before the child even started (a loaded machine) is the
		// same timeout as one spent while it ran, whatever the start reported.
		if p.Reason != "" && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			p.Reason = "timeout"
		}
		cancel()
		if p.Reason != "" {
			reason, remedy := p.Reason, "repair the build there: go build ./cmd/"+e.Name()
			// A child killed because the RUN ran out reports `timeout` like any
			// other, since all it can see is its own cancelled context. The run's
			// context is the one that knows which bound was spent, so it is asked
			// first and a spent budget is never reported as a slow binary.
			if run.Err() != nil {
				reason = "budget"
			}
			switch reason {
			case "timeout":
				// NAME BOTH READINGS OF A TIMEOUT. A binary that does not answer
				// inside its bound is usually broken, and was the only reading
				// this line offered; the other is that the bound was spent on the
				// platform's one-time assessment of an executable this machine has
				// never run, which is what every binary in a freshly installed
				// --bin is (#890). Sending somebody to `go build` a package that
				// builds cleanly is a dead end, so the flag is named too.
				reason = "timeout after " + timeout.String()
				remedy = "repair the build there (go build ./cmd/" + e.Name() + "), or raise --timeout: the first run of a newly installed binary is assessed by the platform and that cost is charged to this deadline"
			case "budget":
				reason = "the run's " + budget.String() + " budget was spent before this binary was read"
				remedy = "raise --budget, or snapshot fewer binaries per --bin"
			}
			return tool.Refuse(fmt.Sprintf("cannot read %s version (%s) (%s)", e.Name(), reason, remedy))
		}
		stamp, revision, platform, src, has, ok := parseVersionLine(p.Stdout)
		if !ok {
			return tool.Refuse(fmt.Sprintf("cannot read %s version (it printed no version line: want `<tool> <stamp> <goos>/<goarch> <go version>` and then any key=value extras) (repair the build there: go build ./cmd/%s)", e.Name(), e.Name()))
		}
		rows = append(rows, snapRow{e.Name(), stamp, revision, platform, src, has})
	}
	if len(rows) == 0 {
		return tool.Refuse(fmt.Sprintf("--bin %s holds no nova-* regular file (supply a readable --bin: a directory of nova-* executables)", bin))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	for i := 1; i < len(rows); i++ {
		if rows[i].stamp != rows[0].stamp {
			return tool.Refuse(fmt.Sprintf("mixed stamps: %s=%s %s=%s (rebuild the set under one stamp with nova-update release build --version <v> --out <dir> --source <checkout>, then nova-update release install --from <dir> --version <v> --bin <dir>; or use a --bin per set)", rows[0].name, rows[0].stamp, rows[i].name, rows[i].stamp))
		}
	}
	// SOURCE METADATA GATE (#2291, SPEC-VERSION item 6). The version stamp
	// is one field a build can carry from a different checkout; the four
	// source keys -- repository, revision, dirty flag, build host -- are
	// the structured view of WHERE the build actually came from, and
	// every stamp read at the gate checks it. A row that does not name
	// source (an old binary, a foreign tool, a `go install` from a tag)
	// contributes no opinion, so the existing tests' four-token stubs
	// remain readable; a row that names source is checked against every
	// other row that named source, and disagreement is refused. Missing
	// in the strict sense ("a binary whose source metadata is missing")
	// is the next issue's slice, once every stamp read across the tree
	// can demand source without breaking the older binaries in the
	// wild.
	var firstSrc buildinfo.Source
	var firstSrcName string
	var firstSrcSet bool
	for _, r := range rows {
		if !r.has {
			continue
		}
		if !firstSrcSet {
			firstSrc, firstSrcName, firstSrcSet = r.src, r.name, true
			continue
		}
		if r.src != firstSrc {
			return tool.Refuse(fmt.Sprintf("mixed source: %s=%s %s=%s (rebuild the set under one source with nova-update release build --version <v> --out <dir> --source <checkout>, then nova-update release install --from <dir> --version <v> --bin <dir>; or use a --bin per set)", firstSrcName, sourceString(firstSrc), r.name, sourceString(r.src)))
		}
	}
	// The rows are the result's items as well as the file's lines, so a reader sees
	// what was recorded; --dry-run is the same reads with the write left out.
	o := tool.Done().Fact("bin", bin).Fact("out", outPath).Fact("tools", len(rows)).Fact("stamp", rows[0].stamp)
	var b strings.Builder
	b.WriteString(snapshotHeader + "\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", r.name, r.stamp, r.revision, r.platform)
		o.Item("row", "name", r.name, "stamp", r.stamp, "revision", r.revision, "platform", r.platform)
	}
	if dryRun { // the skeleton adds dry_run=true
		return o.Note("dry run: " + outPath + " not written")
	}
	if err := os.WriteFile(outPath, []byte(b.String()), 0o644); err != nil {
		return tool.Refuse(fmt.Sprintf("cannot write --out %s (supply a writable --out path)", outPath))
	}
	return o
}

// snapshotAdopted counts how many of the adopted manifest's tools answer, and is
// the --file shape of snapshotVerb (#622). It reads the rule-2 manifest --file
// names and asks each entry its identity exactly as report does, so a recorded
// installed version is known without a process and an installed argv is probed
// once. The count is the manifest's own -- the adopted sixteen -- never the
// thirty-two nova-* executables a bin directory or PATH might hold, and no file
// is written: the manifest is adopted, not discovered. The verdict mirrors
// report's: one count line, exit 0 when every adopted tool answers and exit 1
// when any does not.
func snapshotAdopted(file string, timeout, budget time.Duration) *tool.Out {
	f, err := os.Open(file)
	if err != nil {
		return tool.Refuse(fmt.Sprintf("cannot open %s (supply a readable --file: %s; nova-version example --out %s writes one to start from)", file, manifestShape, file))
	}
	entries, err := Load(f)
	f.Close()
	if err != nil {
		return tool.Refuse(fmt.Sprintf("%s: %s", file, err))
	}
	o := tool.Done()
	known := 0
	run, cancelRun := context.WithTimeout(context.Background(), budget)
	defer cancelRun()
	for _, e := range entries {
		// Installed bounds the tool by timeout under the run's context, and tells a
		// spent budget from a slow tool by that context.
		r := Installed(run, e, timeout, true)
		if r.Known() {
			known++
			continue
		}
		// Each tool that did not answer is named with its reason, so a FAIL needs
		// no second call to learn which.
		o.Item("unknown", "name", e.Name, "reason", r.Reason, "remedy", r.Remedy)
	}
	if known != len(entries) {
		o.Status, o.Exit = tool.Failed, 1
	}
	return o.Fact("checked", len(entries)).Fact("known", known).Fact("unknown", len(entries)-known).Fact("file", file)
}
