package main

import (
	"context"
	"io"
	"time"
)

// promote.go is the failing start of the promote step. The test names the
// branch, the pull request head, the admission, and the one judgment. None of
// that is here yet.

func init() {
	verbClasses["promote"] = classMachine
	notServed = append(notServed, "promote")
}

type promoter struct {
	dir, live, base, check string
	now                    time.Time
	dry                    bool
	env                    []string
	gitRun                 func(ctx context.Context, dir string, args ...string) (string, error)
	ghRun                  func(ctx context.Context, dir string, args ...string) (string, error)
	gate                   func(ctx context.Context, dir, sha string) (string, error)
	judged                 string
	every                  time.Duration
	landings               int
	started, lastAt        time.Time
}

type promoteJudgment struct {
	What      string
	Tail      string
	Decisions []string
}

type promoteOutcome struct {
	Branch   string
	Live     string
	Tip      string
	Head     string
	Body     string
	Cards    []string
	Entry    string
	Promoted string
	Judgment *promoteJudgment
	Dry      bool
	Nothing  bool
	Cut      bool
}

func (p *promoter) step(context.Context, io.Writer, io.Writer) (promoteOutcome, int) {
	return promoteOutcome{}, 0
}
