package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestASecondBoxCannotAnswerForABlownOne is the reproduced bypass, exactly:
// a lockdown is blown in ./box.json, and `check --box ./box.json
// --box=./nonexistent.json` printed FUSE OK at exit 0, because package flag
// keeps the last value of a repeated flag and the second box named a path
// with nothing in it. A repeated --box is refused before any box is read.
func TestASecondBoxCannotAnswerForABlownOne(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	box := filepath.Join(dir, "box.json")
	other := filepath.Join(dir, "nonexistent.json")
	mustRun(t, []string{"lockdown", "--box", box, "test"}, nowish())

	for _, args := range [][]string{
		{"check", "--box", box, "--box=" + other},
		{"check", "--box", box, "--box", other},
		{"check", "--box=" + box, "--box=" + other, "a-forum"},
		{"check", "--box", box, "--box", box},
		{"status", "--box", box, "--box", other},
		{"quarantine", "--box", other, "--box", box, "a-forum", "r"},
		{"lockdown", "--box", box, "--box", other, "r"},
		{"lift", "quarantine", "--box", box, "--box", other, "a-forum"},
		{"path", "--box", box, "--box", other},
	} {
		code, out, errOut := capture(t, args, nowish())
		assert.Equal(t, 2, code, "%q: exit %d, want exit 2", args, code)
		assert.Empty(t, out, "%q: stdout %q, want no OK line", args, out)
		assert.Contains(t, errOut, "--box is given more than once", "%q: stderr %q, want naming --box as given more than once", args, errOut)
		assert.Equal(t, 1, strings.Count(errOut, "\n"), "%q: stderr %q, want one line", args, errOut)
	}
	code, _, _ := capture(t, []string{"check", "--box", box}, nowish())
	require.Equal(t, 1, code, "the lockdown is still blown: check exit %d, want 1", code)
}

// TestEveryFlagOfEveryVerbTakesOneValue is the class: no flag nova-fuse
// parses keeps a last value over a first. A flag named twice is a refusal,
// exit 2, one line naming the flag, whichever verb and whichever flag.
func TestEveryFlagOfEveryVerbTakesOneValue(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	box := filepath.Join(dir, "box.json")
	verbs := map[string][]string{
		"check":           {"a-forum"},
		"status":          nil,
		"lockdown":        {"r"},
		"quarantine":      {"a-forum", "r"},
		"lift quarantine": {"a-forum"},
		"path":            nil,
	}
	flags := map[string][2]string{
		"box": {box, box},
		"max": {"1", "0"},
	}
	for verb, positional := range verbs {
		for name, vals := range flags {
			if name == "max" && verb != "status" {
				continue
			}
			args := append(strings.Fields(verb), "--box", box)
			if name != "box" {
				args = append(args, "--"+name, vals[0], "--"+name+"="+vals[1])
			} else {
				args = append(args, "--box="+vals[1])
			}
			args = append(args, positional...)
			code, out, errOut := capture(t, args, nowish())
			assert.Equal(t, 2, code, "%q: exit %d, want exit 2", args, code)
			assert.Empty(t, out, "%q: stdout %q, want no OK line", args, out)
			assert.Contains(t, errOut, "--"+name+" is given more than once", "%q: stderr %q, want naming --%s", args, errOut, name)
			assert.Equal(t, 1, strings.Count(errOut, "\n"), "%q: stderr %q, want one line", args, errOut)
		}
	}
}

// TestABoxValueShapedLikeAFlagIsRefused: `--box --box=./x` hands package flag
// "--box=./x" as the value of the first --box. A path beginning with "-" is
// refused; a box in such a file is named ./-name.
func TestABoxValueShapedLikeAFlagIsRefused(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"check", "--box", "--box=./x"},
		{"check", "--box", "-h"},
		{"status", "--box", "--max=0"},
		// A verb that reads and does not write, so a red run leaves no box named -x.
		{"lift", "quarantine", "--box", "-x", "a-forum"},
	} {
		code, out, errOut := capture(t, args, nowish())
		assert.Equal(t, 2, code, "%q: exit %d, want exit 2", args, code)
		assert.Empty(t, out, "%q: stdout %q, want no OK line", args, out)
		assert.Contains(t, errOut, `begins with "-"`, "%q: stderr %q, want begins with -", args, errOut)
		assert.Equal(t, 1, strings.Count(errOut, "\n"), "%q: stderr %q, want one line", args, errOut)
	}
}
