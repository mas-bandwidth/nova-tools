// Package spec is the specs table in Redis (nova-tools#3370, part of
// #3364): a typed SPEC line's facts go onto the record pr:<name>:<n> in the
// same call that stores the line, the second distinct 10 at the current rev
// moves the spec from specs:<stream>:working to specs:<stream>:done and
// releases every task waiting on spec:<name>#<n>, and the list reads only
// Redis. Both are one FCALL of a function in
// internal/nsprint/fn/lua/unblock_spec.lua.
package spec

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

// Function names registered by unblock_spec.lua.
const (
	FunctionMark = "ns_spec_mark"
	FunctionList = "ns_spec_list"
)

// Mark is one SPEC fact. Repo is the bare repo name (the record key's name),
// N the issue number. Stream "" keeps the spec's stream (else the record's
// stream field, else "-"); Line "" stores no line; Sprint "" is the first
// of sprint:order.
type Mark struct {
	Repo, N, Who  string
	Rev, Score    int
	Stream, Line  string
	Sprint, Actor string
}

// Result is the function's reply.
type Result struct {
	Answer, State, Stream, Why string
	Tens, Released, Lines      int
}

// ExitCode is 0 for a write or a repeat (RECORDED, NEW_REV, DONE, SAME) and
// 1 for a refusal (STALE_REV, INVALID), which wrote nothing.
func (r Result) ExitCode() int {
	switch r.Answer {
	case "RECORDED", "NEW_REV", "DONE", "SAME":
		return 0
	}
	return 1
}

// Line is the receipt.
func (r Result) Line(m Mark) string {
	if r.ExitCode() != 0 {
		return fmt.Sprintf("SPEC MARK REFUSED %s#%s who=%s rev=%d score=%d answer=%s why=%s", m.Repo, m.N, m.Who, m.Rev, m.Score, r.Answer, r.Why)
	}
	return fmt.Sprintf("SPEC MARK %s#%s who=%s rev=%d score=%d answer=%s state=%s tens=%d released=%d stream=%s", m.Repo, m.N, m.Who, m.Rev, m.Score, r.Answer, r.State, r.Tens, r.Released, r.Stream)
}

// Do is one ns_spec_mark call.
func Do(ctx context.Context, c *redis.Client, m Mark) (Result, error) {
	raw, err := c.FCall(ctx, FunctionMark, nil, m.Repo, m.N, m.Who, strconv.Itoa(m.Rev), strconv.Itoa(m.Score), m.Stream, m.Line, m.Sprint, m.Actor).Result()
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", FunctionMark, err)
	}
	v, ok := raw.([]any)
	if !ok || len(v) == 0 {
		return Result{}, fmt.Errorf("%s: unexpected reply %T", FunctionMark, raw)
	}
	s := func(i int) string {
		if i < len(v) {
			return fmt.Sprint(v[i])
		}
		return ""
	}
	r := Result{Answer: s(0)}
	if r.ExitCode() != 0 {
		r.Why = s(1)
		return r, nil
	}
	r.State, r.Stream = s(1), s(4)
	r.Tens, _ = strconv.Atoi(s(2))
	r.Released, _ = strconv.Atoi(s(3))
	r.Lines, _ = strconv.Atoi(s(5))
	return r, nil
}

// Facts are what a SPEC line carries.
type Facts struct {
	Who, Stream string
	Rev, Score  int
}

var (
	whoRx    = regexp.MustCompile(`(?:^|\s)who=([A-Za-z0-9_.-]+)`)
	revRx    = regexp.MustCompile(`(?:^|\s)rev=r?([0-9]+)`)
	scoreRx  = regexp.MustCompile(`(?:^|\s)score=([0-9]+)`)
	streamRx = regexp.MustCompile(`(?:^|\s)stream=([A-Za-z0-9_./-]+)`)
)

// ParseLine reads the facts from a SPEC line's first line:
// `SPEC who=<f> rev=<k> sha=<sha256> score=N[/10] ...` with an optional
// stream=<name>. A line without who=, rev= or score= is refused.
func ParseLine(line string) (Facts, error) {
	first := strings.SplitN(strings.ReplaceAll(line, "\r\n", "\n"), "\n", 2)[0]
	tok := strings.Fields(first)
	if len(tok) == 0 || tok[0] != "SPEC" {
		return Facts{}, errors.New("not a SPEC line")
	}
	var f Facts
	m := whoRx.FindStringSubmatch(first)
	if m == nil {
		return f, errors.New("SPEC line has no who=<name>")
	}
	f.Who = m[1]
	if m = revRx.FindStringSubmatch(first); m == nil {
		return f, errors.New("SPEC line has no rev=<k>")
	}
	f.Rev, _ = strconv.Atoi(m[1])
	if m = scoreRx.FindStringSubmatch(first); m == nil {
		return f, errors.New("SPEC line has no score=<0..10>")
	}
	f.Score, _ = strconv.Atoi(m[1])
	if f.Rev < 1 || f.Score > 10 {
		return f, fmt.Errorf("SPEC line has rev=%d score=%d; want rev >= 1 and score 0..10", f.Rev, f.Score)
	}
	if m = streamRx.FindStringSubmatch(first); m != nil {
		f.Stream = m[1]
	}
	return f, nil
}

// Row is one stream's counts.
type Row struct {
	Stream        string
	Working, Done int
}

// Listing is one ns_spec_list reply: the rows, and for one stream its ids
// oldest first.
type Listing struct {
	Rows          []Row
	Working, Done []string
}

// List is one FCALL_RO of ns_spec_list. stream "" lists every stream.
func List(ctx context.Context, c *redis.Client, stream string) (Listing, error) {
	raw, err := c.FCallRO(ctx, FunctionList, nil, stream).Result()
	if err != nil {
		return Listing{}, fmt.Errorf("%s: %w", FunctionList, err)
	}
	v, _ := raw.([]any)
	var l Listing
	rows := v
	if stream != "" {
		if len(v) != 3 {
			return l, fmt.Errorf("%s: unexpected reply of %d parts", FunctionList, len(v))
		}
		rows, _ = v[0].([]any)
		l.Working, l.Done = strs(v[1]), strs(v[2])
	}
	for _, r := range rows {
		cells, _ := r.([]any)
		if len(cells) != 3 {
			return l, fmt.Errorf("%s: unexpected row %v", FunctionList, r)
		}
		w, _ := strconv.Atoi(fmt.Sprint(cells[1]))
		d, _ := strconv.Atoi(fmt.Sprint(cells[2]))
		l.Rows = append(l.Rows, Row{Stream: fmt.Sprint(cells[0]), Working: w, Done: d})
	}
	return l, nil
}

func strs(x any) []string {
	v, _ := x.([]any)
	out := make([]string, 0, len(v))
	for _, s := range v {
		out = append(out, fmt.Sprint(s))
	}
	return out
}

// Block is the specs block: the header and one `stream | working | done`
// row per stream.
func Block(rows []Row) string {
	var b strings.Builder
	b.WriteString("specs | working | done\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "%s | %d | %d\n", r.Stream, r.Working, r.Done)
	}
	return b.String()
}
