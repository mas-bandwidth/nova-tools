package main

// cost.go is `nova-ci cost` (card ci-cost-line, nova-tools#4328): the one
// COST line every CI run writes to its log and to the ci:cost stream. It
// reads the forge's job listing for the run on stdin (the body of
// `repos/<owner>/<name>/actions/runs/<id>/jobs`, one page or every page's
// jobs under one `jobs` array), prices it (internal/ci.CostFromJobs) and
// prints the line on stdout; with --redis it appends the same to ci:cost
// first and the line ends in the entry's id. The run's identity is the run
// receipt's (#4375): the flags are `nova-sprint ci github --from-runner`'s,
// spelt the same, so the ci-ok step that writes the receipt writes the cost
// from the same context, and the store is opened the same way (the seat's
// user and password from the environment, never a flag).
//
// The verb makes no call of its own: what fetched the listing is the
// caller's business, and everything on stdin is DATA from a host. A write
// that fails is a refusal on stderr with exit 2, never a line that pretends.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ci"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/webhook"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// costUsage is the verb's flag line, the receipt's flags word for word plus --redis.
const costUsage = "nova-ci cost --repo owner/name --sha <40hex> --run-id <n> --event <ev> --workflow <name> --conclusion success|failure|cancelled [--head-branch <b>] [--base-branch <b>] [--pr <n>] [--at <rfc3339>] [--redis <addr>] < jobs.json"

// costListingCap bounds what the verb reads from stdin: a run has tens of
// jobs, each a few kilobytes of JSON with its steps, so a listing past this
// is not a listing.
const costListingCap = 16 << 20

// costWriteTimeout bounds the one XADD.
const costWriteTimeout = 30 * time.Second

// costOpener opens the store at addr and returns the writer and its close. It
// is the verb's one seam: production opens the sprint store as this process's
// seat, and the tests hand in a fake that records the entry.
type costOpener func(ctx context.Context, addr string) (ci.CostWriter, func() error, error)

// openCostStore is the production opener: store.Open, the same seat handling
// the receipt's writer uses (NOVA_SPRINT_REDIS_USER and the password env).
func openCostStore(ctx context.Context, addr string) (ci.CostWriter, func() error, error) {
	st, err := store.Open(ctx, addr)
	if err != nil {
		return nil, nil, err
	}
	return st.Client(), st.Close, nil
}

// cmdCost is the verb. Exit 0 with the COST line on stdout; 2 for a refusal:
// a flag the receipt refuses, a listing that is not the forge's JSON, or a
// store that would not take the entry.
func cmdCost(args []string, stdin io.Reader, stdout, stderr io.Writer, open costOpener) int {
	fs := flag.NewFlagSet("cost", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	var r webhook.Receipt
	fs.StringVar(&r.Repo, "repo", "", "owner/name")
	fs.StringVar(&r.SHA, "sha", "", "the 40-hex head the run tested")
	fs.StringVar(&r.RunID, "run-id", "", "github.run_id")
	fs.StringVar(&r.Event, "event", "", "github.event_name")
	fs.StringVar(&r.HeadBranch, "head-branch", "", "github.head_ref")
	fs.StringVar(&r.BaseBranch, "base-branch", "", "github.base_ref")
	fs.StringVar(&r.PR, "pr", "", "the pull request number, or nothing")
	fs.StringVar(&r.Workflow, "workflow", "", "github.workflow")
	fs.StringVar(&r.Conclusion, "conclusion", "", "job.status of ci-ok: success, failure or cancelled")
	fs.StringVar(&r.At, "at", "", "RFC3339; empty is now")
	redisAddr := fs.String("redis", "", "the store to append the entry to; empty logs the line only")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, " cost", oneline.Cap(err.Error(), oneline.TailBytes)+"; "+costUsage)
	}
	if fs.NArg() > 0 {
		return refuse(stderr, " cost", fmt.Sprintf("unexpected argument %q; the listing is read on stdin", fs.Arg(0)))
	}
	if err := r.Validate(); err != nil {
		return refuse(stderr, " cost", oneline.Err(err)+"; "+costUsage)
	}
	raw, err := io.ReadAll(io.LimitReader(stdin, costListingCap+1))
	if err != nil {
		return refuse(stderr, " cost", "stdin: "+oneline.Err(err))
	}
	if len(raw) > costListingCap {
		return refuse(stderr, " cost", fmt.Sprintf("stdin is over %d bytes; a run's job listing is not", costListingCap))
	}
	if strings.TrimSpace(string(raw)) == "" {
		return refuse(stderr, " cost", "stdin is empty; it wants the run's job listing (repos/<owner>/<name>/actions/runs/<id>/jobs)")
	}
	_, jobs, err := ci.ParseJobsPage(raw)
	if err != nil {
		return refuse(stderr, " cost", "stdin is not the forge's job listing: "+oneline.Err(err))
	}
	if len(jobs) == 0 {
		return refuse(stderr, " cost", "the listing holds no jobs; a run with none has no cost to write")
	}
	cost := ci.CostFromJobs(jobs)

	ev := ""
	if strings.TrimSpace(*redisAddr) != "" {
		if open == nil {
			return refuse(stderr, " cost", "no store opener")
		}
		ctx, cancel := context.WithTimeout(context.Background(), costWriteTimeout)
		defer cancel()
		w, closeStore, err := open(ctx, strings.TrimSpace(*redisAddr))
		if err != nil {
			return refuse(stderr, " cost", "open "+oneline.Field(*redisAddr)+": "+oneline.Err(err))
		}
		ev, err = ci.WriteCost(ctx, w, &r, cost)
		closeErr := closeStore()
		if err != nil {
			return refuse(stderr, " cost", oneline.Err(err))
		}
		if closeErr != nil && !errors.Is(closeErr, context.Canceled) {
			return refuse(stderr, " cost", "close: "+oneline.Err(closeErr))
		}
	}
	fmt.Fprintln(stdout, cost.Line(r, ev))
	return 0
}
