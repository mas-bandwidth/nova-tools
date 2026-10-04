package main

// The convergence verb asks whether each stream is converging: it reports
// the health metric, the contraction ratio per stream, on every tick, from
// one place, so that one run's answer can be diffed against the next one's.
//
// Seven streams, each read from a real source through a seam: the forge, a
// checkout, the receipts, the retired README, a bin, a version snapshot and the
// pit-stop ledger. Every path comes from a flag; a stream whose source was not
// named is ABSENT and says so. See docs/SPEC-CHECK.md.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/converge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
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

const convDefaultTimeout = 60

func convergenceFlags(f *tool.Flags) {
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
}

func convergence(c *tool.Call) *tool.Out {
	dryRun := c.DryRun()
	repo := c.Str("repo")
	ledger := c.Str("ledger")
	receipts := c.Str("receipts")
	retired := c.Str("retired")
	since := c.Str("since")
	bin := c.Str("bin")
	repoDir := c.Str("repo-dir")
	batchLogs := c.Str("batch-logs")
	versions := c.Str("versions")
	certs := c.Str("certs")
	state := c.Str("state")
	nowFlag := c.Str("now")
	ghBin := c.Str("gh")
	gitBin := c.Str("git")
	timeout := c.Int("timeout")
	asJSON := c.Bool("json")
	by := c.Get("by").([]string)

	if timeout <= 0 {
		return tool.Refuse(fmt.Sprintf("--timeout must be a positive number of seconds (got %d); a child with no deadline is a wait with no end", timeout))
	}

	now := time.Now().UTC()
	if nowFlag != "" {
		at, err := time.Parse(time.RFC3339, nowFlag)
		if err != nil {
			return tool.Refuse("--now " + oneline.Field(nowFlag) + " is not an RFC3339 instant; " + oneline.Err(err))
		}
		now = at.UTC()
	}
	sinceAt, err := converge.ParseSince(since, now)
	if err != nil {
		return tool.Refuse(oneline.Err(err))
	}

	budget := time.Duration(timeout) * time.Second
	opts := converge.Options{
		Repo:         repo,
		LedgerPath:   ledger,
		ReceiptsDir:  receipts,
		RetiredPath:  retired,
		BinDir:       bin,
		RepoDir:      repoDir,
		BatchLogs:    batchLogs,
		VersionsPath: versions,
		CertsPath:    certs,
		By:           by,
		Since:        sinceAt,
		Now:          now,
		Forge:        converge.GH{Repo: repo, Timeout: budget, Bin: ghBin},
	}
	if repoDir != "" {
		opts.Git = converge.RealGit{Dir: repoDir, Timeout: budget, Bin: gitBin}
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
		save = converge.PlanSave
	}
	if err := save(state); err != nil {
		return tool.Refuse("--state " + oneline.Field(state) + " could not be written: " + oneline.Err(err))
	}
	if dryRun && state != "" {
		fmt.Fprintf(c.Stderr, "CONVERGENCE NOTE dry_run=true: --state %s was not written\n", oneline.Field(state))
	}

	if asJSON {
		raw, err := json.Marshal(struct {
			converge.JSON
			DryRun bool `json:"dry_run,omitempty"`
		}{report.AsJSON(now, sinceAt), dryRun && state != ""})
		if err != nil {
			return tool.Refuse(oneline.Err(err))
		}
		fmt.Fprintf(c.Stdout, "%s\n", raw)
	} else {
		for _, line := range report.Lines() {
			fmt.Fprintf(c.Stdout, "%s\n", line)
		}
	}
	if streak {
		return tool.Exit(1)
	}
	return tool.Exit(0)
}
