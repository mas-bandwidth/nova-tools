package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ci"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const queueUsage = "queue --repo <owner/name> [--branch dev] [--json] [--table]"

// cmdQueue implements `nova-ci queue --repo <owner/name> [--branch dev]`.
// It inspects the merge queue of the target branch, prints each entry and its
// state, and for a failed merge-group run prints the failed job name and first FAIL lines.
// It formats output as one receipt line per entry (or table or JSON when requested).
func cmdQueue(ctx context.Context, args []string, stdout, stderr io.Writer, newForge func(repo string) ci.QueueForge) int {
	const where = " queue"
	fs := flag.NewFlagSet("queue", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	repo := fs.String("repo", "", "repository in owner/name form")
	branch := fs.String("branch", "dev", "merge queue target branch")
	asJSON := fs.Bool("json", false, "output as JSON")
	asTable := fs.Bool("table", false, "output as an aligned table")
	format := fs.String("format", "", "output format: receipt, table, or json")
	failLines := fs.Int("fail-lines", ci.DefaultFailLines, "maximum FAIL lines to show per failed job")

	if err := verbflag.Parse(fs, args); err != nil {
		return refuse(stderr, where, oneline.Cap(err.Error(), oneline.TailBytes)+": nova-ci "+queueUsage)
	}
	if fs.NArg() > 0 {
		return refuse(stderr, where, fmt.Sprintf("takes flags only, nothing positional: %q; run: nova-ci help", fs.Arg(0)))
	}

	*repo = strings.TrimSpace(*repo)
	if *repo == "" {
		return refuse(stderr, where, "--repo is required: nova-ci "+queueUsage)
	}
	owner, name, ok := strings.Cut(*repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") || strings.ContainsAny(*repo, " :\t\r\n") {
		return refuse(stderr, where, fmt.Sprintf("--repo wants owner/name, got %q", *repo))
	}
	*branch = strings.TrimSpace(*branch)
	if *branch == "" {
		return refuse(stderr, where, "--branch must not be empty")
	}

	fmtChoice := "receipt"
	if *format != "" {
		switch *format {
		case "receipt", "table", "json":
			fmtChoice = *format
		default:
			return refuse(stderr, where, fmt.Sprintf("--format wants receipt, table, or json, got %q", *format))
		}
	}
	if *asJSON {
		fmtChoice = "json"
	} else if *asTable {
		fmtChoice = "table"
	}

	var forge ci.QueueForge
	if newForge != nil {
		forge = newForge(*repo)
	} else {
		forge = ci.NewGHQueueForge(*repo, 120*time.Second, nil)
	}

	report, err := ci.InspectQueue(ctx, forge, *repo, *branch, *failLines)
	if err != nil {
		return refuse(stderr, where, oneline.Err(err))
	}

	switch fmtChoice {
	case "json":
		data, err := report.JSON()
		if err != nil {
			return refuse(stderr, where, oneline.Err(err))
		}
		fmt.Fprintln(stdout, string(data))
	case "table":
		fmt.Fprint(stdout, report.Table())
	default:
		for _, l := range report.Lines() {
			fmt.Fprintln(stdout, l)
		}
	}
	return 0
}
