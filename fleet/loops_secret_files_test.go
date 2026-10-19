package fleet

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// loops_secret_files_test.go pins the secret_files record shape in fleet/loops.yml:
// a long-running loop gets its secret as a 0600 file path in its environment, never
// as a value, and a record with keys keeps the BASE nova-secrets exec wrapper.
//
// The play and templates are Jinja. The unit tier renders/evaluates the exact
// fragments it pins in Go rather than calling python3 with Jinja2 or
// ansible-playbook, neither of which the card's toolchain promises; the
// ansible-templar render of the same play lives behind //go:build functional
// in loops_play_functional_test.go.

func TestLoopWithSecretFilesRunsNoSecretsExecAndCarriesNoValue(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	secret := fakeLoopSecret(t)
	secretPath := filepath.Join(home, "coordinator%password.env")
	require.NoError(t, os.WriteFile(secretPath, []byte(secret), 0o600))
	require.NoError(t, os.Chmod(secretPath, 0o600))
	accepted, refusal := evaluateLoopSecretAssertion(t, secretPath, "sprint-server", mustCurrentUser(t))
	assert.True(t, accepted, "a 0600 file owned by the login was refused: %s", refusal)
	require.Empty(t, refusal)

	secretFilePath := "~/coordinator%password.env"
	argv := []string{"/opt/nova/bin/nova-sprint", "worker"}
	secretFiles := map[string]string{"NOVA_SPRINT_REDIS_PASSWORD_FILE": secretFilePath}

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
			unit := renderSecretFiles(t, tc.template, home, secretFiles)
			assert.Contains(t, unit, tc.want, "the unit does not carry the expanded secret-file path")
			assert.NotContains(t, unit, "nova-secrets", "a secret-file loop still runs behind nova-secrets exec")
			assert.False(t, strings.Contains(unit, secret), "the unit contains the fake secret value")
		})
	}

	exec := renderExecStart(t, "nova-loop.service.j2", argv)
	assert.NotContains(t, exec, "nova-secrets", "a secret-file loop still runs behind nova-secrets exec")

	vars := loopTemplateTaskVars(t)
	assert.Contains(t, vars["loop_secrets"], "if item['keys'] else []",
		"the loop wrapper is not empty for a record without keys")
	assert.Equal(t, "{{ loop_secrets + loop_argv }}", vars["loop_exec"],
		"the unit command does not use the wrapper only when keys exist")
}

