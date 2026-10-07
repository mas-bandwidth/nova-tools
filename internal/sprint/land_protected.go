package sprint

import (
	"fmt"
	"strings"
)

// A land-protected stream landing on a protected branch of a forge does not push
// onto that branch and does not record the card landed. It pushes land/<stream>,
// opens a pull request into the protected branch, and enables auto-merge when the
// repository allows it. The card stays merging, the pull request's URL on it, until
// that pull request merges (docs/SPEC-SPRINT.md section 7, the protected branches).

const (
	// FieldLandPR is the pull request URL a protected land records on each card of
	// the batch while the card stays merging.
	FieldLandPR = "land_pr"
	// FieldLandHead is the commit the land branch was pushed at.
	FieldLandHead = "land_head"
	// NLandProtectedRefused is the one judgment a failed push or a refused pull
	// request raises. Its text is the remote's words.
	NLandProtectedRefused = "the protected land was refused by the remote"
	// NLandProtectedOpened is the happened note of a pull request opened, the card
	// still merging.
	NLandProtectedOpened = "protected land opened a pull request"
)

// LandBranch is the branch a protected land pushes: land/<stream>, never the
// protected branch.
func LandBranch(stream string) string { return "land/" + stream }

// LandBranchRef is the ref LandBranch is pushed to.
func LandBranchRef(stream string) string { return "refs/heads/" + LandBranch(stream) }

// ForgeOrigin says origin is a forge URL (https, ssh or scp-like), not a local
// path. A lander's own bare clone is a path, and a marked stream still pushes
// that base directly: the pull request is for a forge, whose protected branch
// rejects the push.
func ForgeOrigin(origin string) bool {
	u := strings.TrimSpace(origin)
	if strings.HasPrefix(strings.ToLower(u), "file:") {
		return false
	}
	if strings.Contains(u, "://") {
		return true
	}
	colon := strings.Index(u, ":")
	return colon > 0 && !strings.Contains(u[:colon], "/")
}

// LandProtectedDefers says a landing of stream onto base in repo, fetched from
// origin, opens a pull request instead of pushing the protected branch: the
// base is protected, the stream is marked for the repository, and origin is a
// forge. A local path does not defer.
func LandProtectedDefers(s *Snapshot, stream, repo, base, origin string) bool {
	if s == nil || !ForgeOrigin(origin) || !contains(ProtectedBranches, base) {
		return false
	}
	ctl := s.StreamCtl(stream)
	if ctl == nil || ctl.F(FieldLandProtected) == "" {
		return false
	}
	return ProtectedLandWhy(s, stream, repo, base, "card") == ""
}

// LandRemote is the forge a protected land talks to. A test stands a fake in
// for it. Push sends sha to ref. OpenPullRequest opens head into base and
// answers the pull request's URL. EnableAutoMerge enables auto-merge when the
// repository allows it: allowed is false when it does not, and err is a failure
// of the call, which does not undo the pull request. PullRequestMerged reports
// whether the pull request has merged, and the merge commit when it has.
type LandRemote interface {
	Push(ref, sha string) error
	OpenPullRequest(base, head, title, body string) (string, error)
	EnableAutoMerge(url string) (allowed bool, err error)
	PullRequestMerged(url string) (merged bool, sha string, err error)
}

// LandProtectedCard is one card of a protected batch: its id and the head the
// batch merged.
type LandProtectedCard struct {
	ID   string
	Head string
}

// LandProtectedReq is one protected land of a batch the caller already built.
type LandProtectedReq struct {
	Stream string
	Repo   string
	Base   string
	Cards  []LandProtectedCard
	Tip    string // the commit to push to land/<stream>
	Who    string
}

// LandProtectedFact is what the remote did, which the plan records and does not
// ask again.
type LandProtectedFact struct {
	Stream   string
	Repo     string
	Base     string
	Who      string
	Cards    []LandProtectedCard
	Tip      string
	URL      string
	Auto     string // on, off, or the call's words
	Refused  string // the remote's words; empty when the remote accepted
	Merged   bool
	MergeSHA string
	Poll     string // a poll that failed after the pull request was opened
}

// LandProtectedOutcome is one drive of a protected land: Kind is opened (the
// pull request is recorded, the cards stay merging), waiting (nothing new to
// record), refused (one judgment, the cards stay merging) or landed (the pull
// request has merged).
type LandProtectedOutcome struct {
	Kind string
	Fact LandProtectedFact
	Plan Plan
}

