package life

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// consumer is the seat's consumer id, friend:<f> (#3998).
func (s *Server) consumer() taskcard.Consumer {
	return taskcard.Consumer{Kind: "friend", Name: s.cfg.Friend}
}

// takeCopies is the seat's copy take (#3998): one read of the consumer's
// sets, then every working copy the seat has no child for (the deal duty's
// card work --fill moved it there, #3999) is started under the token on its
// record, and one `card work --as friend:<f>` takes the rest of the free
// width from ready: --fill when the seat's free width covers every free
// slot, else --n <free width>. A copy whose dispatch fails is ended as a
// fail with the reason. It returns how many it started. A seat that cannot
// take (the verbs not granted yet, no slots) takes nothing and prints why
// once.
func (s *Server) takeCopies(ctx context.Context, free int) int {
	c := s.st.Client()
	as := s.consumer()
	pipe := c.Pipeline()
	slotsCmd := pipe.HGet(ctx, as.DesiredKey(), "slots")
	workingCmd := pipe.ZRange(ctx, as.Key("working"), 0, -1)
	readyCmd := pipe.ZCard(ctx, as.Key("ready"))
	_, _ = pipe.Exec(ctx)
	started := 0
	for _, id := range workingCmd.Val() {
		if started >= free {
			return started
		}
		if !taskcard.IsCopy(id) || s.running(card.CopySprint+"/"+id) {
			continue
		}
		token := c.HGet(ctx, taskcard.Key(id), "token").Val()
		s.startOrFail(ctx, id, token)
		started++
	}
	free -= started
	if free <= 0 || readyCmd.Val() == 0 {
		return started
	}
	slots, _ := strconv.Atoi(slotsCmd.Val())
	fill := free >= slots-len(workingCmd.Val())
	w, err := taskcard.Work(ctx, c, as, s.cfg.Actor, free, fill)
	if err != nil {
		if msg := err.Error(); msg != s.copyErr {
			s.copyErr = msg
			fmt.Fprintf(s.cfg.Out, "SERVE %s card work: %v\n", s.cfg.Friend, err)
		}
		return started
	}
	s.copyErr = ""
	for i, id := range w.IDs {
		s.startOrFail(ctx, id, w.Tokens[i])
	}
	return started + len(w.IDs)
}

// running says whether the seat has a child for key (<sprint>/<id>).
func (s *Server) running(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.children[key]
	return ok
}

// startOrFail starts a copy's child; a dispatch that fails ends the copy
// as a fail with the reason.
func (s *Server) startOrFail(ctx context.Context, id, token string) {
	if err := s.startCopy(ctx, id, token); err != nil {
		ch := &child{copy: id, claim: copyClaim(id, token, "")}
		s.receipt(ctx, "take", &ch.claim, "copy dispatch: "+oneLine(err.Error()))
		s.endCopyWith(ctx, ch, taskcard.EndRequest{Why: "dispatch: " + oneLine(err.Error())})
	}
}

// copyClaim is a copy's claim shape for the receipts and the child's
// environment: sprint card.CopySprint, id the copy, attempt its number.
func copyClaim(id, token, kind string) task.Claim {
	n, _ := card.CopyNumber(id)
	return task.Claim{Sprint: card.CopySprint, ID: id, Attempt: n, Token: token, Kind: kind}
}

// startCopy writes the copy's card as the brief (card.RenderCopy of its
// record; the record itself when render refuses it), starts the harness and
// sends the start-ack beat.
func (s *Server) startCopy(ctx context.Context, id, token string) error {
	rec, err := s.st.Client().HGetAll(ctx, taskcard.Key(id)).Result()
	if err != nil {
		return fmt.Errorf("read copy: %w", err)
	}
	ch := &child{copy: id, leg: rec["leg"], kind: rec["kind"], head: rec["head"], exited: make(chan struct{})}
	ch.claim = copyClaim(id, token, ch.kind)
	ch.dir = filepath.Join(s.cfg.Dir, card.CopySprint, card.CopyCardLabel(id))
	if err := os.MkdirAll(ch.dir, 0o755); err != nil {
		return err
	}
	ch.brief = filepath.Join(ch.dir, "brief.md")
	ch.out = filepath.Join(ch.dir, "out.log")
	body, rerr := card.RenderCopy(card.CopyCardFrom(id, rec))
	ch.briefSrc = "copy"
	if rerr != nil {
		ch.briefSrc = "record"
		var b strings.Builder
		fmt.Fprintf(&b, "COPY: %s\nBRIEF: record (%s)\n\n", id, oneLine(rerr.Error()))
		for _, k := range []string{"primary", "leg", "kind", "title", "repo", "pr", "head", "base", "base_sha", "paths",
			"done_when", "finding", "origin", "stream"} {
			if rec[k] != "" {
				fmt.Fprintf(&b, "%s: %s\n", k, rec[k])
			}
		}
		fmt.Fprintf(&b, "\nEnd with one typed line on stdout: SCORE who=<you> head=<sha> score=<n>/10 for a read, "+
			"DONE <what> [pr=<owner/name>#<n> head=<sha>] for work, BLOCKED <why> when you cannot. The seat runs "+
			"nova-sprint card end --id %s with it.\n", id)
		body = []byte(b.String())
	}
	if err := os.WriteFile(ch.brief, body, 0o644); err != nil {
		return err
	}
	if err := s.spawn(ctx, ch); err != nil {
		return err
	}
	s.beatCopies(ctx, []*child{ch})
	return nil
}

