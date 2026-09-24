package consume

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/redis/go-redis/v9"
)

// Function names registered by internal/nsprint/fn/lua/review.lua.
const (
	FunctionPRHead                = "ns_pr_head"
	FunctionPRHeadChange          = "ns_pr_head_change"
	FunctionPREvaluate            = "ns_pr_evaluate"
	FunctionReadsShortDeleteEnded = "ns_reads_short_delete_ended"
	FunctionReadsShortDeleteLeft  = "ns_reads_short_delete_left"
	FunctionPRPass                = "ns_pr_pass"
)

// Remote is the ls-remote seam for checking heads on git remotes.
type Remote func(ctx context.Context, repo string) (map[int]string, error)

// GitRemote runs git ls-remote refs/pull/*/head against repo.
func GitRemote(ctx context.Context, repo string) (map[int]string, error) {
	target := repo
	if !strings.Contains(target, "://") && !strings.HasPrefix(target, "/") &&
		!strings.HasPrefix(target, ".") && !strings.HasPrefix(target, "git@") {
		target = "https://github.com/" + target + ".git"
	}
	cmd := exec.CommandContext(ctx, "git", "ls-remote", target, "refs/pull/*/head")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-remote %s: %w", target, err)
	}
	return ParseLSRemotePullHeads(string(out))
}

// ParseLSRemotePullHeads parses the output of git ls-remote 'refs/pull/*/head'.
func ParseLSRemotePullHeads(out string) (map[int]string, error) {
	heads := make(map[int]string)
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		sha := fields[0]
		ref := fields[1]
		if strings.HasPrefix(ref, "refs/pull/") && strings.HasSuffix(ref, "/head") {
			numStr := strings.TrimSuffix(strings.TrimPrefix(ref, "refs/pull/"), "/head")
			if n, err := strconv.Atoi(numStr); err == nil {
				heads[n] = sha
			}
		}
	}
	return heads, nil
}

// PRRead satisfies consume.PRToRead under lease:route:<S>.
type PRRead struct {
	Store    *store.Store
	Sprint   string
	Consumer string        // the stream consumer
	Instance string        // the lease instance (the fence)
	Actor    string        // receipt actor
	Remote   Remote        // the ls-remote seam
	Every    time.Duration // ls-remote cadence, default 10 s
	Block    time.Duration // stream read block; 0 means 1 s

	Out        io.Writer
	LastReason map[string]string

	mu         sync.Mutex
	lastRemote map[string]time.Time
}

func (p *PRRead) check() error {
	if p == nil || p.Store == nil || p.Sprint == "" {
		return errors.New("pr-to-read: store and sprint are required")
	}
	if p.Instance == "" {
		return errors.New("pr-to-read: instance is required")
	}
	if p.Consumer == "" {
		p.Consumer = p.Instance
	}
	if p.Actor == "" {
		p.Actor = "pr-to-read"
	}
	if p.Remote == nil {
		p.Remote = GitRemote
	}
	if p.Every <= 0 {
		p.Every = 10 * time.Second
	}
	if p.Block == 0 {
		p.Block = time.Second
	}
	p.mu.Lock()
	if p.LastReason == nil {
		p.LastReason = make(map[string]string)
	}
	if p.lastRemote == nil {
		p.lastRemote = make(map[string]time.Time)
	}
	p.mu.Unlock()
	return nil
}

// Reason returns the last evaluation reason recorded for card label.
func (p *PRRead) Reason(label string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.LastReason == nil {
		return ""
	}
	return p.LastReason[label]
}

func (p *PRRead) out() io.Writer {
	if p.Out != nil {
		return p.Out
	}
	return io.Discard
}

// Start creates the consumer group pr-to-read on s:<S>:log and reclaims
// pending entries.
func (p *PRRead) Start(ctx context.Context) error {
	if err := p.check(); err != nil {
		return err
	}
	g := groupLoop{store: p.Store, sprint: p.Sprint, group: RulePRToRead, consumer: p.Consumer}
	return g.start(ctx)
}

func checkLeaseReply(reply []any) error {
	if len(reply) > 0 && reply[0] == "LEASE" {
		holder := ""
		if len(reply) > 1 {
			holder = fmt.Sprint(reply[1])
		}
		return fmt.Errorf("%w held by %s", ErrLeaseLost, holder)
	}
	return nil
}

