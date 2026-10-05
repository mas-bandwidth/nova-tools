package main

import (
	"context"
	"slices"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// baseRecheck is each land pass's re-check of the bases that stopped streams
// (docs/SPEC-SPRINT.md section 8, v11-base-red-auto-resume-now; internal/sprint, land_base.go):
// a stream stopped on its base's red gets no pass of its own, so the pass gates the tip of
// each such base, once a base, and a green tip is recorded with the merge step
// (sprint.MergeReq.BaseGreen); the tick's base-gate rule then resumes every stream stopped on
// it. A red tip is gated again no sooner than the base-gate rule's last wait
// (sprint.BaseGateRetries); a dry run, a twin (no git) and the rule turned off re-check
// nothing. What it did is NOTE lines (baseNotes); it changes no exit.
func (l *lander) baseRecheck(ctx context.Context, s *sprint.Snapshot) {
	if l.dry || l.twin || slices.Contains(l.offRules(ctx), sprint.RuleBaseGate) {
		return
	}
	type site struct{ repo, base string }
	var sites []site
	first := map[site]string{}
	for _, st := range sprint.BaseRedStreams(s) {
		ctl := s.StreamCtl(st)
		at := site{base: l.base}
		for _, c := range s.Merge.Cell(st, sprint.Queued) {
			if pr := s.Work.Placed(c.ID); pr != nil {
				cb := swarm.ReadCardBase([]byte(pr.F("brief")))
				at.repo = cb.Repo
				if cb.Ref != "" {
					at.base = cb.Ref
				}
				break
			}
		}
		if b := ctl.F(sprint.FieldBaseGateBase); b != "" {
			at.base = b
		}
		if at.base == "" {
			continue
		}
		if _, ok := first[at]; !ok {
			first[at] = st
			sites = append(sites, at)
		}
	}
	for _, at := range sites {
		sha, why := l.baseTip(ctx, at.repo, at.base)
		if why != "" {
			l.baseNotes = append(l.baseNotes, "the base "+at.base+" was not re-checked: "+why)
			continue
		}
		if f := l.baseGateFails[sha]; f != nil && l.clock().Before(f.next) {
			continue
		}
		red, cached := l.baseGateCache[sha]
		if !cached || red != "" {
			dir, _ := l.clone(ctx, at.repo)
			red = l.treeGate(ctx, dir, true)
		}
		if red != "" {
			f := l.baseGateFails[sha]
			if f == nil {
				f = &baseGateFail{}
				l.baseGateFails[sha] = f
			}
			f.n, f.why, f.next = f.n+1, red, l.clock().Add(sprint.BaseGateRetries[len(sprint.BaseGateRetries)-1])
			l.baseNotes = append(l.baseNotes, "the base "+at.base+" still fails its tree gate at "+shortSha(sha)+"; re-checked again at "+f.next.UTC().Format("15:04:05 MST"))
			continue
		}
		l.baseGateCache[sha] = ""
		delete(l.baseGateFails, sha)
		res, err := l.greenStep(sprint.MergeReq{Stream: first[at], Base: at.base, BaseGreen: sha, Who: l.c.actor})
		if code := stepExit(res, err); code != 0 {
			l.baseNotes = append(l.baseNotes, "the base "+at.base+" passes its tree gate again at "+shortSha(sha)+"; the merge step did not record it ("+stepWhy(res, err)+")")
			continue
		}
		l.baseNotes = append(l.baseNotes, "the base "+at.base+" passes its tree gate again at "+shortSha(sha)+"; its streams resume by rule at the next tick")
	}
}

// baseTip cuts the clone of repo at the tip of base fetched from origin: the tip's commit, or
// why it could not.
func (l *lander) baseTip(ctx context.Context, repo, base string) (sha, why string) {
	dir, why := l.clone(ctx, repo)
	if why != "" {
		return "", why
	}
	if out, err := l.git(ctx, dir, "status", "--porcelain", "--untracked-files=no"); err != nil || out != "" || l.merging(ctx, dir) {
		return "", "the clone " + dir + " is not clean (" + firstLine(out, err) + ")"
	}
	if _, err := l.git(ctx, dir, "fetch", "--no-tags", "origin", "+refs/heads/"+base+":refs/remotes/origin/"+base); err != nil {
		return "", "the fetch of " + base + " in " + dir + " failed: " + firstLine("", err)
	}
	if _, err := l.git(ctx, dir, "switch", "--no-track", "--force-create", "land/base-check", "refs/remotes/origin/"+base); err != nil {
		return "", "the base " + base + " could not be cut in " + dir + ": " + firstLine("", err)
	}
	sha, err := l.git(ctx, dir, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", "the base " + base + " has no tip in " + dir + ": " + firstLine("", err)
	}
	return sha, ""
}

// greenStep records a green base (sprint.MergeReq.BaseGreen) fenced to the epoch land read: the
// merge step as land's own step runs it, without the batch's check that its stream is not
// stopped, which a green base is the fact for.
func (l *lander) greenStep(r sprint.MergeReq) (store.Result, error) {
	step := store.MergeStep(r)
	epoch := l.epoch
	step.Epoch = &epoch
	if l.c.op != "" {
		step.CallerOp = l.c.op + "." + r.Stream + "." + step.Args
	}
	l.a.serial.Lock()
	defer l.a.serial.Unlock()
	return l.st.Run(context.Background(), step)
}
