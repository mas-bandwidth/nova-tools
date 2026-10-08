package sprint

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeLandRemote is a forge that records what a protected land asked of it and
// never dials a host.
type fakeLandRemote struct {
	url         string
	merged      bool
	mergeSHA    string
	pushErr     error
	openErr     error
	autoAllowed bool
	pushed      []string
	pushedSHA   []string
	base, head  string
	title, body string
	autoAsked   bool
	polls       int
	pollErr     error
}

func (f *fakeLandRemote) Push(ref, sha string) error {
	f.pushed = append(f.pushed, ref)
	f.pushedSHA = append(f.pushedSHA, sha)
	return f.pushErr
}

func (f *fakeLandRemote) OpenPullRequest(base, head, title, body string) (string, error) {
	f.base, f.head, f.title, f.body = base, head, title, body
	if f.openErr != nil {
		return "", f.openErr
	}
	return f.url, nil
}

func (f *fakeLandRemote) EnableAutoMerge(string) (bool, error) {
	f.autoAsked = true
	return f.autoAllowed, nil
}

func (f *fakeLandRemote) PullRequestMerged(string) (bool, string, error) {
	f.polls++
	if f.pollErr != nil {
		return false, "", f.pollErr
	}
	return f.merged, f.mergeSHA, nil
}