func head12(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// Pass executes one evaluation pass of pr-to-read.
func (p *PRRead) Pass(ctx context.Context) (int, error) {
	if err := p.check(); err != nil {
		return 0, err
	}
	startAt := time.Now()
	client := p.Store.Client()
	var passErr error
	nProcessed := 0

	defer func() {
		tookMs := time.Since(startAt).Milliseconds()
		errStr := ""
		if passErr != nil {
			errStr = passErr.Error()
		}
		reply, err := client.FCall(ctx, FunctionPRPass, nil, p.Sprint, p.Instance, "pr-to-read",
			strconv.FormatInt(tookMs, 10), strconv.Itoa(nProcessed), errStr).Slice()
		if err == nil {
			_ = checkLeaseReply(reply)
		}
	}()

	// Read policy
	policy, err := client.HGetAll(ctx, "s:"+p.Sprint+":policy").Result()
	if err != nil {
		passErr = fmt.Errorf("pr-to-read: policy: %w", err)
		return 0, passErr
	}

	// Read tracked PRs
	trackedPRs, err := client.SMembers(ctx, "s:"+p.Sprint+":prs").Result()
	if err != nil {
		passErr = fmt.Errorf("pr-to-read: prs: %w", err)
		return 0, passErr
	}
	sort.Strings(trackedPRs)
	trackedMap := make(map[string]bool, len(trackedPRs))
	for _, prID := range trackedPRs {
		trackedMap[prID] = true
	}

	// 1. One git ls-remote per repo in s:<S>:policy repos, filtered to s:<S>:prs,
	// at most once per Every.
	reposStr := policy["repos"]
	if reposStr != "" {
		repos := strings.FieldsFunc(reposStr, func(r rune) bool { return r == ' ' || r == ',' })
		now := time.Now()
		for _, repo := range repos {
			p.mu.Lock()
			last := p.lastRemote[repo]
			due := now.Sub(last) >= p.Every
			if due {
				p.lastRemote[repo] = now
			}
			p.mu.Unlock()

			if due && p.Remote != nil {
				heads, err := p.Remote(ctx, repo)
				if err != nil {
					passErr = fmt.Errorf("pr-to-read: remote %s: %w", repo, err)
					return 0, passErr
				}
				for prNum, sha := range heads {
					prID := fmt.Sprintf("%s#%d", repo, prNum)
					if !trackedMap[prID] {
						continue
					}
					// Check current head in PR record
					curHead, err := client.HGet(ctx, fmt.Sprintf("s:%s:pr:%s:%d", p.Sprint, repo, prNum), "head").Result()
					if err != nil && err != redis.Nil {
						passErr = fmt.Errorf("pr-to-read: pr %s head: %w", prID, err)
						return 0, passErr
					}
					if curHead != sha {
						reply, err := client.FCall(ctx, FunctionPRHead, nil, p.Sprint, p.Instance,
							repo, strconv.Itoa(prNum), sha, "ls-remote").Slice()
						if err != nil {
							passErr = fmt.Errorf("pr-to-read: ns_pr_head %s: %w", prID, err)
							return 0, passErr
						}
						if err := checkLeaseReply(reply); err != nil {
							passErr = err
							return 0, passErr
						}
						nProcessed++
					}
				}
			}
		}
	}

	// 2. One pipelined batch:
	// Reads each tracked PR's record head and state.
	// Reads repo, pr and state for every card in s:<S>:idx:card:review-ready.
	// Reads one HMGET of s:<S>:unresolved over computed field names <repo>#<n>:reads-short:<head12>.
	type prInfo struct {
		id    string
		repo  string
		prNum int
		head  string
		state string
	}
	prInfos := make([]prInfo, 0, len(trackedPRs))
	for _, prID := range trackedPRs {
		parts := strings.SplitN(prID, "#", 2)
		if len(parts) == 2 {
			prNum, _ := strconv.Atoi(parts[1])
			prInfos = append(prInfos, prInfo{id: prID, repo: parts[0], prNum: prNum})
		}
	}

	pipe := client.Pipeline()
	prHeadCmds := make([]*redis.SliceCmd, len(prInfos))
	for i, info := range prInfos {
		prHeadCmds[i] = pipe.HMGet(ctx, fmt.Sprintf("s:%s:pr:%s:%d", p.Sprint, info.repo, info.prNum), "head", "state")
	}

	cardLabelsCmd := pipe.SMembers(ctx, "s:"+p.Sprint+":idx:card:review-ready")
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		passErr = fmt.Errorf("pr-to-read: pipeline batch: %w", err)
		return 0, passErr
	}

	for i := range prInfos {
		vals := prHeadCmds[i].Val()
		if len(vals) > 0 && vals[0] != nil {
			prInfos[i].head = fmt.Sprint(vals[0])
		}
		if len(vals) > 1 && vals[1] != nil {
			prInfos[i].state = fmt.Sprint(vals[1])
		}
	}

	cardLabels := cardLabelsCmd.Val()
	sort.Strings(cardLabels)

	type cardInfo struct {
		label string
		repo  string
		pr    string
		state string
	}
	cardInfos := make([]cardInfo, len(cardLabels))
	if len(cardLabels) > 0 {
		pipeCard := client.Pipeline()
		cardCmds := make([]*redis.SliceCmd, len(cardLabels))
		for i, label := range cardLabels {
			cardCmds[i] = pipeCard.HMGet(ctx, "s:"+p.Sprint+":card:"+label, "repo", "pr", "state")
		}
		if _, err := pipeCard.Exec(ctx); err != nil && err != redis.Nil {
			passErr = fmt.Errorf("pr-to-read: card batch: %w", err)
			return 0, passErr
		}
		for i, label := range cardLabels {
			vals := cardCmds[i].Val()
			repo, pr, state := "", "", ""
			if len(vals) > 0 && vals[0] != nil {
				repo = fmt.Sprint(vals[0])
			}
			if len(vals) > 1 && vals[1] != nil {
				pr = fmt.Sprint(vals[1])
			}
			if len(vals) > 2 && vals[2] != nil {
				state = fmt.Sprint(vals[2])
			}
			cardInfos[i] = cardInfo{label: label, repo: repo, pr: pr, state: state}
		}
	}

	// Review-ready cards are cards whose state field is "review-ready"
	reviewReadyCardPRs := make(map[string]bool)
	for _, c := range cardInfos {
		if c.state == "review-ready" && c.repo != "" && c.pr != "" {
			reviewReadyCardPRs[c.repo+"#"+c.pr] = true
		}
	}

	// HMGET of s:<S>:unresolved over computed field names <repo>#<n>:reads-short:<head12>
	shortFields := make([]string, len(prInfos))
	for i, info := range prInfos {
		shortFields[i] = fmt.Sprintf("%s:reads-short:%s", info.id, head12(info.head))
	}
	var unresVals []any
	if len(shortFields) > 0 {
		var err error
		unresVals, err = client.HMGet(ctx, "s:"+p.Sprint+":unresolved", shortFields...).Result()
		if err != nil && err != redis.Nil {
			passErr = fmt.Errorf("pr-to-read: unres batch: %w", err)
			return 0, passErr
		}
	}

	for i, info := range prInfos {
		if i >= len(unresVals) || unresVals[i] == nil {
			continue
		}
		field := shortFields[i]
		switch info.state {
		case "landed", "dropped", "closed":
			reply, err := client.FCall(ctx, FunctionReadsShortDeleteEnded, nil, p.Sprint, p.Instance, field).Slice()
			if err != nil {
				passErr = fmt.Errorf("pr-to-read: delete ended %s: %w", field, err)
				return 0, passErr
			}
			if err := checkLeaseReply(reply); err != nil {
				passErr = err
				return 0, passErr
			}
		case "opened", "reading", "landable", "landing":
			if !reviewReadyCardPRs[info.id] {
				reply, err := client.FCall(ctx, FunctionReadsShortDeleteLeft, nil, p.Sprint, p.Instance, field).Slice()
				if err != nil {
					passErr = fmt.Errorf("pr-to-read: delete left %s: %w", field, err)
					return 0, passErr
				}
				if err := checkLeaseReply(reply); err != nil {
					passErr = err
					return 0, passErr
				}
			}
		}
	}

	// 3. Read pr head events in group pr-to-read on s:<S>:log, one Function call each.
	// On a head change A->B cancel A's open reviews and push ReviewID(repo, n, B, f)
	// per prior reader f at B. Skip prior reader who holds an open hold on the PR.
	events, err := p.readHeadEvents(ctx)
	if err != nil {
		passErr = fmt.Errorf("pr-to-read: read events: %w", err)
		return 0, passErr
	}

	friendsList, err := client.SMembers(ctx, "friends").Result()
	if err != nil {
		passErr = fmt.Errorf("pr-to-read: friends: %w", err)
		return 0, passErr
	}
	sort.Strings(friendsList)

	for _, e := range events {
		if e.prev == "" || e.prev == e.head {
			// Not a head change, ack
			_ = client.HSet(ctx, "s:"+p.Sprint+":idem", "pr-to-read:"+e.id, "NOOP").Err()
			_ = client.XAck(ctx, "s:"+p.Sprint+":log", RulePRToRead, e.id).Err()
			continue
		}

		prNum, _ := strconv.Atoi(e.pr)
		// Check prior readers at prev head
		pipeTasks := client.Pipeline()
		existsCmds := make([]*redis.IntCmd, len(friendsList))
		for i, f := range friendsList {
			existsCmds[i] = pipeTasks.Exists(ctx, fmt.Sprintf("s:%s:task:%s", p.Sprint, task.ReviewID(e.repo, prNum, e.prev, f)))
		}
		holdCmd := pipeTasks.HGetAll(ctx, fmt.Sprintf("s:%s:hold:%s:%s", p.Sprint, e.repo, e.pr))
		prAuthorCmd := pipeTasks.HGet(ctx, fmt.Sprintf("s:%s:pr:%s:%s", p.Sprint, e.repo, e.pr), "author")
		if _, err := pipeTasks.Exec(ctx); err != nil && err != redis.Nil {
			passErr = fmt.Errorf("pr-to-read: prior readers exec: %w", err)
			return 0, passErr
		}

		prAuthor := prAuthorCmd.Val()
		openHoldHolders := make(map[string]bool)
		for _, hJSON := range holdCmd.Val() {
			var h struct {
				Holder     string `json:"holder"`
				ReleasedBy string `json:"released_by"`
			}
			if json.Unmarshal([]byte(hJSON), &h) == nil && h.ReleasedBy == "" && h.Holder != "" {
				openHoldHolders[h.Holder] = true
			}
		}

		var cancels []string // [friend, tid, ...]
		var pushes []string  // [friend, tid, priority, payload_sha, ...]
		for i, f := range friendsList {
			if existsCmds[i].Val() > 0 {
				oldTID := task.ReviewID(e.repo, prNum, e.prev, f)
				cancels = append(cancels, f, oldTID)
				if f == prAuthor || f == "jev" || openHoldHolders[f] {
					continue
				}
				newTID := task.ReviewID(e.repo, prNum, e.head, f)
				req := task.PushRequest{
					Sprint: p.Sprint, ID: newTID, Kind: task.KindReview,
					Title:   fmt.Sprintf("review %s#%d at %s", e.repo, prNum, e.head),
					Effects: task.EffectsNone, Repo: e.repo, PR: prNum, Head: e.head,
					To: f, Front: true, Priority: 5,
				}
				pushes = append(pushes, f, newTID, "5", task.PayloadSHA(req))
			}
		}

		args := []any{
			p.Sprint, p.Instance, e.id, e.repo, e.pr, e.head, e.prev, p.Actor,
			strconv.Itoa(len(cancels) / 2),
		}
		for _, c := range cancels {
			args = append(args, c)
		}
		args = append(args, strconv.Itoa(len(pushes)/4))
		for _, pu := range pushes {
			args = append(args, pu)
		}

		reply, err := client.FCall(ctx, FunctionPRHeadChange, nil, args...).Slice()
		if err != nil {
			passErr = fmt.Errorf("pr-to-read: head change %s: %w", e.id, err)
			return 0, passErr
		}
		if err := checkLeaseReply(reply); err != nil {
			passErr = err
			return 0, passErr
		}
		nProcessed++
	}

	// 4. Move cards review-ready -> land-ready
	cardsToEval, err := client.SMembers(ctx, "s:"+p.Sprint+":idx:card:review-ready").Result()
	if err != nil {
		passErr = fmt.Errorf("pr-to-read: review-ready cards: %w", err)
		return 0, passErr
	}
	sort.Strings(cardsToEval)

	for _, label := range cardsToEval {
		cardMap, err := client.HGetAll(ctx, "s:"+p.Sprint+":card:"+label).Result()
		if err != nil || len(cardMap) == 0 {
			continue
		}
		if cardMap["state"] != "review-ready" {
			continue
		}
		repo := cardMap["repo"]
		pr := cardMap["pr"]
		cardAuthor := cardMap["author"]
		attempt := cardMap["attempt"]

		prMap, err := client.HGetAll(ctx, fmt.Sprintf("s:%s:pr:%s:%s", p.Sprint, repo, pr)).Result()
		if err != nil {
			continue
		}
		prState := prMap["state"]
		if prState == "landed" || prState == "dropped" || prState == "closed" {
			continue
		}
		head := prMap["head"]
		if head == "" {
			head = cardMap["head"]
		}
		prAuthor := prMap["author"]
		landBarStr := prMap["land_bar"]

		ciMap, err := client.HGetAll(ctx, fmt.Sprintf("ci:%s:%s", repo, head)).Result()
		if err != nil {
			continue
		}
		ciVerdict := ciMap["verdict"]

		dispMap, err := client.HGetAll(ctx, fmt.Sprintf("s:%s:disp:%s:%s", p.Sprint, repo, pr)).Result()
		if err != nil {
			continue
		}

		holdMap, err := client.HGetAll(ctx, fmt.Sprintf("s:%s:hold:%s:%s", p.Sprint, repo, pr)).Result()
		if err != nil {
			continue
		}

		// Evaluate
		N := RequiredReads(policy, cardMap)
		landBar, barErr := strconv.Atoi(landBarStr)

		// Eligible pool E = friends minus prAuthor minus cardAuthor minus jev
		eligible := make(map[string]bool)
		for _, f := range friendsList {
			if f != prAuthor && f != cardAuthor && f != "jev" {
				eligible[f] = true
			}
		}
		short := len(eligible) < N

		// Count qualifying reads
		countReads := 0
		if barErr == nil {
			countedFriends := make(map[string]bool)
			for field, dispVal := range dispMap {
				// field is <friend>@<head>
				atParts := strings.SplitN(field, "@", 2)
				if len(atParts) != 2 || atParts[1] != head {
					continue
				}
				friend := atParts[0]
				if !eligible[friend] || countedFriends[friend] {
					continue
				}
				tokens := strings.Fields(dispVal)
				if len(tokens) >= 2 && tokens[0] == "APPROVE" {
					if score, sErr := strconv.Atoi(tokens[1]); sErr == nil && score >= landBar {
						countedFriends[friend] = true
						countReads++
					}
				}
			}
		}

		// Check open holds
		hasOpenHold := false
		openHoldKey := ""
		openHolder := ""
		openHoldHead := ""
		var holdKeys []string
		for hk := range holdMap {
			holdKeys = append(holdKeys, hk)
		}
		sort.Strings(holdKeys)
		for _, hk := range holdKeys {
			var h struct {
				Holder     string `json:"holder"`
				Head       string `json:"head"`
				ReleasedBy string `json:"released_by"`
			}
			if json.Unmarshal([]byte(holdMap[hk]), &h) == nil && h.ReleasedBy == "" {
				hasOpenHold = true
				openHoldKey = hk
				openHolder = h.Holder
				openHoldHead = h.Head
				break
			}
		}

		reason := ""
		action := "noop"

		switch {
		case ciVerdict != "OK":
			reason = "ci MISSING"
			if short {
				action = "short"
			} else {
				action = "recovered"
			}
		case prAuthor == "":
			reason = "reads MISSING author"
			if short {
				action = "short"
			} else {
				action = "recovered"
			}
		case barErr != nil || landBarStr == "":
			reason = "reads MISSING land_bar"
			if short {
				action = "short"
			} else {
				action = "recovered"
			}
		case short:
			reason = fmt.Sprintf("reads %d/%d; eligible %d < %d", countReads, N, len(eligible), N)
			action = "short"
		case countReads < N:
			reason = fmt.Sprintf("reads %d/%d", countReads, N)
			action = "recovered"
		case hasOpenHold:
			reason = fmt.Sprintf("hold %s open (%s @%s)", openHoldKey, openHolder, head12(openHoldHead))
			action = "recovered"
		default:
			reason = fmt.Sprintf("reads %d/%d", countReads, N)
			action = "land-ready"
		}

		p.mu.Lock()
		p.LastReason[label] = reason
		p.mu.Unlock()

		if action == "land-ready" {
			fmt.Fprintf(p.out(), "LAND-READY card=%s pr=%s#%s head=%s\n", label, repo, pr, head)
		} else {
			fmt.Fprintf(p.out(), "REVIEW-READY card=%s pr=%s#%s reason=%s\n", label, repo, pr, reason)
		}

		passID := fmt.Sprintf("%d-0", time.Now().UnixMilli())
		reply, err := client.FCall(ctx, FunctionPREvaluate, nil,
			p.Sprint, p.Instance, label, repo, pr, head, action, passID, p.Actor, reason, attempt).Slice()
		if err != nil {
			passErr = fmt.Errorf("pr-to-read: evaluate %s: %w", label, err)
			return 0, passErr
		}
		if err := checkLeaseReply(reply); err != nil {
			passErr = err
			return 0, passErr
		}
		if action == "land-ready" {
			nProcessed++
		}
	}

	return nProcessed, nil
}

