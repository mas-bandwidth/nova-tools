//go:build functional

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// postgres_play_functional_test.go runs the postgres role's list check (roles/postgres/tasks/roles.yml) on localhost
// through ansible-core, with the role's defaults, and prints the list the role then loops over. The rest of the role
// needs a Debian machine with systemd; postgres_play_test.go is the structural half.

const pgRolesPlay = `- hosts: localhost
  connection: local
  gather_facts: false
  vars_files: ["{{ fleet }}/roles/postgres/defaults/main.yml"]
  tasks:
    - ansible.builtin.include_tasks: "{{ fleet }}/roles/postgres/tasks/roles.yml"
    - ansible.builtin.debug: { msg: "PGROLES {{ pg_roles | to_json(sort_keys=True) }}" }
`

func runPgRolesPlay(t *testing.T, extra ...string) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine; postgres_play_test.go still runs")
	}
	fleet, err := filepath.Abs(".")
	require.NoError(t, err)
	dir := t.TempDir()
	play := filepath.Join(dir, "pgroles.yml")
	require.NoError(t, os.WriteFile(play, []byte(pgRolesPlay), 0o644))
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	args := append([]string{"-i", "localhost,", "-e", "fleet=" + fleet}, extra...)
	cmd := exec.CommandContext(ctx, "ansible-playbook", append(args, play)...)
	cmd.Env = append(os.Environ(), "ANSIBLE_NOCOLOR=1", "HOME="+dir, "ANSIBLE_LOCAL_TEMP="+filepath.Join(dir, "tmp"), "TMPDIR="+dir)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// By default the list is nova-config's three, flags and want filled in.
func TestFunctionalPostgresRolesDefault(t *testing.T) {
	t.Parallel()
	out, err := runPgRolesPlay(t)
	require.NoError(t, err, out)
	assert.Contains(t, out, `PGROLES [{\"flags\": \"LOGIN CREATEDB CREATEROLE\", \"name\": \"nova_admin\", \"schema\": \"\", \"seats\": [], \"secret\": \"\", \"want\": \"t|t|t\"}, `+
		`{\"flags\": \"LOGIN NOCREATEDB NOCREATEROLE\", \"name\": \"nova_config\", \"schema\": \"config\", \"seats\": [], \"secret\": \"NOVA_PG_PASSWORD\", \"want\": \"t|f|f\"}, `+
		`{\"flags\": \"LOGIN NOCREATEDB NOCREATEROLE\", \"name\": \"nova_read\", \"schema\": \"\", \"seats\": [], \"secret\": \"\", \"want\": \"t|f|f\"}]`)
}

// nova-sprint's role, given with -e beside the three, is in the list as given; flags and want default.
func TestFunctionalPostgresRolesWithAnAddedRole(t *testing.T) {
	t.Parallel()
	out, err := runPgRolesPlay(t, "-e", `{"nova_pg_roles": [
		{"role": "nova_admin", "flags": "LOGIN CREATEDB CREATEROLE", "want": "t|t|t"},
		{"role": "nova_config", "schema": "config", "secret": "NOVA_PG_PASSWORD"},
		{"role": "nova_read"},
		{"role": "nova_sprint", "schema": "sprint", "secret": "NOVA_SPRINT_PG_PASSWORD", "seats": ["studio", "bench-1"]}]}`)
	require.NoError(t, err, out)
	assert.Contains(t, out, `{\"flags\": \"LOGIN NOCREATEDB NOCREATEROLE\", \"name\": \"nova_sprint\", \"schema\": \"sprint\", \"seats\": [\"studio\", \"bench-1\"], \"secret\": \"NOVA_SPRINT_PG_PASSWORD\", \"want\": \"t|f|f\"}]`)
}

// A name that would be pasted into SQL or a shell unquoted, a duplicate, or a list of the wrong shape stops the run
// before anything changes, naming the entry.
func TestFunctionalPostgresRolesRefusals(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ vars, want string }{
		{`{"nova_pg_roles": "nova_admin"}`, "nova_pg_roles is a list"},
		{`{"nova_pg_roles": [{"schema": "x"}]}`, "PG STOP nova_pg_roles entry 1"},
		{`{"nova_pg_roles": [{"role": "x; drop role nova_admin"}]}`, "PG STOP nova_pg_roles entry 1"},
		{`{"nova_pg_roles": [{"role": "a", "schema": "Sprint"}]}`, "PG STOP nova_pg_roles entry 1"},
		{`{"nova_pg_roles": [{"role": "a", "secret": "x$(id)"}]}`, "PG STOP nova_pg_roles entry 1"},
		{`{"nova_pg_roles": [{"role": "a", "seats": "studio"}]}`, "PG STOP nova_pg_roles entry 1"},
		{`{"nova_pg_roles": [{"role": "a", "seats": ["-x"]}]}`, "PG STOP nova_pg_roles entry 1"},
		{`{"nova_pg_roles": [{"role": "a", "seats": ["s"]}]}`, "PG STOP nova_pg_roles entry 1"},
		{`{"nova_pg_roles": [{"role": "a"}, {"role": "a"}]}`, "names a role, a schema or a secret twice"},
	} {
		out, err := runPgRolesPlay(t, "-e", c.vars)
		assert.Error(t, err, c.vars)
		assert.Contains(t, out, c.want, c.vars)
		assert.NotContains(t, out, "PGROLES", c.vars)
	}
}

// The task that makes a role's password file, run by sh with a stand-in openssl: a failed openssl leaves no
// file (the task's creates: would skip an empty one on every later run), and a good one leaves the whole value,
// mode 0600.
func TestFunctionalPostgresPasswordFileIsWholeOrAbsent(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("roles/postgres/tasks/main.yml")
	require.NoError(t, err)
	var raw []any
	require.NoError(t, yaml.Unmarshal(b, &raw))
	var line string
	for _, task := range flattenTasks(t, mapsOf(t, raw)) {
		if strings.HasPrefix(asText(task["name"]), "a password per role") {
			line = asText(task["ansible.builtin.shell"])
		}
	}
	require.NotEmpty(t, line, "the task that makes the password files")
	for _, c := range []struct {
		openssl string
		want    string // the file's content, or "" for no file
	}{
		{"exit 1", ""},
		{"printf '%064d\\n' 0", strings.Repeat("0", 64) + "\n"},
	} {
		dir, bin := t.TempDir(), t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(bin, "openssl"), []byte("#!/bin/sh\n"+c.openssl+"\n"), 0o755))
		script := strings.NewReplacer("{{ nova_pg_dir }}", dir, "{{ item.name }}", "nova_config").Replace(line)
		cmd := exec.Command("/bin/sh", "-c", script)
		cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		pass := filepath.Join(dir, "nova_config.pass")
		if c.want == "" {
			assert.Error(t, err, "%s", out)
			assert.NoFileExists(t, pass, "a failed openssl leaves no password file")
			continue
		}
		require.NoError(t, err, "%s", out)
		got, err := os.ReadFile(pass)
		require.NoError(t, err)
		assert.Equal(t, c.want, string(got))
		fi, err := os.Stat(pass)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	}
}