// beatCopies renews the named copies' leases in one card beat; when the
// call is refused (a copy no longer working here: revoked, expired, ended),
// each is beaten alone and a fenced one's child is stopped.
func (s *Server) beatCopies(ctx context.Context, copies []*child) {
	if len(copies) == 0 {
		return
	}
	ids := make([]string, len(copies))
	for i, c := range copies {
		ids[i] = c.copy
		c.lastBeat = s.now()
	}
	c := s.st.Client()
	_, err := taskcard.BeatCopies(ctx, c, s.consumer(), ids...)
	if err == nil {
		return
	}
	if _, refused := taskcard.IsRefused(err); !refused {
		fmt.Fprintf(s.cfg.Out, "SERVE %s card beat: %v\n", s.cfg.Friend, err)
		return
	}
	for _, ch := range copies {
		if _, err := taskcard.BeatCopies(ctx, c, s.consumer(), ch.copy); err != nil {
			if _, refused := taskcard.IsRefused(err); refused {
				s.receipt(ctx, "fenced", &ch.claim, "copy="+ch.copy)
				killGroup(ch.cmd)
			}
		}
	}
}

// endCopy returns an exited copy to its primary: nothing when the child
// ended it itself; else from its typed line (SCORE -> the read's score;
// DONE [pr=<repo>#<n> head=<sha> | done-already=<sha>] -> ok) or a fail
// with the exit reason.
func (s *Server) endCopy(ctx context.Context, c *child, rc int, reason, line, output string) {
	where := s.st.Client().HGet(ctx, taskcard.Key(c.copy), "where").Val()
	if where == "ok" || where == "fail" {
		s.receipt(ctx, "done", &c.claim, "copy="+c.copy+" ended by the child: "+where)
		return
	}
	req := taskcard.EndRequest{}
	f := strings.Fields(line)
	switch {
	case rc == 0 && len(f) > 0 && c.leg == "read":
		_, score := VerdictOf(line)
		n, _ := strconv.Atoi(score)
		if f[0] != "SCORE" || n < 1 {
			req.Why = "read without a score: " + oneLine(line)
			break
		}
		req.OK, req.Score, req.Reader = true, n, s.cfg.Friend
		if who := kvOf(f)["who"]; who != "" {
			req.Reader = who
		}
		req.Head = kvOf(f)["head"]
		req.Fields = []string{"evidence", line}
	case rc == 0 && len(f) > 0 && f[0] == "DONE":
		kv := kvOf(f)
		req.OK, req.Fields = true, []string{"evidence", line}
		if pr := kv["pr"]; pr != "" {
			repo, n, ok := strings.Cut(pr, "#")
			if !ok || kv["head"] == "" {
				req.OK, req.Why = false, "DONE names pr= without <repo>#<n> and head=: "+oneLine(line)
				break
			}
			req.Repo, req.PR, req.Head = repo, n, kv["head"]
		} else if sha := kv["done-already"]; sha != "" {
			req.DoneAlready = sha
		}
	default:
		if line == "" {
			reason += "; no typed line"
			if last := lastLine(output); last != "" {
				reason += ": " + last
			}
		} else {
			reason += "; wrote " + line
		}
		req.Why = oneLine(reason)
	}
	s.endCopyWith(ctx, c, req)
}

// endCopyWith is one card end --id <copy> under the copy's token.
func (s *Server) endCopyWith(ctx context.Context, c *child, req taskcard.EndRequest) {
	req.IDs, req.Token, req.By = []string{c.copy}, c.claim.Token, s.cfg.Actor
	if !req.OK && req.Why == "" {
		req.Why = "failed"
	}
	ended, err := taskcard.End(ctx, s.st.Client(), req)
	if err != nil {
		s.receipt(ctx, "done", &c.claim, "copy="+c.copy+" error="+oneLine(err.Error()))
		fmt.Fprintf(s.cfg.Out, "SERVE %s card end %s: %v\n", s.cfg.Friend, c.copy, err)
		return
	}
	detail := "copy=" + c.copy
	if len(ended) == 1 {
		detail += " primary=" + ended[0].Primary + " to=" + ended[0].To
	}
	if !req.OK {
		detail += " why=" + req.Why
	}
	s.receipt(ctx, "done", &c.claim, detail)
}

// kvOf is a typed line's k=v words after the first.
func kvOf(f []string) map[string]string {
	kv := map[string]string{}
	for _, w := range f[1:] {
		if k, v, ok := strings.Cut(w, "="); ok {
			kv[strings.ToLower(k)] = strings.TrimRight(v, ":,;")
		}
	}
	return kv
}
