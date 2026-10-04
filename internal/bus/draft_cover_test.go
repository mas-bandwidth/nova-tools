package bus

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDraftCoverProblemsUnwrapHandsBackEveryReason pins the one method of the Problems
// wrapper no other test reaches: Unwrap, which is what makes errors.Is cross the wrapper
// to EVERY reason rather than the first. The reasons come from a real multi-problem
// draft refusal through the package's own seams; nothing here is slept on, dialed, or
// shelled out.
func TestDraftCoverProblemsUnwrapHandsBackEveryReason(t *testing.T) {
	t.Parallel()
	tab := loadBus(t, writeBus(t, fixture()))
	_, err := PrepareDraft(tab, "From: Ada\nTo: Boe\nRe: bo-deadbeefcafe\nSubject:\n\n\n", at("2026-09-09T12:34:56Z"), "", "")
	require.Error(t, err, "a draft with four problems was accepted")

	var many *Problems
	require.ErrorAs(t, err, &many, "the refusal is not a *Problems: %v", err)
	unwrapped := many.Unwrap()
	require.Len(t, unwrapped, 4, "Unwrap did not hand back the four collected reasons: %v", unwrapped)
	assert.Equal(t, many.Reasons, unwrapped, "Unwrap reordered or replaced the list it holds")
	assert.Equal(t, unwrapped, Reasons(err), "Reasons and Unwrap disagree over the same refusal")

	sentinel := errors.New("a reason that stands in no draft")
	cases := []struct {
		name string
		want error
		ok   bool
	}{
		{name: "the first reason is reached", want: unwrapped[0], ok: true},
		{name: "the last reason is reached, not only the first", want: unwrapped[len(unwrapped)-1], ok: true},
		{name: "an error that is not among the reasons is not matched", want: sentinel, ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.ok, errors.Is(err, tc.want), "errors.Is and Unwrap disagree over %v", tc.want)
		})
	}
}

// TestDraftCoverProblemsRefusalGateKeepsAnEmptyListNoError pins the other side of the
// wrapper: problemsOf refuses to build a refusal out of no reasons, so a draft that
// passes asks nothing of Unwrap, and Error of a real refusal prints every reason on one
// line in the order send collected them.
func TestDraftCoverProblemsRefusalGateKeepsAnEmptyListNoError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		reasons []error
		want    string // the whole Error(); empty when the gate must hand back no error
	}{
		{name: "no reasons is no refusal", reasons: nil, want: ""},
		{name: "one reason prints as itself", reasons: []error{errors.New("the note has no body")}, want: "the note has no body"},
		{name: "two reasons print joined in order", reasons: []error{errors.New("a misspelled recipient"), errors.New("no Subject line")}, want: "a misspelled recipient; no Subject line"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := problemsOf(tc.reasons)
			if tc.want == "" {
				assert.NoError(t, got, "an empty reason list became a refusal")
				return
			}
			require.Error(t, got, "want the refusal %q", tc.want)
			assert.Equal(t, tc.want, got.Error(), "Error did not print every reason in order")
			var many *Problems
			require.ErrorAs(t, got, &many, "the refusal is not a *Problems: %v", got)
			assert.Len(t, many.Unwrap(), len(tc.reasons), "Unwrap lost or invented a reason")
		})
	}
}
