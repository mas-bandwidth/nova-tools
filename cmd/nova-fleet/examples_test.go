package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// docs/examples/*.yml are vars files a reader passes to a play with -e @file. Each parses, and every variable it sets
// is one the plays read: a top-level key of group_vars/all.yml or a role's defaults (a variable with no default is
// there as a commented `# name:` line at the margin). A misspelt or retired name would be silently ignored by
// ansible-playbook.
func TestExampleVarsFilesNameOnlyVariablesThePlaysRead(t *testing.T) {
	t.Parallel()
	known := map[string]bool{}
	declared := regexp.MustCompile(`(?m)^(?:# )?([a-z][a-z0-9_]*):`)
	defaults, err := filepath.Glob("roles/*/defaults/main.yml")
	require.NoError(t, err)
	for _, f := range append(defaults, "group_vars/all.yml") {
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		for _, m := range declared.FindAllStringSubmatch(string(b), -1) {
			known[m[1]] = true
		}
	}
	examples, err := filepath.Glob("../../docs/examples/*.yml")
	require.NoError(t, err)
	require.NotEmpty(t, examples, "docs/examples holds the vars files the READMEs name")
	for _, f := range examples {
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		var vars map[string]any
		require.NoError(t, yaml.Unmarshal(b, &vars), f)
		require.NotEmpty(t, vars, "%s sets nothing", f)
		for name := range vars {
			assert.True(t, known[name], "%s sets %s, which no play's defaults name", f, name)
		}
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "#   nova-fleet ") {
				assert.Contains(t, line, "-e @"+filepath.Base(f), "%s: its usage line passes the file itself: %q", f, line)
			}
		}
	}
}
