package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// native_helpers_unit_cover_test.go pins the file, digest and provider-key helpers of
// native.go that need only t.TempDir() or literal bytes: fileSize, readSince,
// fenceRejected, wroteBytes, sameDir, spendCost, sweepNativeJob,
// modelProviderMissingAuth, keylessProvider, fileSHA256 and the empty-group answer of
// nativeEndLeftovers. The run machinery (initRunState, start, watch, collect, report)
// starts the harness as a process, reads the real clock and sleeps, and the reap path of
// nativeEndLeftovers signals a live group: the functional tier
// (native_spend_functional_test.go, native_yield_functional_test.go) owns them, so they
// are left out here. Publishing, usage, wall and sandboxHostRules are pinned by the
// sibling card nova-swarm-native-publish-usage-and-wall-have-unit-tests in its own file.

func TestSwarmNativeHelpersCoverFileSize(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	five := filepath.Join(dir, "five")
	require.NoError(t, os.WriteFile(five, []byte("12345"), 0o644))
	cases := []struct {
		name string
		path string
		want int64
	}{
		{name: "a missing path is zero", path: filepath.Join(dir, "absent"), want: 0},
		{name: "a five-byte file is five", path: five, want: 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, fileSize(tc.path))
		})
	}
}

func TestSwarmNativeHelpersCoverReadSince(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	capture := filepath.Join(dir, "capture.log")
	require.NoError(t, os.WriteFile(capture, []byte("abcdefghij"), 0o644))
	cases := []struct {
		name    string
		path    string
		offset  int64
		want    string
		wantNil bool
	}{
		{name: "offset zero reads the whole capture", path: capture, offset: 0, want: "abcdefghij"},
		{name: "a middle offset reads only the tail", path: capture, offset: 5, want: "fghij"},
		{name: "past the end reads nothing", path: capture, offset: 20, want: ""},
		{name: "a missing file is nil", path: filepath.Join(dir, "absent"), offset: 0, wantNil: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := readSince(tc.path, tc.offset)
			if tc.wantNil {
				assert.Nil(t, got)
				return
			}
			assert.Equal(t, tc.want, string(got))
		})
	}
}

func TestSwarmNativeHelpersCoverFenceRejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	noCapture := filepath.Join(dir, "no-capture")
	plain := filepath.Join(dir, "plain")
	rejected := filepath.Join(dir, "rejected")
	for _, d := range []string{noCapture, plain, rejected} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(plain, "harness-output.log"), []byte("a quiet run\nno permission here\n"), 0o644))
	// the harness's own words, in the shape internal/swarm/fence.go parses: the mark, the
	// permission and its patterns, the tail. Two patterns prove the FIRST path is answered.
	line := "! " + swarm.FenceRejectionMark + "external_directory (/first/path/*, /second/path/*); auto-rejecting\n"
	require.NoError(t, os.WriteFile(filepath.Join(rejected, "harness-output.log"), []byte(line), 0o644))
	cases := []struct {
		name string
		dir  string
		want string
	}{
		{name: "a job with no capture rejects nothing", dir: noCapture, want: ""},
		{name: "a capture with no rejection rejects nothing", dir: plain, want: ""},
		{name: "the capture's rejection names the first path", dir: rejected, want: "/first/path/*"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, fenceRejected(tc.dir))
		})
	}
}

func TestSwarmNativeHelpersCoverWroteBytes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	full := filepath.Join(dir, "full")
	empty := filepath.Join(dir, "empty")
	sub := filepath.Join(dir, "sub")
	link := filepath.Join(dir, "link")
	require.NoError(t, os.WriteFile(full, []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(empty, nil, 0o644))
	require.NoError(t, os.Mkdir(sub, 0o755))
	require.NoError(t, os.Symlink(full, link))
	cases := []struct {
		name string
		path string
		want bool
	}{
		{name: "a missing path publishes nothing", path: filepath.Join(dir, "absent"), want: false},
		{name: "an empty file publishes nothing", path: empty, want: false},
		{name: "a directory publishes nothing", path: sub, want: false},
		{name: "a symlink to a full file publishes nothing", path: link, want: false},
		{name: "a one-byte regular file publishes", path: full, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, wroteBytes(tc.path))
		})
	}
}

func TestSwarmNativeHelpersCoverSameDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	child := filepath.Join(real, "child")
	other := filepath.Join(dir, "other")
	link := filepath.Join(dir, "link")
	require.NoError(t, os.MkdirAll(child, 0o755))
	require.NoError(t, os.Mkdir(other, 0o755))
	require.NoError(t, os.Symlink(real, link))
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{name: "one directory reached through a symlinked parent", a: filepath.Join(link, "child"), b: child, want: true},
		{name: "two different directories", a: real, b: other, want: false},
		{name: "two non-existent equal strings compare raw", a: filepath.Join(dir, "nope", "x"), b: filepath.Join(dir, "nope", "x"), want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, sameDir(tc.a, tc.b))
		})
	}
}

