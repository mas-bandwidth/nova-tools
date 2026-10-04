package decide

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Rerun returns the failures routed Flaky, the gate's reruns; an empty or
// non-flaky result is none.
func TestGateCoverRerunReturnsFlakyFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		result GateResult
		want   []Failure
	}{
		{
			"only flaky",
			GateResult{Calls: []GateCall{
				{Failure: Failure{Pkg: "m/p", Test: "TestA"}, Route: Flaky},
				{Failure: Failure{Pkg: "m/p", Test: "TestB"}, Route: Flaky},
			}},
			[]Failure{{Pkg: "m/p", Test: "TestA"}, {Pkg: "m/p", Test: "TestB"}},
		},
		{
			"flaky among others",
			GateResult{Calls: []GateCall{
				{Failure: Failure{Pkg: "m/p", Test: "TestA"}, Route: Flaky},
				{Failure: Failure{Pkg: "m/p", Test: "TestB"}, Route: PreExisting},
				{Failure: Failure{Pkg: "m/p", Test: "TestC"}, Route: Caused},
			}},
			[]Failure{{Pkg: "m/p", Test: "TestA"}},
		},
		{
			"none flaky is none rerun",
			GateResult{Calls: []GateCall{
				{Failure: Failure{Pkg: "m/p", Test: "TestA"}, Route: PreExisting},
				{Failure: Failure{Pkg: "m/b"}, Route: Caused},
			}},
			nil,
		},
		{
			"no calls is none",
			GateResult{},
			nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.result.Rerun())
		})
	}
}

// Keys returns each failure's key (<pkg>.<Test>, or <pkg> for a build failure);
// an empty input has no keys.
func TestGateCoverKeysNamesEachFailure(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		failures []Failure
		want     []string
	}{
		{
			"named tests",
			[]Failure{{Pkg: "m/p", Test: "TestA"}, {Pkg: "m/q", Test: "TestB"}},
			[]string{"m/p.TestA", "m/q.TestB"},
		},
		{
			"build failure is its package",
			[]Failure{{Pkg: "m/p", Test: "TestA"}, {Pkg: "m/b"}},
			[]string{"m/p.TestA", "m/b"},
		},
		{
			"empty is empty",
			nil,
			[]string{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, Keys(tc.failures))
		})
	}
}