// A stream marked land-protected stays merging when its land pushes land/<stream>
// and opens a pull request, and lands only once that pull request merges. A
// failed push or a refused pull request is a judgment naming the remote's words,
// and the card is not landed (docs/SPEC-SPRINT.md section 7, the protected branches).
func TestALandProtectedStreamIsLandedOnlyWhenItsPullRequestMerges(t *testing.T) {
	t.Parallel()
	const repo = "mas-bandwidth/nova-sprint"
	const tip = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	w := setup(t, 1)
	accepted(w, "s1-1")
	head := w.s.Work.Card("s1-1").F("head")
	w.must(Set(w.s, SetReq{Streams: []string{"s1"}, LandProtected: repo, Who: "coordinator"}))

	require.False(t, LandProtectedDefers(w.s, "s1", repo, "main", "/tmp/land.git"), "a local bare remote is not a forge: the rigs push main directly")
	require.False(t, LandProtectedDefers(w.s, "s1", repo, "main", "file:///tmp/land.git"), "a file URL is the lander's own clone, not a forge")
	require.True(t, LandProtectedDefers(w.s, "s1", repo, "main", "https://forge.example.invalid/mas-bandwidth/nova-sprint.git"))
	require.True(t, LandProtectedDefers(w.s, "s1", repo, "main", "git@forge.example.invalid:mas-bandwidth/nova-sprint.git"))
	require.False(t, LandProtectedDefers(w.s, "s1", repo, "sprint/mechanical-2026-10-02", "https://forge.example.invalid/mas-bandwidth/nova-sprint.git"), "a sprint branch is not a protected branch")

	remote := &fakeLandRemote{url: "https://forge.example.invalid/mas-bandwidth/nova-sprint/pull/18", autoAllowed: true}
	req := LandProtectedReq{Stream: "s1", Repo: repo, Base: "main", Tip: tip, Who: "coordinator",
		Cards: []LandProtectedCard{{ID: "s1-1", Head: head}}}

	out, err := LandProtectedDrive(w.s, remote, req)
	require.NoError(t, err)
	require.Equal(t, "opened", out.Kind)
	require.Equal(t, []string{LandBranchRef("s1")}, remote.pushed, "the land branch is pushed, not the protected branch")
	require.NotContains(t, remote.pushed, "refs/heads/main")
	require.Equal(t, []string{tip}, remote.pushedSHA)
	require.Equal(t, "main", remote.base)
	require.Equal(t, LandBranch("s1"), remote.head)
	require.Contains(t, remote.title, "s1-1")
	require.Contains(t, remote.body, "s1-1")
	require.Contains(t, remote.body, "head="+head)
	require.Contains(t, remote.body, "reader-")
	require.True(t, remote.autoAsked, "auto-merge is asked when the repository may allow it")
	w.must(out.Plan)
	require.Equal(t, Merging, w.state("s1-1"), "opening the pull request is not a landing")
	require.Equal(t, remote.url, w.s.Work.Card("s1-1").F(FieldLandPR))
	require.Equal(t, tip, w.s.Work.Card("s1-1").F(FieldLandHead))
	require.Equal(t, Queued, w.s.Merge.Placed("s1-1").Col)
	w.clean("opened")

	req.Tip = ""
	out, err = LandProtectedDrive(w.s, remote, req)
	require.NoError(t, err)
	require.Equal(t, "waiting", out.Kind)
	w.must(out.Plan)
	require.Equal(t, Merging, w.state("s1-1"), "a pull request that has not merged is not a landing")
	require.Len(t, remote.pushed, 1, "a recorded pull request is not pushed again")

	remote.pollErr = errors.New("gh pr view: API rate limit exceeded")
	out, err = LandProtectedDrive(w.s, remote, req)
	require.NoError(t, err)
	require.Equal(t, "opened", out.Kind)
	require.Empty(t, out.Fact.Refused, "a failed read of an open pull request is not a refusal")
	require.Contains(t, out.Fact.Poll, "rate limit")
	require.Equal(t, remote.url, out.Fact.URL)
	w.must(out.Plan)
	require.Equal(t, Merging, w.state("s1-1"), "a poll error leaves the card merging")
	require.Empty(t, w.notesOf(NLandProtectedRefused))
	remote.pollErr = nil

	remote.merged, remote.mergeSHA = true, "cccccccccccccccccccccccccccccccccccccccc"
	out, err = LandProtectedDrive(w.s, remote, req)
	require.NoError(t, err)
	require.Equal(t, "landed", out.Kind)
	require.Equal(t, remote.mergeSHA, out.Fact.MergeSHA)
	w.must(out.Plan)
	require.Equal(t, Landed, w.state("s1-1"), "the card lands when the pull request merges")
	require.Equal(t, Merged, w.s.Merge.Placed("s1-1").Col)
	req.Tip = tip

	refused := setup(t, 1)
	accepted(refused, "s1-1")
	refused.must(Set(refused.s, SetReq{Streams: []string{"s1"}, LandProtected: repo, Who: "coordinator"}))
	words := "remote: GH006: Protected branch update failed for refs/heads/main"
	badPush := &fakeLandRemote{pushErr: errors.New(words)}
	out, err = LandProtectedDrive(refused.s, badPush, req)
	require.NoError(t, err)
	require.Equal(t, "refused", out.Kind)
	refused.must(out.Plan)
	require.Equal(t, Merging, refused.state("s1-1"), "a failed push records nothing landed")
	require.Empty(t, refused.s.Work.Card("s1-1").F(FieldLandPR))
	notes := refused.notesOf(NLandProtectedRefused)
	require.Len(t, notes, 1)
	require.Contains(t, notes[0].What, "GH006: Protected branch update failed")
	require.Empty(t, badPush.body, "a failed push opens no pull request")

	again, err := LandProtectedDrive(refused.s, badPush, req)
	require.NoError(t, err)
	require.Equal(t, "waiting", again.Kind, "the same refusal does not raise a second judgment")
	require.Empty(t, again.Fact.URL, "a refused push recorded no pull request")
	require.Contains(t, again.Fact.Refused, "GH006")
	require.Len(t, refused.notesOf(NLandProtectedRefused), 1)

	denied := setup(t, 1)
	accepted(denied, "s1-1")
	denied.must(Set(denied.s, SetReq{Streams: []string{"s1"}, LandProtected: repo, Who: "coordinator"}))
	badPR := &fakeLandRemote{openErr: errors.New("pull request creation failed: Resource not accessible by integration")}
	out, err = LandProtectedDrive(denied.s, badPR, req)
	require.NoError(t, err)
	require.Equal(t, "refused", out.Kind)
	denied.must(out.Plan)
	require.Equal(t, Merging, denied.state("s1-1"))
	require.Empty(t, denied.s.Work.Card("s1-1").F(FieldLandPR))
	require.Contains(t, denied.notesOf(NLandProtectedRefused)[0].What, "Resource not accessible by integration")
}
