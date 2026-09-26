package stream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// run.go is the whole stream landing as one call (nova-tools#3598): what
// Rowan did by hand with land stream, ci request, pr record and land merge.
// Run builds the stream branch off the current base (members merged --no-ff
// oldest first, one batch test, bisect and park on red, push, ONE stream PR),
// requests our own CI for the stream head, waits for the ci word the last
// receipt writes on the stream PR's record, and on green merges the stream PR
// with Merge, which owns the members' merging -> landed move and their
// closes. A base that moved while CI ran is rebuilt on the new tip, so a base
// fix is always in what lands. A re-run resumes an open landing at its CI
// wait instead of rebuilding it.

// RunOptions is one whole landing. Options is the build; the rest is the CI
// wait and the seams a test swaps.
type RunOptions struct {
	Options
	// CIWait bounds the whole CI wait of one run; Tick is the gap between
	// reads of the stream PR's record (default 10 s).
	CIWait, Tick time.Duration
	// Rebuild builds again even when an open landing could be resumed (the
	// way past a red stream head once its cause is fixed).
	Rebuild bool
	// MaxBuilds caps the builds of one run (a base that keeps moving);
	// default 3.
	MaxBuilds int
	// CIURL is the clone URL the CI request carries ("": the runner's
	// mirror).
	CIURL string
	// Request puts the stream head in the CI pool and returns the request's
	// status word (CREATED, EXISTS, ...).
	Request func(ctx context.Context, repo, sha string, pr int, url string) (string, error)
	// Sleep waits one tick; nil sleeps on the wall clock under ctx.
	Sleep func(ctx context.Context, d time.Duration) error
	// BaseTip reads the base branch's tip from the remote; nil runs git
	// ls-remote.
	BaseTip func(ctx context.Context, remote, base string) (string, error)
}

// RunReport is what one land run did. State is landed, red, waiting, or a
// build state that stopped it (empty, conflict, base-red, pushed).
type RunReport struct {
	Slug      string
	State     string
	Builds    int  // land stream builds this run made
	Resumed   bool // an open landing was resumed at its CI wait
	BaseMoved int  // rebuilds because the base moved during CI
	Build     Report
	Landing   Landing
	CIRequest string // the CI request's status word
	CI        string // the stream PR record's ci word at the end of the wait
	Merge     MergeReport
	// Calls is the landing's own REST calls: the stream PR opened by this
	// run, and the merge. The members' closes are Merge's and counted in
	// the GitHub client's total, not here.
	Calls int
}

