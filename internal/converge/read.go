package converge

// read.go is the orchestration: one window, seven streams, one report. It is
// the whole verb apart from flag parsing and printing, which is why it lives
// here and not in the command -- the tests drive it with fakes, and the command
// is thin enough to read in one screen.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/dogfood"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Options is every source of one reading. A path that is empty is a source the
// caller did not name, and its stream is ABSENT -- never zero, and never
// guessed from the working directory.
type Options struct {
	Repo        string
	LedgerPath  string
	ReceiptsDir string
	RetiredPath string

	BinDir       string // SCRIPTS
	RepoDir      string // CLASSES
	BatchLogs    string // LANDING's second source for rounds
	VersionsPath string // FLEET
	CertsPath    string // FLEET's certified fraction

	By []string

	Since time.Time
	Now   time.Time

	Forge Forge
	Git   Git // nil when --repo-dir was not given
}

// Read takes the reading. Every refusal it returns is exit 2 at the command
// line and prints no stream line: a partial reading of convergence is the thing
// this verb exists to replace.
func Read(ctx context.Context, o Options) (Report, error) {
	if o.Forge == nil {
		return Report{}, fmt.Errorf("no forge seam: convergence reads the queue through one")
	}
	streams := make([]Stream, 0, len(Order))

	// LANDING and PRS come from one pair of forge reads.
	open, err := o.Forge.OpenPRs(ctx)
	if err != nil {
		return Report{}, err
	}
	closed, err := o.Forge.ClosedSince(ctx, earliest(o.Since, o.Now))
	if err != nil {
		return Report{}, err
	}
	logs := map[int]int{}
	if o.BatchLogs != "" {
		logs, err = RoundsFromLogs(o.BatchLogs)
		if err != nil {
			return Report{}, fmt.Errorf("--batch-logs %s: %s", oneline.Field(o.BatchLogs), oneline.Err(err))
		}
	}
	streams = append(streams, Landing(Batches(Merged(closed), logs), o.Since, o.Now))

	// CLASSES.
	if o.Git == nil || o.RepoDir == "" {
		streams = append(streams, AbsentStream("CLASSES", "class-test-index-entries", "--repo-dir", false))
	} else {
		nowSpec, err := os.ReadFile(filepath.Join(o.RepoDir, SpecCIPath))
		if err != nil {
			return Report{}, fmt.Errorf("--repo-dir %s: %s", oneline.Field(o.RepoDir), oneline.Err(err))
		}
		rev, err := o.Git.RevBefore(ctx, o.Since)
		if err != nil {
			return Report{}, err
		}
		beforeSpec, err := o.Git.Show(ctx, rev, SpecCIPath)
		if err != nil {
			return Report{}, err
		}
		streams = append(streams, Classes(string(nowSpec), beforeSpec, true).With("rev", rev))
	}

	// SCRIPTS.
	retiredRaw, err := os.ReadFile(o.RetiredPath)
	if err != nil {
		return Report{}, fmt.Errorf("--retired %s: %s", oneline.Field(o.RetiredPath), oneline.Err(err))
	}
	rows := ParseRetired(string(retiredRaw))
	if o.BinDir == "" {
		streams = append(streams, AbsentStream("SCRIPTS", "scripts-left-in-bin", "--bin", true).
			WithInt("retired-rows", len(rows)))
	} else {
		remaining, err := CountScripts(o.BinDir)
		if err != nil {
			return Report{}, fmt.Errorf("--bin %s: %s", oneline.Field(o.BinDir), oneline.Err(err))
		}
		streams = append(streams, Scripts(remaining, rows, o.Since, o.Now))
	}

	// PRS.
	streams = append(streams, PRs(open, closed, o.Since, o.Now))

	// EDGES.
	receipts, bad, err := dogfood.ReadReceipts(o.ReceiptsDir)
	if err != nil {
		return Report{}, fmt.Errorf("--receipts %s: %s", oneline.Field(o.ReceiptsDir), oneline.Err(err))
	}
	streams = append(streams, Edges(receipts, o.Since, o.Now, o.By).WithInt("unreadable", len(bad)))

	// FLEET.
	if o.VersionsPath == "" {
		streams = append(streams, AbsentStream("FLEET", "units-off-the-one-build", "--versions", true))
	} else {
		raw, err := os.ReadFile(o.VersionsPath)
		if err != nil {
			return Report{}, fmt.Errorf("--versions %s: %s", oneline.Field(o.VersionsPath), oneline.Err(err))
		}
		versions, err := ParseVersions(o.VersionsPath, string(raw))
		if err != nil {
			return Report{}, err
		}
		certified, total, haveCerts := 0, 0, false
		if o.CertsPath != "" {
			craw, err := os.ReadFile(o.CertsPath)
			if err != nil {
				return Report{}, fmt.Errorf("--certs %s: %s", oneline.Field(o.CertsPath), oneline.Err(err))
			}
			certified, total, err = ParseCerts(o.CertsPath, string(craw))
			if err != nil {
				return Report{}, err
			}
			haveCerts = true
		}
		streams = append(streams, Fleet(versions, certified, total, haveCerts))
	}

	// LEDGER.
	ledgerRaw, err := os.ReadFile(o.LedgerPath)
	if err != nil {
		return Report{}, fmt.Errorf("--ledger %s: %s", oneline.Field(o.LedgerPath), oneline.Err(err))
	}
	streams = append(streams, Ledger(string(ledgerRaw)))

	return Report{Streams: inOrder(streams)}, nil
}

// inOrder puts the streams in the reading's fixed order, whatever order they
// were built in, so one tick can be diffed against the next line for line.
func inOrder(streams []Stream) []Stream {
	byName := map[string]Stream{}
	for _, s := range streams {
		byName[s.Name] = s
	}
	out := make([]Stream, 0, len(streams))
	for _, name := range Order {
		if s, ok := byName[name]; ok {
			out = append(out, s)
		}
	}
	return out
}

// earliest is the far edge of the forge read: LANDING compares the window with
// the window of equal length before it, so the read has to reach back twice as
// far as --since.
func earliest(since, now time.Time) time.Time {
	return since.Add(-now.Sub(since))
}
