package dogfood

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCliCoverKeysSortsTheKeysItIsGiven pins the one promise Keys makes: the
// key of every verb it is given, one per verb, sorted, whatever order the
// document declared them in and however the verb was written.
func TestCliCoverKeysSortsTheKeysItIsGiven(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		verbs []Verb
		want  []string
	}{
		{
			name: "verbs in reverse order come out sorted",
			verbs: []Verb{
				{Tool: "nova-fuse", Verb: "lift quarantine"},
				{Tool: "nova-check", Verb: "links"},
				{Tool: "nova-example", Verb: "help"},
			},
			want: []string{
				"nova-check links",
				"nova-example help",
				"nova-fuse lift quarantine",
			},
		},
		{
			name:  "a bare invocation keys as the tool alone",
			verbs: []Verb{{Tool: "nova-decide", Verb: BareVerb}},
			want:  []string{"nova-decide"},
		},
		{
			name:  "runs of whitespace in a verb collapse in the key",
			verbs: []Verb{{Tool: "nova-work", Verb: "  ask   once "}},
			want:  []string{"nova-work ask once"},
		},
		{
			name: "duplicates are kept, one key per verb",
			verbs: []Verb{
				{Tool: "nova-check", Verb: "links"},
				{Tool: "nova-check", Verb: "links"},
			},
			want: []string{"nova-check links", "nova-check links"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Keys(tc.verbs), "Keys(%v)", tc.verbs)
		})
	}
}

// TestCliCoverKeysAnswersNothingOverNothing pins the edge a ledger over an
// empty reference would see: no verbs, no keys, no error. Keys is a pure
// spelling over its input, so it has no refusal path to take; the empty rows
// are its refusal.
func TestCliCoverKeysAnswersNothingOverNothing(t *testing.T) {
	t.Parallel()

	assert.Empty(t, Keys(nil), "Keys(nil)")
	assert.Empty(t, Keys([]Verb{}), "Keys(no verbs)")
}
