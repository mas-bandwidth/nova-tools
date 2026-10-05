package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// holdBriefChecks holds every coding brief of an add (swarm.BriefChecked: its header names
// a repository and PATHS) to the brief checks at its BASE tip (swarm.LintBrief;
// docs/SPEC-SPRINT.md section 11, card lint), after the card lint has passed: the tree is
// read in the lander's clone of the repository, fetched to BASE, with no go command, and
// the friends table is read for a WHO: friend card. One failing brief refuses the whole
// call, exit 2, nothing written, each finding on a LINT DRIFT line with its remedy and
// the corrected PATHS or SHARED line. The half of a served add run where it is typed
// checks nothing: the server holds the clones and runs them (ruleSet.server).
func (a *app) holdBriefChecks(verbName string, cards []sprint.CardAdd, rs ruleSet, c *common, st **store.Store, stderr io.Writer) int {
	if rs.server {
		return 0
	}
	ctx := context.Background()
	bases := map[string]swarm.BriefBase{}
	var friends map[string][]string
	friendsWhy, friendsRead := "", false
	type drift struct {
		file string
		f    swarm.BriefFinding
	}
	var all []drift
	var failed []string
	for _, cd := range cards {
		raw := []byte(cd.Brief)
		if cd.Sentinel || !swarm.BriefChecked(raw) {
			continue
		}
		cb := swarm.ReadCardBase(raw)
		key := cb.Repo + "\x00" + cb.Ref
		bb, ok := bases[key]
		if !ok {
			bb = a.briefBase(ctx, cb)
			bases[key] = bb
		}
		if w, _ := cardhdr.ReadWho(cd.Brief); w.Friend {
			if !friendsRead {
				friends, friendsWhy = a.friendTiers(ctx, c, st)
				friendsRead = true
			}
			bb.Friends, bb.FriendsMissing = friends, friendsWhy
		}
		fs := swarm.LintBrief(raw, bb)
		if len(fs) == 0 {
			continue
		}
		name := cd.File
		if name == "" {
			name = cd.ID
		}
		failed = append(failed, name)
		for _, f := range fs {
			all = append(all, drift{name, f})
		}
	}
	if len(all) == 0 {
		return 0
	}
	printed, more := all, false
	if c.max > 0 && len(all) > c.max {
		printed, more = all[:c.max], true
	}
	for _, x := range printed {
		fix := ""
		if x.f.Fix != "" {
			fix = " fix=" + oneline.Escape(x.f.Fix)
		}
		fmt.Fprintf(stderr, "LINT DRIFT brief %s: %s: %d: %s remedy=%s%s\n", oneline.Field(x.file), oneline.Field(x.f.Check), x.f.Line,
			oneline.Escape(oneline.Cap(x.f.Excerpt, oneline.TailBytes)), oneline.Escape(swarm.CardBaseRemedies[x.f.Check]), fix)
	}
	if more {
		fmt.Fprintf(stderr, "LINT MORE brief findings=%d remedy=%s --max 0\n", len(all), verbName)
	}
	return refuse(stderr, verbName, fmt.Sprintf("the brief of %s fails the brief checks at its BASE (%s); apply each fix= line, or re-cut the brief; run: nova-swarm lint --rules", strings.Join(failed, ", "), findingsCount(len(all))))
}

// briefRefRE is a branch name add fetches: no option, no space, no `..`.
var briefRefRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./-]*$`)

// briefBase is the evidence of one brief's BASE: the lander's clone of its repository
// (the directory land keeps it in, landRoot), fetched to BASE, and the tip's sha. A clone
// land has not made, a fetch that failed, or a BASE no branch can be is Missing; a BASE
// origin does not hold is Gone.
func (a *app) briefBase(ctx context.Context, cb swarm.CardBase) swarm.BriefBase {
	var bb swarm.BriefBase
	switch {
	case cb.Ref == "":
		return bb // LintBrief says the BASE is missing
	case cb.Repo == "":
		bb.Missing = "the brief's repository " + cb.Named + " is no clone URL or owner/name"
		return bb
	case !briefRefRE.MatchString(cb.Ref) || strings.Contains(cb.Ref, ".."):
		bb.Missing = "BASE " + cb.Ref + " is no branch name"
		return bb
	}
	root, err := a.landRoot()
	if err != nil {
		bb.Missing = "no directory the lander keeps its clones in (" + oneline.Err(err) + ")"
		return bb
	}
	dir := filepath.Join(root, repoDirName(cb.Repo))
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		bb.Missing = "the lander keeps no clone of " + cb.Repo + " (" + dir + "); land clones it on its first batch, or clone it there"
		return bb
	}
	bb.Repo = dir
	git := func(args ...string) (string, string, error) {
		res, err := gitrun.Run(ctx, gitrun.Options{C: dir, Env: a.gitEnv, OwnRepo: true}, args...)
		return strings.TrimSpace(string(res.Stdout)), strings.TrimSpace(string(res.Stderr)), err
	}
	if _, errs, err := git("fetch", "-q", "--no-tags", "origin", "+refs/heads/"+cb.Ref+":refs/remotes/origin/"+cb.Ref); err != nil {
		if strings.Contains(errs, "couldn't find remote ref") {
			bb.Gone = "origin of " + dir + " has no branch " + cb.Ref
			return bb
		}
		if errs == "" {
			errs = oneline.Err(err)
		}
		bb.Missing = "the fetch of " + cb.Ref + " into " + dir + " failed: " + oneline.Cap(errs, 200)
		return bb
	}
	sha, _, err := git("rev-parse", "--verify", "-q", "refs/remotes/origin/"+cb.Ref+"^{commit}")
	if err != nil || sha == "" {
		bb.Missing = "the fetched " + cb.Ref + " is no commit in " + dir
		return bb
	}
	bb.Sha = sha
	return bb
}

// friendTiers is each friend's tiers, her class as friend sync recorded it, for
// who-serves-tier; nil with why when the friends table could not be read.
func (a *app) friendTiers(ctx context.Context, c *common, st **store.Store) (map[string][]string, string) {
	if *st == nil {
		s, err := a.store(*c)
		if err != nil {
			return nil, oneline.Err(err)
		}
		*st = s
	}
	rows, err := (*st).FriendRows(ctx, a.now())
	if err != nil {
		return nil, oneline.Err(err)
	}
	out := map[string][]string{}
	for _, r := range rows {
		out[r.Name] = sprint.Split(r.Class)
	}
	return out, ""
}
