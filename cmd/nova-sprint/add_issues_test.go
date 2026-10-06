package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/forge"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// issueBrief is a passing brief on nova-tools whose ISSUES: line is issues.
func issueBrief(lead, issues string) string {
	return passingBrief(lead + "\nREPO: mas-bandwidth/nova-tools\nISSUES: " + issues)
}

// add refuses a brief whose issue reference names a repository other than its REPO: in the
// short form, owner/name#N, exit 2, nothing written, naming the line and the full URL to
// write instead; the full URL of the same issue is admitted, and so is #N read against REPO:
// (docs/SPEC-SPRINT.md section 7, "A landing closes the card's issues").
func TestAddRefusesAnIssueOfAnotherRepositoryUnlessByFullURL(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.md")
	require.NoError(t, os.WriteFile(bad, []byte(issueBrief("Fix bad.", "#3, mas-bandwidth/ideas#7")), 0o600))
	code, out, errs := ta.do("add --stream s1 --one --brief-file " + bad)
	require.Equal(t, 2, code, "a short reference to another repository: %s", errs)
	assert.Contains(t, errs, ": "+IssueRepoCheck+": 3: mas-bandwidth/ideas#7 names mas-bandwidth/ideas, and the card's REPO: is mas-bandwidth/nova-tools")
	assert.Contains(t, errs, forge.Web+"mas-bandwidth/ideas/issues/7")
	assert.NotContains(t, out, "MOVED", "nothing written")
	good := filepath.Join(dir, "good.md")
	require.NoError(t, os.WriteFile(good, []byte(issueBrief("Fix good.", "#3, "+forge.Web+"mas-bandwidth/ideas/issues/7")), 0o600))
	assert.Contains(t, ta.ok("add --stream s1 --one --brief-file "+good), "MOVED good -> ready")
}

// downForge is a forge that refuses every close while down, and closes otherwise.
type downForge struct {
	down   bool
	closed []string
}

func (f *downForge) Close(_ context.Context, i forge.Issue, _ string) (bool, error) {
	if f.down {
		return false, errors.New("gh: could not connect to api.github.com")
	}
	f.closed = append(f.closed, i.String())
	return false, nil
}

// A landing whose close the forge refused stays landed, and where shows the pending close,
// with the forge's words, until the server loop's closer closes it after the retry.
func TestWhereShowsThePendingClosesUntilTheForgeTakesThem(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte(issueBrief("Fix a.", "#12")), 0o600))
	ta.ok("add --stream s1 --brief-dir " + dir)
	ta.deal(100)
	ta.ok("take --as m1 --limit 100")
	var q struct{ Cards []queueCard }
	ta.json("queue --as m1", &q)
	require.Len(t, q.Cards, 1)
	ta.ok("finish --as m1 " + q.Cards[0].ID + "@" + strconv.Itoa(q.Cards[0].Gen))
	ta.ok("ask --limit 100")
	ta.ok("read --as reader-a --ok --limit 100")
	ta.ok("read --as reader-b --ok --limit 100")
	ta.ok("accept --read-ok")
	ta.ok("merge --stream s1 --batch 1")
	require.Equal(t, string(sprint.Landed), ta.primary("a").Col)
	assert.NotContains(t, ta.ok("where"), "ISSUES PENDING", "before the closer's first pass where says nothing")

	f := &downForge{down: true}
	ta.a.issueForge = f
	st := &store.Store{B: ta.m, Names: sprint.Names{}, Now: ta.a.now}
	var said []string
	ta.a.closeIssuesTick(context.Background(), st, func(l string) { said = append(said, l) })
	require.Len(t, said, 1)
	assert.Contains(t, said[0], "ISSUES a issues: pending (the forge refused the close of mas-bandwidth/nova-tools#12")
	assert.Equal(t, string(sprint.Landed), ta.primary("a").Col, "the landing stands")
	frame := ta.ok("where")
	assert.Contains(t, frame, "ISSUES PENDING 1 issues of 1 landed cards: a mas-bandwidth/nova-tools#12 (the forge refused the close of mas-bandwidth/nova-tools#12: gh: could not connect to api.github.com)")
	var v whereView
	ta.json("where", &v)
	assert.True(t, strings.HasPrefix(v.IssuesPending, "1 issues of 1 landed cards"), "--json carries it: %q", v.IssuesPending)

	f.down = false
	said = nil
	ta.a.closeIssuesTick(context.Background(), st, func(l string) { said = append(said, l) })
	assert.Empty(t, said, "inside the retry the closer waits")
	ta.a.sleep(sprint.IssuesRetry + time.Second)
	ta.a.closeIssuesTick(context.Background(), st, func(l string) { said = append(said, l) })
	assert.Equal(t, []string{"mas-bandwidth/nova-tools#12"}, f.closed)
	assert.NotContains(t, ta.ok("where"), "ISSUES PENDING")
	var after whereView
	ta.json("where", &after)
	assert.Empty(t, after.IssuesPending)
}

// noForge is the test app's forge: every close succeeds and nothing is asked of a forge.
type noForge struct{}

func (noForge) Close(context.Context, forge.Issue, string) (bool, error) { return false, nil }
