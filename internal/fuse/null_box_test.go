/*
Tests for the top-level shape of a read box.

A bare JSON null is a wrong-shaped value, so it reads as CANNOT TELL like every
other shape that is not an object (SPEC.md, the read's one yes and two noes;
fuse.go note 2). json.Unmarshal into a value Box answers it with no error and a
zero Box, and the nil-quarantine normalisation turns that into VERIFIED CLEAR --
a fail-open in a safety control, reached by one hand-edited byte. ReadBox
unmarshals into a pointer Box instead, so a null arrives as a nil pointer and is
refused at the read, and this file pins the refusal beside the
array/string/number/truncated contract of TestMalformedIsUnreadable, with the two
properties every refusal must carry: the file's bytes survive byte for byte, and
the box handed back is never a trusted clear one.
*/
package fuse

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReadBoxRefusesTopLevelNull: a top-level JSON null, with or without
// surrounding JSON whitespace, is not a fuse box object and must be unreadable.
// The valid rows are the boundary the refusal must not cross: an empty object
// and an object whose members are null are hand-editable boxes and stay
// readable, with a usable quarantine map and no lockdown.
func TestReadBoxRefusesTopLevelNull(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		content  string
		wantRead bool
	}{
		"top-level null":               {`null`, false},
		"whitespace-wrapped null":      {" \t\r\n null \t\r\n ", false},
		"a JSON array":                 {`[]`, false},
		"a bare string":                {`"lockdown"`, false},
		"a number":                     {`7`, false},
		"truncated":                    {`{"lockdown":{"at":"x"`, false},
		"an empty object":              {`{}`, true},
		"null lockdown and quarantine": {`{"lockdown":null,"quarantine":null}`, true},
	} {
		t.Run(name, func(t *testing.T) {
			path := boxIn(t)
			write(t, path, tc.content)
			b, err := ReadBox(path)
			if tc.wantRead {
				assert.NoError(t, err, "%s is a valid empty box and must read: %v", name, err)
				assert.NotNil(t, b.Quarantine, "%s must read into a usable map, not nil", name)
				assert.Nil(t, b.Lockdown, "%s holds no lockdown", name)
				b.Quarantine["x"] = Fuse{} // must not panic
				return
			}
			assert.Error(t, err, "%s must be unreadable, but it parsed as %+v", name, b)
			assert.Equal(t, Box{}, b, "a refusal never returns a trusted clear box: %+v", b)
			got, rerr := os.ReadFile(path)
			require.NoError(t, rerr, "read the box back after the refusal")
			assert.Equal(t, tc.content, string(got), "a refusal must preserve the file's bytes byte for byte")
		})
	}
}
