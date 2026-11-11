package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// opencode_play_test.go pins the opencode a bench worker runs (roles/opencode, used by install.yml for the bench group).
// nova-sprint's opencode adapter needs opencode 1.x (2.x starts a managed background service on a fixed port unless
// given --standalone, which 1.x rejects). Until 2026-11-08 no play owned it: each bench had 1.18.20 put in
// /usr/local/bin by hand, and the studio only Homebrew's 2.x. The run through ansible is opencode_play_functional_test.go.

func opencodeDefaults(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile("roles/opencode/defaults/main.yml")
	require.NoError(t, err)
	var d map[string]any
	require.NoError(t, yaml.Unmarshal(b, &d))
	return d
}

func opencodeTasks(t *testing.T) []map[string]any {
	t.Helper()
	b, err := os.ReadFile("roles/opencode/tasks/main.yml")
	require.NoError(t, err)
	var tasks []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &tasks))
	return flattenTasks(t, tasks)
}

// One version, one digest per platform: the sha256 GitHub publishes for each release archive of sst/opencode v1.18.20.
func TestOpencodeIsPinnedByDigest(t *testing.T) {
	t.Parallel()
	d := opencodeDefaults(t)
	assert.Equal(t, "1.18.20", asText(d["nova_opencode_version"]))
	assert.Equal(t, "https://github.com/sst/opencode/releases/download/v{{ nova_opencode_version }}", asText(d["nova_opencode_release_url"]))
	sums, ok := d["nova_opencode_sha256"].(map[string]any)
	require.True(t, ok, "nova_opencode_sha256 is a map of platform to digest")
	assert.Equal(t, map[string]any{
		"linux-amd64":  "8603214aa1e2e18f9312360c9360dfae6174f5cff6dc4f72b6045c80608af4d4",
		"linux-arm64":  "4c85612e92e8bca95628cadf7dcd92d4150105954004758b2910f868d97ba741",
		"darwin-arm64": "b483e547c029b4f0ba381f0d0c5b420bec48c24c2bbec1fb7f22252bae83da46",
		"darwin-amd64": "abff81c995a137273526044b528ae0dc66b1aafdebd47198a875e7700a571340",
	}, sums)
	// It goes where the tools go: nova_bin_dir, the directory every service unit's PATH starts with.
	assert.Equal(t, "{{ nova_bin_dir }}", asText(d["nova_opencode_bin_dir"]))
}

// The archive is fetched at its digest, the binary unpacked as opencode.bin, and opencode is the wrapper that execs it.
func TestOpencodeRoleFetchesAtTheDigestAndWrapsTheBinary(t *testing.T) {
	t.Parallel()
	tasks := opencodeTasks(t)
	i := taskIndex(tasks, "the release archive")
	require.GreaterOrEqual(t, i, 0)
	get, ok := tasks[i]["ansible.builtin.get_url"].(map[string]any)
	require.True(t, ok, "the archive is not fetched with get_url")
	assert.Equal(t, "sha256:{{ nova_opencode_sha256[nova_opencode_platform] }}", asText(get["checksum"]))
	assert.Contains(t, asText(get["url"]), "{{ nova_opencode_release_url }}/")
	assert.NotContains(t, asText(get["dest"]), "nova_opencode_bin_dir", "the archive is kept out of the bin directory the leftover check reads")

	w := taskIndex(tasks, "the wrapper")
	require.GreaterOrEqual(t, w, 0)
	cp, ok := tasks[w]["ansible.builtin.copy"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "{{ nova_opencode_bin_dir }}/opencode", asText(cp["dest"]))
	assert.Contains(t, asText(cp["content"]), `exec "$(dirname "$0")/opencode.bin" "$@"`)
	assert.Contains(t, asText(cp["content"]), "nova_opencode_env")

	// Both sit in the block for a pinned platform: Windows (captain) gets nothing, its opencode is the WSL one.
	b := taskIndex(tasks, "the pinned opencode")
	require.GreaterOrEqual(t, b, 0)
	assert.Equal(t, "nova_opencode_platform in nova_opencode_sha256", asText(tasks[b]["when"]))
	assert.Less(t, b, i)
	assert.Less(t, b, w)
}

// install.yml gives the bench group the pinned opencode, after the tools.
func TestInstallPlayGivesTheBenchOpencode(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("install.yml")
	require.NoError(t, err)
	var plays []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &plays))
	require.Len(t, plays, 2)
	assert.Equal(t, []any{"nova-tools-install"}, plays[0]["roles"])
	assert.Equal(t, "bench", plays[1]["hosts"])
	assert.Equal(t, []any{"opencode"}, plays[1]["roles"])
}

