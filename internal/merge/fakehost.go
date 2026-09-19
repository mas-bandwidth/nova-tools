package merge

import "fmt"

// FakeHost is the host the tests drive. It lives beside the production one rather than in
// a _test.go file because two packages need it -- internal/merge and cmd/nova-merge --
// and a fake copied into both is two fakes that drift.
//
// It reaches nothing: no network, no subprocess, no environment. Every answer is a field
// a test set, which is the point: the merge condition is a function of what the host said
// and of the records, and a test that cannot say what the host said cannot test it.
type FakeHost struct {
	PRs      map[int]PR
	Branches map[string]string
	ChecksBy map[string]Checks
	// MergeGroupRuns holds one merge-group run per id, the evidence `classify` judges.
	MergeGroupRuns map[int64]MergeRun
	// CheckResults is a rollup whose every entry carries the commit it ran on,
	// so a test can put an old failure on one sha and an in-progress run on the
	// head and see which one the wait verb judges. Checks answers these for any
	// oid, the way a pull-request rollup answers more than the head; the verb,
	// not the fake, is the thing that filters.
	CheckResults []CheckDetail
	Readied      []int
	Atomic       bool
	Merges       []string
	Err          error
	// OnPR, when set, answers PR reads per poll so a test can script a PR that
	// merges on the second poll. It receives the PR number and the 1-based call
	// count; returning ok=false falls through to the PRs map. OnChecks does the
	// same for check reads by commit sha.
	OnPR        func(n, call int) (PR, bool)
	OnChecks    func(oid string, call int) (Checks, bool)
	PRCalls     int
	ChecksCalls int
	// Do is what this fake's two-precondition merge actually DOES, when a test wants the
	// base really moved -- so that the read-back of rule 21 is exercised rather than
	// skipped. A nil Do records the call and moves nothing.
	Do func(n int, headOID, baseSHA, mergeSHA string) error
	// Open is the open pull request list OpenPRs answers: the rebase cutter's whole input.
	Open []RebasePR
	// OpenQueue is the open pull requests the queue sweep walks. It is a SECOND list
	// because the two seams want different rows: the rebase cutter reads the four
	// fields of a RebasePR, and the sweep reads a whole PR -- its head oid, its
	// mergeable state and when the host last saw it move. One method cannot answer two
	// shapes, so QueuePRs answers this one and OpenPRs answers Open.
	//
	// Failures, Changed and Issues are the poison detector's data: the tests that
	// failed, the packages the pull request changed, and the issue a park names.
	OpenQueue []PR
	Failures  map[int][]Failure
	Changed   map[int][]string
	Issues    map[int]string
	// Reads is the verdicts the forge carries for a pull request (#1572): the HOLDs and
	// APPROVEs its readers posted as comments or reviews. VerdictErr is the read failing.
	Reads      map[int][]Verdict
	VerdictErr error
}

// QueuePRs lists the open pull requests this fake reports to the queue sweep. It is the
// queueHost seam, and it is deliberately not OpenPRs: see OpenQueue.
func (f *FakeHost) QueuePRs() ([]PR, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	return f.OpenQueue, nil
}

// PoisonFailures is the detector's view of a pull request's own run.
func (f *FakeHost) PoisonFailures(pr int) []Failure { return f.Failures[pr] }

// ChangedPackages is the packages the pull request changed.
func (f *FakeHost) ChangedPackages(pr int) []string { return f.Changed[pr] }

// IssueFor is the issue a park names, or "".
func (f *FakeHost) IssueFor(pr int) string { return f.Issues[pr] }

// NewFakeHost returns an empty one.
func NewFakeHost() *FakeHost {
	return &FakeHost{PRs: map[int]PR{}, Branches: map[string]string{}, ChecksBy: map[string]Checks{}, MergeGroupRuns: map[int64]MergeRun{}}
}

func (f *FakeHost) PR(n int) (PR, error) {
	if f.Err != nil {
		return PR{}, f.Err
	}
	f.PRCalls++
	if f.OnPR != nil {
		if pr, ok := f.OnPR(n, f.PRCalls); ok {
			return pr, nil
		}
	}
	pr, ok := f.PRs[n]
	if !ok {
		return PR{}, fmt.Errorf("this fake host has no pull request %d", n)
	}
	return pr, nil
}

// OpenPRs answers the list a test set, or this fake's error, the way the real host answers
// gh's one call.
func (f *FakeHost) OpenPRs() ([]RebasePR, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	return f.Open, nil
}

func (f *FakeHost) BranchOID(branch string) (string, error) {
	if f.Err != nil {
		return "", f.Err
	}
	oid, ok := f.Branches[branch]
	if !ok {
		return "", fmt.Errorf("this fake host has no branch %q", branch)
	}
	return oid, nil
}

