package main

// The convergence verb asks whether each stream is converging: it reports
// the health metric, the contraction ratio per stream, on every tick, from
// one place, so that one run's answer can be diffed against the next one's.
// Answered by hand out of several places, the answer is a paragraph nobody can diff.
//
// Seven streams, each read from a real source through a seam: the forge, a
// checkout, the receipts, the retired README, a bin, a version snapshot and the
// pit-stop ledger. Every path comes from a flag; a stream whose source was not
// named is ABSENT and says so, because a stream nobody measured printed as zero
// is the failure this verb exists to remove. See docs/SPEC-CHECK.md.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/converge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The hints, one per required flag. Each says what the flag IS and what a first
// run should put there -- the same law the rest of this binary keeps, with its
// own --ledger, which here is the pit-stop ledger and not the corpus one.
const (
	convRepoHint     = `--repo <owner/name> is the forge repository the queue and the batches are read from (mas-bandwidth/nova-tools); it is a name on a forge, never a directory`
	convLedgerHint   = `--ledger <file> is the pit-stop ledger: the markdown whose table rows carry PASS, FAIL, PARTIAL or TODO in their last cell, and whose open rows are the LEDGER stream`
	convReceiptsHint = `--receipts <dir> is the dogfood receipts directory, the same one nova-dev dogfood reads; its open edges are the EDGES stream`
	convRetiredHint  = `--retired <file> is the retired-scripts README, whose dated rows say what the window retired; with --bin it is the SCRIPTS stream`
	convSinceHint    = `--since <RFC3339|24h> is the far edge of the window: an instant (2026-09-18T00:00:00Z) or how long ago it starts (24h); there is no default, because the window is the whole question`
)

// convDefaultTimeout is the budget one child -- a gh read, a git read -- gets
// before it is killed and named. The same minute the rest of this family gives
// a subprocess: long enough for a cold forge, short enough that a wait ends.
const convDefaultTimeout = 60

// convergenceHint returns this verb's own hint line for a required flag,
// already indented, newline included. It returns package constants only, which
// is why printing its result is safe.
func convergenceHint(name string) string {
	switch name {
	case "repo":
		return "  " + convRepoHint + "\n"
	case "ledger":
		return "  " + convLedgerHint + "\n"
	case "receipts":
		return "  " + convReceiptsHint + "\n"
	case "retired":
		return "  " + convRetiredHint + "\n"
	case "since":
		return "  " + convSinceHint + "\n"
	}
	return ""
}

// requireConvergenceFlags reports EVERY missing required flag, not the first,
// each with the hint that says what it wants. It is this verb's own because
// this verb's --ledger is a different ledger from the corpus verb's, and a hint
// that names the wrong document is worse than none.
func requireConvergenceFlags(stderr io.Writer, required map[string]*string) bool {
	names := make([]string, 0, len(required))
	for name := range required {
		names = append(names, name)
	}
	sort.Strings(names)
	ok := true
	for _, name := range names {
		if *required[name] == "" {
			fmt.Fprintf(stderr, "nova-dev convergence: --%s is required; refusing to guess; run: nova-dev help\n%s", oneline.Field(name), convergenceHint(name))
			ok = false
		}
	}
	return ok
}

// convFlags holds the convergence verb's declared flags, so the skeleton's
// flag set and the verb's own declare the same flags from one place: the
// skeleton answers -h and refuses a typo before the verb runs.
type convFlags struct {
	repo, ledger, receipts, retired, since                        *string
	bin, repoDir, batchLogs, versions, certs, state, now, gh, git *string
	timeout                                                       *int
	asJSON                                                        *bool
	by                                                            repeatable
}

// convergenceFlagValues declares convergence's flags on fs.
func convergenceFlagValues(fs *flag.FlagSet) *convFlags {
	var f convFlags
	f.repo = fs.String("repo", "", "forge repository the queue and the batches are read from, owner/name (required)")
	f.ledger = fs.String("ledger", "", "pit-stop ledger markdown; its open rows are the LEDGER stream (required)")
	f.receipts = fs.String("receipts", "", "dogfood receipts directory; its open edges are the EDGES stream (required)")
	f.retired = fs.String("retired", "", "retired-scripts README; its dated rows are what the window retired (required)")
	f.since = fs.String("since", "", "far edge of the window: an RFC3339 instant, or a duration such as 24h (required)")

	f.bin = fs.String("bin", "", "directory of scripts not yet replaced by a verb; without it the SCRIPTS stream is absent")
	f.repoDir = fs.String("repo-dir", "", "a checkout of --repo, read only; without it the CLASSES stream is absent")
	f.batchLogs = fs.String("batch-logs", "", "directory of <pr>-round-<n>.log gate logs; the second source for a batch's rounds")
	f.versions = fs.String("versions", "", "a nova-version snapshot, or a fleet roll-up of them; without it the FLEET stream is absent")
	f.certs = fs.String("certs", "", "a name<TAB>status certificate roll-up, for FLEET's certified fraction")
	f.state = fs.String("state", "", "where the last tick is remembered; without it no streak can be two and the verb never exits 1")
	f.now = fs.String("now", "", "take the reading as of this RFC3339 instant instead of the clock, so a tick can be re-read exactly")
	f.gh = fs.String("gh", "gh", "the gh executable the forge is read through")
	f.git = fs.String("git", "git", "the git executable --repo-dir is read through")
	f.timeout = fs.Int("timeout", convDefaultTimeout, "seconds one child read may take before it is killed and named")
	f.asJSON = fs.Bool("json", false, "print the reading as one JSON object instead of the lines")
	fs.Var(&f.by, "by", "narrow the EDGES rounds to this friend's receipts (repeatable; empty reads them all)")
	return &f
}