type headEvent struct {
	id     string
	repo   string
	pr     string
	head   string
	prev   string
	source string
}

func (p *PRRead) readHeadEvents(ctx context.Context) ([]headEvent, error) {
	client := p.Store.Client()
	logKey := "s:" + p.Sprint + ":log"

	// Read pending first
	var streams []redis.XStream
	res, err := client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: RulePRToRead, Consumer: p.Consumer,
		Streams: []string{logKey, "0"}, Count: 100,
	}).Result()
	if err == nil && len(res) > 0 && len(res[0].Messages) > 0 {
		streams = res
	} else {
		block := p.Block
		if block <= 0 {
			block = -1
		}
		res, err := client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group: RulePRToRead, Consumer: p.Consumer,
			Streams: []string{logKey, ">"}, Count: 100, Block: block,
		}).Result()
		if err != nil && err != redis.Nil {
			return nil, err
		}
		streams = res
	}

	var events []headEvent
	for _, stream := range streams {
		for _, msg := range stream.Messages {
			if fmt.Sprint(msg.Values["kind"]) == "pr head" {
				events = append(events, headEvent{
					id:     msg.ID,
					repo:   fmt.Sprint(msg.Values["repo"]),
					pr:     fmt.Sprint(msg.Values["pr"]),
					head:   fmt.Sprint(msg.Values["head"]),
					prev:   fmt.Sprint(msg.Values["prev"]),
					source: fmt.Sprint(msg.Values["source"]),
				})
			} else {
				// Ack non-head events
				_ = client.XAck(ctx, logKey, RulePRToRead, msg.ID).Err()
			}
		}
	}
	return events, nil
}

