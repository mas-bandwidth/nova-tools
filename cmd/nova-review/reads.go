package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/line"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The reads view (nova-tools#3874): who has read a PR at its head, from the one
// read record. A read is a typed line record pr:<name>:<n>:line:<head>:<who>:<kind>
// that ns_line_post writes (internal/nsprint/line; nova-sprint read post, pr lines
// and nova-merge read --redis all post through it), so this verb rebuilds nothing:
// no GitHub review survey and no bus scan, which the ledger of #2063 did. Per PR it
// makes one ns_line_list (the current reads at the record's head) and one LRANGE of
// the PR's line log (the newest read of each reader, for the stale ones), all PRs in
// one pipeline. Per reader one current read: the newest read-kind line at head, Jev
// never (line.Current). A reader whose newest read is at another head is stale.

// prList is the repeatable --pr <n>.
type prList []int

func (p *prList) String() string { return fmt.Sprint(*p) }

func (p *prList) Set(v string) error {
	n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(v), "#"))
	if err != nil || n <= 0 {
		return fmt.Errorf("--pr wants a PR number, got %q", v)
	}
	*p = append(*p, n)
	return nil
}

// prReads is one PR's reads as the store holds them.
type prReads struct {
	N       int
	Head    string // the record's head; "" when there is no record with a head
	Current []line.Read
	Stale   []line.Read // each reader's newest read, at another head
}

// loadReads reads every PR's current reads and line log in one pipelined round trip.
func loadReads(ctx context.Context, c redis.Cmdable, repo string, ns []int) ([]prReads, error) {
	pipe := c.Pipeline()
	lists := make([]*redis.Cmd, len(ns))
	logs := make([]*redis.StringSliceCmd, len(ns))
	for i, n := range ns {
		lc, err := line.ListCmd(ctx, pipe, repo, strconv.Itoa(n), "")
		if err != nil {
			return nil, err
		}
		lists[i] = lc
		logs[i] = pipe.LRange(ctx, prkey.Key(repo, n)+":lines", 0, -1)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	out := make([]prReads, len(ns))
	for i, n := range ns {
		out[i].N = n
		head, recs, err := line.ParseList(lists[i])
		switch {
		case errors.Is(err, line.ErrNoHead):
			continue
		case err != nil:
			return nil, err
		}
		out[i].Head = head
		out[i].Current = line.Current(recs)
		out[i].Stale = staleReads(logs[i].Val(), head, out[i].Current)
	}
	return out, nil
}

// staleReads is, per reader with no current read, that reader's newest read in the
// log when it names another head: the reader whose re-read a push made owed.
func staleReads(log []string, head string, current []line.Read) []line.Read {
	have := map[string]bool{}
	for _, r := range current {
		have[r.Who] = true
	}
	newest := map[string]line.Read{}
	var order []string
	for _, text := range log {
		l, err := line.Parse(text)
		if err != nil || !line.ReadKinds[l.Kind] || line.IsJev(l.Who) || have[l.Who] {
			continue
		}
		if _, ok := newest[l.Who]; !ok {
			order = append(order, l.Who)
		}
		newest[l.Who] = line.Read{Who: l.Who, Kind: l.Kind, Head: l.Head, Score: l.Score, Text: l.Text}
	}
	var out []line.Read
	for _, who := range order {
		r := newest[who]
		if !strings.HasPrefix(head, r.Head) {
			out = append(out, r)
		}
	}
	return out
}

func readsRefuse(w io.Writer, reason string) int {
	fmt.Fprintf(w, "READS REFUSED: %s\n", oneline.Escape(reason))
	return 2
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	if sha == "" {
		return "-"
	}
	return sha
}

func scoreField(n int) string {
	if n < 0 {
		return "-"
	}
	return strconv.Itoa(n)
}

// reads is the reads verb: `nova-review reads --redis <addr> --repo <r> --pr <n>...`
// prints, per PR, one READS PR line, one READ line per reader's current read at the
// head, and one READS STALE line per reader whose newest read is at another head.
func reads(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("reads", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addr := fs.String("redis", "", "")
	repo := fs.String("repo", "", "")
	timeout := fs.Int("timeout", 10, "")
	var prs prList
	fs.Var(&prs, "pr", "")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return readsRefuse(errOut, "bad reads flags; run: nova-review help")
	}
	if *addr == "" {
		return readsRefuse(errOut, "--redis <addr> is required; the reads are the one read record in the sprint store (nova-tools#3874)")
	}
	if _, _, err := prkey.Split(*repo); err != nil {
		return readsRefuse(errOut, "--repo <owner/name|name> is required")
	}
	if len(prs) == 0 {
		return readsRefuse(errOut, "--pr <n> is required, once per PR")
	}
	if *timeout <= 0 {
		return readsRefuse(errOut, "--timeout must be positive")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeout)*time.Second)
	defer cancel()
	c := redis.NewClient(&redis.Options{Addr: *addr})
	defer c.Close()
	all, err := loadReads(ctx, c, *repo, prs)
	if err != nil {
		return readsRefuse(errOut, fmt.Sprintf("the store at %s: %v", *addr, err))
	}
	name := prkey.Name(*repo)
	for _, p := range all {
		if p.Head == "" {
			fmt.Fprintf(out, "READS PR repo=%s n=%d head=- current=0 stale=0 why=no-record\n", oneline.Field(name), p.N)
			continue
		}
		fmt.Fprintf(out, "READS PR repo=%s n=%d head=%s current=%d stale=%d\n", oneline.Field(name), p.N, oneline.Field(p.Head), len(p.Current), len(p.Stale))
		for _, r := range p.Current {
			fmt.Fprintf(out, "READ repo=%s n=%d who=%s kind=%s score=%s head=%s at=%d\n",
				oneline.Field(name), p.N, oneline.Field(r.Who), oneline.Field(r.Kind), scoreField(r.Score), oneline.Field(short(r.Head)), r.At)
		}
		for _, r := range p.Stale {
			fmt.Fprintf(out, "READS STALE repo=%s n=%d who=%s kind=%s read=%s live=%s\n",
				oneline.Field(name), p.N, oneline.Field(r.Who), oneline.Field(r.Kind), oneline.Field(short(r.Head)), oneline.Field(short(p.Head)))
		}
	}
	return 0
}
