//go:build functional

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestARealStore runs the built binary against real sops, age-keygen and git, on a
// store and keys made in the test's own directory: init, seat add, set, names, exec with
// its exit status passed through, and the refusals.
func TestARealStore(t *testing.T) {
	t.Parallel()
	sops, err := exec.LookPath("sops")
	if err != nil {
		t.Skip("sops not on PATH")
	}
	ageKeygen, err := exec.LookPath("age-keygen")
	if err != nil {
		t.Skip("age-keygen not on PATH")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "nova-secrets")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	require.NoError(t, build.Run())
	keys, store := filepath.Join(dir, "keys"), filepath.Join(dir, "store")
	require.NoError(t, os.Mkdir(keys, 0o700))
	require.NoError(t, os.Mkdir(store, 0o755))
	env := append(os.Environ(), "HOME="+dir, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	run := func(stdin string, want int, args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env, cmd.Stdin = env, strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			require.NoError(t, err)
		}
		require.Equal(t, want, code, "%v: %s", args, out)
		return string(out)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", store}, args...)...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}

	keygen := func(name string) string {
		key := filepath.Join(keys, name+".key")
		out, err := exec.Command(ageKeygen, "-o", key).CombinedOutput()
		require.NoError(t, err, "age-keygen -o: %s", out)
		pub, err := exec.Command(ageKeygen, "-y", key).Output()
		require.NoError(t, err)
		return strings.TrimSpace(string(pub))
	}
	recPub := keygen("recovery")
	ada := filepath.Join(keys, "ada.key")
	adaPub := keygen("ada")
	pins := filepath.Join(dir, "pins")
	assert.Contains(t, run("", 0, "init", "--store", store, "--recovery", recPub, "--pins", pins), "SECRETS INIT OK store="+store+"\nnext: age-keygen -o "+dir+"/.config/nova-secrets/")
	assert.Contains(t, run("", 2, "init", "--store", store, "--recovery", recPub), "store "+store+" exists; use: nova-secrets seat add")
	git("log", "--oneline")
	assert.Contains(t, run("", 2, "init", "--store", filepath.Join(store, "sub"), "--recovery", recPub), "is inside the git work tree")
	assert.Contains(t, run("", 0, "seat", "add", "ada", adaPub, "--store", store), "SECRETS SEAT ADD OK seat=ada committed")
	plain := filepath.Join(dir, "plain")
	require.NoError(t, os.Mkdir(plain, 0o755))
	assert.Contains(t, run("v\n", 2, "set", "X", "--store", plain, "--pins", pins, "--key", ada, "--as", "ada", "--sops", sops), "not a git repository")

	seat := []string{"--store", store, "--as", "ada", "--key", ada, "--sops", sops}
	rules, err := os.ReadFile(filepath.Join(store, ".sops.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(rules), "creation_rules:\n  - path_regex: ^ada\\.yaml$\n    age: "+adaPub, "block style, 2-space indent")
	assert.Contains(t, run("", 0, "seat", "list", "--store", store), "ada file=no rule=yes\n")
	eve := filepath.Join(keys, "eve.key")
	keygen("eve")
	eveSeat := []string{"--store", store, "--as", "ada", "--key", eve, "--sops", sops}
	assert.Contains(t, run("value-eve-xyz\n", 2, append([]string{"set", "GH_TOKEN", "--pins", pins}, eveSeat...)...), "is not the key in .sops.yaml for ada")
	assert.NoFileExists(t, filepath.Join(store, "ada.yaml"), "the first set under the wrong key encrypts nothing")
	run("value-one-xyz\n", 0, append([]string{"set", "GH_TOKEN", "--pins", pins}, seat...)...)
	run("value-two-xyz\n", 0, append([]string{"set", "API_KEY", "--pins", pins}, seat...)...)
	sealedFile, err := os.ReadFile(filepath.Join(store, "ada.yaml"))
	require.NoError(t, err)
	assert.NotContains(t, string(sealedFile), "value-one-xyz")
	assert.Contains(t, string(sealedFile), "ENC[")

	assert.Contains(t, run("", 0, "names", "--store", store, "--as", "ada"), "API_KEY\nGH_TOKEN\n", "no key needed")
	assert.NotContains(t, run("", 0, "names", "--store", store, "--as", "ada"), "value-")
	got := run("", 0, append(append([]string{"exec"}, seat...), "--only", "GH_TOKEN", "--require=GH_TOKEN", "--",
		"sh", "-c", `test "$GH_TOKEN" = value-one-xyz && test -z "$API_KEY" && echo delivered`)...)
	assert.Contains(t, got, "delivered")
	assert.NotContains(t, got, "value-one-xyz")
	run("", 7, append(append([]string{"exec"}, seat...), "--only", "API_KEY", "--", "sh", "-c", "exit 7")...)
	assert.Contains(t, run("", 125, append(append([]string{"exec"}, seat...), "--only", "NOPE", "--", "true")...), "unknown secret name")

	// A second seat: seat add, then its own key sets and execs.
	bo := filepath.Join(keys, "bo.key")
	run("", 0, "seat", "add", "bo", keygen("bo"), "--store", store)
	boSeat := []string{"--store", store, "--as", "bo", "--key", bo, "--sops", sops}
	run("value-bo-xyz\n", 0, append([]string{"set", "BO_TOKEN", "--pins", pins}, boSeat...)...)
	got = run("", 0, append(append([]string{"exec"}, boSeat...), "--only", "BO_TOKEN", "--", "sh", "-c", `test "$BO_TOKEN" = value-bo-xyz && echo delivered`)...)
	assert.Contains(t, got, "delivered")
	run("", 125, append(append([]string{"exec"}, seat...), "--only", "BO_TOKEN", "--", "true")...)
	wrong := run("", 125, "exec", "--store", store, "--as", "bo", "--key", ada, "--sops", sops, "--only", "BO_TOKEN", "--", "true")
	assert.Contains(t, wrong, "is not the key in .sops.yaml for bo (")
	assert.NotContains(t, wrong, "value-bo-xyz")
	assert.Contains(t, run("", 125, "exec", "--store", store, "--as", "bo", "--key", bo, "--sops", "/nonexistent/sops", "--only", "BO_TOKEN", "--", "true"), "sops not found at /nonexistent/sops; install sops 3.x")
	assert.Contains(t, run("", 125, "exec", "--store", store, "--as", "zed", "--key", bo, "--sops", sops, "--only", "BO_TOKEN", "--", "true"), "no zed.yaml in ")
	assert.Contains(t, run("", 2, append([]string{"set", "N", "--pins", pins}, seat...)...), "empty or multi-line value")

	require.NoError(t, os.Chmod(ada, 0o644))
	run("", 125, append(append([]string{"exec"}, seat...), "--only", "API_KEY", "--", "true")...)
}

// TestARealFleet is grant on real sops: the control node writes bo.grants.yaml sealed to bo's pinned key and the
// pinned recovery key, which its own key cannot open; a hostile .sops.yaml changes nothing about who can; the
// recovery private key is deleted right after init, so nothing here used it.
func TestARealFleet(t *testing.T) {
	t.Parallel()
	sops, err := exec.LookPath("sops")
	if err != nil {
		t.Skip("sops not on PATH")
	}
	ageKeygen, err := exec.LookPath("age-keygen")
	if err != nil {
		t.Skip("age-keygen not on PATH")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "nova-secrets")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	require.NoError(t, build.Run())
	keys, store, pins := filepath.Join(dir, "keys"), filepath.Join(dir, "store"), filepath.Join(dir, "pins")
	require.NoError(t, os.Mkdir(keys, 0o700))
	env := append(os.Environ(), "HOME="+dir, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	run := func(stdin string, want int, args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env, cmd.Stdin = env, strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			require.NoError(t, err)
		}
		require.Equal(t, want, code, "%v: %s", args, out)
		assert.NotContains(t, string(out), "fleet-bus-xyz")
		return string(out)
	}
	keygen := func(name string) (string, string) {
		key := filepath.Join(keys, name+".key")
		out, err := exec.Command(ageKeygen, "-o", key).CombinedOutput()
		require.NoError(t, err, "age-keygen -o: %s", out)
		pub, err := exec.Command(ageKeygen, "-y", key).Output()
		require.NoError(t, err)
		return key, strings.TrimSpace(string(pub))
	}
	recKey, recPub := keygen("recovery")
	run("", 0, "init", "--store", store, "--recovery", recPub, "--pins", pins)
	require.NoError(t, os.Remove(recKey), "offline from here on")
	ctl, ctlPub := keygen("control")
	bo, boPub := keygen("bo")
	stranger, strangerPub := keygen("stranger")
	run("", 0, "seat", "add", "control", ctlPub, "--store", store)
	run("", 0, "seat", "add", "bo", boPub, "--store", store)
	run("fleet-bus-xyz\n", 0, "set", "NOVA_BUS_PASSWORD", "--store", store, "--pins", pins, "--as", "control", "--key", ctl, "--sops", sops)
	grant := func(want int, extra ...string) string {
		return run("", want, append([]string{"grant", "NOVA_BUS_PASSWORD", "--to", "bo", "--pins", pins, "--store", store, "--as", "control", "--key", ctl, "--sops", sops}, extra...)...)
	}
	opens := func(key string) bool {
		t.Helper()
		cmd := exec.Command(sops, "-d", filepath.Join(store, "bo.grants.yaml"))
		cmd.Env = append(env, "SOPS_AGE_KEY_FILE="+key)
		out, err := cmd.Output()
		return err == nil && strings.Contains(string(out), "fleet-bus-xyz")
	}

	assert.Contains(t, grant(0, "--pub", boPub), "SECRETS GRANT OK from=control to=bo names=1 changed=1 committed")
	assert.True(t, opens(bo), "bo opens its grants")
	assert.False(t, opens(ctl), "the granting key does not")
	got := run("", 0, "exec", "--only", "NOVA_BUS_PASSWORD", "--store", store, "--as", "bo", "--key", bo, "--sops", sops, "--", "sh", "-c", `test "$NOVA_BUS_PASSWORD" = fleet-bus-xyz && echo delivered`)
	assert.Contains(t, got, "delivered")
	assert.Contains(t, grant(0), "changed=0 unchanged")

	// A hostile .sops.yaml: a key_groups entry and a catch-all for the stranger. grant does not read either.
	rules, err := os.ReadFile(filepath.Join(store, ".sops.yaml"))
	require.NoError(t, err)
	hostile := strings.Replace(string(rules), "    age: "+boPub, "    key_groups:\n      - age: ["+strangerPub+"]\n    age: "+boPub, 1) + "  - age: " + strangerPub + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(store, ".sops.yaml"), []byte(hostile), 0o644))
	run("fleet-bus-xyz-two\n", 0, "set", "NOVA_BUS_PASSWORD", "--store", store, "--pins", pins, "--as", "control", "--key", ctl, "--sops", sops)
	assert.Contains(t, grant(0), "changed=1 committed")
	assert.False(t, opens(stranger), "the stranger key opens nothing")
	assert.False(t, opens(ctl))
	// set into bo under that rule is refused before sops runs.
	assert.Contains(t, run("bo-own-xyz\n", 2, "set", "BO_OWN", "--store", store, "--pins", pins, "--as", "bo", "--key", bo, "--sops", sops), "reason=untrusted-rule")
	// Anchors are refused outright.
	require.NoError(t, os.WriteFile(filepath.Join(store, ".sops.yaml"), []byte("k: &k "+strangerPub+"\ncreation_rules:\n  - path_regex: ^bo\\.yaml$\n    age: *k\n"), 0o644))
	assert.Contains(t, grant(2), "reason=untrusted-rule")
	require.NoError(t, os.WriteFile(filepath.Join(store, ".sops.yaml"), rules, 0o644))
	// The machine's key replaced in the rule, or a symlinked grants file: refused.
	require.NoError(t, os.WriteFile(filepath.Join(store, ".sops.yaml"), []byte(strings.Replace(string(rules), "age: "+boPub, "age: "+strangerPub, 1)), 0o644))
	assert.Contains(t, grant(2), "reason=key-changed")
	// set as control with control's rule key replaced by the stranger's: refused, and the file keeps its recipients.
	require.NoError(t, os.WriteFile(filepath.Join(store, ".sops.yaml"), []byte(strings.Replace(string(rules), "age: "+ctlPub, "age: "+strangerPub, 1)), 0o644))
	assert.Contains(t, run("fleet-bus-xyz-three\n", 2, "set", "NOVA_BUS_PASSWORD", "--store", store, "--pins", pins, "--as", "control", "--key", ctl, "--sops", sops), "reason=key-changed")
	ctlFile, err := os.ReadFile(filepath.Join(store, "control.yaml"))
	require.NoError(t, err)
	assert.NotContains(t, string(ctlFile), strangerPub)
	require.NoError(t, os.WriteFile(filepath.Join(store, ".sops.yaml"), rules, 0o644))
	// A pusher adds a name to bo's grants file: the next grant refuses instead of filling it.
	grants, err := os.ReadFile(filepath.Join(store, "bo.grants.yaml"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(store, "bo.grants.yaml"), append([]byte("NOVA_BUS_PASSWORD_TWO: ENC[x]\n"), grants...), 0o600))
	assert.Contains(t, grant(2), "reason=grants-changed")
	require.NoError(t, os.WriteFile(filepath.Join(store, "bo.grants.yaml"), grants, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(store, ".sops.yaml"), rules, 0o644))
	require.NoError(t, os.Rename(filepath.Join(store, "bo.grants.yaml"), filepath.Join(dir, "saved.yaml")))
	require.NoError(t, os.Symlink("control.yaml", filepath.Join(store, "bo.grants.yaml")))
	assert.Contains(t, grant(2), "reason=not-regular")
}
