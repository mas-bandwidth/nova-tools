package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/driver"
)

// TestPlayCoverSilenceFlagSetTakesMemberFromAndFor pins the main path of
// silenceFlag.Set (play.go): "member@from+for" appends one driver.Silence
// with the member's name and the two durations parsed, and the flag is
// repeatable, so each call adds one more in order.
func TestPlayCoverSilenceFlagSetTakesMemberFromAndFor(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		given []string
		want  []driver.Silence
	}{
		{
			name:  "one silence",
			given: []string{"m3@30s+20s"},
			want:  []driver.Silence{{Member: "m3", From: 30 * time.Second, For: 20 * time.Second}},
		},
		{
			name:  "repeatable: two calls are two silences in order",
			given: []string{"m3@30s+20s", "reader-a@1m500ms+2h"},
			want: []driver.Silence{
				{Member: "m3", From: 30 * time.Second, For: 20 * time.Second},
				{Member: "reader-a", From: 1*time.Minute + 500*time.Millisecond, For: 2 * time.Hour},
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var f silenceFlag
			for _, given := range c.given {
				require.NoError(t, f.Set(given), "Set(%q) is refused on the main path", given)
			}
			assert.Equal(t, c.want, []driver.Silence(f))
		})
	}
}

// TestPlayCoverSilenceFlagSetRefusesAShapeItCannotRead pins the refusals of
// silenceFlag.Set (play.go): a value without member, from or for is answered
// with what the flag wants, a duration the words do not name is answered
// with its fault, and a refused value adds no silence.
func TestPlayCoverSilenceFlagSetRefusesAShapeItCannotRead(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		given   string
		wantErr string
	}{
		{
			name:    "no member",
			given:   "@30s+20s",
			wantErr: `--silent wants <member>@<from>+<for>, e.g. m3@30s+20s, got "@30s+20s"`,
		},
		{
			name:    "no from and no for",
			given:   "m3",
			wantErr: `--silent wants <member>@<from>+<for>, e.g. m3@30s+20s, got "m3"`,
		},
		{
			name:    "no for",
			given:   "m3@30s",
			wantErr: `--silent wants <member>@<from>+<for>, e.g. m3@30s+20s, got "m3@30s"`,
		},
		{
			name:    "from is not a duration",
			given:   "m3@soon+20s",
			wantErr: `--silent "m3@soon+20s": time: invalid duration "soon"`,
		},
		{
			name:    "for is not a duration",
			given:   "m3@30s+eventually",
			wantErr: `--silent "m3@30s+eventually": time: invalid duration "eventually"`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := silenceFlag{{Member: "m1", From: time.Second, For: time.Second}}
			err := f.Set(c.given)
			require.Error(t, err)
			assert.EqualError(t, err, c.wantErr)
			assert.Equal(t, []driver.Silence{{Member: "m1", From: time.Second, For: time.Second}}, []driver.Silence(f), "a refused value adds no silence")
		})
	}
}