// A worker finds the pinned opencode first: the PATH every service unit gets starts with nova_bin_dir, ahead of
// Homebrew's and /usr/local/bin's copies (the studio's opencode 2.x, the benches' hand-installed 1.18.20).
func TestServicePathPutsTheToolsDirFirst(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("group_vars/all.yml")
	require.NoError(t, err)
	var all map[string]any
	require.NoError(t, yaml.Unmarshal(b, &all))
	p := strings.Split(asText(all["nova_service_path"]), ":")
	require.NotEmpty(t, p)
	assert.Equal(t, "{{ nova_bin_dir }}", p[0])
	assert.Contains(t, p, "/opt/homebrew/bin")
	assert.Contains(t, p, "/usr/local/bin")
}

// The benches' hand-installed copies of this opencode (in /usr/local/bin before the role) are removed, each only when it
// is exactly that copy: opencode.bin identical to the role's, and opencode either identical to it too (captain's bare
// binary) or one of the hand wrappers, with an identical opencode.bin beside it.
func TestOpencodeRoleRemovesOnlyItsOwnHandCopies(t *testing.T) {
	t.Parallel()
	d := opencodeDefaults(t)
	assert.Equal(t, "/usr/local/bin", asText(d["nova_opencode_hand_dir"]))
	re := regexp.MustCompile(asText(d["nova_opencode_hand_wrapper_re"]))
	// The two wrappers the benches had, byte for byte (amd-1/2/3, hetzner1/2, vision, space; hulk and intel).
	for _, w := range []string{
		"#!/bin/sh\n# wrapper (Rowan 2026-10-13): mirrors the darwin harness wrapper so the output-token max reaches opencode\n" +
			"export OPENCODE_EXPERIMENTAL_DISABLE_FILEWATCHER=1\nexport OPENCODE_EXPERIMENTAL_OUTPUT_TOKEN_MAX=128000\n" +
			"exec \"$(dirname \"$0\")/opencode.bin\" \"$@\"\n",
		"#!/bin/sh\n# wrapper (Rowan 2026-11-07): the same as the other benches; the file watcher off and the output-token max up\n" +
			"export OPENCODE_EXPERIMENTAL_DISABLE_FILEWATCHER=1\nexport OPENCODE_EXPERIMENTAL_OUTPUT_TOKEN_MAX=128000\n" +
			"exec \"$(dirname \"$0\")/opencode.bin\" \"$@\"\n",
	} {
		assert.True(t, re.MatchString(w), w)
	}
	for _, w := range []string{
		"#!/bin/sh\nexec /opt/opencode/opencode \"$@\"\n",
		"#!/bin/sh\n# wrapper (Rowan 2026-10-13): x\nexport OPENCODE_X=1\nrm -rf ~\nexec \"$(dirname \"$0\")/opencode.bin\" \"$@\"\n",
		"#!/bin/sh\n# opencode 1.18.20, from nova-tools fleet/roles/opencode: the binary is opencode.bin beside it\n" +
			"exec \"$(dirname \"$0\")/opencode.bin\" \"$@\"\n",
	} {
		assert.False(t, re.MatchString(w), w)
	}

	tasks := opencodeTasks(t)
	s := taskIndex(tasks, "this role's opencode.bin and the hand copies")
	require.GreaterOrEqual(t, s, 0)
	assert.Greater(t, s, taskIndex(tasks, "opencode answers the pinned version"), "the role's own copy is checked first")
	assert.Equal(t, []any{"{{ nova_opencode_bin_dir }}/opencode.bin", "{{ nova_opencode_hand_dir }}/opencode.bin",
		"{{ nova_opencode_hand_dir }}/opencode", "{{ nova_opencode_hand_dir }}"}, tasks[s]["loop"])
	c := taskIndex(tasks, "which hand copies are this opencode")
	require.GreaterOrEqual(t, c, 0)
	sf, ok := tasks[c]["ansible.builtin.set_fact"].(map[string]any)
	require.True(t, ok)
	expr := asText(sf["nova_opencode_hand_copies"])
	assert.Contains(t, expr, "bin.checksum == ours.checksum")
	assert.Contains(t, expr, "top.checksum == ours.checksum")
	assert.Contains(t, expr, "bin_ours and text is match(nova_opencode_hand_wrapper_re)", "a wrapper goes only with its identical opencode.bin")
	r := taskIndex(tasks, "the hand copies of this opencode, removed")
	require.GreaterOrEqual(t, r, 0)
	assert.Greater(t, r, c)
	f, ok := tasks[r]["ansible.builtin.file"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "absent", f["state"])
	assert.Equal(t, "{{ nova_opencode_hand_copies }}", asText(tasks[r]["loop"]))
	// Unlinking takes write on the directory, not ownership of the file: on space the copies were nova's, in root's
	// /usr/local/bin, and a removal without root failed there ("Unlinking failed").
	assert.Equal(t, "{{ not (nova_opencode_copies.results[3].stat.writeable | default(false)) }}", asText(tasks[r]["become"]))
}
