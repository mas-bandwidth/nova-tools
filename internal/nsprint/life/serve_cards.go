// Friend serve over task cards (nova-tools #4095). Glenn 2026-09-25 4:55 PM
// ET: "When a child worker finishes a task, this is a trigger for you to
// tell yourself to grab more tasks from your ready queue ... This applies to
// you, and all other friends. This should be automatic." The coordinator seat
// is a friend seat like any other: `nova-sprint friend serve --as rowan
// --width 32 --dispatch "claude -p --model @model @brief"` takes the oldest
// cards of friend:rowan:cards:ready up to its free width, one child per card.
//
// Every card step is one FCALL of the one writer (ns_tcard_* in
// fn/lua/02_card_move.lua) through package taskcard:
//
//	take   taskcard.Take    friend:<f>:cards:ready -> working, lease started
//	beat   taskcard.Beat    every TaskBeatEvery while the child lives
//	end    taskcard.Done    the child's typed line as evidence (merging with
//	                        a PR, else done/ok)
//	fail   taskcard.Cancel  done/fail: no typed line, a BLOCKED or ABSTAIN
//	                        line, a non-zero exit, or a brief/dispatch refusal
//	give   taskcard.Move    working -> ready when the serve stops
//
// When the table moves land (#3991), take is `card work --fill`, beat is
// `card beat` with the copy's token, and end and fail are `card end` (ok,
// or --fail with the why), which moves the primary in the same call.
package life

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/brief"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/redis/go-redis/v9"
)

