package cardcontract

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cardURL is the card's repository as the frame names it; the shims never reach it (a
// clone of it is a link, a push is a line), so it is a name only.
const cardURL = "https://example.com/Example-Owner/example-repo.git"

func TestJobTextCarriesTheAttemptBefore(t *testing.T) {
	t.Parallel()
	f := Frame{Kind: "work", Card: "c1.w2", Attempt: 2, Tier: "pro", Repo: cardURL, BaseRef: "main", Branch: "sprint/c1.w2",
		PrevHead: "0123456789abcdef0123456789abcdef01234567", Finding: "f.go:12 the bound is not asserted", Rules: "RULES\nNever force-push."}
	s := Staged{Job: "/j", Repo: "/j/repo", Head: "0123456789abcdef0123456789abcdef01234567"}
	for _, family := range []string{"claude", "plain"} {
		text := For(family).JobText(f, s)
		for _, want := range []string{"Attempt 2", "previous attempt's head is 0123456789abcdef0123456789abcdef01234567", "f.go:12 the bound is not asserted", "Tier: pro.", "Never force-push."} {
			assert.Contains(t, text, want, family)
		}
	}
	f.Kind = "read"
	assert.Contains(t, For("claude").JobText(f, s), "gh pr review --request-changes")
	assert.Contains(t, For("plain").JobText(f, s), "verdict: ok | broken")
}

func TestFamilyOfAModelId(t *testing.T) {
	t.Parallel()
	for model, family := range map[string]string{
		"anthropic/claude-opus-5-5": "claude", "claude-sonnet": "claude", "openai/gpt-6": "openai",
		"google/gemini-4-argon": "gemini", "xai/grok-5": "grok", "deepseek/deepseek-chat": "deepseek",
		"local/qwen3": "plain", "": "plain",
	} {
		assert.Equal(t, family, FamilyOf(model), model)
	}
	for _, f := range Families {
		assert.Equal(t, f, For(f).Family())
	}
}

func TestParseResultHoldsTheShape(t *testing.T) {
	t.Parallel()
	whole := "head: 0123456789abcdef0123456789abcdef01234567\nbranch: b\nverdict: ok\ngate: go test ./x\noutput: -\nreport: done it\ntitle: T\n\n## Body\n\nline one\nhead: not a key here\n"
	r := ParseResult([]byte(whole))
	assert.True(t, r.Shaped)
	assert.Equal(t, "line one\nhead: not a key here", r.Body)
	assert.Equal(t, "T", r.Title)
	for name, text := range map[string]string{
		"no report":     strings.Replace(whole, "report: done it\n", "", 1),
		"empty report":  strings.Replace(whole, "report: done it", "report:", 1),
		"no gate":       strings.Replace(whole, "gate: go test ./x\n", "", 1),
		"a bad head":    strings.Replace(whole, "0123456789abcdef0123456789abcdef01234567", "HEAD", 1),
		"a bad verdict": strings.Replace(whole, "verdict: ok", "verdict: done", 1),
		"the old shape": "rev: 0123456789abcdef0123456789abcdef01234567\nverdict: ok\n\n## One line\n\nx\n",
		"nothing":       "",
	} {
		assert.False(t, ParseResult([]byte(text)).Shaped, name)
	}
	assert.True(t, ParseResult([]byte(strings.Replace(whole, "0123456789abcdef0123456789abcdef01234567", "-", 1))).Shaped, "a not-done result may name no head")
}

func TestAFrameRoundTripsAndIsRefusedWithoutAKind(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := Frame{Kind: "work", Card: "c1", Attempt: 2, Repo: cardURL, StageSha: "0123456789abcdef0123456789abcdef01234567"}
	p := filepath.Join(dir, "f.json")
	require.NoError(t, WriteFrame(p, f))
	got, err := ReadFrame(p)
	require.NoError(t, err)
	assert.Equal(t, f, got)
	require.NoError(t, WriteFrame(p, Frame{Card: "c1"}))
	_, err = ReadFrame(p)
	assert.ErrorContains(t, err, "wants kind work or read")
	assert.Equal(t, "Read /j/JOB.md first.\n\ncard", Prompt("/j", "card"))
}
