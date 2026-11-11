//go:build functional

package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// opencode_play_functional_test.go runs roles/opencode on localhost through ansible-core, against a release directory
// it writes itself (a file:// URL, an archive holding a stand-in opencode), with group_vars/all.yml and the role's
// defaults as install.yml reads them. HOME is a temp dir, so nova_bin_dir is <tmp>/.local/bin. opencode_play_test.go
// is the structural half.

const opencodePlay = `- hosts: localhost
  connection: local
  gather_facts: true
  gather_subset: [min]
  vars_files: ["{{ fleet }}/group_vars/all.yml"]
  tasks:
    - ansible.builtin.include_role: { name: opencode }
`

// fakeOpencode answers --version with the pinned version, and env with the variable the wrapper sets.
const fakeOpencode = "#!/bin/sh\ncase \"$1\" in env) echo \"$OPENCODE_EXPERIMENTAL_OUTPUT_TOKEN_MAX\" ;; *) echo 1.18.20 ;; esac\n"

// opencodeRelease writes this platform's archive into a new release directory and returns the extra vars that point
// the role at it, its digest pinned (or a wrong one).
func opencodeRelease(t *testing.T, wrongDigest bool) string {
	t.Helper()
	dir := t.TempDir()
	arch := map[string]string{"amd64": "amd64", "arm64": "arm64"}[runtime.GOARCH]
	platform := runtime.GOOS + "-" + arch
	var buf bytes.Buffer
	var asset string
	if runtime.GOOS == "darwin" {
		asset = "opencode-darwin-x.zip"
		zw := zip.NewWriter(&buf)
		h := &zip.FileHeader{Name: "opencode", Method: zip.Deflate}
		h.SetMode(0o755)
		w, err := zw.CreateHeader(h)
		require.NoError(t, err)
		_, err = w.Write([]byte(fakeOpencode))
		require.NoError(t, err)
		require.NoError(t, zw.Close())
	} else {
		asset = "opencode-linux-x.tar.gz"
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: "opencode", Mode: 0o755, Size: int64(len(fakeOpencode)), Typeflag: tar.TypeReg}))
		_, err := tw.Write([]byte(fakeOpencode))
		require.NoError(t, err)
		require.NoError(t, tw.Close())
		require.NoError(t, gz.Close())
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, asset), buf.Bytes(), 0o644))
	sum := sha256.Sum256(buf.Bytes())
	digest := hex.EncodeToString(sum[:])
	if wrongDigest {
		digest = strings.Repeat("0", 64)
	}
	b, err := json.Marshal(map[string]any{
		"nova_opencode_release_url": "file://" + dir,
		"nova_opencode_assets":      map[string]string{platform: asset},
		"nova_opencode_sha256":      map[string]string{platform: digest},
	})
	require.NoError(t, err)
	return string(b)
}

// runOpencodePlay runs the role on localhost with HOME at home.
func runOpencodePlay(t *testing.T, home, vars string, extra ...string) (string, error) {
	t.Helper()
	fleet, err := filepath.Abs(".")
	require.NoError(t, err)
	dir := t.TempDir()
	play := filepath.Join(dir, "opencode.yml")
	require.NoError(t, os.WriteFile(play, []byte(opencodePlay), 0o644))
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	args := append([]string{"-i", "localhost,", "-e", "fleet=" + fleet, "-e", vars}, extra...)
	cmd := exec.CommandContext(ctx, "ansible-playbook", append(args, play)...)
	cmd.Env = append(os.Environ(), "ANSIBLE_NOCOLOR=1", "HOME="+home, "ANSIBLE_LOCAL_TEMP="+filepath.Join(dir, "tmp"),
		"ANSIBLE_ROLES_PATH="+filepath.Join(fleet, "roles"), "TMPDIR="+dir)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func skipWithoutAnsible(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine; opencode_play_test.go still runs")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("no pinned platform for " + runtime.GOARCH)
	}
}

