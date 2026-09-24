package consume

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/redis/go-redis/v9"
)

func (r *PRToReadRule) adopt(ctx context.Context, policy map[string]string) (int, error) {
	client := r.Store.Client()
	sprint := r.Sprint

	// Roundtrip 1: Read s:<S>:prs and s:<S>:prcard
	pipe1 := client.Pipeline()
	prsCmd := pipe1.SMembers(ctx, "s:"+sprint+":prs")
	prcardCmd := pipe1.HGetAll(ctx, "s:"+sprint+":prcard")
	if _, err := pipe1.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return 0, fmt.Errorf("pr-to-read: read prs and prcard: %w", err)
	}
	prs := prsCmd.Val()
	prcard := prcardCmd.Val()
	if len(prs) == 0 {
		return 0, nil
	}

	sort.Strings(prs)

	// Roundtrip 2: Read PR records, holds, and adopt hashes for all PRs
	pipe2 := client.Pipeline()
	prCmds := make([]*redis.MapStringStringCmd, len(prs))
	holdCmds := make([]*redis.MapStringStringCmd, len(prs))
	adoptCmds := make([]*redis.MapStringStringCmd, len(prs))
	type prIdent struct {
		id   string
		repo string
		n    int
	}
	idents := make([]prIdent, len(prs))

	for i, prID := range prs {
		repo, numStr, _ := strings.Cut(prID, "#")
		n, _ := strconv.Atoi(numStr)
		idents[i] = prIdent{id: prID, repo: repo, n: n}
		sfx := repo + ":" + numStr
		prCmds[i] = pipe2.HGetAll(ctx, "s:"+sprint+":pr:"+sfx)
		holdCmds[i] = pipe2.HGetAll(ctx, "s:"+sprint+":hold:"+sfx)
		adoptCmds[i] = pipe2.HGetAll(ctx, "s:"+sprint+":adopt:"+sfx)
	}
	if _, err := pipe2.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return 0, fmt.Errorf("pr-to-read: read pr records: %w", err)
	}

	// Check if any PR qualifies for adoption and needs reader census
	needsCensus := false
	for i, ident := range idents {
		if prcard[ident.id] != "" {
			continue
		}
		fields := prCmds[i].Val()
		if len(fields) == 0 {
			continue
		}
		state := fields["state"]
		if state == "landed" || state == "dropped" || state == "closed" {
			continue
		}
		if fields["draft"] == "true" {
			continue
		}
		if hasOpenHold(holdCmds[i].Val()) {
			continue
		}
		head := fields["head"]
		if head == "" {
			continue
		}
		adoptRec := adoptCmds[i].Val()
		if adoptRec["head"] == head {
			continue
		}
		if fields["author"] == "" {
			continue
		}
		needsCensus = true
		break
	}

	var census *readerCensus
	if needsCensus {
		// Roundtrip 3: Read reader census
		var err error
		census, err = r.census(ctx, policy)
		if err != nil {
			return 0, fmt.Errorf("pr-to-read: census: %w", err)
		}
	}

	handled := 0
	for i, ident := range idents {
		prID := ident.id
		// 1. Check card
		if prcard[prID] != "" {
			if r.Out != nil {
				fmt.Fprintf(r.Out, "SKIP %s card\n", prID)
			}
			continue
		}
		// 2. Check record exists
		fields := prCmds[i].Val()
		if len(fields) == 0 {
			if r.Out != nil {
				fmt.Fprintf(r.Out, "SKIP %s no-record\n", prID)
			}
			continue
		}
		// 3. Check state
		state := fields["state"]
		if state == "landed" || state == "dropped" || state == "closed" {
			if r.Out != nil {
				fmt.Fprintf(r.Out, "SKIP %s state %s\n", prID, state)
			}
			continue
		}
		// 4. Check draft
		if fields["draft"] == "true" {
			if r.Out != nil {
				fmt.Fprintf(r.Out, "SKIP %s draft\n", prID)
			}
			continue
		}
		// 5. Check hold
		if hasOpenHold(holdCmds[i].Val()) {
			if r.Out != nil {
				fmt.Fprintf(r.Out, "SKIP %s hold\n", prID)
			}
			continue
		}
		head := fields["head"]
		if head == "" {
			if r.Out != nil {
				fmt.Fprintf(r.Out, "SKIP %s no-head\n", prID)
			}
			continue
		}
		// 6. Check already adopted at head
		adoptRec := adoptCmds[i].Val()
		if adoptRec["head"] == head {
			if r.Out != nil {
				fmt.Fprintf(r.Out, "SKIP %s adopted\n", prID)
			}
			continue
		}
		// 7. Check author
		author := fields["author"]
		if author == "" {
			if r.Out != nil {
				fmt.Fprintf(r.Out, "WAIT %s author MISSING\n", prID)
			}
			continue
		}
		// 8. Pick readers
		if census == nil {
			var err error
			census, err = r.census(ctx, policy)
			if err != nil {
				return handled, fmt.Errorf("pr-to-read: census: %w", err)
			}
		}
		want := census.required(fields)
		readers := census.pick(want, author)
		if len(readers) < want {
			if r.Out != nil {
				fmt.Fprintf(r.Out, "WAIT %s no readers\n", prID)
			}
			continue
		}

		// 9. Call CICut
		if r.CICut != nil {
			base := fields["base"]
			if base == "" {
				base = fields["base_sha"]
			}
			if err := r.CICut(ctx, CICut{
				Sprint: r.Sprint,
				Label:  "",
				Repo:   ident.repo,
				PR:     ident.n,
				Head:   head,
				Base:   base,
			}); err != nil {
				return handled, fmt.Errorf("pr-to-read: ci cut %s: %w", prID, err)
			}
		}

		// 10. Call ns_prtoread_adopt
		cutAt := time.Now().UTC().Format(time.RFC3339)
		priority := 0
		if p, err := strconv.Atoi(fields["priority"]); err == nil {
			priority = p
		}
		actor := r.Consumer
		if actor == "" {
			actor = "pr-to-read"
		}

		args := []any{
			r.Sprint, ident.repo, strconv.Itoa(ident.n), head,
			strings.Join(readers, ","), cutAt, actor, strconv.Itoa(len(readers)),
		}
		head12 := head
		if len(head12) > 12 {
			head12 = head12[:12]
		}
		for _, friend := range readers {
			req := task.PushRequest{
				Sprint: r.Sprint, ID: task.ReviewID(ident.repo, ident.n, head, friend),
				Kind:    task.KindReview,
				Title:   fmt.Sprintf("read %s#%d at %s | %s", ident.repo, ident.n, head12, ReadTitleSuffix),
				Effects: task.EffectsNone, Repo: ident.repo, PR: ident.n, Head: head, Ref: "",
				To: friend, Front: true, Priority: priority,
			}
			args = append(args, req.ID, friend, req.Title, strconv.Itoa(priority), task.PayloadSHA(req))
		}

		res, err := client.FCall(ctx, FunctionPRToReadAdopt, nil, args...).Result()
		if err != nil {
			return handled, fmt.Errorf("pr-to-read: adopt %s: %w", prID, err)
		}
		if parts, ok := res.([]any); ok && len(parts) > 0 && parts[0] == "NOOP" {
			if r.Out != nil {
				fmt.Fprintf(r.Out, "SKIP %s adopted\n", prID)
			}
			continue
		}

		for _, friend := range readers {
			census.load[friend]++
		}

		if r.Out != nil {
			fmt.Fprintf(r.Out, "ADOPT %s@%s cut=1 reads=%s\n", prID, head12, strings.Join(readers, ","))
		}
		handled++
	}

	return handled, nil
}

