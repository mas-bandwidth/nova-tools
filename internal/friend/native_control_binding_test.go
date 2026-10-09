//go:build linux || darwin

package friend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNativeControlBindingHoldsChangedIdentityAndLostKnownAuthority(t *testing.T) {
	t.Parallel()
	for _, damage := range []string{"replaced-work", "corrupt-index", "missing-index", "missing-namespace", "symlink-index"} {
		t.Run(damage, func(t *testing.T) {
			t.Parallel()
			home, work := t.TempDir(), filepath.Join(t.TempDir(), "work")
			require.NoError(t, os.Mkdir(work, 0700))
			root, err := NativeControlDir(home, work)
			require.NoError(t, err)
			index := filepath.Join(home, ".nova-friend", "native-control", "INDEX.json")
			before, err := os.ReadFile(index)
			require.NoError(t, err)
			switch damage {
			case "replaced-work":
				require.NoError(t, os.Rename(work, work+"-prior"))
				require.NoError(t, os.Mkdir(work, 0700))
			case "corrupt-index": require.NoError(t, os.WriteFile(index, []byte("not-json"), 0600))
			case "missing-index": require.NoError(t, os.Remove(index))
			case "missing-namespace": require.NoError(t, os.Rename(root, root+"-prior"))
			case "symlink-index": require.NoError(t, os.Rename(index, index+"-prior")); require.NoError(t, os.Symlink(index+"-prior", index))
			}
			_, err = NativeControlDir(home, work)
			assert.Error(t, err, "damaged durable authority must not select a replacement namespace")
			if damage == "replaced-work" || damage == "missing-namespace" {
				after, err := os.ReadFile(index)
				require.NoError(t, err)
				assert.Equal(t, before, after, "hold preserves the durable index")
			}
		})
	}
}

func TestNativeControlStateBindingCannotSilentlyResetExistingOwnership(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	root := filepath.Join(t.TempDir(), "root")
	require.NoError(t, bindNativeControlState(state, root))
	require.NoError(t, bindNativeControlState(state, root))
	require.NoError(t, WriteLanes(state, LaneState{Sessions: map[int]string{1: "owned-session"}}))
	assert.Error(t, bindNativeControlState(state, root+"-replacement"))
	require.NoError(t, os.Remove(filepath.Join(state, "CONTROL-BINDING")))
	assert.ErrorContains(t, bindNativeControlState(state, root), "known control binding")
	lanes, err := ReadLanes(state)
	require.NoError(t, err)
	assert.Equal(t, "owned-session", lanes.Sessions[1])
}

func TestNativeControlPhysicalAliasUsesExistingAuthorityAndRequiresLiveProof(t *testing.T) {
	t.Parallel()
	home, work := t.TempDir(), t.TempDir()
	root, err := NativeControlDir(home, work)
	require.NoError(t, err)
	indexPath := filepath.Join(home, ".nova-friend", "native-control", "INDEX.json")
	b, err := os.ReadFile(indexPath)
	require.NoError(t, err)
	var index nativeControlIndex
	require.NoError(t, json.Unmarshal(b, &index))
	info, err := os.Stat(work)
	require.NoError(t, err)
	id, err := nativeWorkIdentity(info)
	require.NoError(t, err)
	// A distinct existing path resolving to the same live object uses the old
	// namespace. SameFile is essential; numerical reuse without a path holds.
	alias := filepath.Join(t.TempDir(), "alias")
	require.NoError(t, os.Symlink(work, alias))
	selected, err := bindNativeWork(filepath.Dir(root), indexPath, &index, alias, info, id)
	require.NoError(t, err)
	assert.Equal(t, root, selected)
	index.Bindings[0].Paths = []string{filepath.Join(t.TempDir(), "missing-work")}
	_, err = bindNativeWork(filepath.Dir(root), indexPath, &index, work, info, id)
	assert.ErrorContains(t, err, "cannot be verified")
}

type nativeControlStatInfo struct { os.FileInfo; stat syscall.Stat_t }
func (i nativeControlStatInfo) Sys() any { return &i.stat }

func TestNativeControlRejectsForeignWorkUIDAndWritableAccountBase(t *testing.T) {
	t.Parallel()
	work := t.TempDir()
	info, err := os.Stat(work)
	require.NoError(t, err)
	st := *info.Sys().(*syscall.Stat_t)
	st.Uid++
	_, err = nativeWorkIdentity(nativeControlStatInfo{FileInfo: info, stat: st})
	assert.ErrorContains(t, err, "effective OS UID")
	home := t.TempDir()
	require.NoError(t, os.Chmod(home, 0777))
	_, err = NativeControlDir(home, work)
	assert.ErrorContains(t, err, "writable by another account")
}

func TestNativeControlCaseAliasUsesOneExistingNamespaceWhenFilesystemFoldsCase(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	work := filepath.Join(parent, "case-work")
	require.NoError(t, os.Mkdir(work, 0700))
	alias := filepath.Join(parent, strings.ToUpper(filepath.Base(work)))
	first, err := os.Stat(work)
	require.NoError(t, err)
	other, err := os.Stat(alias)
	if err != nil || !os.SameFile(first, other) { t.Skip("this filesystem does not fold case") }
	home := t.TempDir()
	root, err := NativeControlDir(home, work)
	require.NoError(t, err)
	aliased, err := NativeControlDir(home, alias)
	require.NoError(t, err)
	assert.Equal(t, root, aliased)
}
