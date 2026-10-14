package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTableCoverOpts pins renderFlags.opts: on its main path the label width
// is carried into the render options and a width spec is parsed into them; a
// negative label width is refused with what the flag wants, and a width spec
// the store's grammar does not accept comes back wrapped under --width. No
// store, no clock, nothing but the flags.
func TestTableCoverOpts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		rf      renderFlags
		want    ntable.RenderOpts
		wantErr string
	}{
		{name: "main path: the label width alone", rf: renderFlags{labelWidth: intp(25), widths: strp("")}, want: ntable.RenderOpts{LabelWidth: 25}},
		{name: "main path: a width spec parses into the options", rf: renderFlags{labelWidth: intp(0), widths: strp("order=40,ready=12")},
			want: ntable.RenderOpts{LabelWidth: 0, Widths: map[string]int{"order": 40, "ready": 12}}},
		{name: "refusal: a negative label width", rf: renderFlags{labelWidth: intp(-1)}, wantErr: "--label-width: -1 is negative"},
		{name: "refusal: a bad width spec", rf: renderFlags{labelWidth: intp(0), widths: strp("order=none")}, wantErr: "--width: width"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts, err := tc.rf.opts()
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, opts)
		})
	}
}

func intp(n int) *int       { return &n }
func strp(s string) *string { return &s }