// Run lands the streams end to end: build (or resume), CI request, wait,
// merge on green.
func Run(ctx context.Context, c Client, o RunOptions) (RunReport, error) {
	var rep RunReport
	slug, err := Slug(o.Streams...)
	if err != nil {
		return rep, &Refusal{Why: err.Error(), Remedy: "--stream <name as in ws:names>"}
	}
	rep.Slug = slug
	if o.GH == nil {
		return rep, &Refusal{Why: "no GitHub client", Remedy: "set GH_TOKEN"}
	}
	if o.Request == nil {
		return rep, &Refusal{Why: "no CI request seam", Remedy: "call Run from nova-sprint land"}
	}
	if o.DryRun {
		return rep, &Refusal{Why: "a land run does not dry-run", Remedy: "nova-sprint land stream --dry-run"}
	}
	if o.Tick <= 0 {
		o.Tick = 10 * time.Second
	}
	if o.MaxBuilds <= 0 {
		o.MaxBuilds = 3
	}
	if o.Sleep == nil {
		o.Sleep = sleepCtx
	}
	if o.BaseTip == nil {
		o.BaseTip = lsRemote
	}
	cfg, err := LoadConfig(ctx, c, o.Repo)
	if err != nil {
		return rep, err
	}
	remote := o.Remote
	if remote == "" {
		remote = cfg.Remote
	}
	if remote == "" {
		remote = "git@github.com:" + o.Repo + ".git"
	}
	deadline := time.Now().Add(o.CIWait)
	rebuild := o.Rebuild
	// The step lines of the whole landing, with their walls (nova-tools
	// #4324); LandStream prints the build's with the same clock.
	steps := NewSteps(o.Log, o.Now)
	for {
		l, ok, err := LoadLanding(ctx, c, o.Repo, slug)
		if err != nil {
			return rep, err
		}
		resume := false
		if !rebuild && ok && l.State == "open" && l.PR > 0 {
			recs, err := LoadPRs(ctx, c, o.Repo, []int{l.PR})
			if err != nil {
				return rep, err
			}
			tip, err := o.BaseTip(ctx, remote, l.Base)
			if err != nil {
				return rep, err
			}
			if r := recs[0]; r.Exists && r.Head == l.Head && tip == l.BaseSHA {
				if r.CI == "red" {
					rep.State, rep.CI, rep.Landing = "red", r.CI, l
					return rep, nil
				}
				resume = true
			}
		}
		if resume {
			rep.Resumed = true
		} else {
			if rep.Builds >= o.MaxBuilds {
				return rep, fmt.Errorf("the base moved during CI %d times; %d builds this run", rep.BaseMoved, rep.Builds)
			}
			bo := o.Options
			if bo.Workdir != "" && rep.Builds > 0 {
				bo.Workdir = fmt.Sprintf("%s-%d", bo.Workdir, rep.Builds+1)
			}
			before := o.GH.Calls
			b, err := LandStream(ctx, c, bo)
			rep.Builds++
			rep.Build = b
			if !b.Reused && b.PR > 0 {
				rep.Calls += o.GH.Calls - before
			}
			if err != nil {
				return rep, err
			}
			if b.State != "open" {
				rep.State = b.State
				return rep, nil
			}
			if l, _, err = LoadLanding(ctx, c, o.Repo, slug); err != nil {
				return rep, err
			}
			steps.Line("BUILT %s head=%s pr=#%d members=%d build=%d", b.Branch, short(l.Head), l.PR, len(l.Members), rep.Builds)
		}
		rebuild = false
		rep.Landing = l
		word, err := o.Request(ctx, o.Repo, l.Head, l.PR, o.CIURL)
		if err != nil {
			return rep, fmt.Errorf("ci request %s@%s: %w", o.Repo, short(l.Head), err)
		}
		rep.CIRequest = word
		rep.CI, err = waitCI(ctx, c, o, l, deadline)
		if err != nil {
			return rep, err
		}
		steps.Line("CI %s %s pr=#%d request=%s", short(l.Head), orDash(rep.CI), l.PR, orDash(word))
		switch rep.CI {
		case "green":
		case "red":
			rep.State = "red"
			return rep, nil
		default:
			rep.State = "waiting"
			return rep, nil
		}
		tip, err := o.BaseTip(ctx, remote, l.Base)
		if err != nil {
			return rep, err
		}
		if tip != l.BaseSHA {
			// Green on a base that is no longer the tip: build again on the
			// new tip so what merges is what was tested.
			rep.BaseMoved++
			rebuild = true
			continue
		}
		// The stream branch sits on the base tip: it merges cleanly. That is
		// the record's mergeable word, from the fact, not from GitHub.
		if _, err := Record(ctx, c, o.Repo, l.PR, RecordFields{Mergeable: "true"}); err != nil {
			return rep, err
		}
		mr, err := Merge(ctx, c, MergeOptions{Repo: o.Repo, Streams: o.Streams, By: o.By, GH: o.GH})
		rep.Merge = mr
		if mr.MergeSHA != "" && !mr.Already {
			rep.Calls++ // the one merge call
		}
		if mr.MergeSHA != "" {
			steps.Line("MERGED %s pr=#%d already=%t", short(mr.MergeSHA), l.PR, mr.Already)
		}
		if err != nil {
			return rep, err
		}
		rep.State = "landed"
		steps.Line("LANDED n=%d stream=%s moved=%d total_ms=%d", len(l.Members), field(slug), mr.Moved, steps.Total().Milliseconds())
		return rep, nil
	}
}

// waitCI reads the stream PR's record until its ci word is green or red, the
// deadline passes (pending), or the record's head leaves the landing's head
// (another run rebuilt it: an error, nothing merges).
func waitCI(ctx context.Context, c Client, o RunOptions, l Landing, deadline time.Time) (string, error) {
	for {
		recs, err := LoadPRs(ctx, c, o.Repo, []int{l.PR})
		if err != nil {
			return "", err
		}
		r := recs[0]
		if !r.Exists || r.Head != l.Head {
			return "", fmt.Errorf("the record of %s#%d is at %s, not the stream head %s: another run rebuilt it", o.Repo, l.PR, short(r.Head), short(l.Head))
		}
		if r.CI == "green" || r.CI == "red" {
			return r.CI, nil
		}
		if !time.Now().Before(deadline) {
			return orDash(r.CI), nil
		}
		if err := o.Sleep(ctx, o.Tick); err != nil {
			return "", err
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// lsRemote reads refs/heads/<base> from the remote with git: the base tip,
// from the git remote, never the forge API.
func lsRemote(ctx context.Context, remote, base string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-remote", remote, "refs/heads/"+base)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git ls-remote %s: %v: %s", base, err, strings.TrimSpace(errb.String()))
	}
	sha, _, _ := strings.Cut(strings.TrimSpace(out.String()), "\t")
	if len(sha) != 40 {
		return "", errors.New("git ls-remote: no refs/heads/" + base + " on the remote")
	}
	return sha, nil
}