// A fresh machine gets opencode.bin and the wrapper in nova_bin_dir; the wrapper sets nova_opencode_env and execs the
// binary beside it; a second run changes nothing.
func TestOpencodeRoleInstallsThePinnedBinaryAndWrapper(t *testing.T) {
	t.Parallel()
	skipWithoutAnsible(t)
	home := t.TempDir()
	vars := opencodeRelease(t, false)
	out, err := runOpencodePlay(t, home, vars)
	require.NoError(t, err, out)
	bin := filepath.Join(home, ".local", "bin")
	assert.Contains(t, out, "OPENCODE localhost version=1.18.20 path="+bin+"/opencode")

	b, err := os.ReadFile(filepath.Join(bin, "opencode.bin"))
	require.NoError(t, err)
	assert.Equal(t, fakeOpencode, string(b))
	w, err := os.ReadFile(filepath.Join(bin, "opencode"))
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/sh\n# opencode 1.18.20, from nova-tools fleet/roles/opencode: the binary is opencode.bin beside it\n"+
		"export OPENCODE_EXPERIMENTAL_DISABLE_FILEWATCHER=1\nexport OPENCODE_EXPERIMENTAL_OUTPUT_TOKEN_MAX=128000\n"+
		"exec \"$(dirname \"$0\")/opencode.bin\" \"$@\"\n", string(w))
	env, err := exec.Command(filepath.Join(bin, "opencode"), "env").Output()
	require.NoError(t, err)
	assert.Equal(t, "128000\n", string(env))
	entries, err := os.ReadDir(bin)
	require.NoError(t, err)
	assert.Len(t, entries, 2, "only opencode and opencode.bin in the bin directory (the archive is cached elsewhere)")

	out, err = runOpencodePlay(t, home, vars)
	require.NoError(t, err, out)
	assert.Contains(t, out, "changed=0", "a second run changed something")
}

// An opencode.bin that answers another version (the studio's 2.x, a hand copy) is replaced by the pinned one.
func TestOpencodeRoleReplacesAnotherVersion(t *testing.T) {
	t.Parallel()
	skipWithoutAnsible(t)
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(bin, "opencode.bin"), []byte("#!/bin/sh\necho 2.2.6\n"), 0o755))
	out, err := runOpencodePlay(t, home, opencodeRelease(t, false))
	require.NoError(t, err, out)
	b, err := os.ReadFile(filepath.Join(bin, "opencode.bin"))
	require.NoError(t, err)
	assert.Equal(t, fakeOpencode, string(b))
}

// An archive that does not hold the pinned digest is refused, and nothing reaches the bin directory.
func TestOpencodeRoleRefusesAnotherDigest(t *testing.T) {
	t.Parallel()
	skipWithoutAnsible(t)
	home := t.TempDir()
	out, err := runOpencodePlay(t, home, opencodeRelease(t, true))
	require.Error(t, err, out)
	assert.Contains(t, out, "checksum")
	_, err = os.Stat(filepath.Join(home, ".local", "bin", "opencode.bin"))
	assert.True(t, os.IsNotExist(err), "opencode.bin written from an archive with the wrong digest")
}

// --check on a fresh machine writes nothing and says what it would install.
func TestOpencodeRoleCheckWritesNothing(t *testing.T) {
	t.Parallel()
	skipWithoutAnsible(t)
	home := t.TempDir()
	out, err := runOpencodePlay(t, home, opencodeRelease(t, false), "--check")
	require.NoError(t, err, out)
	assert.Contains(t, out, "WOULD-INSTALL opencode 1.18.20")
	_, err = os.Stat(filepath.Join(home, ".local"))
	assert.True(t, os.IsNotExist(err), "--check wrote into HOME")
}

