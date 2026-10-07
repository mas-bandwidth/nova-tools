package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAssets is a release's host in memory: its assets by name, and its tag's message. No
// network: the follow's fetch is held to what it reads and what it refuses.
type fakeAssets struct {
	assets  map[string][]byte
	message string
	tagErr  error
	got     []string
}

func (f *fakeAssets) Asset(_ context.Context, repo, version, name string) ([]byte, error) {
	f.got = append(f.got, repo+"@"+version+"/"+name)
	b, ok := f.assets[name]
	if !ok {
		return nil, errors.New("404 Not Found")
	}
	return b, nil
}

func (f *fakeAssets) TagMessage(context.Context, string, string) (string, error) {
	return f.message, f.tagErr
}

func sumHex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// release is a fake release of one tool for two platforms with its SHA256SUMS over them.
func release(t *testing.T) (*fakeAssets, string) {
	t.Helper()
	name := AssetName("nova-friend", "v1.2.0", "linux", "amd64")
	other := AssetName("nova-friend", "v1.2.0", "darwin", "arm64")
	bin, bin2 := []byte("#!/bin/sh\necho nova-friend v1.2.0\n"), []byte("other bytes")
	sums := fmt.Sprintf("%s  %s\n%s  %s\n", sumHex(bin2), other, sumHex(bin), name)
	return &fakeAssets{assets: map[string][]byte{name: bin, other: bin2, SumsFile: []byte(sums)}}, sumHex([]byte(sums))
}

func TestAssetName(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "nova-friend_v1.2.0_linux_amd64", AssetName("nova-friend", "v1.2.0", "linux", "amd64"))
	assert.Equal(t, "nova-friend_v1.2.0_windows_amd64.exe", AssetName("nova-friend", "v1.2.0", "windows", "amd64"))
}

// The tool's asset is fetched by the name SHA256SUMS lists it under, verified against its
// line, and staged beside the running binary as a private executable; the tag's digest,
// when the tag carries one, holds SHA256SUMS itself.
func TestFetchToolStagesTheVerifiedAssetAndHoldsSumsToTheTag(t *testing.T) {
	t.Parallel()
	src, digest := release(t)
	src.message = "v1.2.0\n\n" + AnnotationSumsPrefix + digest + "\n"
	dir := t.TempDir()
	got, err := FetchTool(context.Background(), src, "owner/name", "v1.2.0", "nova-friend", "linux", "amd64", dir)
	require.NoError(t, err)
	assert.Equal(t, digest, got.Digest, "SHA256SUMS matched the digest the tag was cut with")
	assert.Equal(t, dir, filepath.Dir(got.Staged))
	assert.True(t, strings.HasPrefix(filepath.Base(got.Staged), ".nova-friend.v1.2.0.new."), got.Staged)
	info, err := os.Stat(got.Staged)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
	body, err := os.ReadFile(got.Staged)
	require.NoError(t, err)
	assert.Equal(t, src.assets["nova-friend_v1.2.0_linux_amd64"], body)
	assert.Equal(t, []string{"owner/name@v1.2.0/SHA256SUMS", "owner/name@v1.2.0/nova-friend_v1.2.0_linux_amd64"}, src.got, "the checksum file first, then the one asset")
	assert.NoError(t, Swap(got.Staged, filepath.Join(dir, "nova-friend")))
	_, err = os.Stat(filepath.Join(dir, "nova-friend"))
	assert.NoError(t, err)
}

// A tag with no digest line (a release cut before the tags were annotated, or a
// lightweight tag) is the release's own checksum file alone, said by the empty Digest.
func TestFetchToolWithoutATagDigestVerifiesByTheReleaseAlone(t *testing.T) {
	t.Parallel()
	src, _ := release(t)
	src.message = "v1.2.0\n\nCut from abc.\n"
	got, err := FetchTool(context.Background(), src, "owner/name", "v1.2.0", "nova-friend", "linux", "amd64", t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, got.Digest)
	assert.NotEmpty(t, got.Staged)
}

