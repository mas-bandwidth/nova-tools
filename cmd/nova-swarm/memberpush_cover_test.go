package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The unit tier's per-function coverage table showed openPR (memberpush.go:254) at
// 0.0%: no test reached it. openPR's main path runs a real `gh pr create` child, a
// subprocess the unit tier refuses and one whose binary this card may not touch
// (touch no existing file), so the main path is not covered here; the report says
// so. The one path that needs no child is the refusal reached before the exec: gh
// is not on the machine's PATH, and openPR answers with a note naming it. No
// sleep, no clock, no file, no network and no store is touched.
func TestMemberpushCoverOpenPRNoGHOnPath(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		gh   string
	}{
		{name: "a bare name that is not on the PATH", gh: "nova-no-such-gh-for-memberpush-cover"},
		{name: "a path that does not exist", gh: filepath.Join(t.TempDir(), "no-gh")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := &gitPusher{gh: tc.gh}
			pr, note := g.openPR("https://example.com/o/n.git", "main", "sprint/c1", "a title", "a body")
			assert.Empty(t, pr, "no pull request is opened when gh is absent")
			assert.True(t, strings.HasPrefix(note, "no "+tc.gh+" on this machine's PATH: "),
				"the refusal names the missing binary, got %q", note)
			assert.NotContains(t, note, "\n", "the note is one line")
		})
	}
}

func TestMemberpushOpenPRSurfacesInvalidSeatEnvironment(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	seatDir := filepath.Join(root, "nova-sprint")
	if err := os.Mkdir(seatDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seatDir, "seat-environment.json"), []byte(`{"version":1,"forge_identity":null}`), 0600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "gh-ran")
	gh := filepath.Join(root, "gh")
	if err := os.WriteFile(gh, []byte("#!/bin/sh\nprintf called > \""+marker+"\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	g := &gitPusher{
		gh: gh,
		getenv: func(key string) string {
			if key == "XDG_CONFIG_HOME" {
				return root
			}
			return ""
		},
		processEnv: func() []string { return []string{"PATH=/bin"} },
	}
	pr, note := g.openPR("https://github.com/o/n.git", "main", "sprint/c1", "title", "body")
	assert.Empty(t, pr)
	assert.Contains(t, note, "seat environment is invalid")
	assert.NotContains(t, note, "retry", "an invalid identity is not a forge outage")
	assert.NotContains(t, note, "\n")
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("gh ran despite invalid seat environment; marker stat error = %v", err)
	}
}
