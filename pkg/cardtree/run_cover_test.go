package cardtree

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRunCoverLineRendersTheVerdictLine(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		r    Result
		want string
	}{
		{"an ok step with its commit", Result{Num: "1", Verdict: OK, Sha: "0123456789abcdef0123456789abcdef01234567", Words: "post holds"},
			"step 1: ok 0123456789abcdef0123456789abcdef01234567 post holds"},
		{"a step that committed nothing shows -", Result{Num: "2.1", Verdict: NotDone, Sha: "", Words: "the result carries no line for this step"},
			"step 2.1: not-done - the result carries no line for this step"},
		{"empty words leave no trailing space", Result{Num: "3", Verdict: Skipped, Sha: ""},
			"step 3: skipped -"},
		{"the words' padding and line breaks collapse to single spaces", Result{Num: "4", Verdict: Broken, Sha: "abc", Words: "  a\nb\t c  "},
			"step 4: broken abc a b c"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.r.Line())
		})
	}
}

func TestRunCoverLineCapsRunawayWordsWithAMark(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, want string
		words      string
	}{
		{"an ascii word over 300 bytes is cut and says what was dropped",
			strings.Repeat("a", 292) + "...+28B", strings.Repeat("a", 320)},
		{"the cut never lands inside a multibyte rune",
			strings.Repeat("é", 146) + "...+108B", strings.Repeat("é", 200)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := (Result{Num: "5", Verdict: OK, Sha: "abc", Words: tc.words}).Line()
			assert.Equal(t, "step 5: ok abc "+tc.want, got)
			assert.LessOrEqual(t, len(got), 300+len("step 5: ok abc "), "the line stays bounded, the mark is the say-so")
		})
	}
}