func TestLoopSecretFileMustBeOwnerOnly(t *testing.T) {
	t.Parallel()

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

	vars := loopTemplateTaskVars(t)
	assert.Contains(t, vars["loop_secrets"], "--only", "the existing keys wrapper no longer selects named keys")
	assert.Contains(t, vars["loop_secrets"], "item['keys'] | join(',')",
		"the existing keys wrapper no longer carries the record's key names")
	argv := []string{
		"/opt/nova/bin/nova-secrets", "exec", "--store", "/tmp/store", "--as", "friend.test",
		"--key", "/home/test/.config/nova-secrets/friend.test.key", "--sops", "/usr/bin/sops",
		"--only", "KEY_ONE", "--require=KEY_ONE", "--", "/opt/nova/bin/nova-sprint", "worker",
	}
	assert.Equal(t, `ExecStart="/opt/nova/bin/nova-secrets" "exec" "--store" "/tmp/store" "--as" "friend.test" "--key" "/home/test/.config/nova-secrets/friend.test.key" "--sops" "/usr/bin/sops" "--only" "KEY_ONE" "--require=KEY_ONE" "--" "/opt/nova/bin/nova-sprint" "worker"`,
		renderExecStart(t, "nova-loop.service.j2", argv),
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

func readLoopTemplate(t *testing.T, template string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("templates", template))
	require.NoError(t, err)
	return string(b)
}

// renderSecretFiles expands the template's secret_files for-block only: the
// one place a path reaches the unit's environment. Every filter the block
// carries is applied so the result is the byte string the template writes.
func renderSecretFiles(t *testing.T, template, home string, secretFiles map[string]string) string {
	t.Helper()
	src := readLoopTemplate(t, template)
	const marker = "{% for secret_file in loop_secret_files %}"
	start := strings.Index(src, marker)
	require.GreaterOrEqual(t, start, 0, "the template has no secret_files loop")
	rest := src[start+len(marker):]
	end := strings.Index(rest, "{% endfor %}")
	require.GreaterOrEqual(t, end, 0, "the secret_files loop is not closed")
	body := rest[:end]

	names := make([]string, 0, len(secretFiles))
	for name := range secretFiles {
		names = append(names, name)
	}
	sort.Strings(names)

	var out strings.Builder
	for _, name := range names {
		path := expandHome(home, secretFiles[name])
		out.WriteString(renderMustaches(t, body, func(expr string) string {
			return evalSecretFileExpr(t, expr, name, path, home)
		}))
	}
	return out.String()
}

// renderExecStart expands the service template's ExecStart for-loop over
// loop_exec, the one place a command (and any nova-secrets wrapper) is written.
func renderExecStart(t *testing.T, template string, argv []string) string {
	t.Helper()
	src := readLoopTemplate(t, template)
	const marker = "ExecStart={% for w in loop_exec %}"
	start := strings.Index(src, marker)
	require.GreaterOrEqual(t, start, 0, "the service template has no ExecStart loop_exec")
	rest := src[start+len(marker):]
	end := strings.Index(rest, "{% endfor %}")
	require.GreaterOrEqual(t, end, 0, "the ExecStart loop is not closed")
	body := rest[:end]

	var out strings.Builder
	out.WriteString("ExecStart=")
	for i, word := range argv {
		last := i == len(argv)-1
		out.WriteString(renderMustaches(t, body, func(expr string) string {
			return evalExecStartExpr(t, expr, word, last)
		}))
	}
	return out.String()
}

func renderMustaches(t *testing.T, body string, eval func(string) string) string {
	t.Helper()
	mustache := regexp.MustCompile(`\{\{(.*?)\}\}`)
	return mustache.ReplaceAllStringFunc(body, func(match string) string {
		return eval(strings.TrimSpace(match[2 : len(match)-2]))
	})
}

func expandHome(home, path string) string {
	if strings.HasPrefix(path, "~/") {
		return home + "/" + strings.TrimPrefix(path, "~/")
	}
	return path
}

func evalSecretFileExpr(t *testing.T, expr, name, path, home string) string {
	t.Helper()
	parts := strings.Split(expr, "|")
	var value string
	switch base := strings.TrimSpace(parts[0]); base {
	case "secret_file.name":
		value = name
	case "secret_file.path":
		value = path
	default:
		t.Fatalf("the secret_files block interpolates unexpected %q", base)
	}
	for _, raw := range parts[1:] {
		value = applyLoopFilter(t, strings.TrimSpace(raw), value, home)
	}
	return value
}

func evalExecStartExpr(t *testing.T, expr, word string, last bool) string {
	t.Helper()
	if expr == "' ' if not loop.last else ''" {
		if last {
			return ""
		}
		return " "
	}
	parts := strings.Split(expr, "|")
	if strings.TrimSpace(parts[0]) != "w" {
		t.Fatalf("the ExecStart block interpolates unexpected %q", parts[0])
	}
	value := word
	for _, raw := range parts[1:] {
		value = applyLoopFilter(t, strings.TrimSpace(raw), value, "")
	}
	return value
}

func applyLoopFilter(t *testing.T, filter, value, home string) string {
	t.Helper()
	switch filter {
	case "e":
		return html.EscapeString(value)
	case `regex_replace('^~/', nova_home ~ '/')`:
		return expandHome(home, value)
	case `replace('\\', '\\\\')`:
		return strings.ReplaceAll(value, `\`, `\\`)
	case `replace('"', '\\"')`:
		return strings.ReplaceAll(value, `"`, `\"`)
	case `replace('%', '%%')`:
		return strings.ReplaceAll(value, `%`, `%%`)
	case `replace('$', '$$')`:
		return strings.ReplaceAll(value, `$`, `$$`)
	default:
		t.Fatalf("the loop unit template uses an unexpected filter %q", filter)
		return ""
	}
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

// evaluateLoopSecretAssertion answers the owner-only assertion for one file
// without a secret store or a templar: it reads the task's own checks and fail
// message, applies them to the file's stat, and expands the message's simple
// references. No check reads the file's content.
func evaluateLoopSecretAssertion(t *testing.T, path, recordName, login string) (bool, string) {
	t.Helper()
	task := loopSecretOwnerAssertion(t)
	assertion, ok := task["ansible.builtin.assert"].(map[string]any)
	require.True(t, ok)
	checks := assertionChecks(t, assertion)

	info, err := os.Stat(path)
	require.NoError(t, err)

	accepted := true
	for _, check := range checks {
		if !evalLoopSecretCheck(t, check, info, login) {
			accepted = false
		}
	}
	if accepted {
		return true, ""
	}
	message := expandAssertionMessage(t, asText(assertion["fail_msg"]), map[string]string{
		"item.item.record": recordName,
		"item.item.path":   path,
		"ansible_user_id":  login,
	})
	return false, message
}

func assertionChecks(t *testing.T, assertion map[string]any) []string {
	t.Helper()
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
	return checks
}

func evalLoopSecretCheck(t *testing.T, check string, info os.FileInfo, login string) bool {
	t.Helper()
	switch {
	case strings.Contains(check, ".stat.exists"):
		return true
	case strings.Contains(check, ".stat.isreg"):
		return info.Mode().IsRegular()
	case strings.Contains(check, ".stat.pw_name"):
		return fileOwnerName(info) == login
	case strings.Contains(check, ".stat.mode"):
		allowed := regexp.MustCompile(`'([0-7]{4})'`).FindAllStringSubmatch(check, -1)
		require.NotEmpty(t, allowed, "the owner-only assertion names no allowed modes: %q", check)
		mode := fmt.Sprintf("%04o", info.Mode().Perm())
		for _, candidate := range allowed {
			if candidate[1] == mode {
				return true
			}
		}
		return false
	default:
		t.Fatalf("the owner-only assertion carries an unknown check %q", check)
		return false
	}
}

func fileOwnerName(info os.FileInfo) string {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		if owner, err := user.LookupId(fmt.Sprint(stat.Uid)); err == nil {
			return owner.Username
		}
	}
	return ""
}

func expandAssertionMessage(t *testing.T, message string, context map[string]string) string {
	t.Helper()
	mustache := regexp.MustCompile(`\{\{(.*?)\}\}`)
	return mustache.ReplaceAllStringFunc(message, func(match string) string {
		expr := strings.TrimSpace(match[2 : len(match)-2])
		value, ok := context[expr]
		require.Truef(t, ok, "the owner-only refusal carries an unknown reference %q", expr)
		return value
	})
}
