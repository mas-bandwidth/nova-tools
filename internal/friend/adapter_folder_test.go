package friend

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFolderRouteRequiresRealCodexSessionAndWatchedDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, tc := range []struct{ harness, session, adapter, target, why string }{
		{"codex", "", "folder", dir, "--session"},
		{"claude", "real", "folder", dir, "--harness codex"},
		{"codex", "real", "", dir, "--adapter folder"},
		{"codex", "real", "folder", "relative", "absolute path"},
		{"codex", "real", "folder", filepath.Join(dir, "missing"), "not an existing directory"},
	} {
		_, err := SelectDeliverer("bob", tc.harness, "/work", tc.session, tc.adapter, tc.target, nil, nil)
		require.ErrorContains(t, err, tc.why)
	}
	d, err := SelectDeliverer("bob", "codex", "/work", "real-thread", "folder", dir, nil, nil)
	require.NoError(t, err)
	assert.IsType(t, &Folder{}, d)
	defaultRoute, err := SelectDeliverer("bob", "codex", "/work", "real-thread", "", "", nil, nil)
	require.NoError(t, err)
	assert.IsType(t, &Codex{}, defaultRoute)
}

func TestFolderConformanceRequiresSessionPongWithCurrentNonce(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, answer, stage string
	}{
		{"real session answers", "current", ""},
		{"wrong nonce is not proof", "wrong", StageAct},
		{"stale nonce is not proof", "stale", StageAct},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newConformanceRig(t, "opencode")
			dir := t.TempDir()
			r.check.Harness = "codex"
			r.check.Deliver = &Folder{Dir: dir, Friend: "bob", Session: "real-thread", WorkDir: "/work/bob"}
			if tc.answer == "stale" {
				r.session.act(r.check.Text("older"))
			}
			r.session.poll = func() {
				if tc.answer == "stale" {
					return
				}
				text, err := os.ReadFile(filepath.Join(dir, "FRIEND-CHECK-c0ffee.md"))
				require.NoError(t, err)
				if tc.answer == "wrong" {
					r.session.act(r.check.Text("different"))
					return
				}
				r.session.act(string(text))
			}
			got := r.check.Run(context.Background())
			assert.Equal(t, tc.stage, got.Stage, got.Line())
		})
	}
}

func TestFolderDeliversExactNativeTextWithSidecarAndNonceDedup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := &Folder{Dir: dir, Friend: "bob", Session: "real-thread", WorkDir: "/work/bob"}
	text := SessionCheckText("nonce1", "nova-friend pong --as bob --nonce nonce1", "ada")
	for range 2 {
		exit, err := f.Deliver(context.Background(), text)
		require.NoError(t, err)
		assert.Zero(t, exit)
	}
	path := filepath.Join(dir, "FRIEND-CHECK-nonce1.md")
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, text, string(got))
	var meta map[string]string
	raw, err := os.ReadFile(path + ".meta.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &meta))
	assert.Equal(t, "bob", meta["friend"])
	assert.Equal(t, "codex", meta["harness"])
	assert.Equal(t, "real-thread", meta["session"])
	assert.Equal(t, "/work/bob", meta["work_dir"])
	assert.Equal(t, "check", meta["kind"])
	assert.Equal(t, "nonce1", meta["nonce"])
	assert.Equal(t, fmt.Sprintf("%x", sha256.Sum256(got)), meta["sha256"])
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 3, "reasking the same nonce cannot create another composer item")
	_, err = f.Deliver(context.Background(), strings.ReplaceAll(text, "ada", "carol"))
	require.ErrorContains(t, err, "refused to replace unacknowledged")
	f.Session = "different-thread"
	_, err = f.Deliver(context.Background(), text)
	require.ErrorContains(t, err, "without matching session metadata")
	f.Session = "real-thread"
	require.NoError(t, os.Remove(path+".meta.json"))
	_, err = f.Deliver(context.Background(), text)
	require.ErrorContains(t, err, "without matching session metadata")
	assert.FileExists(t, path)
}

