package main

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// THE LANDED SCORE (docs/SPEC-SPRINT.md section 7, the landed score; docs/SPEC-NOVA-DECIDE.md
// section 9). After a batch is pushed and reported, land scores each card's merge diff (the
// diff its own checks read, checkCard) against the card's brief with nova-decide's score
// decision, recorded in <land root>/decide/score.jsonl under <card>@landed@<head>, and
// reports the batch's scores in one store step (store.ScoreStep): the top class and its p on
// each landed card, and one "landed work scored low" judgment listing the cards at or above
// the sprint row's decide_score_bar. A score never holds a landing back: the batch has
// landed before it is asked, and a score that cannot be made (no key, a backend that
// fails) is said on the batch's line and changes nothing else.

// landScore is a landed batch's scores: how many of its cards were scored, whether the
// step raised the scored-low judgment, and why a card or the batch was not scored.
type landScore struct {
	Scored int    `json:"scored"`
	Judged bool   `json:"judged,omitempty"`
	Why    string `json:"why,omitempty"`
}

// scoreWait bounds one score's answer, as nova-decide's own --timeout default does.
const scoreWait = time.Minute

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
	return decide.JevHTTP(key), ""
}

// score scores the landed batch's cards and reports their scores; the batch's line
// carries what happened (b.Score).
func (l *lander) score(b *landBatch, stream string, pins []landCard) {
	ls := &landScore{}
	b.Score = ls
	backend, why := l.a.scorer()
	if why != "" {
		ls.Why = why
		return
	}
	root := l.root
	if root == "" {
		var err error
		if root, err = l.a.landRoot(); err != nil {
			ls.Why = "no directory for the score record: " + oneline.Err(err)
			return
		}
	}
	record := filepath.Join(root, "decide", "score.jsonl")
	if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
		ls.Why = "the score record's directory: " + oneline.Err(err)
		return
	}
	var scores []sprint.CardScore
	for _, c := range pins {
		diff, ok := l.diffs[c.id]
		if !ok {
			continue // a merge that made no commit changes nothing to score
		}
		ctx, cancel := context.WithTimeout(context.Background(), scoreWait)
		d, err := decide.Score(ctx, backend, c.brief, diff, record, decide.ScoreOp(c.id, c.head), l.a.now())
		cancel()
		if err != nil {
			ls.Why = c.id + " was not scored: " + oneline.Err(err)
			continue
		}
		top, p := decide.Top(d)
		scores = append(scores, sprint.CardScore{ID: c.id, Op: d.ID, Class: top, P: p})
	}
	if len(scores) == 0 {
		return
	}
	step := store.ScoreStep(sprint.ScoreReq{Stream: stream, Scores: scores, Who: l.c.actor})
	epoch := l.epoch
	step.Epoch = &epoch
	l.a.serial.Lock()
	res, err := l.st.Run(context.Background(), step)
	l.a.serial.Unlock()
	if code := stepExit(res, err); code != 0 {
		ls.Why = "the scores were recorded in " + record + " and not reported (" + stepWhy(res, err) + ")"
		return
	}
	ls.Scored, ls.Judged = len(scores), res.Notes > 0
}
