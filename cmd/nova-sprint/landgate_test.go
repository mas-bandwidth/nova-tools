package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/decide"
)

// oneClass answers every failure of the gate decision with one class's probabilities; err,
// when set, is every answer.
type oneClass struct {
	p    map[string]float64
	err  error
	asks int
}

func (o *oneClass) Name() string { return "fake" }

func (o *oneClass) Ask(context.Context, decide.Schema, string) (map[string]decide.Answer, decide.Usage, error) {
	o.asks++
	if o.err != nil {
		return nil, decide.Usage{}, o.err
	}
	best := decide.Caused
	for k, v := range o.p {
		if v > o.p[best] {
			best = k
		}
	}
	return map[string]decide.Answer{"class": {Type: decide.Choice, Value: best, P: o.p}}, decide.Usage{}, nil
}

// redCheck writes a check script that is red on its first run, go test's output naming
// TestA in m/p, and green after (again: red every run); its --check word.
func redCheck(t *testing.T, dir string, again bool) string {
	gate := "printf '%s\\n' '--- FAIL: TestA (1.02s)' '    a_test.go:9: timed out after 1s' 'FAIL' 'FAIL\tm/p\t1.1s'\nexit 1\n"
	script := gate
	if !again {
		script = "if [ -f " + filepath.Join(dir, "ran") + " ]; then exit 0; fi\ntouch " + filepath.Join(dir, "ran") + "\n" + gate
	}
	path := filepath.Join(dir, "check.sh")
	require.NoError(t, os.WriteFile(path, []byte(script), 0o600))
	return "'sh " + path + "'"
}

// A red batch check is decided failure by failure (docs/SPEC-SPRINT.md section 7, the
// lander's gate): no failure caused and one flaky at the bar, the check runs once more, and
// green lands the batch, the rerun's result the decision's outcome (flaky); red again is
// red, with the decision named, and red-again its outcome. A caused failure is red at once,
// the check run once. With the bars unset (the sprint row's default) each failure is still
// decided, recorded and shown in the red reason, and nothing is rerun.
func TestLandRerunsARedCheckOnceWhenItsFailuresAreFlaky(t *testing.T) {
	t.Parallel()
	flaky := map[string]float64{decide.Flaky: 0.9, decide.Caused: 0.05, decide.PreExisting: 0.05}
	caused := map[string]float64{decide.Flaky: 0.1, decide.Caused: 0.85, decide.PreExisting: 0.05}
	for _, tc := range []struct {
		name    string
		bars    bool
		p       map[string]float64
		again   bool
		code    int
		says    string
		outcome string
		asks    int
	}{
		{"flaky, green on the rerun", true, flaky, false, 0, "LAND OK stream=s1 cards=1", decide.Flaky, 1},
		{"flaky, red again", true, flaky, true, 1, "(run once more: the gate decision classed TestA flaky, op land/s1@", decide.RedAgain, 1},
		{"caused", true, caused, false, 1, "TestA:caused:0.85; caused, not rerun)", "", 1},
		{"bars unset: recorded and shown, nothing rerun", false, flaky, false, 1, "(the gate decision, land/s1@", "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newLandRig(t)
			if tc.bars {
				r.m.SetGateBars("0.8", "0.8")
			}
			fake := &oneClass{p: tc.p}
			r.a.gateBackend = func() (decide.Backend, func() time.Time) {
				return fake, func() time.Time { return time.Date(2026, 10, 3, 8, 0, 0, 0, time.UTC) }
			}
			r.ok("add --stream s1 --count 1 --one")
			r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n")}, "s1-1")
			code, out, errs := r.do("land --repo-dir " + r.clone + " --base main --check " + redCheck(t, r.dir, tc.again))
			assert.Equal(t, tc.code, code, out+errs)
			assert.Contains(t, out+errs, tc.says)
			assert.Equal(t, tc.asks, fake.asks)
			if !tc.bars {
				assert.Contains(t, out+errs, "TestA:flaky:0.90; recorded; the flaky bar is unset, so nothing is rerun)")
			}
			ds, err := decide.Load(filepath.Join(r.dir, "land", "decide", "gate.jsonl"))
			require.NoError(t, err)
			require.Len(t, ds, tc.asks)
			if tc.asks == 0 {
				return
			}
			assert.Regexp(t, `^land/s1@[0-9a-f]{12}@gate/m/p\.TestA$`, ds[0].ID)
			label := ""
			if ds[0].Outcome != nil {
				label = ds[0].Outcome.Label
			}
			assert.Equal(t, tc.outcome, label)
		})
	}
}

// A red batch check whose gate decision cannot be made (no key in land's environment, a
// backend that fails) is red as before, with why no decision was made, and records nothing;
// the check is not run again, whatever the bars say.
func TestLandKeepsARedBatchRedWithNoGateDecision(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		backend decide.Backend // nil: none, and land's environment holds no key
		says    string
	}{
		{"no key", nil, "(no gate decision: JEV_API_KEY is absent from land's environment)"},
		{"a backend that fails", &oneClass{err: errors.New("HTTP 402: no credits")}, "(no gate decision: HTTP 402: no credits)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newLandRig(t)
			r.m.SetGateBars("0.8", "0.8")
			if tc.backend != nil {
				r.a.gateBackend = func() (decide.Backend, func() time.Time) {
					return tc.backend, func() time.Time { return time.Date(2026, 10, 3, 8, 0, 0, 0, time.UTC) }
				}
			}
			r.ok("add --stream s1 --count 1 --one")
			r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n")}, "s1-1")
			code, out, errs := r.do("land --repo-dir " + r.clone + " --base main --check " + redCheck(t, r.dir, false))
			assert.Equal(t, 1, code, out+errs)
			assert.Contains(t, out+errs, tc.says)
			assert.NotContains(t, out+errs, "LAND OK")
			ds, err := decide.Load(filepath.Join(r.dir, "land", "decide", "gate.jsonl"))
			require.NoError(t, err)
			assert.Empty(t, ds)
		})
	}
}
