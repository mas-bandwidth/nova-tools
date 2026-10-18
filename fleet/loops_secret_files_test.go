package fleet

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestLoopWithSecretFilesRunsNoSecretsExecAndCarriesNoValue(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 with Jinja2 is required to render the loop unit template")
	}

	home := t.TempDir()
	secret := fakeLoopSecret(t)
	secretPath := filepath.Join(home, "coordinator%password.env")
	require.NoError(t, os.WriteFile(secretPath, []byte(secret), 0o600))
	require.NoError(t, os.Chmod(secretPath, 0o600))
	accepted, refusal := evaluateLoopSecretAssertion(t, secretPath, "sprint-server", mustCurrentUser(t))
	assert.True(t, accepted, "a 0600 file owned by the login was refused: %s", refusal)
	require.Empty(t, refusal)
	secretFilePath := "~/coordinator%password.env"
	for _, tc := range []struct {
		name     string
		template string
		want     string
	}{
		{"linux", "nova-loop.service.j2", `Environment="NOVA_SPRINT_REDIS_PASSWORD_FILE=` + strings.ReplaceAll(secretPath, "%", "%%") + `"`},
		{"darwin", "nova-loop.plist.j2", "<key>NOVA_SPRINT_REDIS_PASSWORD_FILE</key>\n<string>" + secretPath + "</string>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			unit := renderLoopTemplate(t, tc.template, home, []string{"/opt/nova/bin/nova-sprint", "worker"}, map[string]string{
				"NOVA_SPRINT_REDIS_PASSWORD_FILE": secretFilePath,
			})
			assert.Contains(t, unit, tc.want, "the unit does not carry the expanded secret-file path")
			assert.NotContains(t, unit, "nova-secrets", "a secret-file loop still runs behind nova-secrets exec")
			assert.False(t, strings.Contains(unit, secret), "the unit contains the fake secret value")
		})
	}

	vars := loopTemplateTaskVars(t)
	assert.Contains(t, vars["loop_secrets"], "if item['keys'] else []",
		"the loop wrapper is not empty for a record without keys")
	assert.Equal(t, "{{ loop_secrets + loop_argv }}", vars["loop_exec"],
		"the unit command does not use the wrapper only when keys exist")
}

func TestLoopSecretFileMustBeOwnerOnly(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 with Jinja2 is required to evaluate the loop assertion")
	}

	home := t.TempDir()
	secret := fakeLoopSecret(t)
	secretPath := filepath.Join(home, "coordinator-password.env")
	require.NoError(t, os.WriteFile(secretPath, []byte(secret), 0o644))
	require.NoError(t, os.Chmod(secretPath, 0o644))
	statTask := loopTaskByName(t, "stat every loop secret file without reading it")
	statArgs, ok := statTask["ansible.builtin.stat"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "{{ item.path }}", asText(statArgs["path"]))
	assert.Equal(t, false, statArgs["get_checksum"], "stat must not hash secret-file contents")
	assert.Equal(t, false, statArgs["get_mime"], "stat must not inspect secret-file contents for MIME")
	assert.Equal(t, "loop_secret_file_stats", asText(statTask["register"]))
	ownerTask := loopSecretOwnerAssertion(t)
	assert.Equal(t, "{{ loop_secret_file_stats.results }}", asText(ownerTask["loop"]))
	assert.Less(t, taskIndex(t, "a loop record cannot mix keys and secret files"), firstWriteTaskIndex(t))
	assert.Less(t, taskIndex(t, asText(statTask["name"])), firstWriteTaskIndex(t))
	assert.Less(t, taskIndex(t, asText(ownerTask["name"])), firstWriteTaskIndex(t))
	refused, output := evaluateLoopSecretAssertion(t, secretPath, "sprint-server", mustCurrentUser(t))
	assert.False(t, refused, "the 0644 secret file was accepted")
	assert.Contains(t, output, secretPath, "the refusal does not name the file")
	assert.Contains(t, output, "nova-secrets place", "the refusal omits the placement remedy")
	assert.Contains(t, output, "chmod 600", "the refusal omits the mode remedy")
	assert.False(t, strings.Contains(output, secret), "the refusal output contains the fake secret value")
}

