package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// briefBaseFetch bounds the fetch of one base into the lander's clone: a forge that does not
// answer is a MISSING finding, never an add that hangs.
const briefBaseFetch = 2 * time.Minute

// briefBaseRef is where add fetches a base's tip in the lander's clone: a namespace of its
// own, so a fetch here never takes the lock of a ref the lander is moving.
const briefBaseRef = "refs/nova-add/"

// holdBriefBase holds each card brief of an add (one with a PATHS: line) to the brief checks
// at its base (swarm.LintBrief; docs/SPEC-SPRINT.md section 11, the brief checks): the
// repository its REPO: names, in the lander's clone of it (made as land makes it, when there is
// none), at the tip of its BASE: fetched once a call, read with git and no go command; beside it
// the bases the store lists in use and the friends' tiers. Each finding prints as a LINT DRIFT
// line with its remedy, each corrected header line as a LINT FIX line, and one refusal follows,
// exit 2, nothing written. A brief with no PATHS: line is no card brief and is not held here.
func (a *app) holdBriefBase(verbName string, st *store.Store, allowPersonal bool, stderr io.Writer, briefs ...briefCheck) int {
	var cards []briefCheck
	friendNeeded := false
	for _, b := range briefs {
		if _, ok := swarm.CardHeaderValue([]byte(b.brief), "PATHS"); !ok {
			continue
		}
		cards = append(cards, b)
		if w, _ := cardhdr.ReadWho(b.brief); w.Friend {
			friendNeeded = true
		}
	}
	if len(cards) == 0 {
		return 0
	}
	ctx := context.Background()
	s, err := st.Load(ctx, []string{sprint.Work, sprint.Merge}, nil)
	if err != nil {
		return a.readFailed(verbName, err, stderr)
	}
	var listed []string
	for _, r := range basesInUse(s) {
		if !slices.Contains(listed, r.Base) {
			listed = append(listed, r.Base)
		}
	}
	var friends map[string][]string
	if friendNeeded {
		seats, err := st.FriendSeats(ctx, a.now())
		if err != nil {
			return a.readFailed(verbName, err, stderr)
		}
		friends = map[string][]string{}
		for _, f := range seats {
			tiers := f.Tiers
			if len(tiers) == 0 {
				tiers = sprint.Split(f.Class)
			}
			friends[f.Name] = tiers
		}
	}
	type key struct{ repo, ref, pin string }
	evidence := map[key]swarm.BriefBase{}
	l := &lander{a: a}
	var lines []string
	findings, first := 0, ""
	for _, c := range cards {
		cb := swarm.ReadCardBase([]byte(c.brief))
		bb := swarm.BriefBase{Friends: friends, Listed: listed}
		if cb.Ref != "" && cb.Named != "" {
			k := key{cb.Named, cb.Ref, cb.Sha}
			got, ok := evidence[k]
			if !ok {
				got = a.briefBaseAt(ctx, l, cb)
				evidence[k] = got
			}
			bb.Repo, bb.Sha, bb.Missing, bb.Gone = got.Repo, got.Sha, got.Missing, got.Gone
			if allowPersonal {
				bb.Listed = append(append([]string(nil), listed...), cb.Ref) // --allow-personal-base admits the base it names
			}
		}
		fs, fix := swarm.LintBrief([]byte(c.brief), bb)
		id := c.id
		if id == "" {
			id = "-"
		}
		for _, f := range fs {
			if first == "" {
				first = f.Check + ": " + f.Excerpt
			}
			lines = append(lines, fmt.Sprintf("LINT DRIFT card=%s check=%s line=%d: %s remedy=%s", oneline.Field(id), f.Check, f.Line,
				oneline.Escape(oneline.Cap(f.Excerpt, oneline.TailBytes)), oneline.Escape(swarm.BriefRemedy(f.Check))))
		}
		for _, x := range fix {
			lines = append(lines, "LINT FIX card="+oneline.Field(id)+" "+oneline.Escape(x))
		}
		findings += len(fs)
	}
	if findings == 0 {
		return 0
	}
	for _, x := range lines {
		fmt.Fprintln(stderr, x)
	}
	return refuse(stderr, verbName, fmt.Sprintf("%d brief finding(s) at the base, the first %s; nothing was written; each LINT FIX line above is a corrected header line: apply it to the brief and add it again", findings, oneline.Cap(first, 300)))
}

// pinOf is the @<sha> a BASE: line pins, "" when it pins none.
func pinOf(cb swarm.CardBase) string {
	if cb.Sha == "" {
		return ""
	}
	return "@" + cb.Sha
}

// briefBaseAt is the evidence at one card's base: the lander's clone of its repository and
// the commit at the base's tip (or the commit BASE: pins, cb.Sha), with Missing naming what
// could not be had, and Gone when origin holds no branch of the base's name.
func (a *app) briefBaseAt(ctx context.Context, l *lander, cb swarm.CardBase) swarm.BriefBase {
	if cb.Repo == "" {
		return swarm.BriefBase{Missing: "REPO " + cb.Named + " is no repository a clone can be made of, so the brief was not read at its base"}
	}
	ctx, cancel := context.WithTimeout(ctx, briefBaseFetch)
	defer cancel()
	dir, why := l.clone(ctx, cb.Repo)
	if why != "" {
		return swarm.BriefBase{Missing: "the lander's clone of " + cb.Named + " could not be had: " + why}
	}
	if _, err := l.git(ctx, dir, "fetch", "--no-tags", "origin", "+refs/heads/"+cb.Ref+":"+briefBaseRef+cb.Ref); err != nil {
		heads, lerr := l.git(ctx, dir, "ls-remote", "--heads", "origin", "refs/heads/"+cb.Ref)
		if lerr == nil && strings.TrimSpace(heads) == "" {
			return swarm.BriefBase{Gone: true, Missing: "origin " + cb.Named + " holds no branch " + cb.Ref + ", so the brief was not read at its base"}
		}
		return swarm.BriefBase{Missing: "the base " + cb.Ref + " could not be fetched into " + dir + ": " + firstLine("", err)}
	}
	want := briefBaseRef + cb.Ref
	if cb.Sha != "" {
		want = cb.Sha
	}
	sha, err := l.git(ctx, dir, "rev-parse", "--verify", "--quiet", "--end-of-options", want+"^{commit}")
	if err != nil || sha == "" {
		return swarm.BriefBase{Missing: "the base " + cb.Ref + pinOf(cb) + " is no commit in " + dir}
	}
	return swarm.BriefBase{Repo: dir, Sha: sha}
}
