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

func TestCompleteResultRecordsTheTipAndNotesWhatTheChildStated(t *testing.T) {
	t.Parallel()
	head, stated := strings.Repeat("a", 40), strings.Repeat("c", 40)
	resolve := func(ref string) (string, error) {
		if ref == stated {
			return stated, nil
		}
		return "", errors.New("unknown")
	}
	none := func(string) error { return nil }
	for name, tc := range map[string]struct{ raw, note string }{
		"a head that is not the tip": {"head: " + stated + "\nbranch: b\nverdict: ok\ngate: -\noutput: -\nreport: done\n\n## Body\nstep 3: ok - words\n",
			"finish: the result named head " + stated + ", not the checkout's tip " + head + ", which is recorded"},
		"a head that is no commit": {"head: zzz\nbranch: b\nverdict: ok\ngate: -\noutput: -\nreport: done\n",
			"finish: the result named head zzz, not the checkout's tip " + head + ", which is recorded"},
		"no commit named": {"head: -\nbranch: b\nverdict: ok\ngate: -\noutput: -\nreport: done\n",
			"finish: the result named no commit; the checkout's tip " + head + " is recorded"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := CompleteResult([]byte(tc.raw), head, "owned", resolve, none)
			require.NoError(t, err)
			text := string(got)
			assert.True(t, strings.HasPrefix(text, "head: "+head+"\nbranch: owned\n"), text)
			assert.Contains(t, text, "## Body")
			assert.True(t, strings.HasSuffix(text, tc.note+"\n"), text)
			assert.Equal(t, 1, strings.Count(text, "## Body"))
		})
	}
	got, err := CompleteResult([]byte("head: "+head+"\nbranch: b\nverdict: ok\ngate: -\noutput: -\nreport: done\n"), head, "owned", func(string) (string, error) { return head, nil }, none)
	require.NoError(t, err)
	assert.NotContains(t, string(got), "finish:", "the tip stated is the tip: no note")
}

func TestGofmtListedIsEveryFileNamedOnePerLine(t *testing.T) {
	t.Parallel()
	for _, list := range []string{"", "\n", " \n\n"} {
		assert.Empty(t, GofmtListed(list), "%q: nothing listed", list)
	}
	assert.Equal(t, []string{"cmd/a.go", "internal/b_test.go"}, GofmtListed("cmd/a.go\n\n internal/b_test.go \n"))
}