// LandProtectedDrive talks to the remote and returns the plan for what it did.
// It pushes and opens only when the batch has no pull request yet. A pull
// request already recorded is polled, and the cards land only when it has merged.
func LandProtectedDrive(s *Snapshot, remote LandRemote, r LandProtectedReq) (LandProtectedOutcome, error) {
	if remote == nil {
		return LandProtectedOutcome{}, fmt.Errorf("no remote")
	}
	if why := landProtectedPathWhy(s, r); why != "" {
		var p Plan
		p.on(s)
		p.refuse(r.Stream, why)
		return LandProtectedOutcome{Kind: "refused", Plan: p}, nil
	}
	fact := LandProtectedFact{Stream: r.Stream, Repo: r.Repo, Base: r.Base, Who: r.Who, Cards: append([]LandProtectedCard(nil), r.Cards...), Tip: r.Tip}
	url, mismatch := recordedLandPR(s, r.Cards)
	if mismatch != "" {
		fact.Refused = mismatch
		return landProtectedRefused(s, fact), nil
	}
	if url == "" {
		if err := remote.Push(LandBranchRef(r.Stream), r.Tip); err != nil {
			fact.Refused = landRemoteWords(err)
			return landProtectedRefused(s, fact), nil
		}
		title, body := LandProtectedText(s, r.Stream, r.Base, r.Cards)
		opened, err := remote.OpenPullRequest(r.Base, LandBranch(r.Stream), title, body)
		if err != nil {
			fact.Refused = landRemoteWords(err)
			return landProtectedRefused(s, fact), nil
		}
		fact.URL = strings.TrimSpace(opened)
		allowed, aerr := remote.EnableAutoMerge(fact.URL)
		switch {
		case aerr != nil:
			fact.Auto = landRemoteWords(aerr)
		case allowed:
			fact.Auto = "on"
		default:
			fact.Auto = "off"
		}
		merged, sha, err := remote.PullRequestMerged(fact.URL)
		if err != nil {
			fact.Poll = landRemoteWords(err)
			return landProtectedOpened(s, fact), nil
		}
		if !merged {
			return landProtectedOpened(s, fact), nil
		}
		fact.Merged, fact.MergeSHA = true, sha
		return landProtectedLanded(s, fact), nil
	}
	fact.URL = url
	merged, sha, err := remote.PullRequestMerged(url)
	if err != nil {
		fact.Refused = landRemoteWords(err)
		return landProtectedRefused(s, fact), nil
	}
	if !merged {
		var p Plan
		p.on(s)
		return LandProtectedOutcome{Kind: "waiting", Fact: fact, Plan: p}, nil
	}
	fact.Merged, fact.MergeSHA = true, sha
	return landProtectedLanded(s, fact), nil
}

// LandProtectedPlan is the store plan for a fact the remote already gave. It
// does not talk to the remote. A merged pull request is the merge step's
// landing; anything else leaves the cards merging.
func LandProtectedPlan(s *Snapshot, fact LandProtectedFact) Plan {
	if fact.Merged {
		ids := landProtectedIDs(fact.Cards)
		return Lawful(mergeStep(s, MergeReq{Stream: fact.Stream, Who: fact.Who, Cards: ids}))
	}
	if fact.Refused != "" {
		return landProtectedJudgment(s, fact)
	}
	return landProtectedOpenPlan(s, fact)
}

// LandProtectedText is the pull request's title and body: the batch's cards,
// each one's head, and the readers who said ok at that head.
func LandProtectedText(s *Snapshot, stream, base string, cards []LandProtectedCard) (title, body string) {
	ids := landProtectedIDs(cards)
	title = "land " + span(ids) + " onto " + base
	var b strings.Builder
	fmt.Fprintf(&b, "stream %s\nbranch %s\n", stream, LandBranch(stream))
	for _, c := range cards {
		head := c.Head
		readers := "none"
		if s != nil && s.Work != nil {
			if pr := s.Work.Placed(c.ID); pr != nil && head == "" {
				head = pr.F("head")
			}
		}
		if s != nil && s.Readers != nil && s.Work != nil {
			if pr := s.Work.Placed(c.ID); pr != nil {
				var names []string
				for _, rc := range okReaders(s, pr) {
					if name := rc.F("reader"); name != "" {
						names = append(names, name)
					}
				}
				if len(names) > 0 {
					readers = strings.Join(names, ", ")
				}
			}
		}
		fmt.Fprintf(&b, "- %s head=%s readers=%s\n", c.ID, head, readers)
	}
	return title, b.String()
}

func landProtectedPathWhy(s *Snapshot, r LandProtectedReq) string {
	if s == nil || s.StreamCtl(r.Stream) == nil {
		return "no such stream"
	}
	card := "card"
	if len(r.Cards) > 0 {
		card = r.Cards[0].ID
	}
	if !contains(ProtectedBranches, r.Base) {
		return "base " + r.Base + " is not a protected branch"
	}
	if why := ProtectedLandWhy(s, r.Stream, r.Repo, r.Base, card); why != "" {
		return why
	}
	if len(r.Cards) == 0 {
		return "the batch names no card"
	}
	if r.Tip == "" {
		// a pull request already opened is polled; it pushes nothing
		if s.Work != nil {
			if url, mismatch := recordedLandPR(s, r.Cards); mismatch == "" && url != "" {
				return ""
			}
		}
		return "the batch has no commit to push"
	}
	return ""
}

