package github

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A reference is explicit: an ISSUES: line's tokens and a closing keyword's object; a short
// form naming another repository is refused with the full URL as its remedy, and a number
// in prose is none.
func TestRefsReadsOnlyExplicitReferences(t *testing.T) {
	t.Parallel()
	text := "c: x\nREPO: mas-bandwidth/nova-tools\nISSUES: #1, other/repo#2 " + Web + "mas-bandwidth/ideas/issues/3\n\n#4327 deleted it. Fixes #5. closes mas-bandwidth/nova-tools#6"
	refs := Refs(text, BriefRepo(text))
	var got []string
	for _, r := range refs {
		if r.Why != "" {
			got = append(got, "!"+r.Text)
			continue
		}
		got = append(got, r.Issue.String())
	}
	assert.Equal(t, []string{"mas-bandwidth/nova-tools#1", "!other/repo#2", "mas-bandwidth/ideas#3", "mas-bandwidth/nova-tools#5", "mas-bandwidth/nova-tools#6"}, got)
	assert.Contains(t, refs[1].Why, Web+"other/repo/issues/2")
	assert.Equal(t, 3, refs[1].Line)
	assert.Contains(t, Refs("ISSUES: #9", "")[0].Why, "names no repository")
}

func TestSlugReadsEverySpelling(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"mas-bandwidth/nova-tools", Web + "mas-bandwidth/nova-tools.git", "git@github.com:mas-bandwidth/nova-tools", "ssh://git@github.com/mas-bandwidth/nova-tools/"} {
		got, ok := Slug(s)
		assert.True(t, ok, s)
		assert.Equal(t, "mas-bandwidth/nova-tools", got, s)
	}
	for _, s := range []string{"", "/srv/repo.git", "https://example.invalid/a/b", "../x"} {
		_, ok := Slug(s)
		assert.False(t, ok, s)
	}
}

// GH reads the state first: a closed issue is left alone, an open one is closed with the
// comment, an open one holding the comment already is closed with no second one, and gh's
// refusal is the error.
func TestGHClosesOnlyAnOpenIssue(t *testing.T) {
	t.Parallel()
	var calls []string
	view := `{"state":"OPEN","comments":[{"body":"a person's word"}]}`
	g := GH{Run: func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[1] == "view" {
			return view, nil
		}
		return "", nil
	}}
	i := Issue{Repo: "o/r", N: 4}
	already, err := g.Close(context.Background(), i, "done by a\n\n- more")
	require.NoError(t, err)
	assert.False(t, already)
	assert.Equal(t, []string{"issue view 4 --repo o/r --json state,comments", "issue close 4 --repo o/r --comment done by a\n\n- more"}, calls)

	calls, view = nil, `{"state":"OPEN","comments":[{"body":"done by a\n\n- more"}]}`
	already, err = g.Close(context.Background(), i, "done by a\n\n- more")
	require.NoError(t, err)
	assert.False(t, already)
	assert.Equal(t, []string{"issue view 4 --repo o/r --json state,comments", "issue close 4 --repo o/r"}, calls, "the comment is there: closed with no second one")

	calls, view = nil, `{"state":"CLOSED","comments":[]}`
	already, err = g.Close(context.Background(), i, "done")
	require.NoError(t, err)
	assert.True(t, already)
	assert.Len(t, calls, 1, "a closed issue is not closed again")

	view = `not json`
	_, err = g.Close(context.Background(), i, "done")
	assert.ErrorContains(t, err, "not an issue")
	g.Run = func(context.Context, ...string) (string, error) { return "", errors.New("gh issue view: no network") }
	_, err = g.Close(context.Background(), i, "done")
	assert.ErrorContains(t, err, "no network")
}
