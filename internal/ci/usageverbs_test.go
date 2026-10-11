package ci

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/dogfood"
)

// usageHeading opens a banner's usage block: `usage:`, or a qualified
// `usage, in a nova-tools checkout:` where a tool states two.
var usageHeading = regexp.MustCompile(`^usage\b[^:]*:\s*$`)

// usageVerbs are the verbs a banner's own usage block names: the lines under
// each `usage:` heading up to the next blank line, through the dogfood
// ledger's parser, less help. Nothing else in a banner states a verb: an
// invocation in the first-run paragraph or under example: is an invocation
// with arguments, not a verb.
func usageVerbs(tool, banner string) []string {
	var block strings.Builder
	in := false
	for _, line := range strings.Split(banner, "\n") {
		switch {
		case usageHeading.MatchString(line):
			in = true
		case strings.TrimSpace(line) == "":
			in = false
		case in:
			block.WriteString(line + "\n")
		}
	}
	var verbs []string
	for _, v := range dogfood.ParseHelp(block.String()) {
		if v.Tool != tool || v.Verb == "" || v.Verb == "help" {
			continue
		}
		verbs = append(verbs, v.Verb)
	}
	return verbs
}

// bannerVerbs is usageVerbs, refusing a banner whose usage block names no
// verb: a walk over no verbs would pass by checking nothing.
func bannerVerbs(tool, banner string) ([]string, error) {
	verbs := usageVerbs(tool, banner)
	if len(verbs) == 0 {
		return nil, fmt.Errorf("%s: its help names no verb under a `usage:` heading; a banner states its verbs in a `usage:` block, one per line, ended by a blank line", tool)
	}
	return verbs, nil
}

// The walk's verb list is the banner's `usage:` block and nothing else. A
// first-run paragraph that shows an invocation (`nova-demo create notes
// --columns a,b`) is prose: read as a usage line it would make `create` a
// verb group, and the walk would hold `create -h` to listing members it does
// not have. A real two-word verb under usage: is still a group.
func TestUsageVerbsReadOnlyTheUsageBlock(t *testing.T) {
	t.Parallel()
	banner := `nova-demo: a demo tool

how it works: one table per file.
first run: with no store, a write runs under --dry-run:
  nova-demo create notes --columns a,b --dry-run

usage:
  nova-demo create <table> --columns <list>
  nova-demo session start --session <path>
  nova-demo session stop
  nova-demo help

example:
  nova-demo create notes --columns a,b
`
	assert.Equal(t, []string{"create", "session start", "session stop"}, usageVerbs("nova-demo", banner))
	assert.Equal(t, []string{"session"}, verbGroups(usageVerbs("nova-demo", banner)))
}

// nova-update's banner before it named its usage block: its verb lines stood
// at column one after the opening, under no `usage:` heading. Read from the
// usage block alone it names no verb, and the walk refuses it by name rather
// than passing by checking nothing.
func TestABannerWithNoUsageBlockIsRefusedByName(t *testing.T) {
	t.Parallel()
	oldUpdateBanner := `nova-update: compare installed tools with their latest releases, and update one when asked

how it works: the manifest is a tab-separated file you write, one tool per line:
how to read its installed version, where its latest release is published, and
the command that installs it. check and report compare the two; apply runs one
named entry's command and reads the version again, nothing else. The release
verbs build, publish and install nova-tools' own releases.
first run: the binary alone; the lines under example: write a one-tool manifest
(Go) to ./versions.tsv and read it; they install nothing.

nova-update example [--out <path>]
nova-update check --file <path> [--max <n>] [--timeout <d>] [--budget <d>] [--kind <k>]
nova-update apply --file <path> <name> [--version <v>] [--dry-run] [--timeout <d>]
nova-update help
nova-update version (or --version)
Defaults: --max 20 (0 = all), --timeout 5s, --budget 60s.

example:
  nova-update example --out versions.tsv
`
	verbs, err := bannerVerbs("nova-update", oldUpdateBanner)
	assert.Empty(t, verbs)
	require.Error(t, err)
	assert.Equal(t, "nova-update: its help names no verb under a `usage:` heading; a banner states its verbs in a `usage:` block, one per line, ended by a blank line", err.Error())

	verbs, err = bannerVerbs("nova-update", "nova-update: x\n\nusage:\nnova-update check --file <path>\nnova-update version\n\nexample:\n  nova-update check --file v.tsv\n")
	require.NoError(t, err)
	assert.Equal(t, []string{"check", "version"}, verbs)
}
