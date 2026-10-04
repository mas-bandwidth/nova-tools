package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// promotionReview binds coordinator conflict review to an exact candidate and
// inventory. Card acceptance remains the independent per-card review gate.
type promotionReview struct {
	Epoch             uint64              `json:"epoch"`
	Repo              string              `json:"repo"`
	Target            string              `json:"target"`
	Candidate         string              `json:"candidate_tip"`
	Inventory         string              `json:"inventory_sha256"`
	Reviewer          string              `json:"reviewer"`
	Disposition       string              `json:"disposition"`
	ReviewedAt        time.Time           `json:"reviewed_at"`
	CrossStreamReview bool                `json:"cross_stream_review"`
	ConflictsResolved bool                `json:"conflicts_resolved"`
	Entries           []sprint.PinnedCard `json:"entries"`
}

// promotionInventory includes every open writer's exact scope and accepted
// pin, so a coordinator review cannot silently omit another work stream.
func promotionInventory(s *sprint.Snapshot) string {
	var lines []string
	for _, stream := range s.Streams() {
		for _, state := range []string{sprint.Working, sprint.Review, sprint.Merging} {
			for _, c := range s.Work.Cell(stream, state) {
				cb := swarm.ReadCardBase([]byte(c.F("brief")))
				paths := swarm.CardPaths([]byte(c.F("brief")))
				slices.Sort(paths)
				lines = append(lines, strings.Join([]string{stream, c.ID, c.F("attempt"), c.F("head"), cb.Repo, cb.Ref, strings.Join(paths, ",")}, "\x00"))
			}
		}
	}
	slices.Sort(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// promotionPins is the compatible ordered prefix. It never skips a conflicting
// repo or a card not yet staged (docs/SPEC-SPRINT.md section 7).
func promotionPins(s *sprint.Snapshot, stream string) ([]landCard, string) {
	ctl := s.StreamCtl(stream)
	if ctl == nil {
		return nil, "no such stream; run: nova-sprint where"
	}
	if ctl.F("state") == sprint.StreamStopped {
		return nil, "stream is stopped; run: nova-sprint inbox"
	}
	var pins []landCard
	for _, c := range landQueue(s, stream) {
		pr := s.Work.Placed(c.ID)
		if pr == nil || pr.Col != sprint.Merging || pr.F("staged_tip") == "" || pr.F("staged_head_pin") != pr.F("head") || pr.F("staged_attempt") != pr.F("attempt") || pr.F("staged_returns") != pr.F("returns") {
			break
		}
		repo := pr.F("staged_repo")
		if len(pins) > 0 && !sameRepo(repo, pins[0].repo) {
			break
		}
		pins = append(pins, landCard{id: c.ID, head: pr.F("head"), attempt: pr.F("attempt"), repo: repo, base: "dev", primary: pr, paths: swarm.CardPaths([]byte(pr.F("brief"))), brief: pr.F("brief")})
	}
	if len(pins) == 0 {
		return nil, "no compatible staged prefix; run: nova-sprint land --stream " + stream + " --check <command>"
	}
	return pins, ""
}

func samePromotionPins(a []sprint.PinnedCard, b []landCard) bool {
	if len(a) != len(b) {
		return false
	}
	for i, p := range b {
		if a[i].ID != p.id || a[i].Head != p.head || a[i].Attempt != p.attempt {
			return false
		}
	}
	return true
}

// promotionGH runs a bounded read or normal queue mutation; it never bypasses
// branch protection, merges directly, or enables automatic PR merging.
func (l *lander) promotionGH(ctx context.Context, args ...string) ([]byte, error) {
	b := subproc.Prepare(ctx, 2*time.Minute, "gh", args...)
	defer b.Cancel()
	b.Cmd.Env = l.a.gitEnv
	raw, err := b.Cmd.CombinedOutput()
	return raw, b.Wrap("promotion GitHub", err)
}

// cmdPromote verifies a reviewed candidate, queues its exact PR head, and
// records delivery only after a fresh dev fetch proves ancestry. A queued PR
// is pending, never landed. Replaying after merge verifies and reports once.
func (a *app) cmdPromote(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("promote")
	stream := fs.String("stream", "", "the staged stream prefix to promote")
	dir := fs.String("repo-dir", "", "the existing clone whose origin receives delivery")
	candidate := fs.String("candidate", "", "the full40 reviewed PR candidate, already built from dev")
	pr := fs.Int("pr", 0, "the reviewed GitHub PR number targeting dev")
	reviewPath := fs.String("review-record", "", "the JSON coordinator review of exact candidate and cross-stream inventory")
	check := fs.String("check", "", "the substantive green candidate check command (required; bounded to 30m)")
	prepare := fs.Bool("prepare", false, "build and check the exact dev candidate locally; print review template; report conflict/red escalation without delivery")
	dry := fs.Bool("dry-run", false, "read the store and print exact pins/inventory; no git, GitHub or store writes")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "promote", err.Error())
	}
	if *stream == "" || len(pos) > 0 {
		return refuse(stderr, "promote", "wants --stream <s>; run: nova-sprint promote --stream s1 --dry-run")
	}
	a.serial.Lock()
	st, err := a.store(*c)
	a.serial.Unlock()
	if err != nil {
		return refuse(stderr, "promote", err.Error())
	}
	a.serial.Lock()
	s, err := st.Load(context.Background(), []string{sprint.Work, sprint.Merge}, nil)
	a.serial.Unlock()
	if err != nil {
		return a.readFailed("promote", err, stderr)
	}
	if c.epoch >= 0 && uint64(c.epoch) != s.Epoch {
		return refuse(stderr, "promote", "the requested epoch is stale; run: nova-sprint where")
	}
	var review promotionReview
	var reviewRaw []byte
	if !*dry && *reviewPath != "" {
		reviewRaw, err = os.ReadFile(*reviewPath)
		if err != nil {
			return refuse(stderr, "promote", "review record unreadable: "+oneline.Err(err)+"; run: nova-sprint promote --stream "+*stream+" --dry-run")
		}
		if err = json.Unmarshal(reviewRaw, &review); err != nil {
			return refuse(stderr, "promote", "invalid review record: "+oneline.Err(err)+"; run: nova-sprint promote --stream "+*stream+" --dry-run")
		}
	}
	pins, why := promotionPins(s, *stream)
	replayed := false
	if len(review.Entries) > 0 {
		pins, replayed, why = reviewedPromotionPins(s, *stream, review, reviewRaw)
	}
	if why != "" && !(*dry && strings.HasPrefix(why, "no compatible staged prefix;")) {
		return refuse(stderr, "promote", why)
	}
	inventory := promotionInventory(s)
	repo := ""
	if len(pins) > 0 {
		repo = pins[0].repo
	}
	output := func(status string, exit int, note string) int {
		v := map[string]any{"verb": "promote", "status": status, "exit": exit, "stream": *stream, "target": "dev", "candidate_tip": *candidate, "inventory_sha256": inventory, "cards": len(pins), "dry_run": *dry, "note": note, "pins": promotionEntries(pins), "review_template": promotionReview{Epoch: s.Epoch, Repo: repo, Target: "dev", Candidate: *candidate, Inventory: inventory, Entries: promotionEntries(pins)}}
		if c.json {
			raw, e := json.Marshal(v)
			if e != nil {
				return refuse(stderr, "promote", e.Error())
			}
			fmt.Fprintln(stdout, string(raw))
		} else {
			fmt.Fprintf(stdout, "PROMOTE %s stream=%s target=dev candidate=%s cards=%d inventory_sha256=%s\n", strings.ToUpper(status), oneline.Field(*stream), dashed(*candidate), len(pins), inventory)
			if note != "" {
				fmt.Fprintln(stdout, "NOTE "+oneline.Escape(note))
			}
		}
		return exit
	}
	if *dry {
		if len(pins) == 0 {
			return output("ok", 0, why)
		}
		return output("ok", 0, "inspection only; review the compatible prefix and all cross-stream scopes, then supply exact candidate, PR, review record and check")
	}
	if *prepare {
		if *dir == "" || strings.TrimSpace(*check) == "" {
			return refuse(stderr, "promote", "prepare wants --repo-dir <clone> --check <command>; run: nova-sprint promote --stream "+*stream+" --dry-run")
		}
		l := &lander{a: a, c: *c, st: st, epoch: s.Epoch, repoDir: *dir, check: *check, diffs: map[string]string{}}
		tip, why := l.preparePromotion(context.Background(), *dir, *stream, pins)
		if why != "" {
			return refuse(stderr, "promote", why+"; run: nova-sprint inbox")
		}
		*candidate = tip
		return output("ok", 0, "checked local candidate only; coordinator must review cross-stream scopes/exact diff and obtain ordinary reviewed dev PR before promotion")
	}
	if *dir == "" || !shaRE.MatchString(*candidate) || len(*candidate) != 40 || *pr <= 0 || *reviewPath == "" || strings.TrimSpace(*check) == "" {
		return refuse(stderr, "promote", "wants --repo-dir <clone> --candidate <full40> --pr <number> --review-record <json> --check <command>; run: nova-sprint promote --stream "+*stream+" --dry-run")
	}
	if review.Epoch != s.Epoch || review.Target != "dev" || review.Candidate != *candidate || len(review.Inventory) != 64 || review.Disposition != "approve" || review.Reviewer == "" || review.ReviewedAt.IsZero() || !review.CrossStreamReview || !review.ConflictsResolved || !sameRepo(review.Repo, pins[0].repo) || !samePromotionPins(review.Entries, pins) {
		return refuse(stderr, "promote", "exact candidate/pins and holistic cross-stream review are missing or stale; coordinator review/escalation required; run: nova-sprint promote --stream "+*stream+" --dry-run")
	}
	l := &lander{a: a, c: *c, st: st, epoch: s.Epoch, repoDir: *dir, check: *check}
	ctx := context.Background()
	if why = l.originIs(ctx, *dir, pins[0].repo); why != "" {
		return refuse(stderr, "promote", why)
	}
	if _, err = l.git(ctx, *dir, "fetch", "origin", "refs/heads/dev:refs/remotes/origin/dev"); err != nil {
		return refuse(stderr, "promote", firstLine("", err)+"; run: nova-sprint promote --stream "+*stream+" --dry-run")
	}
	for _, pin := range pins {
		if _, err = l.git(ctx, *dir, "merge-base", "--is-ancestor", pin.head, *candidate); err != nil {
			return refuse(stderr, "promote", "candidate omits "+pin.id+"; rebuild reviewed prefix; run: nova-sprint promote --stream "+*stream+" --dry-run")
		}
	}
	if replayed {
		devTip, e := l.git(ctx, *dir, "rev-parse", "refs/remotes/origin/dev^{commit}")
		if e != nil {
			return refuse(stderr, "promote", firstLine("", e)+"; run: nova-sprint promote --stream "+*stream+" --dry-run")
		}
		for _, head := range append([]string{*candidate}, promotionHeads(pins)...) {
			if _, e = l.git(ctx, *dir, "merge-base", "--is-ancestor", head, devTip); e != nil {
				return refuse(stderr, "promote", "stored delivery is no longer in fresh dev; coordinator escalation required; run: nova-sprint promote --stream "+*stream+" --dry-run")
			}
		}
		l.finalizeDelivery(landBatch{Stream: *stream, Repo: pins[0].repo, Base: "dev", Tip: devTip, Dir: *dir}, *stream, pins)
		l.scoreAll(ctx)
		return output("ok", 0, "same exact delivery receipt already recorded; fresh dev ancestry reverified; delivery effects retried without duplicate store transition")
	}
	return l.promoteCandidate(ctx, *dir, *stream, *candidate, *pr, reviewRaw, pins, review.Inventory, output, stderr)
}