// Checks answers the buckets a test set for that commit, plus the sha-carrying
// rollup. An unknown commit has no checks at all, which is PENDING and not
// green -- the same answer the real host gives for a pull request whose
// workflows have not been queued.
func (f *FakeHost) Checks(oid string) (Checks, error) {
	if f.Err != nil {
		return Checks{}, f.Err
	}
	f.ChecksCalls++
	if f.OnChecks != nil {
		if c, ok := f.OnChecks(oid, f.ChecksCalls); ok {
			return c, nil
		}
	}
	c := f.ChecksBy[oid]
	for _, r := range f.CheckResults {
		c.AddRun(r.Name, r.Conclusion, r.SHA)
	}
	return c, nil
}

func (f *FakeHost) Ready(n int) error {
	f.Readied = append(f.Readied, n)
	return nil
}

// MergeGroupRun answers one run a test set, or an error naming the id when it set none.
func (f *FakeHost) MergeGroupRun(id int) (MergeRun, error) {
	if f.Err != nil {
		return MergeRun{}, f.Err
	}
	run, ok := f.MergeGroupRuns[int64(id)]
	if !ok {
		return MergeRun{}, fmt.Errorf("this fake host has no merge-group run %d", id)
	}
	return run, nil
}

func (f *FakeHost) AtomicMerge() bool { return f.Atomic }

// Merge is the two-precondition primitive, and this fake is a GUARD rather than a
// recorder: a call missing either precondition is refused, so no test passes branch (a)
// of rule 21 by accident. The call is recorded in the spelling rule 21 states, so a test
// reads the preconditions that were supplied and not the ones the signature implies.
func (f *FakeHost) Merge(n int, headOID, baseSHA, mergeSHA string) error {
	if !f.Atomic {
		return fmt.Errorf("this fake host offers no two-precondition merge and Merge was called anyway")
	}
	if !IsSHA(headOID) || !IsSHA(baseSHA) || !IsSHA(mergeSHA) {
		return fmt.Errorf("a two-precondition merge wants an expected head, an expected base and the gated object, all full shas; got head %q base %q merge %q", headOID, baseSHA, mergeSHA)
	}
	f.Merges = append(f.Merges, fmt.Sprintf("pr=%d match-head-commit=%s base=%s merge=%s", n, headOID, baseSHA, mergeSHA))
	if f.Do != nil {
		return f.Do(n, headOID, baseSHA, mergeSHA)
	}
	return nil
}

// SetChecks is the shorthand a test uses to say what a commit's evidence is.
func (f *FakeHost) SetChecks(oid string, green, pending int, red ...string) {
	c := Checks{Green: green, Pending: pending, Red: len(red), RedNames: red}
	for i := 0; i < pending; i++ {
		name := fmt.Sprintf("pending-%d", i+1)
		c.PendingNames = append(c.PendingNames, name)
		c.Details = append(c.Details, CheckDetail{Name: name, Conclusion: "pending"})
	}
	for _, name := range red {
		c.Details = append(c.Details, CheckDetail{Name: name, Conclusion: "failure"})
	}
	f.ChecksBy[oid] = c
}

// SetCheckDetails sets a commit's checks from explicit name/conclusion pairs,
// so a test can name the one pending or failing check the wait verb must print.
func (f *FakeHost) SetCheckDetails(oid string, details ...CheckDetail) {
	var c Checks
	for _, d := range details {
		c.Add(d.Name, d.Conclusion)
	}
	f.ChecksBy[oid] = c
}

// SetCheckResult records one check run against the commit whose head_sha it
// names. A run on a sha the pull request has moved past is a stale conclusion,
// and a test uses this to prove the wait verb ignores it (nova-tools #1014).
func (f *FakeHost) SetCheckResult(sha, name, conclusion string) {
	f.CheckResults = append(f.CheckResults, CheckDetail{Name: name, Conclusion: conclusion, SHA: sha})
}

// SetCheckRuns sets one commit's checks from details that carry their own sha, so a test
// can put a green run, a current red and a stale red on the same rollup and see which
// bucket each lands in.
func (f *FakeHost) SetCheckRuns(oid string, details ...CheckDetail) {
	var c Checks
	for _, d := range details {
		c.AddRun(d.Name, d.Conclusion, d.SHA)
	}
	f.ChecksBy[oid] = c
}

// Verdicts is the reads a test says this pull request carries (#1572). A host with no
// entry for a pull request carries none, which is the ordinary case.
func (f *FakeHost) Verdicts(n int) ([]Verdict, error) {
	if f.VerdictErr != nil {
		return nil, f.VerdictErr
	}
	return append([]Verdict(nil), f.Reads[n]...), nil
}

// SetVerdicts records what the forge says a pull request's readers have said.
func (f *FakeHost) SetVerdicts(n int, vs ...Verdict) {
	if f.Reads == nil {
		f.Reads = map[int][]Verdict{}
	}
	f.Reads[n] = append(f.Reads[n], vs...)
}
