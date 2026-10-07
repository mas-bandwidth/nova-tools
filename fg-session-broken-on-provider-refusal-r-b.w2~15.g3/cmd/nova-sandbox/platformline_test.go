package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The one rule: a backticked span on the section's `Platform:` line of the bare
// form `<name>=` -- a field name with nothing after the `=` -- is a claim that
// the transcript has no such field, unless the same line also spells that field
// WITH a value somewhere (which is the line's way of saying "this field differs
// by platform"); every remaining claim must be true of what this bench actually
// prints.
func TestThePlatformLineNamesNoFieldThisBenchAlreadyPrints(t *testing.T) {
	t.Parallel()

	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	require.NoError(t, err)

	var platform string
	inSection := false
	for _, l := range strings.Split(string(doc), "\n") {
		if strings.HasPrefix(l, "## ") {
			inSection = l == "## nova-sandbox"
			continue
		}
		if !inSection {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(l), "Platform:") {
			require.Empty(t, platform, "the nova-sandbox section has more than one Platform: line: %q and %q", platform, l)
			platform = l
		}
	}
	require.NotEmpty(t, platform, "the nova-sandbox section has no Platform: line to read")

	valued := map[string]bool{}
	var bare []string
	span := regexp.MustCompile("`([^`]*)`")
	valuedRe := regexp.MustCompile(`^[a-z_]+=.+$`)
	bareRe := regexp.MustCompile(`^[a-z_]+=$`)
	for _, m := range span.FindAllStringSubmatch(platform, -1) {
		s := m[1]
		switch {
		case valuedRe.MatchString(s):
			valued[s[:strings.Index(s, "=")]] = true
		case bareRe.MatchString(s):
			bare = append(bare, s[:strings.Index(s, "=")])
		}
	}

	var claims []string
	for _, name := range bare {
		if !valued[name] {
			claims = append(claims, name)
		}
	}
	require.NotEmpty(t, claims, "the Platform: line makes no bare `name=` claim; this test would pass by checking nothing")

	var out, errb bytes.Buffer
	code := run([]string{"check"}, strings.NewReader(""), &out, &errb, os.Environ())
	require.Equal(t, 0, code, "nova-sandbox check exited %d, want 0; stderr: %s", code, errb.String())
	var printed string
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "CHECK OK ") {
			printed = strings.TrimSpace(l)
		}
	}
	require.NotEmpty(t, printed, "nova-sandbox check printed no CHECK OK line")

	printedFields := map[string]bool{}
	for _, n := range checkFieldNames(printed) {
		printedFields[n] = true
	}
	for _, name := range claims {
		assert.False(t, printedFields[name], "TESTS.md Platform: line calls `%s=` a field this transcript has no slot for, but this bench's check prints `%s=`; the transcript has the slot, so the Platform: line is wrong\n Platform: line: %q\n printed line:  %q", name, name, platform, printed)
	}
}