// promoteCandidate checks the exact commit, verifies normal GitHub queue
// provenance, and fetches dev again before the fenced store report (Sprint §7).
func (l *lander) promoteCandidate(ctx context.Context, dir, stream, candidate string, pr int, reviewRaw []byte, pins []landCard, inventory string, output func(string, int, string) int, stderr io.Writer) int {
	fail := func(why string) int {
		return refuse(stderr, "promote", why+"; run: nova-sprint promote --stream "+stream+" --dry-run")
	}
	if out, err := l.git(ctx, dir, "status", "--porcelain", "--untracked-files=no"); err != nil || out != "" {
		return fail("candidate checkout is not clean: " + firstLine(out, err))
	}
	if _, err := l.git(ctx, dir, "checkout", "--detach", candidate); err != nil {
		return fail(firstLine("", err))
	}
	if why, _ := l.runCheck(ctx, dir); why != "" {
		return fail(why)
	}
	head, err := l.git(ctx, dir, "rev-parse", "HEAD")
	if err != nil || head != candidate {
		return fail("check changed candidate HEAD; its green result cannot certify the reviewed commit")
	}
	if out, err := l.git(ctx, dir, "status", "--porcelain", "--untracked-files=no"); err != nil || out != "" {
		return fail("check changed tracked candidate content: " + firstLine(out, err))
	}
	// GitHub owns queue/protection checks. Local CI is exact-tip evidence, not
	// authority to bypass a required review or an ordinary queue rejection.
	repo := strings.TrimPrefix(normRepo(pins[0].repo), "github.com/")
	var view struct {
		ID     string                                       `json:"id"`
		Head   string                                       `json:"headRefOid"`
		Base   string                                       `json:"baseRefName"`
		State  string                                       `json:"state"`
		Review string                                       `json:"reviewDecision"`
		URL    string                                       `json:"url"`
		Checks []struct{ Status, Conclusion, State string } `json:"statusCheckRollup"`
	}
	raw, err := l.promotionGH(ctx, "pr", "view", strconv.Itoa(pr), "--repo", repo, "--json", "id,headRefOid,baseRefName,state,reviewDecision,statusCheckRollup,url")
	if err != nil {
		return fail("GitHub PR evidence unreadable: " + firstLine(string(raw), err))
	}
	if err = json.Unmarshal(raw, &view); err != nil {
		return fail("invalid GitHub PR evidence: " + oneline.Err(err))
	}
	if view.ID == "" || view.Head != candidate || view.Base != "dev" || view.URL == "" {
		return fail("GitHub PR does not pin reviewed candidate and dev target")
	}
	if view.State != "MERGED" {
		if view.State != "OPEN" || view.Review != "APPROVED" || len(view.Checks) == 0 {
			return fail("PR needs ordinary approved review and completed green checks before queueing")
		}
		for _, check := range view.Checks {
			if check.State != "" {
				if check.State != "SUCCESS" {
					return fail("GitHub status is not green")
				}
				continue
			}
			if check.Status != "COMPLETED" || check.Conclusion != "SUCCESS" && check.Conclusion != "NEUTRAL" && check.Conclusion != "SKIPPED" {
				return fail("GitHub check is not completed green")
			}
		}

		queueQuery := `query($pr:ID!){node(id:$pr){... on PullRequest {headRefOid mergeQueueEntry{id}}}}`
		queueRaw, queueErr := l.promotionGH(ctx, "api", "graphql", "-f", "query="+queueQuery, "-f", "pr="+view.ID)
		if queueErr != nil {
			return fail("queue replay evidence unreadable: " + firstLine(string(queueRaw), queueErr))
		}
		var queueState struct {
			Data struct {
				Node struct {
					Head  string `json:"headRefOid"`
					Entry *struct {
						ID string `json:"id"`
					} `json:"mergeQueueEntry"`
				} `json:"node"`
			} `json:"data"`
			Errors []json.RawMessage `json:"errors"`
		}
		if e := json.Unmarshal(queueRaw, &queueState); e != nil || len(queueState.Errors) > 0 || queueState.Data.Node.Head != candidate {
			return fail("queue replay proof does not pin exact reviewed head")
		}
		if queueState.Data.Node.Entry != nil && queueState.Data.Node.Entry.ID != "" {
			return output("ok", 0, "exact reviewed candidate is already queued; not landed; run promote again after merge")
		}
		// Recheck the whole inventory and pins just before the external delivery.
		l.a.serial.Lock()
		fresh, e := l.st.Load(ctx, []string{sprint.Work, sprint.Merge}, nil)
		l.a.serial.Unlock()
		if e != nil || fresh.Epoch != l.epoch || promotionInventory(fresh) != inventory || headWhy(fresh, stream, pins) != "" {
			return fail("queue inventory/pins/epoch changed since reviewed candidate; coordinator conflict review is stale")
		}

		query := `mutation($pr:ID!,$head:GitObjectID!){enqueuePullRequest(input:{pullRequestId:$pr,expectedHeadOid:$head}){mergeQueueEntry{id position state pullRequest{number headRefOid}}}}`
		raw, err = l.promotionGH(ctx, "api", "graphql", "-f", "query="+query, "-f", "pr="+view.ID, "-f", "head="+candidate)
		if err != nil {
			return fail("ordinary GitHub queue refused the candidate: " + firstLine(string(raw), err))
		}
		var queued struct {
			Data struct {
				Enqueue struct {
					Entry struct {
						ID string `json:"id"`
						PR struct {
							Head string `json:"headRefOid"`
						} `json:"pullRequest"`
					} `json:"mergeQueueEntry"`
				} `json:"enqueuePullRequest"`
			} `json:"data"`
			Errors []json.RawMessage `json:"errors"`
		}
		if err = json.Unmarshal(raw, &queued); err != nil || len(queued.Errors) > 0 || queued.Data.Enqueue.Entry.ID == "" || queued.Data.Enqueue.Entry.PR.Head != candidate {
			return fail("GitHub queue did not return the exact candidate receipt")
		}
		return output("ok", 0, "queued reviewed candidate via ordinary GitHub merge queue; not landed; run promote again after queue merge")
	}
	if _, err = l.git(ctx, dir, "fetch", "origin", "refs/heads/dev:refs/remotes/origin/dev"); err != nil {
		return fail("fresh dev receipt fetch failed: " + firstLine("", err))
	}
	devTip, err := l.git(ctx, dir, "rev-parse", "refs/remotes/origin/dev^{commit}")
	if err != nil {
		return fail(firstLine("", err))
	}
	for _, head := range append([]string{candidate}, promotionHeads(pins)...) {
		if _, err = l.git(ctx, dir, "merge-base", "--is-ancestor", head, devTip); err != nil {
			return fail("fresh dev does not contain the exact reviewed candidate and every pinned card")
		}
	}
	sum := sha256.Sum256(reviewRaw)
	reviewHash := hex.EncodeToString(sum[:])
	receipt := &sprint.DevReceipt{Epoch: l.epoch, Repo: pins[0].repo, Branch: "dev", Tip: devTip, CandidateTip: candidate, CITip: candidate, CIReceipt: "local check: " + l.check + "; merged queue PR " + view.URL, Review: "sha256:" + reviewHash + "; " + view.URL, BatchID: candidate + "." + stream, VerifiedAt: l.a.now()}
	// A store crash can leave only part of this certified batch recorded.
	// Reverify the entire original batch above, then report only its remaining
	// merging cards; previously recorded receipts retain their original proof.
	var pending []landCard
	for _, pin := range pins {
		if pin.primary.Col == sprint.Landed {
			continue
		}
		pending = append(pending, pin)
		receipt.Entries = append(receipt.Entries, sprint.PinnedCard{ID: pin.id, Head: pin.head, Attempt: pin.attempt, ResolvedHead: pin.primary.F("staged_head")})
	}
	b := landBatch{Stream: stream, Repo: pins[0].repo, Base: "dev", Tip: devTip, Dir: dir}
	if !l.landed(b, stream, pending, receipt) {
		return fail("development ancestry verified but store receipt refused; pinned work remains recoverable")
	}
	l.scoreAll(ctx)
	return output("ok", 0, "verified fresh dev ancestry and recorded exact epoch/head/attempt delivery; no work branch was discarded")
}

