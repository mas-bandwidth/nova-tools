package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A file that is valid JSON, or near it, but not a box is CANNOT TELL: check
// and status refuse at exit 2 and never print clear; quarantine and lift refuse
// and leave the bytes as they are; lockdown, the one verb that must work on a
// broken box, keeps the bytes beside it and blows. `{}` is a box with both keys
// left out, as a hand-edited box may be, so it is not in this table.
func TestAFileThatIsNotABoxIsNeverClear(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"null":            "null\n",
		"true":            "true",
		"zero":            "0",
		"empty string":    `""`,
		"array":           "[]",
		"unknown key":     `{"lockdwn": {"at": "x", "reason": "y"}, "quarantine": {}}`,
		"wrong type":      `{"lockdown": "blown", "quarantine": {}}`,
		"trailing bytes":  `{"lockdown": null, "quarantine": {}} {}`,
		"duplicate key":   `{"lockdown": {"at": "x", "reason": "y"}, "lockdown": null}`,
		"empty file":      "",
		"whitespace":      " \n\t",
		"byte order mark": "\ufeff{\"lockdown\": null, \"quarantine\": {}}",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, tc := range []struct {
				args []string
				code int
			}{
				{[]string{"check"}, 2}, {[]string{"check", "s"}, 2}, {[]string{"status"}, 2},
				{[]string{"quarantine", "s", "why"}, 2}, {[]string{"lift", "quarantine", "s"}, 2},
			} {
				box := filepath.Join(t.TempDir(), "box.json")
				writeRaw(t, box, body)
				args := append([]string{tc.args[0]}, tc.args[1:]...)
				if tc.args[0] == "lift" {
					args = []string{"lift", "quarantine", "--box", box, "s"}
				} else {
					args = append([]string{tc.args[0], "--box", box}, tc.args[1:]...)
				}
				code, out, errOut := capture(t, args, nowish())
				assert.Equal(t, tc.code, code, "%v: %s%s", args, out, errOut)
				assert.NotContains(t, out+errOut, "lockdown=clear", "%v reported clear", args)
				assert.Equal(t, body, readRaw(t, box), "%v changed the file", args)
			}
			box := filepath.Join(t.TempDir(), "box.json")
			writeRaw(t, box, body)
			code, _, errOut := capture(t, []string{"lockdown", "--box", box, "why"}, nowish())
			assert.Equal(t, 0, code, errOut)
			assert.True(t, strings.HasPrefix(errOut, "LOCKDOWN NOTE box was unreadable"), errOut)
			kept, err := os.ReadFile(box + ".unreadable")
			if assert.NoError(t, err) {
				assert.Equal(t, body, string(kept), "the broken bytes were not kept")
			}
		})
	}
}
