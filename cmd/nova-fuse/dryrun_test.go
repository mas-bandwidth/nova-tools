package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --dry-run on every verb that writes the box makes the checks the write would,
// says what it would do with dry_run=true, and writes nothing: the box's bytes
// are what they were, and a path with no box still has none. A dry lockdown is
// said not to be blown, so no reader takes its OK for a blown fuse.
func TestDryRunWritesNothing(t *testing.T) {
	t.Parallel()

	quarantined := `{"lockdown":null,"quarantine":{"a-forum":{"at":"2026-09-09T18:27:40Z","reason":"r"}}}`
	for _, tc := range []struct {
		name, box string // box "" is a path with no box at it
		verb, pos []string
		code      int
		want      string
	}{
		{"init", "", []string{"init"}, []string{}, 0, "INIT OK box="},
		{"init over a box", quarantined, []string{"init"}, []string{}, 1, "INIT FAIL box="},
		{"lockdown", quarantined, []string{"lockdown"}, []string{"why"}, 0, "LOCKDOWN OK dry_run=true: nothing written, the lockdown is not blown; a real run would blow the lockdown in the box there: why"},
		{"lockdown with no box", "", []string{"lockdown"}, []string{"why"}, 0, "a real run would make a box there holding a blown lockdown"},
		{"lockdown over an unreadable box", "{", []string{"lockdown"}, []string{"why"}, 0, "keep the unreadable box's bytes beside it"},
		{"quarantine", quarantined, []string{"quarantine"}, []string{"discord", "why"}, 0, "QUARANTINE OK discord dry_run=true: nothing written, the surface is not quarantined"},
		{"quarantine with no box", "", []string{"quarantine"}, []string{"discord", "why"}, 2, "nova-fuse quarantine REFUSED:"},
		{"lift quarantine", quarantined, []string{"lift", "quarantine"}, []string{"a-forum"}, 0, "LIFT OK quarantine=a-forum dry_run=true: would lift it; nothing written, it still stands"},
		{"lift what is not there", quarantined, []string{"lift", "quarantine"}, []string{"discord"}, 1, "LIFT FAIL quarantine=discord"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			box := absentBoxIn(t)
			if tc.box != "" {
				writeRaw(t, box, tc.box)
			}
			args := append(append(append([]string{}, tc.verb...), "--box", box, "--dry-run"), tc.pos...)
			code, out, errOut := capture(t, args, nowish())
			assert.Equal(t, tc.code, code, "%v\nstdout: %s\nstderr: %s", args, out, errOut)
			assert.Contains(t, out+errOut, tc.want)
			if tc.code == 0 {
				assert.Contains(t, out, "dry_run=true")
			}
			if tc.box == "" {
				_, err := os.Lstat(box)
				assert.True(t, os.IsNotExist(err), "a dry run made a box: %v", err)
			} else {
				assert.Equal(t, tc.box, readRaw(t, box), "a dry run changed the box")
			}
			entries, err := os.ReadDir(strings.TrimSuffix(box, "/fuses.json"))
			require.NoError(t, err)
			assert.LessOrEqual(t, len(entries), 1, "a dry run left a file beside the box: %v", entries)
		})
	}
}

// Every refusal carries the REFUSED word after the tool and verb, the grammar of
// every tool here: a bare verb, an unknown flag, a missing --box, a late flag.
func TestEveryRefusalSaysREFUSED(t *testing.T) {
	t.Parallel()
	box := boxIn(t)
	for _, args := range [][]string{
		nil, {"defuse"}, {"status"}, {"check", "--zzz"}, {"check", "--box", box, "-h"},
		{"quarantine", "--box", box}, {"lift"}, {"lift", "lockdown"}, {"version", "x"},
		{"status", "--box", box, "--max", "-1"}, {"help", "defuse"},
	} {
		code, out, errOut := capture(t, args, nowish())
		assert.Equal(t, 2, code, "%v", args)
		assert.Empty(t, out, "%v", args)
		first, _, _ := strings.Cut(errOut, "\n")
		assert.True(t, strings.HasPrefix(first, "nova-fuse") && strings.Contains(first, " REFUSED"), "%v: %q", args, first)
	}
}