func promotionHeads(pins []landCard) []string {
	heads := make([]string, len(pins))
	for i, p := range pins {
		heads[i] = p.head
	}
	return heads
}

// reviewedPromotionPins recovers the exact previously reviewed batch across
// unrelated queue advances. A changed accepted head/attempt still refuses.
func reviewedPromotionPins(s *sprint.Snapshot, stream string, r promotionReview, raw []byte) ([]landCard, bool, string) {
	queue := map[string]bool{}
	ordered := landQueue(s, stream)
	for _, c := range ordered {
		queue[c.ID] = true
	}
	sum := sha256.Sum256(raw)
	reviewPrefix := "sha256:" + hex.EncodeToString(sum[:]) + "; "
	replay := true
	seen := map[string]bool{}
	var pins []landCard
	for _, e := range r.Entries {
		pr := s.Work.Placed(e.ID)
		if seen[e.ID] || pr == nil || pr.Row != stream || pr.F("head") != e.Head || pr.F("attempt") != e.Attempt {
			return nil, false, "reviewed epoch/head/attempt no longer matches; run: nova-sprint promote --stream " + stream + " --dry-run"
		}
		seen[e.ID] = true
		recorded := sprint.Delivered(s, e.ID) && pr.F("dev_candidate_tip") == r.Candidate && strings.HasPrefix(pr.F("dev_review"), reviewPrefix)
		if !recorded && (!queue[e.ID] || pr.Col != sprint.Merging || pr.F("staged_head_pin") != e.Head || pr.F("staged_attempt") != e.Attempt || pr.F("staged_returns") != pr.F("returns")) {
			return nil, false, "reviewed batch is neither unchanged staged work nor the same recorded dev delivery; run: nova-sprint promote --stream " + stream + " --dry-run"
		}
		replay = replay && recorded
		repo := pr.F("staged_repo")
		if recorded {
			repo = pr.F("dev_repo")
		}
		pins = append(pins, landCard{id: e.ID, head: e.Head, attempt: e.Attempt, repo: repo, base: "dev", primary: pr, paths: swarm.CardPaths([]byte(pr.F("brief"))), brief: pr.F("brief")})
	}
	if len(pins) == 0 {
		return nil, false, "review names no pinned work; run: nova-sprint promote --stream " + stream + " --dry-run"
	}
	next := 0
	for _, pin := range pins {
		if pin.primary.Col == sprint.Landed {
			continue
		}
		if next >= len(ordered) || ordered[next].ID != pin.id {
			return nil, false, "reviewed pending cards are not the ordered queue prefix; coordinator must review a compatible prefix; run: nova-sprint promote --stream " + stream + " --dry-run"
		}
		next++
	}
	return pins, replay, ""
}