func TestLoopWithKeysStillRendersAsBefore(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 with Jinja2 is required to render the loop unit template")
	}
	vars := loopTemplateTaskVars(t)
	assert.Contains(t, vars["loop_secrets"], "--only", "the existing keys wrapper no longer selects named keys")
	assert.Contains(t, vars["loop_secrets"], "item['keys'] | join(',')",
		"the existing keys wrapper no longer carries the record's key names")
	unit := renderLoopTemplate(t, "nova-loop.service.j2", t.TempDir(), []string{
		"/opt/nova/bin/nova-secrets", "exec", "--store", "/tmp/store", "--as", "friend.test",
		"--key", "/home/test/.config/nova-secrets/friend.test.key", "--sops", "/usr/bin/sops",
		"--only", "KEY_ONE", "--require=KEY_ONE", "--", "/opt/nova/bin/nova-sprint", "worker",
	}, nil)
	assert.Contains(t, unit,
		`ExecStart="/opt/nova/bin/nova-secrets" "exec" "--store" "/tmp/store" "--as" "friend.test" "--key" "/home/test/.config/nova-secrets/friend.test.key" "--sops" "/usr/bin/sops" "--only" "KEY_ONE" "--require=KEY_ONE" "--" "/opt/nova/bin/nova-sprint" "worker"`,
		"a record with keys no longer renders the BASE nova-secrets exec command")
}

func fakeLoopSecret(t *testing.T) string {
	t.Helper()
	var b [24]byte
	_, err := rand.Read(b[:])
	require.NoError(t, err)
	return hex.EncodeToString(b[:])
}

func mustCurrentUser(t *testing.T) string {
	t.Helper()
	current, err := user.Current()
	require.NoError(t, err)
	return current.Username
}

func renderLoopTemplate(t *testing.T, template, home string, loopExec []string, secretFiles map[string]string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("templates", template))
	require.NoError(t, err)
	record := map[string]any{"name": "sprint-server", "enabled": true, "keepalive": false, "every": 10}
	if secretFiles != nil {
		record["secret_files"] = secretFiles
	}
	input, err := json.Marshal(map[string]any{
		"template": path,
		"variables": map[string]any{
			"loop_dir": home, "loop_system": false, "ansible_user_id": "test-owner",
			"loop_exec": loopExec, "loop_member": false, "nova_member_stop_timeout": 10,
			"loop_log": filepath.Join(home, "loop.log"), "nova_home": home,
			"nova_loop_path": "/opt/nova/bin", "nova_redis_addr": "127.0.0.1:6379",
			"l": record,
		},
	})
	require.NoError(t, err)
	const script = `import json, os, re, sys
from jinja2 import Environment, FileSystemLoader, StrictUndefined
data = json.load(sys.stdin)
path = data["template"]
env = Environment(loader=FileSystemLoader(os.path.dirname(path)), undefined=StrictUndefined, autoescape=False)
env.filters["regex_replace"] = lambda value, pattern, repl: re.sub(pattern, repl, value)
print(env.get_template(os.path.basename(path)).render(**data["variables"]))
`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", script)
	cmd.WaitDelay = 5 * time.Second
	cmd.Stdin = strings.NewReader(string(input))
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "Jinja template render failed: %s", string(out))
	return string(out)
}

func loopTemplateTaskVars(t *testing.T) map[string]string {
	t.Helper()
	for _, task := range firstPlayTasks(t) {
		if asText(task["name"]) != "one unit per record" {
			continue
		}
		vars, ok := task["vars"].(map[string]any)
		require.True(t, ok, "the loop unit task has no vars")
		out := make(map[string]string, len(vars))
		for key, value := range vars {
			out[key] = asText(value)
		}
		return out
	}
	require.FailNow(t, "fleet/loops.yml has no unit-template task")
	return nil
}

