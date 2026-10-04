package cardcontract

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompleteResultKeepsEvidenceAndUsesResolvedCommits(t *testing.T) {
	t.Parallel()
	head := strings.Repeat("a", 40)
	source := strings.Repeat("b", 40)
	for _, width := range []int{9, 13, 14, 12, 40} {
		t.Run(strings.Repeat("a", width), func(t *testing.T) {
			t.Parallel()
			raw := "head: " + head[:9] + "\nbranch: copied-name\nverdict: not-done\ngate: go test\noutput: gate.log\nreport: source " + source + "\n\n## Body\n\nstep 3: broken " + head[:width] + " actual tests failed\nRated source: " + source + "\n"
			before := raw
			got, err := CompleteResult([]byte(raw), head, "owned-branch", func(ref string) (string, error) {
				require.True(t, strings.HasPrefix(head, ref))
				return head, nil
			}, func(ref string) error {
				assert.Equal(t, "gate.log", ref)
				return nil
			})
			require.NoError(t, err)
			assert.Contains(t, string(got), "head: "+head+"\nbranch: owned-branch\nverdict: not-done")
			assert.Contains(t, string(got), "step 3: broken "+head+" actual tests failed")
			assert.Contains(t, string(got), "Rated source: "+source)
			assert.Contains(t, string(got), "report: source "+source)
			assert.Equal(t, before, raw, "the original result stays evidence")
		})
	}
}

func TestCompleteResultRefusesUnknownMetadata(t *testing.T) {
	t.Parallel()
	head := strings.Repeat("a", 40)
	base := "head: " + head + "\nbranch: b\nverdict: ok\ngate: -\noutput: -\nreport: completed\n\n## Body\nstep 3: ok " + head[:9] + " measured\n"
	for _, tc := range []struct {
		name, raw string
		resolve   func(string) (string, error)
		artifact  func(string) error
	}{
		{"missing verdict", strings.Replace(base, "verdict: ok\n", "", 1), nil, nil},
		{"unknown verdict", strings.Replace(base, "verdict: ok", "verdict: maybe", 1), nil, nil},
		{"no commit", strings.Replace(base, "head: "+head, "head: -", 1), nil, nil},
		{"wrong result commit", base, func(string) (string, error) { return strings.Repeat("c", 40), nil }, nil},
		{"unknown or ambiguous step", base, func(ref string) (string, error) {
			if ref == head {
				return head, nil
			}
			return "", errors.New("unknown or ambiguous commit")
		}, nil},
		{"missing artifact", strings.Replace(base, "output: -", "output: missing.log", 1), nil, func(string) error { return errors.New("not found") }},
		{"step is a word", strings.Replace(base, head[:9]+" measured", "committed measured", 1), nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resolve := tc.resolve
			if resolve == nil {
				resolve = func(string) (string, error) { return head, nil }
			}
			artifact := tc.artifact
			if artifact == nil {
				artifact = func(string) error { return nil }
			}
			got, err := CompleteResult([]byte(tc.raw), head, "owned", resolve, artifact)
			require.Error(t, err)
			assert.Nil(t, got, "a refusal supplies no completed result")
		})
	}
}

func TestCompleteResultNamesBothHeadsWhenTheStatedOneIsNotTheTip(t *testing.T) {
	t.Parallel()
	head, stated := strings.Repeat("a", 40), strings.Repeat("c", 40)
	raw := "head: " + stated + "\nbranch: b\nverdict: ok\ngate: -\noutput: -\nreport: done\n"
	got, err := CompleteResult([]byte(raw), head, "owned", func(string) (string, error) { return stated, nil }, func(string) error { return nil })
	require.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), stated)
	assert.Contains(t, err.Error(), head)
}

func TestUnformattedNamesEveryFileGofmtListed(t *testing.T) {
	t.Parallel()
	for _, list := range []string{"", "\n", " \n\n"} {
		assert.NoError(t, Unformatted(list), "%q: nothing listed is nothing to refuse", list)
	}
	err := Unformatted("cmd/a.go\n\ninternal/b_test.go\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "2 unformatted")
	assert.Contains(t, err.Error(), "cmd/a.go internal/b_test.go")
	assert.Contains(t, err.Error(), "gofmt -w", "the remedy")
}
