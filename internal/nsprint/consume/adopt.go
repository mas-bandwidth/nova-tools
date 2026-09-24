package consume

// adopt.go is the adoption half of the pr-to-read rule (nova-tools #3040
// rev 4, #2756 10.8.3): a sprint PR that no card produced gets its ci cut and
// one review task per required reader at its head, once per head. A PR
// qualifies when it is in s:<S>:prs, its state is not landed, dropped or
// closed, draft is not true, it has no s:<S>:prcard entry (ns_card_harvested
// writes one for every card PR) and no open hold. The PR records are read in
// two round trips; the reader census is ok-to-friend's.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/redis/go-redis/v9"
)

type adoptPR struct {
	id     string // <repo>#<n>, the s:<S>:prs member
	repo   string
	n      int
	rec    *redis.MapStringStringCmd
	card   *redis.StringCmd
	holds  *redis.MapStringStringCmd
	adopt  *redis.StringCmd
	parsed bool
}

// openHold: any hold on the PR not yet released (#3092's record shape
// {holder, head, released_by}); an unreadable one counts as open.
func openHold(holds map[string]string) bool {
	for _, raw := range holds {
		var h struct {
			ReleasedBy string `json:"released_by"`
		}
		if err := json.Unmarshal([]byte(raw), &h); err != nil || h.ReleasedBy == "" {
			return true
		}
	}
	return false
}

func (p *PRToReadRule) adopt(ctx context.Context, out *strings.Builder) (int, error) {
	client := p.Store.Client()
	S := p.Sprint
	members, err := client.SMembers(ctx, "s:"+S+":prs").Result()
	if err != nil {
		return 0, fmt.Errorf("pr-to-read: prs: %w", err)
	}
	if len(members) == 0 {
		return 0, nil
	}
	sort.Strings(members)
	prs := make([]*adoptPR, 0, len(members))
	pipe := client.Pipeline()
	for _, m := range members {
		pr := &adoptPR{id: m}
		if i := strings.LastIndex(m, "#"); i > 0 {
			if n, err := strconv.Atoi(m[i+1:]); err == nil && n > 0 {
				pr.repo, pr.n, pr.parsed = m[:i], n, true
			}
		}
		prs = append(prs, pr)
		if !pr.parsed {
			continue
		}
		num := strconv.Itoa(pr.n)
		pr.rec = pipe.HGetAll(ctx, "s:"+S+":pr:"+pr.repo+":"+num)
		pr.card = pipe.HGet(ctx, "s:"+S+":prcard", m)
		pr.holds = pipe.HGetAll(ctx, "s:"+S+":hold:"+pr.repo+":"+num)
		pr.adopt = pipe.HGet(ctx, "s:"+S+":adopt:"+pr.repo+":"+num, "head")
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return 0, fmt.Errorf("pr-to-read: pr records: %w", err)
	}
	var census *readerCensus
	adopted := 0
	for _, pr := range prs {
		if !pr.parsed {
			fmt.Fprintf(out, "SKIP %s member\n", pr.id)
			continue
		}
		rec := pr.rec.Val()
		head := rec["head"]
		switch {
		case len(rec) == 0:
			fmt.Fprintf(out, "WAIT %s record MISSING\n", pr.id)
			continue
		case rec["state"] == "landed" || rec["state"] == "dropped" || rec["state"] == "closed":
			fmt.Fprintf(out, "SKIP %s state %s\n", pr.id, rec["state"])
			continue
		case rec["draft"] == "true" || rec["draft"] == "1":
			fmt.Fprintf(out, "SKIP %s draft\n", pr.id)
			continue
		case pr.card.Val() != "":
			fmt.Fprintf(out, "SKIP %s card %s\n", pr.id, pr.card.Val())
			continue
		case openHold(pr.holds.Val()):
			fmt.Fprintf(out, "SKIP %s hold\n", pr.id)
			continue
		case head == "":
			fmt.Fprintf(out, "WAIT %s head MISSING\n", pr.id)
			continue
		case pr.adopt.Val() == head:
			fmt.Fprintf(out, "SKIP %s adopted\n", pr.id)
			continue
		case rec["author"] == "":
			fmt.Fprintf(out, "WAIT %s author MISSING\n", pr.id)
			continue
		}
		if census == nil {
			census, err = (&OkFriend{Store: p.Store, Sprint: S}).census(ctx)
			if err != nil {
				return adopted, fmt.Errorf("pr-to-read: %w", err)
			}
		}
		want := census.required(rec)
		readers := census.pick(want, rec["author"])
		if len(readers) < want || len(readers) == 0 {
			fmt.Fprintf(out, "WAIT %s no readers\n", pr.id)
			continue
		}
		cut := "0"
		if p.CICut != nil {
			if err := p.CICut(ctx, CICut{Sprint: S, Repo: pr.repo, PR: pr.n, Head: head, Base: rec["base"]}); err != nil {
				return adopted, fmt.Errorf("pr-to-read: ci cut %s at %s: %w", pr.id, head12(head), err)
			}
			cut = "1"
		}
		ref := pr.id
		args := []any{S, pr.repo, strconv.Itoa(pr.n), head, p.Actor, cut, strconv.Itoa(len(readers))}
		for _, friend := range readers {
			req := task.PushRequest{
				Sprint: S, ID: task.ReviewID(pr.repo, pr.n, head, friend), Kind: task.KindReview,
				Title:   fmt.Sprintf("read %s at %s (no card) | %s", pr.id, head12(head), ReadTitleSuffix),
				Effects: task.EffectsNone, Repo: pr.repo, PR: pr.n, Head: head, Ref: ref,
				To: friend, Front: true,
			}
			args = append(args, req.ID, friend, req.Title, "0", task.PayloadSHA(req), ref)
		}
		reply, err := client.FCall(ctx, FunctionPRToReadAdopt, nil, args...).StringSlice()
		if err != nil {
			return adopted, fmt.Errorf("pr-to-read: adopt %s: %w", pr.id, err)
		}
		switch {
		case len(reply) > 0 && reply[0] == "OK":
			fmt.Fprintf(out, "ADOPT %s@%s cut=%s reads=%s\n", pr.id, head12(head), cut, strings.Join(readers, ","))
			for _, friend := range readers {
				census.load[friend]++
			}
			adopted++
		case len(reply) > 0 && reply[0] == "NOOP":
			fmt.Fprintf(out, "SKIP %s adopted\n", pr.id)
		default:
			fmt.Fprintf(out, "WAIT %s %s\n", pr.id, strings.Join(reply, " "))
		}
	}
	return adopted, nil
}