func loopSecretOwnerAssertion(t *testing.T) map[string]any {
	t.Helper()
	return loopTaskByName(t, "a loop secret file is a regular owner-only file")
}

func loopTaskByName(t *testing.T, name string) map[string]any {
	t.Helper()
	for _, task := range firstPlayTasks(t) {
		if asText(task["name"]) == name {
			return task
		}
	}
	require.FailNowf(t, "missing play task", "fleet/loops.yml has no task named %q", name)
	return nil
}

func taskIndex(t *testing.T, name string) int {
	t.Helper()
	for i, task := range firstPlayTasks(t) {
		if asText(task["name"]) == name {
			return i
		}
	}
	require.FailNowf(t, "missing play task", "fleet/loops.yml has no task named %q", name)
	return -1
}

func firstWriteTaskIndex(t *testing.T) int {
	t.Helper()
	for i, task := range firstPlayTasks(t) {
		if _, ok := task["ansible.builtin.file"]; ok {
			return i
		}
		if _, ok := task["ansible.builtin.template"]; ok {
			return i
		}
	}
	require.FailNow(t, "fleet/loops.yml has no file write task")
	return -1
}

func firstPlayTasks(t *testing.T) []map[string]any {
	t.Helper()
	b, err := os.ReadFile("loops.yml")
	require.NoError(t, err)
	var plays []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &plays))
	require.NotEmpty(t, plays)
	raw, ok := plays[0]["tasks"].([]any)
	require.True(t, ok)
	tasks := make([]map[string]any, 0, len(raw))
	for _, value := range raw {
		task, ok := value.(map[string]any)
		if ok {
			tasks = append(tasks, task)
		}
	}
	return tasks
}

func evaluateLoopSecretAssertion(t *testing.T, path, recordName, login string) (bool, string) {
	t.Helper()
	task := loopSecretOwnerAssertion(t)
	assertion, ok := task["ansible.builtin.assert"].(map[string]any)
	require.True(t, ok)
	var checks []string
	switch value := assertion["that"].(type) {
	case []any:
		for _, check := range value {
			checks = append(checks, asText(check))
		}
	case string:
		checks = append(checks, value)
	default:
		require.FailNowf(t, "unsupported that value", "secret-file assertion has unsupported that value %T", value)
	}
	info, err := os.Stat(path)
	require.NoError(t, err)
	input, err := json.Marshal(map[string]any{
		"checks":   checks,
		"fail_msg": asText(assertion["fail_msg"]),
		"context": map[string]any{
			"ansible_user_id": login,
			"item": map[string]any{
				"item": map[string]any{"record": recordName, "path": path},
				"stat": map[string]any{
					"exists": true, "isreg": info.Mode().IsRegular(),
					"pw_name": login, "mode": fmt.Sprintf("%04o", info.Mode().Perm()),
				},
			},
		},
	})
	const script = `import json, sys
from jinja2 import Environment, StrictUndefined
data = json.load(sys.stdin)
env = Environment(undefined=StrictUndefined, autoescape=False)
context = data["context"]
accepted = all(bool(env.compile_expression(check)(**context)) for check in data["checks"])
message = "" if accepted else env.from_string(data["fail_msg"]).render(**context)
print(json.dumps({"accepted": accepted, "message": message}))
`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", script)
	cmd.WaitDelay = 5 * time.Second
	cmd.Stdin = strings.NewReader(string(input))
	out, runErr := cmd.CombinedOutput()
	require.NoErrorf(t, runErr, "Jinja assertion evaluation failed: %s", string(out))
	var result struct {
		Accepted bool   `json:"accepted"`
		Message  string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(out, &result))
	return result.Accepted, result.Message
}
