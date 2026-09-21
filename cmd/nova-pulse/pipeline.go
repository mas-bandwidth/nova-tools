package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

func cmdPipeline(args []string, stdout, stderr io.Writer, now time.Time) int {
	nowFn := func() time.Time {
		if !now.IsZero() {
			return now
		}
		return time.Now().UTC()
	}
	fs := flag.NewFlagSet("pipeline", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		queueDir     = fs.String("queue", "", "path to the queue directory")
		openPR       = fs.Bool("open", false, "pipeline the second phase for an opened PR")
		repo         = fs.String("repo", "", "repository owner/name")
		pr           = fs.Int("pr", 0, "pull request number")
		head         = fs.String("head", "", "head commit SHA")
		base         = fs.String("base", "dev", "base ref or branch")
		title        = fs.String("title", "", "card or PR title")
		files        = fs.String("files", "", "comma or space separated list of touched files")
		checks       = fs.String("checks", "pass", "status checks summary")
		holds        = fs.Int("holds", 0, "count of active unresolved holds")
		disposition  = fs.String("disposition", "", "typed DISPOSITION line")
		landableList = fs.Bool("landable", false, "list all landable PRs")
		popLandable  = fs.Bool("pop-landable", false, "pop the next landable PR")
		backpressure = fs.Bool("backpressure", false, "check and update read queue backpressure")
		capLimit     = fs.Int("cap", pulse.DefaultReadQueueCap, "read queue cap before backpressure triggers")
	)

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *queueDir == "" {
		fmt.Fprintln(stderr, "nova-pulse pipeline: missing --queue; refusing to guess")
		return 2
	}

	if *openPR {
		if *repo == "" || *pr <= 0 || *head == "" {
			fmt.Fprintln(stderr, "nova-pulse pipeline --open requires --repo, --pr, and --head")
			return 2
		}
		var touched []string
		if *files != "" {
			for _, f := range strings.FieldsFunc(*files, func(r rune) bool {
				return r == ',' || r == ' ' || r == '\t'
			}) {
				if f != "" {
					touched = append(touched, f)
				}
			}
		}
		res, err := pulse.PipelineOnPROpen(pulse.PipelinePROpenInput{
			QueueDir:     *queueDir,
			Repo:         *repo,
			PR:           *pr,
			Head:         *head,
			Base:         *base,
			Title:        *title,
			TouchedFiles: touched,
			Checks:       *checks,
			Holds:        *holds,
			ReadCap:      *capLimit,
			Now:          nowFn,
		})
		if err != nil {
			fmt.Fprintf(stderr, "nova-pulse pipeline open failed: %s\n", err)
			return 1
		}
		fmt.Fprintln(stdout, res.Line)
		return 0
	}

	if *disposition != "" {
		landable, err := pulse.ProcessTypedRead(*queueDir, *disposition, nowFn)
		if err != nil {
			fmt.Fprintf(stderr, "nova-pulse pipeline disposition failed: %s\n", err)
			return 1
		}
		if landable == nil {
			fmt.Fprintln(stdout, "PIPELINE HELD")
			return 0
		}
		fmt.Fprintf(stdout, "PIPELINE LANDABLE repo=%s pr=%d head=%s who=%s score=%s class=%s\n",
			oneline.Field(landable.Repo), landable.PR, oneline.Field(landable.Head),
			landable.Disposition.Who, landable.Disposition.Score, landable.PathClass)
		return 0
	}

	if *landableList {
		list, err := pulse.ListLandablePRs(*queueDir)
		if err != nil {
			fmt.Fprintf(stderr, "nova-pulse pipeline list landable failed: %s\n", err)
			return 1
		}
		for _, l := range list {
			fmt.Fprintf(stdout, "LANDABLE repo=%s pr=%d head=%s base=%s who=%s score=%s class=%s\n",
				oneline.Field(l.Repo), l.PR, oneline.Field(l.Head), oneline.Field(l.Base),
				l.Disposition.Who, l.Disposition.Score, l.PathClass)
		}
		return 0
	}

	if *popLandable {
		popped, err := pulse.PopLandablePR(*queueDir)
		if err != nil {
			fmt.Fprintf(stderr, "nova-pulse pipeline pop landable failed: %s\n", err)
			return 1
		}
		if popped == nil {
			fmt.Fprintln(stdout, "LANDABLE EMPTY")
			return 0
		}
		fmt.Fprintf(stdout, "POPPED repo=%s pr=%d head=%s base=%s who=%s score=%s class=%s\n",
			oneline.Field(popped.Repo), popped.PR, oneline.Field(popped.Head), oneline.Field(popped.Base),
			popped.Disposition.Who, popped.Disposition.Score, popped.PathClass)
		return 0
	}

	if *backpressure {
		active, pending, err := pulse.UpdateBackpressure(*queueDir, *capLimit)
		if err != nil {
			fmt.Fprintf(stderr, "nova-pulse pipeline backpressure failed: %s\n", err)
			return 1
		}
		status := "clear"
		if active {
			status = "active"
		}
		fmt.Fprintf(stdout, "BACKPRESSURE pending=%d cap=%d status=%s\n", pending, *capLimit, status)
		return 0
	}

	fmt.Fprintln(stderr, "nova-pulse pipeline: specify an action (--open, --disposition, --landable, --pop-landable, or --backpressure)")
	return 2
}
