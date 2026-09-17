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
	// CreatePR and ClosePR are the fold verb's forge operations (docs/SPEC-MERGE.md
	// "The fold (#1142)"). Created is one line per opened pull request and Closed is
	// the numbers closed as superseded by a squash; nextPR hands out numbers so a fold
	// reads one back on its FOLD OK line.
	Created []string
	Closed  []int
	nextPR  int
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
}

// NewFakeHost returns an empty one.
func NewFakeHost() *FakeHost {
	return &FakeHost{PRs: map[int]PR{}, Branches: map[string]string{}, ChecksBy: map[string]Checks{}}
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

// CreatePR records one opened pull request and hands back a fresh number. It is the
// fold verb's forge side (docs/SPEC-MERGE.md "The fold (#1142)").
func (f *FakeHost) CreatePR(head, base, title, body string) (int, error) {
	if f.Err != nil {
		return 0, f.Err
	}
	if f.nextPR == 0 {
		f.nextPR = 900
	}
	f.nextPR++
	n := f.nextPR
	f.Created = append(f.Created, fmt.Sprintf("head=%s base=%s title=%s body=%s", head, base, title, body))
	if f.PRs == nil {
		f.PRs = map[int]PR{}
	}
	f.PRs[n] = PR{Number: n, Base: base, HeadRef: head, Body: body}
	return n, nil
}

// ClosePR records a folded pull request closed as superseded by the squash.
func (f *FakeHost) ClosePR(n int) error {
	if f.Err != nil {
		return f.Err
	}
	f.Closed = append(f.Closed, n)
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