// What is refused stages nothing: SHA256SUMS not the tag's, the asset's bytes not its
// line's, a platform the release does not ship, a tag that cannot be read, a version that
// is none, and a repository that is not owner/name.
func TestFetchToolRefusals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t.Run("the checksum file is not the one the tag was cut with", func(t *testing.T) {
		t.Parallel()
		src, _ := release(t)
		src.message = AnnotationSumsPrefix + strings.Repeat("a", 64) + "\n"
		dir := t.TempDir()
		_, err := FetchTool(ctx, src, "owner/name", "v1.2.0", "nova-friend", "linux", "amd64", dir)
		require.ErrorContains(t, err, "not the one it was cut with")
		assert.Len(t, src.got, 1, "nothing more is fetched")
		assertEmptyDir(t, dir)
	})
	t.Run("the asset's bytes are not its line's", func(t *testing.T) {
		t.Parallel()
		src, _ := release(t)
		src.assets["nova-friend_v1.2.0_linux_amd64"] = []byte("tampered")
		dir := t.TempDir()
		_, err := FetchTool(ctx, src, "owner/name", "v1.2.0", "nova-friend", "linux", "amd64", dir)
		require.ErrorContains(t, err, "not the bytes that were cut")
		assertEmptyDir(t, dir)
	})
	t.Run("a platform the release does not ship", func(t *testing.T) {
		t.Parallel()
		src, _ := release(t)
		_, err := FetchTool(ctx, src, "owner/name", "v1.2.0", "nova-friend", "plan9", "mips", t.TempDir())
		assert.ErrorIs(t, err, ErrNotShipped)
	})
	t.Run("the tag cannot be read", func(t *testing.T) {
		t.Parallel()
		src, _ := release(t)
		src.tagErr = errors.New("403 rate limited")
		_, err := FetchTool(ctx, src, "owner/name", "v1.2.0", "nova-friend", "linux", "amd64", t.TempDir())
		assert.ErrorContains(t, err, "cannot be read for its digest: 403 rate limited")
	})
	t.Run("no checksum file", func(t *testing.T) {
		t.Parallel()
		src, _ := release(t)
		delete(src.assets, SumsFile)
		_, err := FetchTool(ctx, src, "owner/name", "v1.2.0", "nova-friend", "linux", "amd64", t.TempDir())
		assert.ErrorContains(t, err, "SHA256SUMS of release v1.2.0 of owner/name cannot be fetched: 404")
	})
	t.Run("a version that is none, a repository that is none", func(t *testing.T) {
		t.Parallel()
		src, _ := release(t)
		_, err := FetchTool(ctx, src, "owner/name", "1.2.0", "nova-friend", "linux", "amd64", t.TempDir())
		assert.ErrorContains(t, err, "not v-prefixed")
		_, err = FetchTool(ctx, src, "name", "v1.2.0", "nova-friend", "linux", "amd64", t.TempDir())
		assert.ErrorContains(t, err, "not owner/name")
		assert.Empty(t, src.got)
	})
}

func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "nothing staged")
}

// ParseSums is what ReadSums reads off a file: the same lines, the same refusals.
func TestParseSumsIsReadSums(t *testing.T) {
	t.Parallel()
	body := []byte("bbbb  z-tool\naaaa  a-tool\n")
	arts, err := ParseSums(body)
	require.NoError(t, err)
	assert.Equal(t, []Artifact{{Name: "a-tool", Sum: "aaaa"}, {Name: "z-tool", Sum: "bbbb"}}, arts, "sorted by name")
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, SumsFile), body, 0o644))
	read, err := ReadSums(dir)
	require.NoError(t, err)
	assert.Equal(t, arts, read)
	_, err = ParseSums([]byte("aaaa  ../escape\n"))
	assert.ErrorContains(t, err, "names a path rather than a file")
	_, err = ParseSums([]byte("\n"))
	assert.ErrorContains(t, err, "lists no artifact")
}