func hasOpenHold(m map[string]string) bool {
	for _, v := range m {
		var h struct {
			ReleasedBy string `json:"released_by"`
		}
		if err := json.Unmarshal([]byte(v), &h); err == nil {
			if h.ReleasedBy == "" {
				return true
			}
		}
	}
	return false
}

func (r *PRToReadRule) census(ctx context.Context, policy map[string]string) (*readerCensus, error) {
	client := r.Store.Client()
	names, err := client.SMembers(ctx, "friends").Result()
	if err != nil {
		return nil, fmt.Errorf("friends: %w", err)
	}
	sort.Strings(names)
	c := &readerCensus{load: map[string]int{}, readers: 1, security: 2}
	if n, err := strconv.Atoi(policy["readers"]); err == nil && n > 0 {
		c.readers = n
	}
	if n, err := strconv.Atoi(policy["readers_security"]); err == nil && n > 0 {
		c.security = n
	}
	c.secPrefixes = strings.FieldsFunc(policy["security_paths"], func(rn rune) bool { return rn == ' ' || rn == ',' })

	pipe := client.Pipeline()
	type row struct {
		beat    *redis.IntCmd
		desired *redis.SliceCmd
		start   *redis.IntCmd
		living  *redis.IntCmd
		open    *redis.IntCmd
	}
	rows := make([]row, len(names))
	for i, f := range names {
		rows[i] = row{
			beat:    pipe.Exists(ctx, "friend:"+f+":beat"),
			desired: pipe.HMGet(ctx, "friend:"+f+":desired", "slots", "paused"),
			start:   pipe.ZCard(ctx, "friend:"+f+":starting"),
			living:  pipe.ZCard(ctx, "friend:"+f+":living"),
			open:    pipe.ZCard(ctx, "s:"+r.Sprint+":open:"+f),
		}
	}
	if len(names) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
			return nil, fmt.Errorf("census: %w", err)
		}
	}
	for i, f := range names {
		ro := rows[i]
		if ro.beat.Val() == 0 || f == "jev" {
			continue
		}
		d := ro.desired.Val()
		slots, _ := strconv.Atoi(fmt.Sprint(d[0]))
		if slots <= 0 || fmt.Sprint(d[1]) == "1" {
			continue
		}
		c.friends = append(c.friends, f)
		c.load[f] = int(ro.start.Val() + ro.living.Val() + ro.open.Val())
	}
	return c, nil
}
