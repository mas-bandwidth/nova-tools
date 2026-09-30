package main

// cost.go is `nova-ci cost`: the one COST line every CI run writes to its log
// and to the ci:cost stream. It reads the forge's job listing for the run on
// stdin (the body of `repos/<owner>/<name>/actions/runs/<id>/jobs`: one JSON
// object whose jobs array holds exactly total_count jobs, whether one complete
// page or jobs combined across every page into one object; raw concatenated
// pages are refused), prices it (internal/cicost) and prints the line on
// stdout; with --redis it appends the same entry to ci:cost first and the line
// ends in the entry's id. The run's identity is the run receipt's: the flags
// are `nova-ci github receipt`'s, spelt the same, so the ci-ok step that writes
// the receipt writes the cost from the same context, and the store is opened
// the same way (the seat's user and password from the environment, never a flag).
//
// The verb makes no call of its own: what fetched the listing is the
// caller's business, and everything on stdin is DATA from a host. A complete
// listing is required; a partial or count-mismatched listing is refused (exit 2)
// before any dial. An open failure is known to have written nothing. An XADD
// error cannot prove whether the entry committed, so exit 1 reports it as
// unconfirmed and asks for inspection before any retry. If XADD succeeds but
// closing the connection fails, the COST line with its event id is printed on
// stdout, the close failure is reported on stderr, and the verb exits 1
// without instructing the caller to rerun the write.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cicost"
	"github.com/mas-bandwidth/nova-tools/internal/cireceipt"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// costUsage is the verb's flag line: the receipt's flags word for word, plus --redis.
const costUsage = "nova-ci cost --repo owner/name --sha <40hex> --run-id <n> --workflow <name> --conclusion success|failure|cancelled [--pr <n>] [--at <rfc3339>] [--redis <addr>] < jobs.json"

// costListingCap bounds what the verb reads from stdin: a run has tens of
// jobs, each a few kilobytes of JSON with its steps, so a listing past this
// is not a listing.
const costListingCap = 16 << 20

// costWriteTimeout bounds the one XADD, so a store that is down or hung
// reddens ci-ok inside its cap instead of holding the job to it.
const costWriteTimeout = 20 * time.Second

// costOpener opens the store at addr and returns the writer and its close. It
// is the verb's one seam: production opens the store as this process's seat,
// and the tests hand in a fake that records the entry.
type costOpener func(ctx context.Context, addr string) (cicost.Writer, func() error, error)

// openCostStore is the production opener: store.Open, the same seat handling
// the receipt's writer uses (NOVA_SPRINT_REDIS_USER and the password env).
func openCostStore(ctx context.Context, addr string) (cicost.Writer, func() error, error) {
	st, err := store.Open(ctx, addr)
	if err != nil {
		return nil, nil, err
	}
	return st.Client(), st.Close, nil
}

