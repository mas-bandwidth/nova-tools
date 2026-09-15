package wake

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Comments and reviews on a pull request -- --pr, --owned-prs, the amendment of
// 2026-09-13.
//
// THE HURT, 2026-09-12: two review HOLDs with concrete defects sat unread for
// 100 and 45 minutes, because they were comments on pull requests the window
// owned and "the bus wait is my only wake, and it does not know about PR
// comments". An entry's checks were a source and its conversation was not.
//
// THE STATE VALUE is, in order: the count of issue comments, the count of
// reviews, the count of review threads, the pull request's own updatedAt, the
// head its own branch points at, and the kind, id and stamp of the newest item
// of the three kinds. A change in any field is a change, and a count that FALLS
// is a change too -- a deleted comment is news.
//
// EVERY MOVEMENT THIS SOURCE OBSERVES IS A CHANGE, and setting this actor's own
// words aside is an OPT-IN narrowing over ids it recorded itself (draft 4). A
// login is a credential and not a window: a caller's other windows, its
// children and its AI friends write with it, so two friends on one login
// suppressed each other's findings -- a reviewer's HOLD dropped as "the
// window's own words", which is the missed wake this source exists to prevent
// arriving under the name of a false-wake repair.
//
// THE WATERMARK AND THE SUPPRESSION ARE TWO THINGS (draft 6, K4). updated= is
// stored on EVERY tick exactly as observed and carries no claim about
// attribution; suppression sets aside only what the tick fetched and matched,
// and it may not make a moved stamp silent.

// PRNodeWindow is the ten-node read. Ten is the window, and it is a cap on the
// READ rather than on the news: past it the tool wakes rather than guesses,
// because a missed wake here is a reviewer's HOLD unread.
const PRNodeWindow = 10

// OwnedCap is the listing law over one repository's owned pull requests.
const OwnedCap = 20

// ForgeIntervalFloor is --forge-interval's floor. It is higher than
// --interval's because these sources have no run length to pace by, they are
// one API call per watched thing per tick against a shared hourly limit, and
// people write comments and push branches at a rate a 30-second tick already
// over-serves.
const ForgeIntervalFloor = 30 * time.Second

// PRs is the --pr and --owned-prs source.
type PRs struct {
	Names   []string // <owner/repo>#<n>, named with --pr
	Owned   []string // <owner/repo>, named with --owned-prs
	Every_  time.Duration
	Timeout time.Duration
	// NotMine holds the forge node ids this actor emitted, re-read at the start
	// of each forge tick so an id appended mid-run is set aside from the next
	// tick on. Nil means the flag was not given, and then NOTHING is set aside.
	NotMine func() (map[string]bool, error)
	Prev    func(key string) (string, bool)

	calls
	read, changed, unreadable, self int
	login                           string
	loginRead, polled               bool
	capped                          map[string]bool
	notes                           []string
}

func (p *PRs) Name() string         { return "prs" }
func (p *PRs) Every() time.Duration { return p.Every_ }

func (p *PRs) Counts() (read, changed, unreadable, calls, self int, login string) {
	return p.read, p.changed, p.unreadable, p.Calls(), p.self, p.login
}

// standingQuery is one request under 2 KB each way, and the reason the value is
// counts, a stamp and a newest rather than a list of ids: the read is the size
// of the ANSWER and never of the conversation.
const standingQuery = `query($owner:String!,$repo:String!,$number:Int!){
  repository(owner:$owner,name:$repo){
    pullRequest(number:$number){
      updatedAt headRefOid url
      comments(last:1){totalCount nodes{id createdAt url author{login}}}
      reviews(last:1){totalCount nodes{id createdAt url state author{login}}}
      reviewThreads(last:1){totalCount nodes{comments(last:1){nodes{id createdAt url author{login}}}}}
    }
  }
}`