func promotionEntries(pins []landCard) []sprint.PinnedCard {
	entries := make([]sprint.PinnedCard, len(pins))
	for i, p := range pins {
		entries[i] = sprint.PinnedCard{ID: p.id, Attempt: p.attempt, Head: p.head, ResolvedHead: p.primary.F("staged_head")}
	}
	return entries
}

// preparePromotion reuses the lander's ordered merge, scope, ledger and
// conflict path, but builds from dev and never pushes it (Sprint §7).
func (l *lander) preparePromotion(ctx context.Context, dir, stream string, pins []landCard) (string, string) {
	if why := l.originIs(ctx, dir, pins[0].repo); why != "" {
		return "", why
	}
	if out, err := l.git(ctx, dir, "status", "--porcelain", "--untracked-files=no"); err != nil || out != "" {
		return "", "candidate clone is not clean: " + firstLine(out, err)
	}
	merged, failed, why := l.build(ctx, dir, stream, pins, &landTimes{})
	if why != "" {
		return "", why
	}
	if failed.id != "" {
		l.conflict(stream, failed)
		return "", "candidate conflict stops prefix: " + failed.id + "; coordinator conflict review/escalation required"
	}
	if len(merged) != len(pins) {
		return "", "candidate does not contain the full reviewed prefix"
	}
	tip, err := l.git(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", firstLine("", err)
	}
	if why, _ = l.runCheck(ctx, dir); why != "" {
		l.fact(landBatch{Stream: stream, Base: "dev", Dir: dir}, sprint.MergeReq{Stream: stream, Red: true, Note: why}, pins, "red", why)
		return "", l.out[len(l.out)-1].Reason
	}
	after, err := l.git(ctx, dir, "rev-parse", "HEAD")
	if err != nil || after != tip {
		return "", "check changed candidate HEAD; rebuild and review the exact commit"
	}
	if out, err := l.git(ctx, dir, "status", "--porcelain", "--untracked-files=no"); err != nil || out != "" {
		return "", "check changed tracked candidate content: " + firstLine(out, err)
	}
	return tip, ""
}