func TestSwarmNativeHelpersCoverSpendCost(t *testing.T) {
	t.Parallel()
	tokens := func() cardcost.Tokens { return cardcost.Tokens{Input: 40} }
	priced := func(actual string) cardcost.Usage {
		return cardcost.Usage{Tokens: tokens(), Model: "opencode/deepseek-v4-pro", Actual: actual, ActualBy: cardcost.ActualByHarness}
	}
	cases := []struct {
		name     string
		launches []cardcost.Usage
		want     string
	}{
		{name: "no launches spend nothing", want: ""},
		{name: "one launch with tokens and no cost", launches: []cardcost.Usage{{Tokens: tokens()}}, want: ""},
		{name: "one launch priced and one not", launches: []cardcost.Usage{{Tokens: tokens()}, priced("0.05")}, want: ""},
		{name: "every launch carries its cost", launches: []cardcost.Usage{priced("0.01"), priced("0.02")}, want: "0.03"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, spendCost(tc.launches))
		})
	}
}

func TestSwarmNativeHelpersCoverSweepNativeJob(t *testing.T) {
	t.Parallel()
	t.Run("an empty root is refused", func(t *testing.T) {
		t.Parallel()
		require.ErrorContains(t, sweepNativeJob("", t.TempDir()), "not known")
	})
	t.Run("an empty job is refused", func(t *testing.T) {
		t.Parallel()
		require.ErrorContains(t, sweepNativeJob(t.TempDir(), "   "), "not known")
	})
	t.Run("a job under the root is removed", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		job := filepath.Join(root, "job")
		require.NoError(t, os.MkdirAll(job, 0o755))
		require.NoError(t, sweepNativeJob(root, job))
		assert.NoDirExists(t, job)
	})
	t.Run("a job outside the root is refused and kept", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		outside := filepath.Join(t.TempDir(), "kept")
		require.NoError(t, os.MkdirAll(outside, 0o755))
		require.Error(t, sweepNativeJob(root, outside))
		assert.DirExists(t, outside)
	})
}

func TestSwarmNativeHelpersCoverModelProviderMissingAuth(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	named := filepath.Join(dir, "named-auth.json")
	other := filepath.Join(dir, "other-auth.json")
	bad := filepath.Join(dir, "bad-auth.json")
	require.NoError(t, os.WriteFile(named, []byte(`{"opencode":{"type":"api"}}`), 0o644))
	require.NoError(t, os.WriteFile(other, []byte(`{"other":{"type":"api"}}`), 0o644))
	require.NoError(t, os.WriteFile(bad, []byte("not json"), 0o644))
	keyed := []byte(`{"provider":{"opencode":{"options":{"apiKey":"x"}}}}`)
	keyless := []byte(`{"provider":{"opencode":{"options":{"baseURL":"http://localhost:11434"}}}}`)
	cases := []struct {
		name     string
		raw      []byte
		authPath string
		provider string
		want     bool
	}{
		{name: "a config that does not parse names nothing", raw: []byte("{"), provider: "opencode", want: false},
		{name: "a config with no provider object names nothing", raw: []byte(`{"foo":1}`), provider: "opencode", want: false},
		{name: "a provider the config does not name is not asked about", raw: []byte(`{"provider":{"other":{"options":{"apiKey":"x"}}}}`), provider: "opencode", want: false},
		{name: "a keyless entry needs no key", raw: keyless, provider: "opencode", want: false},
		{name: "a keyed entry with no auth path is missing auth", raw: keyed, provider: "opencode", want: true},
		{name: "a keyed entry whose auth file is missing is missing auth", raw: keyed, authPath: filepath.Join(dir, "absent-auth.json"), provider: "opencode", want: true},
		{name: "a keyed entry whose auth file is not JSON is missing auth", raw: keyed, authPath: bad, provider: "opencode", want: true},
		{name: "a keyed entry the auth file does not name is missing auth", raw: keyed, authPath: other, provider: "opencode", want: true},
		{name: "a keyed entry the auth file names has auth", raw: keyed, authPath: named, provider: "opencode", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, modelProviderMissingAuth(tc.raw, tc.authPath, tc.provider))
		})
	}
}

func TestSwarmNativeHelpersCoverKeylessProvider(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		v    any
		want bool
	}{
		{name: "a non-map value is not a provider", v: "opencode", want: false},
		{name: "a provider entry with no options is not keyless", v: map[string]any{"type": "api"}, want: false},
		{name: "an entry whose options carry no baseURL is not keyless", v: map[string]any{"options": map[string]any{"apiKey": "x"}}, want: false},
		{name: "an entry with an empty baseURL is not keyless", v: map[string]any{"options": map[string]any{"baseURL": ""}}, want: false},
		{name: "an entry with a baseURL and an apiKey is not keyless", v: map[string]any{"options": map[string]any{"baseURL": "http://localhost:11434", "apiKey": "x"}}, want: false},
		{name: "an entry with a baseURL and no apiKey is keyless", v: map[string]any{"options": map[string]any{"baseURL": "http://localhost:11434"}}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, keylessProvider(tc.v))
		})
	}
}

func TestSwarmNativeHelpersCoverFileSHA256(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "card.md")
	body := []byte("the card as written\n")
	require.NoError(t, os.WriteFile(path, body, 0o644))
	sum := sha256.Sum256(body)
	got, err := fileSHA256(path)
	require.NoError(t, err)
	assert.Equal(t, hex.EncodeToString(sum[:]), got)
	_, err = fileSHA256(filepath.Join(dir, "absent"))
	assert.Error(t, err)
}

func TestSwarmNativeHelpersCoverNativeEndLeftovers(t *testing.T) {
	t.Parallel()
	// a pgid of zero names no group: swarm.GroupAlive refuses it before anything is
	// signalled, so there is nothing to end and nothing to report.
	assert.Empty(t, nativeEndLeftovers(0, ""))
}
