package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/stretchr/testify/assert"
)

// The receipt's display of a changed field: a digest identifies a value and does
// not prove two values equal, so two long sides are listed, never dropped.
func TestChangedFieldsListsTwoLongValuesWithTheirDigests(t *testing.T) {
	t.Parallel()
	short, other := "x", "y"
	sha := strings.Repeat("a", 40)
	m := ntable.BatchMemberDelta{Fields: map[string]ntable.FieldChange{
		"same-long":  {BeforeBytes: 70, BeforeSHA1: sha, AfterBytes: 70, AfterSHA1: sha},
		"diff-long":  {BeforeBytes: 70, BeforeSHA1: sha, AfterBytes: 71, AfterSHA1: strings.Repeat("b", 40)},
		"long-short": {BeforeBytes: 70, BeforeSHA1: sha, After: &short},
		"same-short": {Before: &short, After: &short},
		"changed":    {Before: &short, After: &other},
		"both-none":  {},
	}}
	got := changedFields(m)
	for _, name := range []string{"same-long", "diff-long", "long-short", "changed"} {
		{
			_, ok := got[name]
			assert.True(t, ok, "%s is not listed: %v", name, got)
		}
	}
	for _, name := range []string{"same-short", "both-none"} {
		{
			_, ok := got[name]
			assert.False(t, ok, "%s changed nothing and is listed: %v", name, got)
		}
	}
	{
		line := fmt.Sprint(got["same-long"])
		assert.EqualValues(t, 2, strings.Count(line, sha), "two long sides are shown with their digests side by side: %s", line)
	}
}

// create past the column bound is the store's kind of no, exit 1 like col add,
// with the bound and the count; a malformed list stays usage, exit 2.
func TestCreateColumnsPastTheBoundExitsOne(t *testing.T) {
	t.Parallel()
	names := make([]string, ntable.LimitColumns+1)
	for i := range names {
		names[i] = fmt.Sprintf("c%d", i)
	}
	code, stdout, stderr := runTable("create", "wide", "--columns", strings.Join(names, ","))
	assert.EqualValues(t, 1, code, "create with %d columns: exit %d stdout %q stderr %q", len(names), code, stdout, stderr)
	assert.Empty(t, stdout, "create with %d columns: exit %d stdout %q stderr %q", len(names), code, stdout, stderr)
	assert.Contains(t, stderr, "columns per table: bound 1000, observed 1001", "create with %d columns: exit %d stdout %q stderr %q", len(names), code, stdout, stderr)
	code, _, stderr = runTable("create", "wide", "--columns", "a,a")
	assert.EqualValues(t, 2, code, "create with a repeated column: exit %d stderr %q; want usage, 2", code, stderr)
	code, _, stderr = runTable("set", "wide", "--columns", strings.Join(names, ","))
	assert.EqualValues(t, 1, code, "set --columns past the bound: exit %d stderr %q", code, stderr)
	assert.Contains(t, stderr, "columns per table", "set --columns past the bound: exit %d stderr %q", code, stderr)
}