// Once runs pr-to-read under lease:route:<S> for a single pass.
func (p *PRRead) Once(ctx context.Context) error {
	if p == nil || p.Store == nil || p.Sprint == "" {
		return errors.New("pr-to-read: store and sprint are required")
	}
	if p.Instance == "" {
		inst, err := NewInstance()
		if err != nil {
			return err
		}
		p.Instance = inst
	}
	if p.Consumer == "" {
		p.Consumer = p.Instance
	}
	if p.Actor == "" {
		p.Actor = "pr-to-read"
	}
	origBlock := p.Block
	if p.Block == 0 {
		p.Block = -1
	}
	defer func() { p.Block = origBlock }()
	token, err := randomHex(16)
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	ttl := 6 * time.Second
	renew := 2 * time.Second

	client := p.Store.Client()
	reply, err := client.FCall(ctx, FunctionRouteLeaseTake, nil, p.Sprint, p.Instance, token, host,
		strconv.FormatInt(ttl.Milliseconds(), 10)).Slice()
	if err != nil {
		return fmt.Errorf("pr-to-read: take %s: %w", LeaseKey(p.Sprint), err)
	}
	if len(reply) > 0 && reply[0] == "HELD" {
		held := &LeaseHeldError{Sprint: p.Sprint}
		if len(reply) > 1 {
			held.Holder = fmt.Sprint(reply[1])
		}
		if len(reply) > 2 {
			held.At = fmt.Sprint(reply[2])
		}
		return held
	}

	renewCtx, stopRenew := context.WithCancel(ctx)
	defer stopRenew()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(renew)
		defer ticker.Stop()
		for {
			select {
			case <-renewCtx.Done():
				return
			case <-ticker.C:
			}
			_ = client.FCall(renewCtx, FunctionRouteLeaseRenew, nil, p.Sprint, p.Instance, token,
				strconv.FormatInt(ttl.Milliseconds(), 10)).Err()
		}
	}()

	defer func() {
		stopRenew()
		wg.Wait()
		_ = client.FCall(context.WithoutCancel(ctx), FunctionRouteLeaseRelease, nil, p.Sprint, p.Instance, token).Err()
	}()

	if err := p.Start(ctx); err != nil {
		return err
	}
	_, err = p.Pass(ctx)
	return err
}