func landProtectedIDs(cards []LandProtectedCard) []string {
	ids := make([]string, len(cards))
	for i, c := range cards {
		ids[i] = c.ID
	}
	return ids
}

// recordedLandPR is the pull request URL every card of the batch already
// carries, or "" when none does. mismatch is set when the cards disagree.
func recordedLandPR(s *Snapshot, cards []LandProtectedCard) (url, mismatch string) {
	if s == nil || s.Work == nil {
		return "", "the work table is not loaded"
	}
	seen := false
	for _, c := range cards {
		pr := s.Work.Placed(c.ID)
		if pr == nil {
			return "", c.ID + " is not on the table"
		}
		u := pr.F(FieldLandPR)
		if !seen {
			url, seen = u, true
			continue
		}
		if u != url {
			return "", "the batch names more than one pull request"
		}
	}
	return url, ""
}

func landProtectedRefused(s *Snapshot, fact LandProtectedFact) LandProtectedOutcome {
	if landProtectedJudged(s, landProtectedIDs(fact.Cards)) {
		var p Plan
		p.on(s)
		return LandProtectedOutcome{Kind: "waiting", Fact: fact, Plan: p}
	}
	return LandProtectedOutcome{Kind: "refused", Fact: fact, Plan: landProtectedJudgment(s, fact)}
}

func landProtectedOpened(s *Snapshot, fact LandProtectedFact) LandProtectedOutcome {
	return LandProtectedOutcome{Kind: "opened", Fact: fact, Plan: landProtectedOpenPlan(s, fact)}
}

func landProtectedLanded(s *Snapshot, fact LandProtectedFact) LandProtectedOutcome {
	fact.Merged = true
	return LandProtectedOutcome{Kind: "landed", Fact: fact, Plan: LandProtectedPlan(s, fact)}
}

func landProtectedJudgment(s *Snapshot, fact LandProtectedFact) Plan {
	var p Plan
	p.on(s)
	ids := landProtectedIDs(fact.Cards)
	if landProtectedJudged(s, ids) {
		return p
	}
	j := judgment(NLandProtectedRefused, fact.Stream, s.Now, 0, ids...)
	j.Who = fact.Who
	j.What = cutText(fact.Refused, MaxCardTextBytes)
	j.Decisions = []string{"retry", "wait"}
	p.Notes = []Note{j}
	return p
}

func landProtectedOpenPlan(s *Snapshot, fact LandProtectedFact) Plan {
	var p Plan
	p.on(s)
	if fact.URL == "" {
		p.refuse(fact.Stream, "a protected land names no pull request")
		return p
	}
	ids := make([]string, 0, len(fact.Cards))
	var units []Unit
	for _, c := range fact.Cards {
		pr := s.Work.Placed(c.ID)
		if pr == nil || pr.Row != fact.Stream || pr.Col != Merging {
			p.refuse(c.ID, "not merging in stream "+fact.Stream)
			return p
		}
		ids = append(ids, c.ID)
		set := map[string]string{FieldLandPR: fact.URL}
		if fact.Tip != "" {
			set[FieldLandHead] = fact.Tip
		}
		units = append(units, Unit{Key: c.ID, Stream: fact.Stream,
			Changes: []Change{change(Work, setEntry(pr, set))},
			Moved:   c.ID + " stays merging"})
	}
	what := fact.URL
	if fact.Auto != "" {
		what += " auto-merge " + fact.Auto
	}
	if fact.Poll != "" {
		what += " poll: " + fact.Poll
	}
	note := happened(NLandProtectedOpened, fact.Stream, s.Now, ids...)
	note.Who, note.What = fact.Who, cutText(what, MaxCardTextBytes)
	units[0].Notes = []Note{note}
	p.Units = units
	p.Closes = landProtectedCloses(s, ids)
	return Lawful(p)
}

func landProtectedJudged(s *Snapshot, ids []string) bool {
	if s == nil || len(ids) == 0 {
		return false
	}
	open := map[string]bool{}
	for _, o := range s.Open {
		if o.Note.Type == NLandProtectedRefused {
			open[o.Subject()] = true
		}
	}
	for _, id := range ids {
		if !open[id] {
			return false
		}
	}
	return true
}

func landProtectedCloses(s *Snapshot, ids []string) []Open {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []Open
	for _, o := range s.Open {
		if o.Note.Type == NLandProtectedRefused && want[o.Subject()] {
			out = append(out, o)
		}
	}
	return out
}

func landRemoteWords(err error) string {
	if err == nil {
		return ""
	}
	return cutText(strings.Join(strings.Fields(err.Error()), " "), 400)
}
