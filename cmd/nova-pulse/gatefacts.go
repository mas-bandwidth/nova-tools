package main

import (
	"fmt"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

func cmdGateFacts(args []string, stdout, stderr io.Writer) int {
	f := newFlags("gate-facts")
	dir := f.fs.String("dir", "", "")
	base := f.fs.String("base", "", "")
	head := f.fs.String("head", "", "")
	pr := f.fs.Int("pr", 0, "")
	repo := f.fs.String("repo", "", "")
	card := f.fs.String("card", "", "")
	paths := f.fs.String("paths", "", "")
	rollup := f.fs.String("rollup", "", "")
	receipt := f.fs.String("receipt-file", "", "")
	timeout := f.fs.Int("timeout", 120, "")
	max := f.fs.Int("max", bounded.Default, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*dir, "dir", "the git working copy this stamp reads")
	f.want(*base, "base", "the landing-base ref, such as origin/dev")
	f.want(*head, "head", "the head commit or branch to stamp")
	if *timeout < 1 {
		f.add("--timeout wants a whole number of seconds; every child this verb starts is bounded")
	}
	if *max < 0 {
		f.add(fmt.Sprintf("--max is 0 or more, got %d; 0 already means all", *max))
	}
	if *pr < 0 {
		f.add("--pr is a pull request number, 0 means none")
	}
	if *pr > 0 && *rollup == "" && *repo == "" {
		f.add("--pr without --rollup wants --repo owner/name; refusing to guess")
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.GateFacts(pulse.GateFactsInput{
		Dir: *dir, Base: *base, Head: *head,
		PR: *pr, Repo: *repo, Card: *card, Paths: *paths,
		Rollup: *rollup, ReceiptFile: *receipt,
		Timeout: time.Duration(*timeout) * time.Second,
		Max:     *max,
		Stdout:  stdout, Stderr: stderr,
	})
}
