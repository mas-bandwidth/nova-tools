// The stream branch lifecycle (nova-tools#3358) as verbs beside stream
// ls|order|rename. Each is at most a guard read of the landing record plus
// the land verb that already does the step, so every step has one path:
//
//	nova-sprint stream open   --repo <owner/repo> --stream <s> [land stream flags]
//	nova-sprint stream rebase --repo <owner/repo> --stream <s> [land stream flags]
//	nova-sprint stream pr     --repo <owner/repo> --stream <s> [land stream flags]
//	nova-sprint stream status --repo <owner/repo> [--redis <addr>]
//	nova-sprint stream close  --repo <owner/repo> --stream <s> [land merge flags]
//
// open: refused when land:<repo>:<slug> is already open with a stream PR
// (remedy: stream rebase); else one land stream run, which cuts
// stream/<slug> off the base tip, merges the members oldest first, runs the
// batch test, pushes, opens the ONE stream PR and records base, base_sha,
// branch and head on land:<repo>:<slug>.
// rebase: refused unless that landing is open; else the same run rebuilds
// the branch on the base head, re-runs the batch test, pushes, reuses the PR
// and records the new base_sha.
// pr: the same run with no guard (opens the PR, or reuses the open one).
// status: land status --repo, from Redis alone.
// close: land merge, the ONE event that merges the stream PR, moves every
// member merging -> landed and closes the members. There is no second close.
//
// A guard refusal prints one REFUSED line on stdout and exits 1; usage is
// the land verb's own (exit 2).
package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// flagValues returns every value of --name (or -name) in args, both the
// "--name v" and "--name=v" forms.
func flagValues(args []string, name string) []string {
	var vs []string
	for i := 0; i < len(args); i++ {
		a := strings.TrimPrefix(strings.TrimPrefix(args[i], "-"), "-")
		if a == name && i+1 < len(args) {
			vs = append(vs, args[i+1])
			i++
		} else if v, ok := strings.CutPrefix(a, name+"="); ok {
			vs = append(vs, v)
		}
	}
	return vs
}

// streamLanding reads land:<repo>:<slug> for the --repo and --stream flags in
// args. ok is false when the flags are incomplete; the land verb then refuses
// with its own usage line.
func streamLanding(ctx context.Context, args []string) (l stream.Landing, found, ok bool, err error) {
	repos, streams := flagValues(args, "repo"), flagValues(args, "stream")
	var addr string
	if rs := flagValues(args, "redis"); len(rs) > 0 {
		addr = rs[len(rs)-1]
	}
	addr = redisOr(addr) // the one resolver (seat.go), as landRedisAddr
	if len(repos) != 1 || !landRepoOK(repos[0]) || len(streams) == 0 || addr == "" {
		return l, false, false, nil
	}
	slug, err := stream.Slug(streams...)
	if err != nil {
		return l, false, false, nil
	}
	st, err := store.Open(ctx, addr)
	if err != nil {
		return l, false, true, err
	}
	defer st.Close()
	l, found, err = stream.LoadLanding(ctx, st.Client(), repos[0], slug)
	return l, found, true, err
}

// runStreamLife runs one lifecycle subverb; handled is false for any other.
func runStreamLife(ctx context.Context, sub string, args []string, out, errOut io.Writer) (int, bool) {
	switch sub {
	case "status":
		return runLandStreamStatus(ctx, args, out, errOut), true
	case "close":
		return runLandMerge(ctx, args, out, errOut), true
	case "pr":
		return runLandStream(ctx, args, out, errOut), true
	case "open", "rebase":
	default:
		return 0, false
	}
	l, found, ok, err := streamLanding(ctx, args)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint stream %s: %v\n", sub, err)
		return 6, true
	}
	if ok {
		open := found && l.State == "open" && l.PR > 0
		switch {
		case sub == "open" && open:
			fmt.Fprintf(out, "REFUSED %s is open as #%d on %s at base %s@%s remedy=nova-sprint stream rebase (or stream close once green)\n",
				stream.LandKey(l.Repo, l.Slug), l.PR, l.Branch, l.Base, stream.Short(l.BaseSHA))
			return 1, true
		case sub == "rebase" && !open:
			state := "absent"
			if found {
				state = l.State
			}
			fmt.Fprintf(out, "REFUSED no open stream PR to rebase (landing %s) remedy=nova-sprint stream open\n", state)
			return 1, true
		}
	}
	return runLandStream(ctx, args, out, errOut), true
}