// cmdCost is the verb. Exit 0 with the COST line on stdout; 1 when opening or
// writing the store failed or closing it failed (on close failure after a
// successful write, stdout retains the line with its event id, stderr reports
// the close error, and no rerun is recommended); 2 for a refusal before any
// dial: a flag the receipt refuses, a partial or count-mismatched listing, or a
// listing that is not the forge's.
func cmdCost(args []string, stdin io.Reader, stdout, stderr io.Writer, open costOpener) int {
	const where = " cost"
	fs := flag.NewFlagSet("cost", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	var r cireceipt.Receipt
	fs.StringVar(&r.Repo, "repo", "", "owner/name")
	fs.StringVar(&r.SHA, "sha", "", "the 40-hex head the run tested")
	fs.StringVar(&r.RunID, "run-id", "", "github.run_id")
	fs.StringVar(&r.PR, "pr", "", "the pull request number, or nothing")
	fs.StringVar(&r.Workflow, "workflow", "", "github.workflow")
	fs.StringVar(&r.Conclusion, "conclusion", "", "job.status of ci-ok: success, failure or cancelled")
	fs.StringVar(&r.At, "at", "", "RFC3339; empty is now")
	redisAddr := fs.String("redis", "", "the store to append the entry to; empty prints the line only")
	if err := verbflag.Parse(fs, args); err != nil {
		return refuse(stderr, where, oneline.Cap(err.Error(), oneline.TailBytes)+": "+costUsage)
	}
	if fs.NArg() > 0 {
		return refuse(stderr, where, fmt.Sprintf("unexpected argument %q; the listing is read on stdin: %s", fs.Arg(0), costUsage))
	}
	if err := r.Validate(); err != nil {
		return refuse(stderr, where, oneline.Err(err)+": "+costUsage)
	}
	raw, err := io.ReadAll(io.LimitReader(stdin, costListingCap+1))
	if err != nil {
		return refuse(stderr, where, "stdin: "+oneline.Err(err))
	}
	if len(raw) > costListingCap {
		return refuse(stderr, where, fmt.Sprintf("stdin is over %d bytes; a run's job listing is not", costListingCap))
	}
	if strings.TrimSpace(string(raw)) == "" {
		return refuse(stderr, where, "stdin is empty; it wants the run's job listing (repos/<owner>/<name>/actions/runs/<id>/jobs)")
	}
	totalCount, jobs, err := cicost.ParseJobs(raw)
	if err != nil {
		return refuse(stderr, where, "stdin is not the forge's job listing (repos/<owner>/<name>/actions/runs/<id>/jobs): "+oneline.Err(err))
	}
	if len(jobs) == 0 {
		return refuse(stderr, where, "the listing holds no jobs; a run with none has no cost to write")
	}
	if len(jobs) < totalCount {
		return refuse(stderr, where, fmt.Sprintf("listing is partial (%d jobs read, %d expected); page through the forge's listing, or pass every page", len(jobs), totalCount))
	}
	if len(jobs) > totalCount {
		return refuse(stderr, where, fmt.Sprintf("listing holds %d jobs but total_count is %d", len(jobs), totalCount))
	}
	cost := cicost.FromJobs(jobs)

	ev := ""
	var closeErr error
	if addr := strings.TrimSpace(*redisAddr); addr != "" {
		if open == nil {
			return refuse(stderr, where, "no store opener")
		}
		ctx, cancel := context.WithTimeout(context.Background(), costWriteTimeout)
		defer cancel()
		w, closeStore, err := open(ctx, addr)
		if err != nil {
			return costUnwritten(stderr, "open "+oneline.Field(addr)+": "+oneline.Err(err))
		}
		ev, err = cicost.Write(ctx, w, &r, cost)
		closeErr = closeStore()
		if err != nil {
			return costUnconfirmed(stderr, oneline.Err(err))
		}
	}
	fmt.Fprintln(stdout, cost.Line(r, ev))
	if closeErr != nil && !errors.Is(closeErr, context.Canceled) {
		fmt.Fprintf(stderr, "nova-ci cost: close: %s\n", oneline.Escape(oneline.Err(closeErr)))
		return 1
	}
	return 0
}

// costUnwritten applies only before XADD was attempted, so the state is known.
func costUnwritten(stderr io.Writer, cause string) int {
	fmt.Fprintf(stderr, "nova-ci cost: %s; the COST entry was not written: fix the store or the bench seat and rerun ci-ok\n", oneline.Escape(cause))
	return 1
}

// costUnconfirmed follows the receipt's uncertain-write convention: an XADD
// error can follow a committed entry whose reply was lost. Do not recommend a
// blind rerun that could append the same run's cost twice.
func costUnconfirmed(stderr io.Writer, cause string) int {
	fmt.Fprintf(stderr, "nova-ci cost: %s; COST entry write could not be confirmed; next: inspect ci:cost for repo/sha/run_id before any retry\n", oneline.Escape(cause))
	return 1
}