// takeCards takes up to n cards from friend:<f>:cards:ready, reads their
// records and starts one child per card. A card whose brief or dispatch
// refuses is failed at once with the refusal as its why.
//
// Since the one task store (#3778) a sprint task is a task:<id> record in the
// same friend:<f>:cards:ready. A record with an attempt field is such a
// sprint-store task: it is left for task.TakeAvailable, which claims it with
// its fence token, so only cards are taken here. Three round trips: the ready
// set, the attempt field of each, then one pipeline that takes each card by
// name (one ns_tcard_take per id, so a card another taker moved first is
// skipped, never a refusal of the rest) and reads its record.
func (s *Server) takeCards(ctx context.Context, n int) (taken, failed int, err error) {
	client := s.st.Client()
	ready, err := client.ZRange(ctx, "friend:"+s.cfg.Friend+":cards:ready", 0, -1).Result()
	if err != nil || len(ready) == 0 {
		return 0, 0, err
	}
	pipe := client.Pipeline()
	sprintTask := make([]*redis.BoolCmd, len(ready))
	for i, id := range ready {
		sprintTask[i] = pipe.HExists(ctx, taskcard.Key(id), "attempt")
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return 0, 0, fmt.Errorf("read ready: %w", err)
	}
	var ids []string
	for i, id := range ready {
		// A consumer copy (<id>~<n>, #3998) is takeCopies' through card work.
		if len(ids) < n && !sprintTask[i].Val() && !taskcard.IsCopy(id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return 0, 0, nil
	}
	pipe = client.Pipeline()
	takes := make([]*redis.Cmd, len(ids))
	recs := make([]*redis.MapStringStringCmd, len(ids))
	for i, id := range ids {
		takes[i] = pipe.FCall(ctx, taskcard.FnTake, nil, s.cfg.Friend, 1, s.cfg.Actor, id)
		recs[i] = pipe.HGetAll(ctx, taskcard.Key(id))
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		if _, ok := err.(redis.Error); !ok {
			return 0, 0, fmt.Errorf("take cards: %w", err)
		}
	}
	for i, id := range ids {
		if !tookCard(takes[i]) {
			continue
		}
		taken++
		rec := recs[i].Val()
		s.cardReceipt(ctx, "take", id, "kind="+rec["kind"])
		if err := s.startCard(ctx, id, rec); err != nil {
			s.failCard(ctx, id, "dispatch: "+err.Error())
			failed++
		}
	}
	return taken, failed, nil
}

// tookCard is true when one named ns_tcard_take moved its card to working
// (TAKEN 1 <id>); a refusal (another taker moved it first) is false.
func tookCard(cmd *redis.Cmd) bool {
	reply, err := cmd.Slice()
	if err != nil || len(reply) < 3 {
		return false
	}
	word, _ := reply[0].(string)
	return word == "TAKEN"
}

// startCard renders the card's brief from its record and launches its child.
func (s *Server) startCard(ctx context.Context, id string, rec map[string]string) error {
	attempt, _ := strconv.Atoi(rec["attempts"])
	ch := &child{
		card: true, head: rec["head"], kind: rec["kind"], exited: make(chan struct{}),
		claim: task.Claim{Sprint: rec["sprint"], ID: id, Kind: rec["kind"], Attempt: attempt + 1},
	}
	ch.model = s.model(ch.kind)
	ch.dir = filepath.Join(s.cfg.Dir, CardsDirName, id+"-"+strconv.FormatInt(s.now().UnixMilli(), 10))
	if err := os.MkdirAll(ch.dir, 0o755); err != nil {
		return err
	}
	ch.brief = filepath.Join(ch.dir, "brief.md")
	ch.out = filepath.Join(ch.dir, "out.log")
	b, err := brief.RenderCard(id, rec, ch.model)
	if err != nil {
		return err
	}
	if err := os.WriteFile(ch.brief, b, 0o644); err != nil {
		return err
	}
	ch.briefSrc = "card"
	return s.launch(ctx, ch)
}

// beatCard renews a live card child's lease. A refusal (the card left
// working, or another friend holds it) is the fence: the child's work no
// longer has a card, so it is stopped.
func (s *Server) beatCard(ctx context.Context, c *child) {
	_, err := taskcard.Beat(ctx, s.st.Client(), c.claim.ID, s.cfg.Friend)
	c.lastBeat = s.now()
	if why, refused := taskcard.IsRefused(err); refused {
		s.cardReceipt(ctx, "fenced", c.claim.ID, "why="+oneLine(why))
		killGroup(c.cmd)
		return
	}
	if err != nil {
		fmt.Fprintf(s.cfg.Out, "SERVE %s beat card:%s: %v\n", s.cfg.Friend, c.claim.ID, err)
	}
}

// closeCard closes an exited card child: its exit receipt, then endCard with
// the typed line it wrote.
func (s *Server) closeCard(ctx context.Context, c *child, rc int, reason string) {
	output := readTail(c.out)
	line := TypedLine(output)
	secs := int(s.now().Sub(c.started).Seconds())
	s.cardReceipt(ctx, "exit", c.claim.ID, fmt.Sprintf("rc=%d secs=%d typed=%t", rc, secs, line != ""))
	s.endCard(ctx, c.claim.ID, rc, reason, line, output)
}

// endCard ends an exited card child: its last typed line through the done
// move when it exited 0 with a line that is not BLOCKED or ABSTAIN, else the
// card fails with the exit and what the child wrote.
func (s *Server) endCard(ctx context.Context, id string, rc int, reason, line, output string) {
	if rc == 0 && line != "" && !failLine(line) {
		res, err := taskcard.Done(ctx, s.st.Client(), id, s.cfg.Actor, line, "")
		if err != nil {
			s.cardReceipt(ctx, "done", id, "error="+oneLine(err.Error()))
			return
		}
		s.cardReceipt(ctx, "done", id, "to="+res.To+" evidence="+line)
		return
	}
	if line == "" {
		reason += "; no typed line"
		if last := lastLine(output); last != "" {
			reason += ": " + last
		}
	} else {
		reason += "; wrote " + line
	}
	s.failCard(ctx, id, reason)
}

// failCard moves a card to done/fail with why (the one move, one call).
func (s *Server) failCard(ctx context.Context, id, why string) {
	why = oneLine(why)
	res, err := taskcard.Cancel(ctx, s.st.Client(), id, s.cfg.Actor, why)
	if err != nil {
		s.cardReceipt(ctx, "fail", id, "error="+oneLine(err.Error()))
		return
	}
	s.cardReceipt(ctx, "fail", id, "to="+res.To+" why="+why)
}

// failLine is a typed line that says the work was not done.
func failLine(line string) bool {
	f := strings.Fields(line)
	return len(f) > 0 && (f[0] == "BLOCKED" || f[0] == "ABSTAIN")
}