// NativeFolderFile identifies files the folder watcher may forward. It does
// not itself prove that a session read or acted on their contents.
func NativeFolderFile(name string) bool {
	if !strings.HasSuffix(name, ".md") {
		return false
	}
	return strings.HasPrefix(name, "FRIEND-CHECK-") || strings.HasPrefix(name, "FRIEND-WAKE-") || strings.HasPrefix(name, "FRIEND-PUSH-")
}

func TestNativeFolderFileExcludesMetadata(t *testing.T) {
	t.Parallel()
	assert.True(t, NativeFolderFile("FRIEND-CHECK-n1.md"))
	assert.False(t, NativeFolderFile("FRIEND-CHECK-n1.md.meta.json"))
	assert.False(t, NativeFolderFile(".friend-push-incomplete"))
}

func TestFolderConcurrentSameNonceKeepsOneMatchingPair(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := &Folder{Dir: dir, Friend: "bob", Session: "real-thread", WorkDir: "/work/bob"}
	texts := []string{
		SessionCheckText("same", "nova-friend pong --as bob --nonce same", "ada"),
		SessionCheckText("same", "nova-friend pong --as bob --nonce same", "carol"),
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range texts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = f.Deliver(context.Background(), texts[i])
		}(i)
	}
	wg.Wait()
	assert.Equal(t, 1, boolCount(errs[0] == nil, errs[1] == nil), "exactly one publication wins")
	path := filepath.Join(dir, "FRIEND-CHECK-same.md")
	payload, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, texts, string(payload))
	var meta map[string]string
	data, err := os.ReadFile(path + ".meta.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &meta))
	assert.Equal(t, fmt.Sprintf("%x", sha256.Sum256(payload)), meta["sha256"])
}

func boolCount(bs ...bool) int {
	n := 0
	for _, b := range bs {
		if b {
			n++
		}
	}
	return n
}

func TestFolderDeliveryFailureAndWakeDoNotImpersonatePong(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := &Folder{Dir: dir, Friend: "bob", Session: "real-thread", WorkDir: "/work/bob"}
	_, err := f.Deliver(context.Background(), WakeTurnText("nova-friend pong --as bob --nonce wake1"))
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(dir, "FRIEND-WAKE-wake1.md"))
	assert.NoFileExists(t, filepath.Join(dir, "pong.json"))
	_, err = f.Deliver(context.Background(), "PRESENT: card /work/bob/jobs/one")
	require.NoError(t, err)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Condition(t, func() bool {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "FRIEND-PUSH-") && strings.HasSuffix(e.Name(), ".md") {
				return true
			}
		}
		return false
	})
	f.Dir = filepath.Join(dir, "gone")
	_, err = f.Deliver(context.Background(), "x")
	require.ErrorContains(t, err, "not a directory")
}

func TestFolderUnlockFaultDoesNotRetryAnAlreadyPublishedTurn(t *testing.T) {
	t.Parallel()
	unlockErr := errors.New("sync failed")
	path := "/watch/FRIEND-PUSH-one.md"
	var reportedPath string
	var reportedErr error
	report := func(path string, err error) { reportedPath, reportedErr = path, err }

	// Returning an error after the rename would make the daemon deliver this
	// non-nonce turn again under a new random filename.
	require.NoError(t, folderReleaseResult(nil, unlockErr, path, "/watch", report))
	assert.Equal(t, path, reportedPath)
	assert.ErrorIs(t, reportedErr, unlockErr)

	// Before publication, both the original failure and cleanup fault matter.
	deliveryErr := errors.New("write failed")
	reportedPath, reportedErr = "", nil
	err := folderReleaseResult(deliveryErr, unlockErr, "", "/watch", report)
	assert.ErrorIs(t, err, deliveryErr)
	assert.ErrorIs(t, err, unlockErr)
	assert.Empty(t, reportedPath)
	assert.NoError(t, reportedErr)
}
