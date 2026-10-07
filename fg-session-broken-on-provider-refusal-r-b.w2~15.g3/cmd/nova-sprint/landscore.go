package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// THE LANDED SCORE (docs/SPEC-SPRINT.md section 7, the landed score; docs/SPEC-NOVA-DECIDE.md
// section 9). Once every stream of the run has landed what it could, land scores each landed
// card's merge diff (the diff its own checks read, checkCard) against the card's brief with
// nova-decide's score decision, recorded in <land root>/decide/score.jsonl under
// <card>@landed@<head>, and reports each batch's scores in one store step (store.ScoreStep):
// the top class and its p on each landed card, and one "landed work scored low" judgment per
// batch listing the cards at or above the sprint row's decide_score_bar (empty, the default,
// raises none). A score never holds a landing back: it runs after the whole land pass, under
// one deadline for the pass (scoreWait) and the land loop's context, and it stops at the
// first backend failure; every card it did not score is named on a NOTE.

// landScore is a landed batch's scores: how many of its cards were scored, whether the
// step raised the scored-low judgment, and why a card or the batch was not scored.
type landScore struct {
	Scored int    `json:"scored"`
	Judged bool   `json:"judged,omitempty"`
	Why    string `json:"why,omitempty"`
}

// scoreWait bounds the whole scoring pass of one land run, every batch together.
const scoreWait = time.Minute

// scoreJob is a landed batch to score: its place in the run's output, its stream, its cards.
type scoreJob struct {
	at     int
	stream string
	pins   []landCard
}

// scorer is the backend land scores through: the app's (a test's), else Jev over its
// real transport with the key JEV_API_KEY holds; why is a refusal to score.
func (a *app) scorer() (decide.Backend, string) {
	if a.scoreBackend != nil {
		return a.scoreBackend, ""
	}
	key := a.getenv(decide.JevSecret)
	if key == "" {
		return nil, decide.JevSecret + " is absent, so no landed diff was scored; run land under nova-secrets exec --only " + decide.JevSecret + ",<its other keys>"
	}
	return decide.JevHTTP(key, decide.JevTimeout), ""
}

// scoreAll scores the run's landed batches, after every stream has landed, under ctx (the
// land loop's) and one deadline for the pass; a backend failure, or the deadline, ends the
// pass and every card not yet scored is named on its batch's NOTE.
func (l *lander) scoreAll(ctx context.Context) {
	if len(l.toScore) == 0 {
		return
	}
	note := func(j scoreJob, why string) { l.out[j.at].Score = &landScore{Why: why} }
	backend, why := l.a.scorer()
	root := l.root
	if why == "" && root == "" {
		var err error
		if root, err = l.a.landRoot(); err != nil {
			why = "no directory for the score record: " + oneline.Err(err)
		}
	}
	record := filepath.Join(root, "decide", "score.jsonl")
	if why == "" {
		if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
			why = "the score record's directory: " + oneline.Err(err)
		}
	}
	if why != "" {
		for _, j := range l.toScore {
			note(j, why)
		}
		return
	}
	pass, cancel := context.WithTimeout(ctx, scoreWait)
	defer cancel()
	stopped := "" // why the pass stopped: the first failure, or the deadline
	for _, j := range l.toScore {
		ls := &landScore{}
		l.out[j.at].Score = ls
		var scores []sprint.CardScore
		var failed, skipped []string
		for _, c := range j.pins {
			diff, ok := l.diffs[c.id]
			switch {
			case !ok:
				continue // a merge that made no commit changes nothing to score
			case stopped == "" && pass.Err() != nil:
				stopped = "the scoring pass ran out of its " + scoreWait.String() + " (" + oneline.Err(pass.Err()) + ")"
			}
			if stopped != "" {
				skipped = append(skipped, c.id)
				continue
			}
			d, err := decide.Score(pass, backend, c.brief, diff, record, decide.ScoreOp(c.id, c.head), l.a.now())
			if err != nil {
				failed = append(failed, c.id+" was not scored: "+oneline.Err(err))
				stopped = "the scoring pass stopped at the first failure"
				continue
			}
			top, p := decide.Top(d)
			scores = append(scores, sprint.CardScore{ID: c.id, Op: d.ID, Class: top, P: p})
		}
		if len(skipped) > 0 {
			failed = append(failed, stopped+"; not scored: "+strings.Join(skipped, ", "))
		}
		ls.Why = strings.Join(failed, "; ")
		if len(scores) == 0 {
			continue
		}
		step := store.ScoreStep(sprint.ScoreReq{Stream: j.stream, Scores: scores, Who: l.c.actor})
		epoch := l.epoch
		step.Epoch = &epoch
		l.a.serial.Lock()
		res, err := l.st.Run(ctx, step)
		l.a.serial.Unlock()
		if code := stepExit(res, err); code != 0 {
			ls.Why = strings.TrimPrefix(ls.Why+"; the scores were recorded in "+record+" and not reported ("+stepWhy(res, err)+")", "; ")
			continue
		}
		ls.Scored, ls.Judged = len(scores), res.Notes > 0
	}
}
