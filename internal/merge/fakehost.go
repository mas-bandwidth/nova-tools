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
	Readied  []int
	Atomic   bool
	Merges   []string
	Err      error
}

// NewFakeHost returns an empty one.
func NewFakeHost() *FakeHost {
	return &FakeHost{PRs: map[int]PR{}, Branches: map[string]string{}, ChecksBy: map[string]Checks{}}
}

func (f *FakeHost) PR(n int) (PR, error) {
	if f.Err != nil {
		return PR{}, f.Err
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

// Checks answers the buckets a test set for that commit. An unknown commit has no checks
// at all, which is PENDING and not green -- the same answer the real host gives for a
// pull request whose workflows have not been queued.
func (f *FakeHost) Checks(oid string) (Checks, error) {
	if f.Err != nil {
		return Checks{}, f.Err
	}
	return f.ChecksBy[oid], nil
}

func (f *FakeHost) Ready(n int) error {
	f.Readied = append(f.Readied, n)
	return nil
}

func (f *FakeHost) AtomicMerge() bool { return f.Atomic }

func (f *FakeHost) Merge(n int, headOID, baseSHA, mergeSHA string) error {
	if !f.Atomic {
		return fmt.Errorf("this fake host offers no two-precondition merge and Merge was called anyway")
	}
	f.Merges = append(f.Merges, fmt.Sprintf("%d %s %s %s", n, headOID, baseSHA, mergeSHA))
	return nil
}

// SetChecks is the shorthand a test uses to say what a commit's evidence is.
func (f *FakeHost) SetChecks(oid string, green, pending int, red ...string) {
	c := Checks{Green: green, Pending: pending, Red: len(red), RedNames: red}
	f.ChecksBy[oid] = c
}