// handOpencodeWrapper is the wrapper the benches had in /usr/local/bin before the role (amd-1, 2026-10-13).
const handOpencodeWrapper = "#!/bin/sh\n# wrapper (Rowan 2026-10-13): mirrors the darwin harness wrapper so the output-token max reaches opencode\n" +
	"export OPENCODE_EXPERIMENTAL_DISABLE_FILEWATCHER=1\nexport OPENCODE_EXPERIMENTAL_OUTPUT_TOKEN_MAX=128000\n" +
	"exec \"$(dirname \"$0\")/opencode.bin\" \"$@\"\n"

// The hand copies of this opencode (an identical opencode.bin with a hand wrapper, or the bare binary as opencode)
// are removed; a copy that differs stays, and so does a wrapper whose opencode.bin differs. --check removes nothing.
func TestOpencodeRoleRemovesItsHandCopies(t *testing.T) {
	t.Parallel()
	skipWithoutAnsible(t)
	cases := []struct {
		name          string
		bin, top      string // "" is no file
		wantBin, keep bool
	}{
		{name: "wrapper and identical binary", bin: fakeOpencode, top: handOpencodeWrapper},
		{name: "bare identical binary", top: fakeOpencode},
		{name: "another binary", bin: "#!/bin/sh\necho 1.17.0\n", top: handOpencodeWrapper, keep: true},
		{name: "another wrapper", bin: fakeOpencode, top: "#!/bin/sh\nexec /opt/x/opencode \"$@\"\n", wantBin: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			hand := t.TempDir()
			if c.bin != "" {
				require.NoError(t, os.WriteFile(filepath.Join(hand, "opencode.bin"), []byte(c.bin), 0o755))
			}
			require.NoError(t, os.WriteFile(filepath.Join(hand, "opencode"), []byte(c.top), 0o755))
			vars := opencodeRelease(t, false)
			handVar := `{"nova_opencode_hand_dir": "` + hand + `"}`

			out, err := runOpencodePlay(t, home, vars, "-e", handVar)
			require.NoError(t, err, out)
			_, errTop := os.Stat(filepath.Join(hand, "opencode"))
			_, errBin := os.Stat(filepath.Join(hand, "opencode.bin"))
			if c.keep {
				assert.NoError(t, errTop, out)
				assert.NoError(t, errBin, out)
				return
			}
			if c.wantBin {
				assert.NoError(t, errTop, "a wrapper that is not the hand one stays: "+out)
				assert.True(t, os.IsNotExist(errBin), "the identical opencode.bin goes: "+out)
				return
			}
			assert.True(t, os.IsNotExist(errTop), out)
			assert.True(t, os.IsNotExist(errBin), out)
			out, err = runOpencodePlay(t, home, vars, "-e", handVar)
			require.NoError(t, err, out)
			assert.Contains(t, out, "changed=0", "a second run changed something")
		})
	}
}

// --check names the hand copies it would remove and removes none.
func TestOpencodeRoleCheckKeepsHandCopies(t *testing.T) {
	t.Parallel()
	skipWithoutAnsible(t)
	home := t.TempDir()
	hand := t.TempDir()
	vars := opencodeRelease(t, false)
	out, err := runOpencodePlay(t, home, vars)
	require.NoError(t, err, out)
	require.NoError(t, os.WriteFile(filepath.Join(hand, "opencode.bin"), []byte(fakeOpencode), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(hand, "opencode"), []byte(handOpencodeWrapper), 0o755))
	out, err = runOpencodePlay(t, home, vars, "-e", `{"nova_opencode_hand_dir": "`+hand+`"}`, "--check")
	require.NoError(t, err, out)
	assert.Contains(t, out, hand+"/opencode.bin")
	_, err = os.Stat(filepath.Join(hand, "opencode"))
	assert.NoError(t, err, "--check removed the wrapper")
	_, err = os.Stat(filepath.Join(hand, "opencode.bin"))
	assert.NoError(t, err, "--check removed opencode.bin")
}