func cmdConvergence(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("convergence", flag.ContinueOnError)
	f := convergenceFlagValues(fs)

	if !parseFlags(fs, args, stderr) {
		return 2
	}
	if !requireConvergenceFlags(stderr, map[string]*string{
		"repo": f.repo, "ledger": f.ledger, "receipts": f.receipts, "retired": f.retired, "since": f.since,
	}) {
		return 2
	}
	if *f.timeout <= 0 {
		fmt.Fprintf(stderr, "nova-dev convergence: --timeout must be a positive number of seconds (got %d); a child with no deadline is a wait with no end; run: nova-dev help\n", *f.timeout)
		return 2
	}

	now := time.Now().UTC()
	if *f.now != "" {
		at, err := time.Parse(time.RFC3339, *f.now)
		if err != nil {
			fmt.Fprintf(stderr, "nova-dev convergence: --now %s is not an RFC3339 instant; %s; run: nova-dev help\n", oneline.Field(*f.now), oneline.Err(err))
			return 2
		}
		now = at.UTC()
	}
	sinceAt, err := converge.ParseSince(*f.since, now)
	if err != nil {
		fmt.Fprintf(stderr, "nova-dev convergence: %s; run: nova-dev help\n", oneline.Err(err))
		return 2
	}

	budget := time.Duration(*f.timeout) * time.Second
	opts := converge.Options{
		Repo:         *f.repo,
		LedgerPath:   *f.ledger,
		ReceiptsDir:  *f.receipts,
		RetiredPath:  *f.retired,
		BinDir:       *f.bin,
		RepoDir:      *f.repoDir,
		BatchLogs:    *f.batchLogs,
		VersionsPath: *f.versions,
		CertsPath:    *f.certs,
		By:           f.by,
		Since:        sinceAt,
		Now:          now,
		Forge:        converge.GH{Repo: *f.repo, Timeout: budget, Bin: *f.gh},
	}
	if *f.repoDir != "" {
		opts.Git = converge.RealGit{Dir: *f.repoDir, Timeout: budget, Bin: *f.git}
	}

	st, err := converge.LoadState(*f.state)
	if err != nil {
		fmt.Fprintf(stderr, "nova-dev convergence: %s; run: nova-dev help\n", oneline.Err(err))
		return 2
	}
	report, err := converge.Read(context.Background(), opts)
	if err != nil {
		fmt.Fprintf(stderr, "nova-dev convergence: %s; run: nova-dev help\n", oneline.Err(err))
		return 2
	}
	report, next, streak := report.Apply(st, now)
	if err := next.Save(*f.state); err != nil {
		fmt.Fprintf(stderr, "nova-dev convergence: --state %s could not be written: %s; run: nova-dev help\n", oneline.Field(*f.state), oneline.Err(err))
		return 2
	}

	if *f.asJSON {
		raw, err := json.Marshal(report.AsJSON(now, sinceAt))
		if err != nil {
			fmt.Fprintf(stderr, "nova-dev convergence: %s; run: nova-dev help\n", oneline.Err(err))
			return 2
		}
		printJSON(stdout, raw)
	} else {
		printLines(stdout, report.Lines())
	}
	if streak {
		return 1
	}
	return 0
}

// printLines writes the reading. Every field of every line was rendered through
// internal/oneline inside internal/converge, where the spec's hostile-value test
// pins it, so what arrives here is already one line and one token per field.
func printLines(w io.Writer, lines []string) {
	for _, line := range lines {
		fmt.Fprintf(w, "%s\n", line)
	}
}

// printJSON writes the object encoding/json built. json.Marshal escapes every
// control character as \u, so the object is one line whatever a title, a stamp
// or a ledger cell holds.
func printJSON(w io.Writer, raw []byte) {
	fmt.Fprintf(w, "%s\n", raw)
}
