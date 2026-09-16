package main

// `nova-check edges` is the second half of class J (#828): the tools file the rows, and this
// verb turns them into issues. It is in nova-check and not in nova-pulse because the rows
// come from EVERY tool -- nova-pulse, nova-bus, nova-swarm, nova-review -- and the verb that
// files them should not belong to any one of them.
//
// One issue per DISTINCT open row, in the dogfood shape, and the row is marked filed, so a
// second run over the same ledger opens nothing. --dry-run opens nothing and marks nothing
// and prints the same counts.

import (
	"flag"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/edges"
)

// cmdEdges files the queue's open edge rows as issues.
func cmdEdges(args []string, stdout, stderr io.Writer) int {
	return cmdEdgesWith(args, stdout, stderr, nil)
}

// cmdEdgesWith is cmdEdges with the issue creator handed in, so a test files into a fake and
// opens no network. A nil creator is the real `gh issue create`.
func cmdEdgesWith(args []string, stdout, stderr io.Writer, creator edges.Creator) int {
	fs := flag.NewFlagSet("edges", flag.ContinueOnError)
	queue := fs.String("queue", "", "the queue directory holding EDGES.tsv (required)")
	repo := fs.String("repo", "", "owner/name the issues are opened on (required)")
	dryRun := fs.Bool("dry-run", false, "open nothing, mark nothing, print the same counts")
	timeout := fs.Int("timeout", 60, "seconds one gh issue create may take")
	if !parse(fs, args, stderr, map[string]*string{"queue": queue, "repo": repo}) {
		return 2
	}
	if creator == nil {
		creator = edges.GH{Timeout: time.Duration(*timeout) * time.Second}
	}
	return edges.Report(edges.ReportInput{
		Queue:   *queue,
		Repo:    *repo,
		DryRun:  *dryRun,
		Creator: creator,
		Stdout:  stdout,
		Stderr:  stderr,
	})
}
