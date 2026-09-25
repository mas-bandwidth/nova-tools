package ci

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// cardStates are the card index states a ci card passes through (3.2).
var cardStates = []string{"queued", "dealt", "launched", "running", "ended"}

// Card is one ci card as the status line and the rerun policy see it.
type Card struct {
	Label   string
	State   string
	Repo    string
	Head    string
	Attempt string
	CutAt   int64
	Blocked string
	Verdict string // the record's verdict, MISSING when absent
	Owner   bool   // the record's latest pointer is this card's attempt
}

// Cards reads every ci card of the sprint and its record in two pipelined
// batches (never one round trip per card).
func Cards(ctx context.Context, st *store.Store, sprint string) ([]Card, error) {
	client := st.Client()
	pipe := client.Pipeline()
	members := make(map[string]string)
	cmds := make([]interface{ Result() ([]string, error) }, len(cardStates))
	for i, s := range cardStates {
		cmds[i] = pipe.SMembers(ctx, "s:"+sprint+":idx:card:"+s)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("card indexes: %w", err)
	}
	for i, s := range cardStates {
		labels, err := cmds[i].Result()
		if err != nil {
			return nil, err
		}
		for _, l := range labels {
			if strings.HasPrefix(l, "ci-") {
				members[l] = s
			}
		}
	}
	labels := make([]string, 0, len(members))
	for l := range members {
		labels = append(labels, l)
	}
	sort.Strings(labels)
	reads := make([]store.HashRead, len(labels))
	for i, l := range labels {
		reads[i] = store.HashRead{Key: "s:" + sprint + ":card:" + l,
			Fields: []string{"ci_repo", "ci_head", "attempt", "cut_at", "blocked", "verdict"}}
	}
	cardVals, err := st.PipelineHMGet(ctx, reads)
	if err != nil {
		return nil, err
	}
	cards := make([]Card, len(labels))
	for i, l := range labels {
		v := cardVals[i]
		c := Card{
			Label:   l,
			State:   members[l],
			Repo:    str(v[0]),
			Head:    str(v[1]),
			Attempt: str(v[2]),
			Blocked: str(v[4]),
			Verdict: str(v[5]),
			Owner:   true,
		}
		c.CutAt, _ = strconv.ParseInt(str(v[3]), 10, 64)
		if c.Verdict == "" {
			c.Verdict = Missing
		}
		cards[i] = c
	}
	return cards, nil
}

func str(v any) string {
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// ReconcileReruns is the reconciler's rerun policy (10.5 item 2): for every
// ended ci card whose own latest attempt left FAIL or MISSING and that has
// no rerun yet, cut the one rerun on another healthy bench. No friend and no
// model decides it. It returns the results by label.
func ReconcileReruns(ctx context.Context, st *store.Store, sprint string) (map[string]Result, error) {
	cards, err := Cards(ctx, st, sprint)
	if err != nil {
		return nil, err
	}
	out := map[string]Result{}
	for _, c := range cards {
		if c.State != "ended" || !c.Owner || c.Blocked != "" {
			continue
		}
		if c.Verdict != Fail && c.Verdict != Missing {
			continue
		}
		r, err := Rerun(ctx, st, sprint, c.Label, "reconciler", "policy ci_reruns")
		if err != nil {
			return out, err
		}
		if r.Status == "SPENT" {
			continue
		}
		out[c.Label] = r
	}
	return out, nil
}

// Status is the ci pipeline in one line (4.8): heads OK over heads required,
// then cut, dealt, running, the verdicts at head, blocked and the oldest
// age of a head without an OK. A head counts once however many attempts it
// took (10.4 item 5).
type Status struct {
	Required, OK                  int
	Cut, Dealt, Running           int
	Pending, Fail, Flaky, Missing int
	Blocked                       int
	OldestS                       int64
}

func (s Status) String() string {
	line := fmt.Sprintf("ci %d/%d OK | cut %d dealt %d running %d | PENDING %d FAIL %d FLAKY %d MISSING %d",
		s.OK, s.Required, s.Cut, s.Dealt, s.Running, s.Pending, s.Fail, s.Flaky, s.Missing)
	if s.Blocked > 0 {
		line += fmt.Sprintf(" | blocked: no alternate bench %d", s.Blocked)
	}
	return line + fmt.Sprintf(" | oldest %ds", s.OldestS)
}

// ReadStatus derives the ci line from the card indexes and the records.
func ReadStatus(ctx context.Context, st *store.Store, sprint string) (Status, error) {
	cards, err := Cards(ctx, st, sprint)
	if err != nil {
		return Status{}, err
	}
	nowT, err := st.Client().Time(ctx).Result()
	if err != nil {
		return Status{}, fmt.Errorf("redis TIME: %w", err)
	}
	now := nowT.UnixMilli()
	var s Status
	for _, c := range cards {
		s.Required++
		switch c.State {
		case "queued":
			s.Cut++
		case "dealt":
			s.Dealt++
		case "launched", "running":
			s.Running++
		}
		switch c.Verdict {
		case OK:
			s.OK++
		case Pending:
			s.Pending++
		case Fail:
			s.Fail++
		case Flaky:
			s.Flaky++
		default:
			s.Missing++
		}
		if c.Blocked != "" {
			s.Blocked++
		}
		if c.Verdict != OK && c.CutAt > 0 {
			if age := (now - c.CutAt) / 1000; age > s.OldestS {
				s.OldestS = age
			}
		}
	}
	return s, nil
}
