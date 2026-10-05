package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// server install (docs/SPEC-SPRINT.md, "The server", install-server-unit-by-verb-r.w4)
// writes the server's unit with no actor in it and the store's credentials behind
// nova-secrets exec --only, records its hash, and refuses to install over a unit edited
// by hand, printing the diff with no secret's value; the loader is the test's.
func TestServerInstallWritesAUnitWithNoActorAndRefusesAHandEdit(t *testing.T) {
	t.Parallel()
	type rig struct {
		a     *app
		dir   string
		calls []string
	}
	newRig := func(t *testing.T, goos string) *rig {
		env := map[string]string{"NOVA_SPRINT_REDIS": "127.0.0.1:6379", "NOVA_SPRINT_ACTOR": "someone", "PATH": "/opt/nova/bin:/usr/bin"}
		r := &rig{a: newApp(func(k string) string { return env[k] }), dir: t.TempDir()}
		r.a.goos = goos
		r.a.home = func() (string, error) { return r.dir, nil }
		r.a.executable = func() (string, error) { return "/opt/nova/bin/nova-sprint", nil }
		r.a.seatLoad = func(goos, op, path string) error { r.calls = append(r.calls, goos+" "+op+" "+path); return nil }
		return r
	}
	flags := []string{"--dir", "", "--listen", "127.0.0.1:7480", "--land", "--decide", "/srv/decide", "--redis-user", "coordinator",
		"--only", "JEV_API_KEY", "--secrets-store", "/srv/secrets", "--secrets-as", "coordinator", "--secrets-key", "/srv/k.key", "--sops", "/usr/bin/sops"}
	do := func(r *rig, extra ...string) (int, string, string) {
		args := append([]string{"server", "install"}, flags...)
		args[3] = r.dir
		var out, errb bytes.Buffer
		code := r.a.run(append(args, extra...), &out, &errb)
		return code, out.String(), errb.String()
	}

	t.Run("dry run prints a unit with no actor and the secrets behind nova-secrets exec", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "darwin")
		code, out, errs := do(r, "--dry-run")
		require.Equal(t, 0, code, errs)
		unit := filepath.Join(r.dir, serverLabel+".plist")
		assert.Contains(t, out, "SERVER INSTALL DRY-RUN unit="+unit+" plan=write")
		assert.NotContains(t, out, "NOVA_SPRINT_ACTOR")
		assert.NotContains(t, out, "someone")
		assert.Contains(t, out, "<string>/opt/nova/bin/nova-secrets</string>\n\t\t<string>exec</string>")
		assert.Contains(t, out, "<string>--only</string>\n\t\t<string>NOVA_REDIS_PASSWORD,JEV_API_KEY</string>\n\t\t<string>--require</string>\n\t\t<string>NOVA_REDIS_PASSWORD</string>\n\t\t<string>--</string>\n\t\t<string>/opt/nova/bin/nova-sprint</string>\n\t\t<string>run</string>\n\t\t<string>--listen</string>\n\t\t<string>127.0.0.1:7480</string>\n\t\t<string>--land</string>\n\t\t<string>--decide</string>")
		assert.Contains(t, out, "<key>NOVA_SPRINT_REDIS_PASSWORD_ENV</key>\n\t\t<string>NOVA_REDIS_PASSWORD</string>")
		assert.NoFileExists(t, unit, "a dry run writes nothing")
		assert.Empty(t, r.calls, "a dry run loads nothing")
	})

	t.Run("install writes the unit and its hash, loads it, and a second install keeps it", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "linux")
		code, out, errs := do(r)
		require.Equal(t, 0, code, errs)
		unit := filepath.Join(r.dir, serverService)
		assert.Contains(t, out, "SERVER INSTALL OK unit="+unit+" written=true replaced-hand-edit=false loaded=true")
		b, err := os.ReadFile(unit)
		require.NoError(t, err)
		assert.Contains(t, string(b), `ExecStart="/opt/nova/bin/nova-secrets" "exec" "--store" "/srv/secrets"`)
		assert.Contains(t, string(b), `"--" "/opt/nova/bin/nova-sprint" "run" "--listen" "127.0.0.1:7480" "--land" "--decide" "/srv/decide"`)
		assert.Contains(t, string(b), `Environment="NOVA_SPRINT_REDIS_USER=coordinator"`)
		assert.NotContains(t, string(b), "NOVA_SPRINT_ACTOR")
		h, err := os.ReadFile(unit + ".sha256")
		require.NoError(t, err)
		assert.Equal(t, unitHash(b)+"\n", string(h))
		assert.Equal(t, []string{"linux load " + unit}, r.calls)

		code, out, errs = do(r)
		require.Equal(t, 0, code, errs)
		assert.Contains(t, out, "written=false replaced-hand-edit=false loaded=true")
		assert.Len(t, r.calls, 2, "a kept unit is loaded again")
	})

	t.Run("a hand edit is refused with the diff and no secret value, and replaced only when asked", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "darwin")
		code, _, errs := do(r)
		require.Equal(t, 0, code, errs)
		unit := filepath.Join(r.dir, serverLabel+".plist")
		b, err := os.ReadFile(unit)
		require.NoError(t, err)
		edited := strings.Replace(string(b), "\t<dict>\n", "\t<dict>\n\t\t<key>NOVA_SPRINT_ACTOR</key>\n\t\t<string>someone</string>\n\t\t<key>NOVA_REDIS_PASSWORD</key>\n\t\t<string>hunter2-value</string>\n", 1)
		require.NoError(t, os.WriteFile(unit, []byte(edited), 0o644))

		code, out, errs := do(r)
		assert.Equal(t, 1, code, out)
		assert.Contains(t, errs, "nova-sprint server install REFUSED: the unit at "+unit+" is not the one server install wrote")
		assert.Contains(t, errs, "--replace-hand-edit")
		assert.Contains(t, errs, "  -\t\t<key>NOVA_SPRINT_ACTOR</key>\n  -\t\t<string><redacted></string>\n")
		assert.Contains(t, errs, "  -\t\t<string><redacted></string>")
		assert.NotContains(t, errs, "hunter2-value", "a diff never prints a secret's value")
		assert.NotContains(t, errs, "hunter2")
		after, err := os.ReadFile(unit)
		require.NoError(t, err)
		assert.Equal(t, edited, string(after), "a refused install writes nothing")
		assert.Len(t, r.calls, 1, "a refused install loads nothing")

		code, out, _ = do(r, "--dry-run")
		assert.Equal(t, 0, code)
		assert.Contains(t, out, "plan=refuse")

		code, out, errs = do(r, "--replace-hand-edit")
		require.Equal(t, 0, code, errs)
		assert.Contains(t, out, "written=true replaced-hand-edit=true loaded=true")
		after, err = os.ReadFile(unit)
		require.NoError(t, err)
		assert.Equal(t, string(b), string(after))
	})

	t.Run("a unit written by hand, with no recorded hash, is refused", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "linux")
		unit := filepath.Join(r.dir, serverService)
		unitContent := "[Service]\nEnvironment=NOVA_SPRINT_ACTOR=someone\nEnvironment=JEV_API_KEY=sk-live-value\nEnvironment=NOVA_SPRINT_REDIS=redis://:hunter2@host:6379\nEnvironment=\"REDIS_AUTH=hunter2\"\nEnvironment=\"NOVA_REDIS_PASSWORD=two words\"\n"
		require.NoError(t, os.WriteFile(unit, []byte(unitContent), 0o644))
		code, _, errs := do(r)
		assert.Equal(t, 1, code)
		assert.Contains(t, errs, "has no recorded hash: it was written by hand")
		assert.Contains(t, errs, "  -Environment=NOVA_SPRINT_ACTOR=<redacted>")
		assert.Contains(t, errs, "  -Environment=JEV_API_KEY=<redacted>")
		assert.Contains(t, errs, "  -Environment=NOVA_SPRINT_REDIS=<redacted>")
		assert.Contains(t, errs, "  -Environment=\"REDIS_AUTH=<redacted>\"")
		assert.Contains(t, errs, "  -Environment=\"NOVA_REDIS_PASSWORD=<redacted>\"")
		assert.NotContains(t, errs, "sk-live-value")
		assert.NotContains(t, errs, "hunter2")
		assert.NotContains(t, errs, "two words")
		assert.Empty(t, r.calls)
	})

	t.Run("server switch swaps the binary the unit names and writes no unit: the unit and its hash stay as install wrote them", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "linux")
		bin := filepath.Join(r.dir, "nova-sprint")
		require.NoError(t, os.WriteFile(bin, []byte("version-1"), 0o755))
		r.a.executable = func() (string, error) { return bin, nil }
		code, _, errs := do(r)
		require.Equal(t, 0, code, errs)
		unit := filepath.Join(r.dir, serverService)
		before, err := os.ReadFile(unit)
		require.NoError(t, err)
		hash, err := os.ReadFile(unit + ".sha256")
		require.NoError(t, err)
		assert.Contains(t, string(before), `"--" "`+bin+`" "run" "--listen"`)

		// the candidate passes the canary: its shadow tick prints a plan (shadow.go)
		candidate := filepath.Join(r.dir, "nova-sprint-candidate")
		version2 := "#!/bin/sh\necho '" + `{"shadow":{"epoch":0,"state":"STOPPED","parts":[],"size":0,"took_ns":1}}` + "'\n"
		require.NoError(t, os.WriteFile(candidate, []byte(version2), 0o755))
		var out, errb bytes.Buffer
		code = r.a.run([]string{"server", "switch", candidate, "--target", bin}, &out, &errb)
		require.Equal(t, 0, code, errb.String())
		assert.Contains(t, out.String(), "SERVER SWITCH OK")
		swapped, err := os.ReadFile(bin)
		require.NoError(t, err)
		assert.Equal(t, version2, string(swapped))

		after, err := os.ReadFile(unit)
		require.NoError(t, err)
		assert.Equal(t, string(before), string(after), "switch writes no unit")
		afterHash, err := os.ReadFile(unit + ".sha256")
		require.NoError(t, err)
		assert.Equal(t, string(hash), string(afterHash), "switch writes no hash")
		_, why, err := handEdit(unit)
		require.NoError(t, err)
		assert.Empty(t, why, "after a switch the unit is still the one install wrote")
		assert.Len(t, r.calls, 1, "switch loads nothing: the server exits 3 on its replaced binary and the unit's Restart=always starts the new one")

		code, dry, errs := do(r, "--dry-run")
		require.Equal(t, 0, code, errs)
		assert.Contains(t, dry, "plan=keep")
	})

	t.Run("refusals name every problem and write nothing", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "linux")
		var out, errb bytes.Buffer
		code := r.a.run([]string{"server", "install", "--dir", r.dir, "--redis", "mem:0", "--only", "sk-live=value"}, &out, &errb)
		assert.Equal(t, 2, code)
		for _, want := range []string{"--listen <address:port> is required", "is a Redis store, not mem:0", "never a value", "--secrets-store <path> is required", "--secrets-as <seat> is required"} {
			assert.Contains(t, errb.String(), want)
		}
		assert.NoFileExists(t, filepath.Join(r.dir, serverService))
		assert.Empty(t, r.calls)
	})
}
