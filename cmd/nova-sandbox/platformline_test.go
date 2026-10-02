package main

import (
	"os"
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

	platform := onlyLine(t, "TESTS.md's nova-sandbox section", sandboxSection(t), "Platform:")
	valued := map[string]bool{}
	var bare []string
	field := regexp.MustCompile(`^[a-z_]+=`)
	for _, m := range regexp.MustCompile("`([^`]*)`").FindAllStringSubmatch(platform, -1) {
		name, value, _ := strings.Cut(m[1], "=")
		switch {
		case !field.MatchString(m[1]):
		case value != "":
			valued[name] = true
		default:
			bare = append(bare, name)
		}
	}
	var claims []string
	for _, name := range bare {
		if !valued[name] {
			claims = append(claims, name)
		}
	}
	require.NotEmpty(t, claims, "the Platform: line makes no bare `name=` claim; this test would pass by checking nothing")

	r := withEnv(run, os.Environ()).Do(t, "check").Exit(0)
	printed := onlyLine(t, "nova-sandbox check's stdout", r.Stdout, "CHECK OK ")
	for _, name := range claims {
		assert.NotContains(t, checkFieldNames(printed), name, "TESTS.md's Platform: line calls `%s=` a field this transcript has no slot for, but this bench's check prints it, so the Platform: line is wrong\n Platform: line: %q\n printed line:  %q", name, platform, printed)
	}
}
