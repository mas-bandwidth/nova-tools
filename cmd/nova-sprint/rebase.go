package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// rebase is the coordinator's: it rewrites the base of every unlanded card on
// a branch (coordinator.go, who may run a verb).
func init() { verbClasses["rebase"] = classCoordinator }

// rebaseStep is the store step the verb runs: it plans sprint.Rebase over the
// work table (sprint.Rebase, docs/SPEC-SPRINT.md, the rebase verb).
func rebaseStep(r sprint.RebaseReq) store.Step {
	return store.Step{Args: store.ArgsOf(r), Verb: "rebase", Load: []string{sprint.Work}, Mirrors: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Rebase(s, r) }}
}

// cmdRebase moves every unlanded card whose BASE is --from to --to
// (docs/SPEC-SPRINT.md, the rebase verb): an undealt card's brief changes, a
// dealt or merging card keeps its head, and the new base must contain the old
// one (--repo-dir, git merge-base --is-ancestor). It runs on a RUNNING
// machine as on a STOPPED one. The preview only reads; runStep records the
// actor.
func (a *app) cmdRebase(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("rebase")
	from := fs.String("from", "", "the base branch the cards name now, on their BASE: line")
	to := fs.String("to", "", "the branch that replaces it: it must contain --from (--repo-dir checks it, git merge-base --is-ancestor)")
	repoDir := fs.String("repo-dir", "", "a clone of the cards' repository, its branches as fetched, for the one git merge-base --is-ancestor --from --to that a dealt card needs; without it, a dealt card's head is not checked")
	dry := fs.Bool("dry-run", false, "show the planned rebases and refusals without writing")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "rebase", err.Error())
	}
	if len(pos) > 0 {
		return refuse(stderr, "rebase", "takes no card ids: "+strings.Join(pos, " ")+"; it moves every card whose BASE is --from")
	}
	if strings.TrimSpace(*from) == "" || strings.TrimSpace(*to) == "" {
		return refuse(stderr, "rebase", "wants --from <branch> and --to <branch>; run: nova-sprint help rebase")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "rebase", err.Error())
	}
	req := sprint.RebaseReq{From: *from, To: *to, Who: c.actor}
	if *repoDir != "" {
		dir := *repoDir
		req.Contains = func(from, to string) error {
			_, err := sprint.RunGit(context.Background(), dir, "merge-base", "--is-ancestor", "--end-of-options", from, to)
			return err
		}
	}
	if *dry {
		_, snap, err := st.Check(context.Background(), 1)
		if err != nil {
			return a.readFailed("rebase", err, stderr)
		}
		plan := sprint.Rebase(snap, req)
		lines := append([]string(nil), plan.Said...)
		for _, u := range plan.Units {
			lines = append(lines, u.Moved)
		}
		for _, r := range plan.Refused {
			lines = append(lines, "REFUSED "+r.Key+": "+r.Why)
		}
		code, status := 0, "ok"
		if len(plan.Refused) > 0 {
			code, status = 1, "refused"
		}
		sayOK(stdout, c.json, "rebase", fmt.Sprintf("REBASE %s DRY-RUN from=%s to=%s changes=%d refused=%d\n%s", strings.ToUpper(status), *from, *to, len(plan.Units), len(plan.Refused), strings.Join(lines, "\n")), map[string]any{"status": status, "exit": code, "dry_run": true, "from": *from, "to": *to, "changes": len(plan.Units), "lines": lines, "refused": plan.Refused})
		return code
	}
	return a.runStep("rebase", *c, st, rebaseStep(req), stdout, stderr)
}
