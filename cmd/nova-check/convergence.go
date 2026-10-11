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
	"fmt"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/converge"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// The hints, one per required flag. Each says what the flag is and what a first
// run should put there, with this verb's own --ledger, which here is the
// pit-stop ledger and not the corpus one.
const (
	convRepoHint     = `--repo <owner/name> is the forge repository the queue and the batches are read from (an owner/name such as example/project); it is a name on a forge, never a directory`
	convLedgerHint   = `--ledger <file> is the pit-stop ledger: the markdown whose table rows carry PASS, FAIL, PARTIAL or TODO in their last cell, and whose open rows are the LEDGER stream`
	convReceiptsHint = `--receipts <dir> is the dogfood receipts directory, the same one nova-check dogfood reads; its open edges are the EDGES stream`
	convRetiredHint  = `--retired <file> is the retired-scripts README, whose dated rows say what the window retired; with --bin it is the SCRIPTS stream`
	convSinceHint    = `--since <RFC3339|24h> is the far edge of the window: an instant (2026-09-18T00:00:00Z) or how long ago it starts (24h); there is no default, because the window is the whole question`
)

// convDefaultTimeout is the budget one child -- a gh read, a git read -- gets
// before it is killed and named. The same minute the rest of this family gives
// a subprocess: long enough for a cold forge, short enough that a wait ends.
const convDefaultTimeout = 60

func convergenceVerb() tool.Verb {
	return tool.Verb{
		Name: "convergence",
		Usage: "convergence --repo <owner/name> --ledger <md> --receipts <dir> --retired <file> --since <RFC3339|24h> " +
			"[--bin <dir>] [--repo-dir <dir>] [--batch-logs <dir>] [--versions <tsv>] " +
			"[--certs <tsv>] [--state <file>] [--by <name>] [--json] [--dry-run]",
		Effect: tool.Effect("local write: --state stores the two-tick streak (--dry-run writes none); LANDING and PRS read the forge through gh, over the network, and CLASSES reads --repo-dir through git"),
		Detail: "LANDING and PRS read the forge through gh; CLASSES reads the optional checkout through git. SCRIPTS,\n" +
			"EDGES, FLEET and LEDGER read the named paths. --state stores the two-tick streak. Each stream shows now,\n" +
			"--since, ratio and trend; an unnamed optional source is ABSENT, not zero.",
		ExitTable: exitCodes + "; 1 is two consecutive widening ticks",
		DryRun:    true,
		Flags: func(f *tool.Flags) {
			f.Prints()
			f.Required("repo", convRepoHint)
			f.Required("ledger", convLedgerHint)
			f.Required("receipts", convReceiptsHint)
			f.Required("retired", convRetiredHint)
			f.Required("since", convSinceHint)
			f.String("bin", "", "directory of scripts not yet replaced by a verb; without it the SCRIPTS stream is absent")
			f.String("repo-dir", "", "a checkout of --repo, read only; without it the CLASSES stream is absent")
			f.String("batch-logs", "", "directory of <pr>-round-<n>.log gate logs; the second source for a batch's rounds")
			f.String("versions", "", "a nova-version snapshot, or a fleet roll-up of them; without it the FLEET stream is absent")
			f.String("certs", "", "a name<TAB>status certificate roll-up, for FLEET's certified fraction")
			f.String("state", "", "where the last tick is remembered; without it no streak can be two and the verb never exits 1")
			f.String("now", "", "take the reading as of this RFC3339 instant instead of the clock, so a tick can be re-read exactly")
			f.String("gh", "gh", "the gh executable the forge is read through")
			f.String("git", "git", "the git executable --repo-dir is read through")
			f.Int("timeout", convDefaultTimeout, "seconds one child read may take before it is killed and named")
			f.Bool("json", false, "print the reading as one JSON object instead of the lines")
			f.Var(&repeatable{}, "by", "narrow the EDGES rounds to this friend's receipts (repeatable; empty reads them all)")
			f.Check(func(c *tool.Call) {
				if n := c.Int("timeout"); n <= 0 {
					c.Problem(fmt.Sprintf("--timeout must be a positive number of seconds (got %d); a child with no deadline is a wait with no end", n))
				}
			})
		},
		Run: convergence,
	}
}

// convergence prints its own reading (Prints): the lines internal/converge
// builds, or its JSON object, the CONVERGENCE NOTE of a dry run, and the exit 1
// of a streak. Its refusals are the skeleton's.
func convergence(c *tool.Call) *tool.Out {
	dryRun := c.DryRun()
	state := c.Str("state")
	now := time.Now().UTC()
	if nowFlag := c.Str("now"); nowFlag != "" {
		at, err := time.Parse(time.RFC3339, nowFlag)
		if err != nil {
			return tool.Refuse(fmt.Sprintf("--now %s is not an RFC3339 instant; %s", oneline.Field(nowFlag), oneline.Err(err)))
		}
		now = at.UTC()
	}
	sinceAt, err := converge.ParseSince(c.Str("since"), now)
	if err != nil {
		return tool.Refuse(oneline.Err(err))
	}

	budget := time.Duration(c.Int("timeout")) * time.Second
	repo, repoDir := c.Str("repo"), c.Str("repo-dir")
	opts := converge.Options{
		Repo:         repo,
		LedgerPath:   c.Str("ledger"),
		ReceiptsDir:  c.Str("receipts"),
		RetiredPath:  c.Str("retired"),
		BinDir:       c.Str("bin"),
		RepoDir:      repoDir,
		BatchLogs:    c.Str("batch-logs"),
		VersionsPath: c.Str("versions"),
		CertsPath:    c.Str("certs"),
		By:           c.Get("by").([]string),
		Since:        sinceAt,
		Now:          now,
		Forge:        converge.GH{Repo: repo, Timeout: budget, Bin: c.Str("gh")},
	}
	if repoDir != "" {
		opts.Git = converge.RealGit{Dir: repoDir, Timeout: budget, Bin: c.Str("git")}
	}

	st, err := converge.LoadState(state)
	if err != nil {
		return tool.Refuse(oneline.Err(err))
	}
	report, err := converge.Read(context.Background(), opts)
	if err != nil {
		return tool.Refuse(oneline.Err(err))
	}
	report, next, streak := report.Apply(st, now)
	save := next.Save
	if dryRun {
		save = converge.PlanSave // the plan of the write: every check it makes, nothing written
	}
	if err := save(state); err != nil {
		return tool.Refuse(fmt.Sprintf("--state %s could not be written: %s", oneline.Field(state), oneline.Err(err)))
	}
	if dryRun && state != "" {
		fmt.Fprintf(c.Stderr, "CONVERGENCE NOTE dry_run=true: --state %s was not written\n", oneline.Field(state))
	}

	if c.Bool("json") {
		// The dry-run fact the line form prints as its NOTE is a field here, so
		// the two renderings carry the same facts.
		raw, err := json.Marshal(struct {
			converge.JSON
			DryRun bool `json:"dry_run,omitempty"`
		}{report.AsJSON(now, sinceAt), dryRun && state != ""})
		if err != nil {
			return tool.Refuse(oneline.Err(err))
		}
		printJSON(c.Stdout, raw)
	} else {
		printLines(c.Stdout, report.Lines())
	}
	if streak {
		return tool.Exit(1)
	}
	return tool.Exit(0)
}

// printLines writes the reading. Every field of every line was rendered through
// pkg/oneline inside internal/converge, where the spec's hostile-value test
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
