package tool

import (
	"bytes"
	"strconv"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/buildinfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEveryToolsVersionLineNamesItsVerbSet pins the fact pkg/tool's version verb
// now carries on every skeleton tool's line (docs/SPEC-VERSION.md item 12): the
// count of the verbs the tool answers and the generation hash of their sorted
// names, so a reader holding two binaries can tell whether they carry the same
// verb set. Two tools whose verbs are the same set in another order share the
// generation; one added verb is a new one. The version verb is run in memory
// through Tool.Run with captured streams -- no binary is built or executed.
func TestEveryToolsVersionLineNamesItsVerbSet(t *testing.T) {
	t.Parallel()

	run := func(name string, verbs ...string) buildinfo.Fields {
		t.Helper()
		tool := &Tool{Name: name}
		for _, v := range verbs {
			tool.Verbs = append(tool.Verbs, Verb{Name: v})
		}
		var out, errs bytes.Buffer
		code := tool.Run([]string{"version"}, nil, &out, &errs)
		require.Equal(t, 0, code, "%s version exit: stdout %q stderr %q", name, out.String(), errs.String())
		fields, ok := buildinfo.Parse(out.String())
		require.Truef(t, ok, "%s: the version verb printed no version line: %q", name, out.String())
		return fields
	}

	ab := run("nova-a", "a", "b")
	ba := run("nova-b", "b", "a")
	abc := run("nova-c", "a", "b", "c")

	for _, tc := range []struct {
		name   string
		fields buildinfo.Fields
		want   int
	}{
		{"nova-a", ab, 3},
		{"nova-b", ba, 3},
		{"nova-c", abc, 4},
	} {
		got, ok := tc.fields.Extra("verbs")
		require.Truef(t, ok, "%s: the version line names no verbs= count", tc.name)
		assert.Equal(t, strconv.Itoa(tc.want), got,
			"%s: verbs= counts the names the tool answers (its verbs plus version)", tc.name)
		hash, ok := tc.fields.Extra("verbset")
		require.Truef(t, ok, "%s: the version line names no verbset= generation", tc.name)
		assert.Len(t, hash, 12, "%s: the generation is twelve hex digits like a short revision", tc.name)
	}

	abHash, _ := ab.Extra("verbset")
	baHash, _ := ba.Extra("verbset")
	abcHash, _ := abc.Extra("verbset")
	assert.Equal(t, abHash, baHash, "the same verb set in another order is the same generation")
	assert.NotEqual(t, abHash, abcHash, "one added verb is a new generation")
}