// prAnswer is the shape of both reads: the standing one and the node fetch.
type prAnswer struct {
	Data struct {
		Repository struct {
			PullRequest struct {
				UpdatedAt  string `json:"updatedAt"`
				HeadRefOid string `json:"headRefOid"`
				URL        string `json:"url"`
				Comments   struct {
					TotalCount int      `json:"totalCount"`
					Nodes      []prNode `json:"nodes"`
				} `json:"comments"`
				Reviews struct {
					TotalCount int      `json:"totalCount"`
					Nodes      []prNode `json:"nodes"`
				} `json:"reviews"`
				ReviewThreads struct {
					TotalCount int `json:"totalCount"`
					Nodes      []struct {
						Comments struct {
							Nodes []prNode `json:"nodes"`
						} `json:"comments"`
					} `json:"nodes"`
				} `json:"reviewThreads"`
			} `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
}

type prNode struct {
	ID        string `json:"id"`
	CreatedAt string `json:"createdAt"`
	URL       string `json:"url"`
	State     string `json:"state"`
	Author    struct {
		Login string `json:"login"`
	} `json:"author"`
}

// kinded is one fetched node with the word for what kind of thing it is.
type kinded struct {
	kind string
	prNode
}

func (p *PRs) Poll(ctx context.Context, now time.Time) (Result, error) {
	res := Result{}
	var mine map[string]bool
	if p.NotMine != nil {
		var err error
		mine, err = p.NotMine()
		if err != nil {
			return res, err
		}
	}
	names, listFailed, reason := p.watched(ctx, &res)
	// The cold-join rule applies from the SECOND tick on. A first poll is the
	// cold-start rule's, which main.go's watcher owns, and --baseline is the
	// caller asking for the world listed once.
	joined := p.polled
	p.polled = true
	type answer struct {
		i    int
		item Item
		bad  bool
	}
	out := make([]answer, len(names))
	sem := make(chan struct{}, ForgeBatch)
	done := make(chan answer, len(names))
	for i, name := range names {
		go func(i int, name string) {
			sem <- struct{}{}
			defer func() { <-sem }()
			item, bad := p.one(ctx, name, mine, joined)
			done <- answer{i: i, item: item, bad: bad}
		}(i, name)
	}
	for range names {
		a := <-done
		out[a.i] = a
	}
	bad := 0
	var firstReason string
	for _, a := range out {
		p.read++
		if a.bad {
			bad++
			p.unreadable++
			if firstReason == "" {
				_, firstReason = Unreadable(a.item.Value)
			}
		}
		res.Items = append(res.Items, a.item)
	}
	// The prs source fails when every watched pull request is unreadable OR a
	// --owned-prs listing fails -- so a listing that cannot run is loud in three
	// ticks rather than silently narrowing what is watched.
	if listFailed {
		return res, fmt.Errorf("an --owned-prs listing failed: %s", oneLineOf(reason))
	}
	if len(names) > 0 && bad == len(names) {
		return res, fmt.Errorf("every watched pull request is unreadable: %s", firstReason)
	}
	return res, nil
}

// watched is the --pr names plus this tick's --owned-prs listing.
func (p *PRs) watched(ctx context.Context, res *Result) (names []string, listFailed bool, reason string) {
	names = append(names, p.Names...)
	if len(p.Owned) == 0 {
		return names, false, ""
	}
	// gh api user is read ONCE PER RUN and only when --owned-prs is given, to
	// list that account's open authored pull requests. --pr names the window's
	// work itself and reads no login at all.
	if !p.loginRead {
		p.loginRead = true
		raw, err := gh(ctx, p.Timeout, &p.calls, "api", "user")
		if err != nil {
			res.Notes = append(res.Notes, fmt.Sprintf(
				"owned-prs: host login unreadable (%s); no pull request is listed from this account, and --pr names one directly",
				oneLineOf(err.Error())))
			return names, true, err.Error()
		}
		var who struct {
			Login string `json:"login"`
		}
		if uerr := json.Unmarshal(raw, &who); uerr != nil || who.Login == "" {
			res.Notes = append(res.Notes, "owned-prs: host login unreadable (gh answered no login); no pull request is listed from this account, and --pr names one directly")
			return names, true, "gh answered no login"
		}
		p.login = who.Login
	}
	if p.login == "" {
		return names, true, "no host login"
	}
	seen := map[string]bool{}
	for _, n := range names {
		seen[n] = true
	}
	for _, repo := range p.Owned {
		raw, err := gh(ctx, p.Timeout, &p.calls, "pr", "list", "--repo", repo,
			"--author", p.login, "--state", "open", "--json", "number", "--limit", strconv.Itoa(OwnedCap+3))
		if err != nil {
			// The set STANDS AS IT WAS, the pull requests already in it are
			// polled as usual, and the tick counts toward fail:prs.
			listFailed, reason = true, err.Error()
			continue
		}
		var list []struct {
			Number int `json:"number"`
		}
		if uerr := json.Unmarshal(raw, &list); uerr != nil {
			listFailed, reason = true, uerr.Error()
			continue
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Number > list[j].Number })
		if len(list) > OwnedCap {
			if p.capped == nil {
				p.capped = map[string]bool{}
			}
			if !p.capped[repo] {
				p.capped[repo] = true
				res.Notes = append(res.Notes, fmt.Sprintf(
					"owned-prs %s capped at %d of %d; name the rest with --pr", repo, OwnedCap, len(list)))
			}
			list = list[:OwnedCap]
		}
		for _, one := range list {
			name := fmt.Sprintf("%s#%d", repo, one.Number)
			if seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	return names, listFailed, reason
}

// one is the standing read of a pull request, the node fetch --not-mine may
// earn, and the decision over what was fetched.
func (p *PRs) one(ctx context.Context, name string, mine map[string]bool, joined bool) (Item, bool) {
	key := "pr:" + name
	repo, number, err := SplitPR(name)
	if err != nil {
		return Item{Kind: KindPR, Key: key, Value: Compose("unreadable", oneLineOf(err.Error()))}, true
	}
	owner, rname := ownerRepo(repo)
	raw, err := gh(ctx, p.Timeout, &p.calls, "api", "graphql",
		"-f", "query="+standingQuery, "-F", "owner="+owner, "-F", "repo="+rname, "-F", "number="+number)
	if err != nil {
		return Item{Kind: KindPR, Key: key, Value: Compose("unreadable", oneLineOf(err.Error()))}, true
	}
	var answer prAnswer
	if uerr := json.Unmarshal(raw, &answer); uerr != nil {
		return Item{Kind: KindPR, Key: key,
			Value: Compose("unreadable", "gh answered something this tool cannot parse: "+oneLineOf(uerr.Error()))}, true
	}
	pr := answer.Data.Repository.PullRequest
	comments, reviews, threads := pr.Comments.TotalCount, pr.Reviews.TotalCount, pr.ReviewThreads.TotalCount

	// The standing read's last:1 node of each kind IS the attribution where
	// nothing has to be excluded from it.
	var newest kinded
	take := func(kind string, n prNode) {
		if n.ID == "" {
			return
		}
		if newest.ID == "" || n.CreatedAt > newest.CreatedAt {
			newest = kinded{kind: kind, prNode: n}
		}
	}
	for _, n := range pr.Comments.Nodes {
		take("comment", n)
	}
	for _, n := range pr.Reviews.Nodes {
		take("review", n)
	}
	for _, t := range pr.ReviewThreads.Nodes {
		for _, n := range t.Comments.Nodes {
			take("thread", n)
		}
	}
	newestField := "-"
	if newest.ID != "" {
		newestField = fmt.Sprintf("%s:%s@%s", newest.kind, newest.ID, newest.CreatedAt)
	}
	value := Compose(strconv.Itoa(comments), strconv.Itoa(reviews), strconv.Itoa(threads),
		pr.UpdatedAt, pr.HeadRefOid, newestField)

	old, had := p.Prev(key)
	oldP := fields(Decompose(old), 6)
	moved := func(i, now int) int {
		if !had {
			return 0
		}
		was, _ := strconv.Atoi(oldP[i])
		if d := now - was; d > 0 {
			return d
		}
		return 0
	}
	dComments, dReviews, dThreads := moved(0, comments), moved(1, reviews), moved(2, threads)
	countMoved := had && (oldP[0] != strconv.Itoa(comments) || oldP[1] != strconv.Itoa(reviews) || oldP[2] != strconv.Itoa(threads))
	updatedMoved := had && oldP[3] != pr.UpdatedAt
	headMoved := had && oldP[4] != pr.HeadRefOid

	self := 0
	attributed := newest
	allMine := false
	if mine != nil && (dComments+dReviews+dThreads) > 0 {
		// Under --not-mine, a moved count is fetched again: last:min(d,10)
		// nodes of the kind that moved, in ONE further call for that pull
		// request on that tick.
		fetched := p.nodes(ctx, owner, rname, number, dComments, dReviews, dThreads)
		if len(fetched) > 0 {
			allMine = dComments <= PRNodeWindow && dReviews <= PRNodeWindow && dThreads <= PRNodeWindow
			var newestKept kinded
			for _, n := range fetched {
				if mine[n.ID] {
					self++
					continue
				}
				allMine = false
				if newestKept.ID == "" || n.CreatedAt > newestKept.CreatedAt {
					newestKept = n
				}
			}
			// On every change this source reports, newest=/by=/url= name the
			// newest fetched node that was NOT set aside, or the newest fetched
			// node when every one of them was.
			if newestKept.ID != "" {
				attributed = newestKept
			} else if len(fetched) > 0 {
				attributed = fetched[len(fetched)-1]
			}
		}
	}
	if mine != nil && headMoved && mine[pr.HeadRefOid] {
		self++
	}
	push := updatedMoved && headMoved && !countMoved
	rescan := updatedMoved && !countMoved && !headMoved
	if mine != nil && allMine && updatedMoved {
		// The counted part of this movement was mine; what else moved is not
		// resolved by this read.
		rescan = true
	}
	if rescan && attributed.ID == newest.ID && !countMoved {
		attributed = kinded{}
	}

	by, review, url, atStamp := "-", "-", pr.URL, pr.UpdatedAt
	newestLine := "-"
	if attributed.ID != "" {
		by = attributed.Author.Login
		newestLine = attributed.kind + ":" + attributed.ID
		if attributed.kind == "review" && attributed.State != "" {
			review = attributed.State
		}
		if attributed.URL != "" {
			url = attributed.URL
		}
		if attributed.CreatedAt != "" {
			atStamp = attributed.CreatedAt
		}
	}
	display := Compose(strconv.Itoa(comments), strconv.Itoa(reviews), strconv.Itoa(threads),
		strconv.Itoa(self), boolWord(rescan), boolWord(push), pr.HeadRefOid,
		newestLine, by, review, atStamp, url)

	item := Item{Kind: KindPR, Key: key, Value: value, Display: display}
	// The cold-join rule: a pull request that opens mid-run joins the set with
	// its conversation RECORDED AND NOT REPORTED, which is the cold-start rule
	// applied to a thing that did not exist at the start of the run.
	if !had && joined {
		item.Record = true
	}
	// The third of the three observation-time exclusions: a tick, under
	// --not-mine, whose whole movement the tool proved was this actor's own
	// emitted ids and whose updated= did not move. A moved stamp is never made
	// silent (draft 6, K4).
	if mine != nil && allMine && !updatedMoved && !headMoved {
		item.Record = true
		p.self += self
		return item, false
	}
	p.self += self
	if !had || old != value {
		p.changed++
	}
	return item, false
}

// nodes is the second call, and it is --not-mine's alone: without the flag
// nothing has to be excluded, so no second gh call is ever made.
func (p *PRs) nodes(ctx context.Context, owner, repo, number string, dc, dr, dt int) []kinded {
	window := func(d int) int {
		if d > PRNodeWindow {
			return PRNodeWindow
		}
		return d
	}
	var parts []string
	if dc > 0 {
		parts = append(parts, fmt.Sprintf("comments(last:%d){nodes{id createdAt url author{login}}}", window(dc)))
	}
	if dr > 0 {
		parts = append(parts, fmt.Sprintf("reviews(last:%d){nodes{id createdAt url state author{login}}}", window(dr)))
	}
	if dt > 0 {
		parts = append(parts, fmt.Sprintf("reviewThreads(last:%d){nodes{comments(last:1){nodes{id createdAt url author{login}}}}}", window(dt)))
	}
	query := fmt.Sprintf(`query($owner:String!,$repo:String!,$number:Int!){repository(owner:$owner,name:$repo){pullRequest(number:$number){%s}}}`,
		strings.Join(parts, " "))
	raw, err := gh(ctx, p.Timeout, &p.calls, "api", "graphql",
		"-f", "query="+query, "-F", "owner="+owner, "-F", "repo="+repo, "-F", "number="+number, "-F", "fetch=1")
	if err != nil {
		return nil
	}
	var answer prAnswer
	if json.Unmarshal(raw, &answer) != nil {
		return nil
	}
	pr := answer.Data.Repository.PullRequest
	var out []kinded
	for _, n := range pr.Comments.Nodes {
		out = append(out, kinded{kind: "comment", prNode: n})
	}
	for _, n := range pr.Reviews.Nodes {
		out = append(out, kinded{kind: "review", prNode: n})
	}
	for _, t := range pr.ReviewThreads.Nodes {
		for _, n := range t.Comments.Nodes {
			out = append(out, kinded{kind: "thread", prNode: n})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

func boolWord(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
